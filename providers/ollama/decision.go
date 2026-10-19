package loomollama

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"

	"github.com/masterkeysrd/loom/decision"
	"github.com/masterkeysrd/loom/message"
	"github.com/ollama/ollama/api"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

var _ decision.Provider = (*Provider)(nil)

// Evaluate executes a decision evaluation request for the given questions using
// Ollama's native SystemOne API.
func (p *Provider) Evaluate(ctx context.Context, request *decision.Request) (*decision.Response, error) {
	if p == nil || p.client == nil {
		return nil, errors.New("ollama client is not initialized")
	}
	if request == nil {
		return nil, errors.New("decision request cannot be nil")
	}

	sysReq, err := toSystemOneRequest(request)
	if err != nil {
		return nil, fmt.Errorf("failed to build systemone request: %w", err)
	}

	// Decorate OTel span with ollama.api.type = "systemone"
	span := trace.SpanFromContext(ctx)
	attrs := []attribute.KeyValue{
		attribute.String("ollama.api.type", "systemone"),
	}
	if p.baseURL != nil {
		attrs = append(attrs, attribute.String("server.address", p.baseURL.Hostname()))
		if portStr := p.baseURL.Port(); portStr != "" {
			if port, err := strconv.Atoi(portStr); err == nil {
				attrs = append(attrs, attribute.Int("server.port", port))
			}
		} else if p.baseURL.Scheme == "https" {
			attrs = append(attrs, attribute.Int("server.port", 443))
		} else if p.baseURL.Scheme == "http" {
			attrs = append(attrs, attribute.Int("server.port", 80))
		}
	}
	span.SetAttributes(attrs...)

	sysResp, err := p.client.SystemOne(ctx, sysReq)
	if err != nil {
		return nil, fmt.Errorf("failed to execute systemone call: %w", err)
	}
	if sysResp == nil {
		return nil, errors.New("received nil response from ollama systemone")
	}

	return toDecisionResponse(sysResp, request)
}

func toSystemOneRequest(request *decision.Request) (*api.SystemOneRequest, error) {
	// Marshal state
	var stateRaw json.RawMessage
	if request.Input.State == nil {
		data, err := json.Marshal("")
		if err != nil {
			return nil, fmt.Errorf("failed to marshal empty state: %w", err)
		}
		stateRaw = json.RawMessage(data)
	} else {
		data, err := json.Marshal(request.Input.State)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal state: %w", err)
		}
		stateRaw = json.RawMessage(data)
	}

	// Multimodal images
	var images []api.ImageData
	for _, img := range request.Input.Images {
		if len(img.Data) > 0 {
			images = append(images, api.ImageData(img.Data))
		}
	}

	// Questions
	questions := new(api.SystemOneQuestions)
	_ = questions.UnmarshalJSON([]byte("{}"))

	for _, q := range request.Questions {
		instBytes, err := json.Marshal(q.Instructions)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal instructions for question %q: %w", q.Name, err)
		}

		soq := api.SystemOneQuestion{
			Instructions: json.RawMessage(instBytes),
		}

		switch q.Type {
		case decision.QuestionPredicate:
			soq.Type = "noul"
			if q.TrueDescription != "" || q.FalseDescription != "" {
				crit := make(map[string]string)
				if q.TrueDescription != "" {
					crit["true"] = q.TrueDescription
				}
				if q.FalseDescription != "" {
					crit["false"] = q.FalseDescription
				}
				critBytes, err := json.Marshal(crit)
				if err != nil {
					return nil, fmt.Errorf("failed to marshal criteria for question %q: %w", q.Name, err)
				}
				soq.Criteria = json.RawMessage(critBytes)
			}

		case decision.QuestionChoice:
			soq.Type = "choice"
			var buf bytes.Buffer
			buf.WriteByte('{')
			for i, c := range q.Choices {
				if i > 0 {
					buf.WriteByte(',')
				}
				keyBytes, err := json.Marshal(c.Value)
				if err != nil {
					return nil, fmt.Errorf("failed to marshal choice value %q in question %q: %w", c.Value, q.Name, err)
				}
				buf.Write(keyBytes)
				buf.WriteByte(':')
				if c.Description != "" {
					descBytes, err := json.Marshal(c.Description)
					if err != nil {
						return nil, fmt.Errorf("failed to marshal choice description in question %q: %w", q.Name, err)
					}
					buf.Write(descBytes)
				} else {
					buf.WriteString("null")
				}
			}
			buf.WriteByte('}')
			soq.Criteria = json.RawMessage(buf.Bytes())

		case decision.QuestionScore:
			soq.Type = "score"
			criteriaList := make([]string, len(q.Levels))
			for i, lvl := range q.Levels {
				if lvl.Description != "" {
					criteriaList[i] = lvl.Label + ": " + lvl.Description
				} else {
					criteriaList[i] = lvl.Label
				}
			}
			critBytes, err := json.Marshal(criteriaList)
			if err != nil {
				return nil, fmt.Errorf("failed to marshal criteria for question %q: %w", q.Name, err)
			}
			soq.Criteria = json.RawMessage(critBytes)

		default:
			return nil, fmt.Errorf("unsupported question type %q for question %q", q.Type, q.Name)
		}

		questions.Set(q.Name, soq)
	}

	sysReq := &api.SystemOneRequest{
		Model:     request.Model,
		State:     stateRaw,
		Images:    images,
		Questions: questions,
	}

	if request.Options != nil {
		if runner, ok := request.Options["runner"].(string); ok && runner != "" {
			sysReq.Runner = runner
		}
	}

	return sysReq, nil
}

