package loomopenai

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/masterkeysrd/loom/decision"
	"github.com/masterkeysrd/loom/message"
	"github.com/masterkeysrd/loom/stream"
	"github.com/openai/openai-go/v3"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

var _ decision.Provider = (*Provider)(nil)

// Evaluate executes a decision evaluation request for the given questions using OpenAI's Decisions API.
func (p *Provider) Evaluate(ctx context.Context, request *decision.Request) (*decision.Response, error) {
	if p == nil || p.client == nil {
		return nil, fmt.Errorf("openai: client is not initialized")
	}
	if request == nil {
		return nil, fmt.Errorf("decision: request cannot be nil")
	}

	params, err := toDecisionNewParams(request)
	if err != nil {
		return nil, fmt.Errorf("failed to convert decision request: %w", err)
	}

	if sw, ok := stream.WriterFromContext(ctx); ok {
		_ = sw.Write(ctx, stream.Event{
			Name: "on_llm_request",
			Data: map[string]any{
				"provider": "openai",
				"payload":  params,
			},
		})
	}

	// OpenAI Specific Span Decoration
	span := trace.SpanFromContext(ctx)
	span.SetAttributes(
		attribute.String("openai.api.type", "decisions"),
	)

	res, err := p.client.Decisions.New(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("failed to evaluate decisions: %w", err)
	}
	if res == nil {
		return nil, fmt.Errorf("empty decision response")
	}

	return toDecisionResponse(request, res), nil
}

func toDecisionNewParams(request *decision.Request) (openai.DecisionNewParams, error) {
	if request == nil {
		return openai.DecisionNewParams{}, fmt.Errorf("decision: request cannot be nil")
	}

	params := openai.DecisionNewParams{
		Model: request.Model,
	}

	// 1. Convert State
	var stateText string
	switch v := request.Input.State.(type) {
	case string:
		stateText = v
	case nil:
		stateText = ""
	default:
		data, err := json.Marshal(v)
		if err != nil {
			return openai.DecisionNewParams{}, fmt.Errorf("failed to marshal input state to JSON: %w", err)
		}
		stateText = string(data)
	}

	// 2. Convert Input
	if len(request.Input.Images) == 0 {
		params.Input = openai.DecisionNewParamsInputUnion{
			OfString: openai.String(stateText),
		}
	} else {
		var parts []openai.DecisionInputPartUnionParam
		if stateText != "" {
			parts = append(parts, openai.DecisionInputPartUnionParam{
				OfInputText: &openai.DecisionInputTextParam{
					Text: stateText,
				},
			})
		}
		for _, img := range request.Input.Images {
			var imageURL string
			if img.URL != "" {
				imageURL = img.URL
			} else if len(img.Data) > 0 {
				mediaType := img.MIMEType
				if mediaType == "" {
					mediaType = "image/png"
				}
				imageURL = "data:" + mediaType + ";base64," + base64.StdEncoding.EncodeToString(img.Data)
			}
			if imageURL != "" {
				parts = append(parts, openai.DecisionInputPartUnionParam{
					OfInputImage: &openai.DecisionInputImageParam{
						ImageURL: imageURL,
					},
				})
			}
		}
		params.Input = openai.DecisionNewParamsInputUnion{
			OfDecisionInputMessageArray: []openai.DecisionInputMessageParam{
				{
					Content: openai.DecisionInputMessageContentUnionParam{
						OfParts: parts,
					},
				},
			},
		}
	}

	// 3. Convert Questions
	questions := make([]openai.DecisionNewParamsQuestionUnion, 0, len(request.Questions))
	for _, q := range request.Questions {
		switch q.Type {
		case decision.QuestionPredicate:
			instructions := q.Instructions
			var criteria []string
			if q.TrueDescription != "" {
				criteria = append(criteria, fmt.Sprintf("True: %s", q.TrueDescription))
			}
			if q.FalseDescription != "" {
				criteria = append(criteria, fmt.Sprintf("False: %s", q.FalseDescription))
			}
			if len(criteria) > 0 {
				if instructions != "" {
					instructions += "\n\nCriteria:\n" + strings.Join(criteria, "\n")
				} else {
					instructions = "Criteria:\n" + strings.Join(criteria, "\n")
				}
			}
			questions = append(questions, openai.DecisionNewParamsQuestionUnion{
				OfPredicate: &openai.DecisionNewParamsQuestionPredicate{
					Name:         openai.String(q.Name),
					Instructions: instructions,
				},
			})

		case decision.QuestionChoice:
			choices := make([]openai.DecisionNewParamsQuestionChoiceChoice, 0, len(q.Choices))
			for _, c := range q.Choices {
				choice := openai.DecisionNewParamsQuestionChoiceChoice{
					Value: openai.DecisionNewParamsQuestionChoiceChoiceValueUnion{
						OfString: openai.String(c.Value),
					},
				}
				if c.Description != "" {
					choice.Description = openai.String(c.Description)
				}
				choices = append(choices, choice)
			}
			questions = append(questions, openai.DecisionNewParamsQuestionUnion{
				OfChoice: &openai.DecisionNewParamsQuestionChoice{
					Name:         openai.String(q.Name),
					Instructions: q.Instructions,
					Choices:      choices,
				},
			})

		case decision.QuestionScore:
			levels := make([]openai.DecisionNewParamsQuestionScoreLevel, 0, len(q.Levels))
			for _, l := range q.Levels {
				lvl := openai.DecisionNewParamsQuestionScoreLevel{
					Label: l.Label,
				}
				if l.Description != "" {
					lvl.Description = openai.String(l.Description)
				}
				levels = append(levels, lvl)
			}
			questions = append(questions, openai.DecisionNewParamsQuestionUnion{
				OfScore: &openai.DecisionNewParamsQuestionScore{
					Name:         openai.String(q.Name),
					Instructions: q.Instructions,
					Levels:       levels,
				},
			})

		default:
			return openai.DecisionNewParams{}, fmt.Errorf("unsupported question type: %s", q.Type)
		}
	}
	params.Questions = questions

	return params, nil
}