type baseAnswer struct {
	Type string `json:"type"`
}

type rawNoulAnswer struct {
	Type       string  `json:"type"`
	Noul       float64 `json:"noul"`
	Confidence float64 `json:"confidence,omitempty"`
}

type rawChoiceAnswer struct {
	Type          string             `json:"type"`
	Choice        string             `json:"choice"`
	Probabilities map[string]float64 `json:"probabilities"`
	Confidence    float64            `json:"confidence"`
}

type rawScoreAnswer struct {
	Type          string             `json:"type"`
	Score         float64            `json:"score"`
	Legend        map[string]string  `json:"legend"`
	Probabilities map[string]float64 `json:"probabilities"`
	Confidence    float64            `json:"confidence"`
}

func toDecisionResponse(sysResp *api.SystemOneResponse, request *decision.Request) (*decision.Response, error) {
	questionMap := make(map[string]decision.Question, len(request.Questions))
	for _, q := range request.Questions {
		questionMap[q.Name] = q
	}

	answers := make(map[string]decision.Answer)

	if sysResp.Answers != nil {
		for name, rawJSON := range sysResp.Answers.All() {
			var base baseAnswer
			if err := json.Unmarshal(rawJSON, &base); err != nil {
				return nil, fmt.Errorf("failed to unmarshal answer %q: %w", name, err)
			}

			// Fallback type from request question if empty in response
			ansType := base.Type
			if ansType == "" {
				if q, ok := questionMap[name]; ok {
					switch q.Type {
					case decision.QuestionPredicate:
						ansType = "noul"
					case decision.QuestionChoice:
						ansType = "choice"
					case decision.QuestionScore:
						ansType = "score"
					}
				}
			}

			switch ansType {
			case "noul":
				var raw rawNoulAnswer
				if err := json.Unmarshal(rawJSON, &raw); err != nil {
					return nil, fmt.Errorf("failed to unmarshal noul answer %q: %w", name, err)
				}
				ans := decision.Answer{
					Name:        name,
					Type:        decision.QuestionPredicate,
					Probability: raw.Noul,
				}
				if raw.Confidence > 0 {
					ans.Confidence = raw.Confidence
				}
				answers[name] = ans

			case "choice":
				var raw rawChoiceAnswer
				if err := json.Unmarshal(rawJSON, &raw); err != nil {
					return nil, fmt.Errorf("failed to unmarshal choice answer %q: %w", name, err)
				}
				ans := decision.Answer{
					Name:       name,
					Type:       decision.QuestionChoice,
					Choice:     raw.Choice,
					Confidence: raw.Confidence,
				}
				if prob, ok := raw.Probabilities[raw.Choice]; ok {
					ans.Probability = prob
				}

				var choiceProbs []decision.OptionProbability
				if q, ok := questionMap[name]; ok && len(q.Choices) > 0 {
					seen := make(map[string]bool, len(q.Choices))
					for _, c := range q.Choices {
						var prob float64
						if raw.Probabilities != nil {
							prob = raw.Probabilities[c.Value]
						}
						choiceProbs = append(choiceProbs, decision.OptionProbability{
							Value:       c.Value,
							Probability: prob,
						})
						seen[c.Value] = true
					}
					if len(choiceProbs) < len(raw.Probabilities) {
						var extraKeys []string
						for k := range raw.Probabilities {
							if !seen[k] {
								extraKeys = append(extraKeys, k)
							}
						}
						sort.Strings(extraKeys)
						for _, k := range extraKeys {
							choiceProbs = append(choiceProbs, decision.OptionProbability{
								Value:       k,
								Probability: raw.Probabilities[k],
							})
						}
					}
				} else if len(raw.Probabilities) > 0 {
					keys := make([]string, 0, len(raw.Probabilities))
					for k := range raw.Probabilities {
						keys = append(keys, k)
					}
					sort.Strings(keys)
					for _, k := range keys {
						choiceProbs = append(choiceProbs, decision.OptionProbability{
							Value:       k,
							Probability: raw.Probabilities[k],
						})
					}
				}
				ans.ChoiceProbabilities = choiceProbs
				answers[name] = ans

			case "score":
				var raw rawScoreAnswer
				if err := json.Unmarshal(rawJSON, &raw); err != nil {
					return nil, fmt.Errorf("failed to unmarshal score answer %q: %w", name, err)
				}
				ans := decision.Answer{
					Name:       name,
					Type:       decision.QuestionScore,
					Score:      raw.Score,
					Confidence: raw.Confidence,
				}

				var scoreProbs []decision.LevelProbability
				if q, ok := questionMap[name]; ok && len(q.Levels) > 0 {
					scoreProbs = make([]decision.LevelProbability, len(q.Levels))
					for i := 0; i < len(q.Levels); i++ {
						var prob float64
						if raw.Probabilities != nil {
							prob = raw.Probabilities[strconv.Itoa(i)]
						}
						scoreProbs[i] = decision.LevelProbability{
							Index:       i,
							Label:       q.Levels[i].Label,
							Probability: prob,
						}
					}
				} else if len(raw.Legend) > 0 {
					scoreProbs = make([]decision.LevelProbability, len(raw.Legend))
					for i := 0; i < len(raw.Legend); i++ {
						idxStr := strconv.Itoa(i)
						var prob float64
						if raw.Probabilities != nil {
							prob = raw.Probabilities[idxStr]
						}
						scoreProbs[i] = decision.LevelProbability{
							Index:       i,
							Label:       raw.Legend[idxStr],
							Probability: prob,
						}
					}
				}
				ans.ScoreProbabilities = scoreProbs
				answers[name] = ans

			default:
				return nil, fmt.Errorf("unsupported answer type %q for question %q", ansType, name)
			}
		}
	}

	metrics := &message.TokenMetrics{
		TotalTokens: sysResp.Usage.InputTokens + sysResp.Usage.OutputTokens,
		Tokens: message.TokenDetails{
			Input:  sysResp.Usage.InputTokens,
			Output: sysResp.Usage.OutputTokens,
		},
	}
	if sysResp.PromptEvalCachedCount != nil {
		metrics.Tokens.CacheRead = *sysResp.PromptEvalCachedCount
	}

	modelName := sysResp.Model
	if modelName == "" {
		modelName = request.Model
	}

	return &decision.Response{
		Model:   modelName,
		Answers: answers,
		Metrics: metrics,
	}, nil
}