func toDecisionResponse(request *decision.Request, res *openai.Decision) *decision.Response {
	questionTypes := make(map[string]decision.QuestionType)
	if request != nil {
		for _, q := range request.Questions {
			questionTypes[q.Name] = q.Type
		}
	}

	answers := make(map[string]decision.Answer, len(res.Answers))
	for _, ansUnion := range res.Answers {
		switch ans := ansUnion.AsAny().(type) {
		case openai.DecisionAnswerPredicate:
			answers[ans.Name] = decision.Answer{
				Name:        ans.Name,
				Type:        decision.QuestionPredicate,
				Probability: ans.Probability,
			}

		case openai.DecisionAnswerChoice:
			choiceProbs := make([]decision.OptionProbability, len(ans.Probabilities))
			for i, item := range ans.Probabilities {
				choiceProbs[i] = decision.OptionProbability{
					Value:       item.Value.AsString(),
					Probability: item.Probability,
				}
			}
			answers[ans.Name] = decision.Answer{
				Name:                ans.Name,
				Type:                decision.QuestionChoice,
				Choice:              ans.Choice.AsString(),
				Confidence:          ans.Confidence,
				ChoiceProbabilities: choiceProbs,
			}

		case openai.DecisionAnswerScore:
			scoreProbs := make([]decision.LevelProbability, len(ans.Probabilities))
			for i, item := range ans.Probabilities {
				scoreProbs[i] = decision.LevelProbability{
					Index:       int(item.Value),
					Label:       item.Label,
					Probability: item.Probability,
				}
			}
			answers[ans.Name] = decision.Answer{
				Name:               ans.Name,
				Type:               decision.QuestionScore,
				Score:              ans.Score,
				Confidence:         ans.Confidence,
				ScoreProbabilities: scoreProbs,
			}

		case openai.DecisionAnswerRefusal:
			qType := decision.QuestionType("")
			if t, ok := questionTypes[ans.Name]; ok {
				qType = t
			}
			answers[ans.Name] = decision.Answer{
				Name:    ans.Name,
				Type:    qType,
				Refused: true,
			}

		default:
			name := ansUnion.Name
			qType := decision.QuestionType(ansUnion.Type)
			if t, ok := questionTypes[name]; ok {
				qType = t
			}
			if name != "" {
				answers[name] = decision.Answer{
					Name: name,
					Type: qType,
				}
			}
		}
	}

	model := res.Model
	if model == "" && request != nil {
		model = request.Model
	}

	return &decision.Response{
		Model:   model,
		Answers: answers,
		Metrics: &message.TokenMetrics{
			TotalTokens: int(res.Usage.InputTokens + res.Usage.OutputTokens),
			Tokens: message.TokenDetails{
				Input:  int(res.Usage.InputTokens),
				Output: int(res.Usage.OutputTokens),
			},
		},
	}
}
