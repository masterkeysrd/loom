package decision

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/masterkeysrd/loom/llm"
	"github.com/masterkeysrd/loom/message"
)

var (
	_ Provider        = (*LLMAdapter)(nil)
	_ ProfileProvider = (*LLMAdapter)(nil)
)

// LLMAdapter adapts any [llm.Model] to implement the [Provider] interface,
// providing structured evaluation of probabilistic decision questions using
// structured outputs.
type LLMAdapter struct {
	model     *llm.Model
	modelName string
	provider  llm.Provider
	profile   *llm.ModelProfile
}

// NewLLMAdapter creates a new decision provider backed by an LLM model.
// Returns an error if model is nil.
func NewLLMAdapter(model *llm.Model) (*LLMAdapter, error) {
	if model == nil {
		return nil, errors.New("decision: model cannot be nil")
	}

	name, provider, profile := extractModelInfo(model)

	return &LLMAdapter{
		model:     model,
		modelName: name,
		provider:  provider,
		profile:   profile,
	}, nil
}

// NewFromLLM is a convenience constructor that wraps an [llm.Model] in an [LLMAdapter]
// and returns a decision [Model].
func NewFromLLM(model *llm.Model, config *ModelConfig) (*Model, error) {
	adapter, err := NewLLMAdapter(model)
	if err != nil {
		return nil, err
	}

	name := adapter.modelName
	if name == "" {
		name = "llm"
	}

	return NewModel(adapter, name, config)
}

// Name returns the provider identifier.
func (a *LLMAdapter) Name() string {
	if a.provider != nil && a.provider.Name() != "" {
		return a.provider.Name()
	}
	return "llm"
}

// GetProfile returns the model profile if available from the underlying provider or model.
func (a *LLMAdapter) GetProfile(id string) (llm.ModelProfile, bool) {
	if a.provider != nil {
		if p, ok := a.provider.GetProfile(id); ok {
			return p, true
		}
	}
	if a.profile != nil && (id == "" || id == a.modelName || id == a.profile.Name) {
		return *a.profile, true
	}
	return llm.ModelProfile{}, false
}

// Evaluate evaluates one or more decision questions against the input context using structured LLM generation.
func (a *LLMAdapter) Evaluate(ctx context.Context, req *Request) (*Response, error) {
	if req == nil {
		return nil, errors.New("decision: request cannot be nil")
	}
	if len(req.Questions) == 0 {
		return nil, errors.New("decision: at least one question must be provided")
	}

	sysPrompt, userPrompt, err := buildPrompt(req)
	if err != nil {
		return nil, err
	}

	userBlocks := make([]message.Block, 0, 1+len(req.Input.Images))
	userBlocks = append(userBlocks, &message.TextBlock{Text: userPrompt})
	for _, img := range req.Input.Images {
		imgCopy := img
		userBlocks = append(userBlocks, &imgCopy)
	}

	messages := []message.Message{
		message.NewSystemText(sysPrompt),
		message.NewUser(userBlocks...),
	}

	schema := buildSchema(req.Questions)

	var callOpts []llm.CallOption
	for _, ext := range req.Extensions {
		if llmExt, ok := ext.(llm.Extension); ok {
			callOpts = append(callOpts, llm.WithExtensionOption(llmExt))
		}
	}

	msg, err := a.model.WithStructuredOutput(schema).Invoke(ctx, messages, callOpts...)
	if err != nil {
		return nil, err
	}

	modelName := req.Model
	if modelName == "" {
		modelName = a.modelName
	}

	resp := &Response{
		Model:   modelName,
		Answers: make(map[string]Answer, len(req.Questions)),
		Metrics: msg.Metrics,
	}

	rawText := cleanJSON(msg.Content.Text())
	answersMap, err := extractAnswersMap(rawText, req.Questions)
	if err != nil {
		return nil, fmt.Errorf("decision: failed to parse LLM structured output: %w", err)
	}

	for _, q := range req.Questions {
		rawQ, ok := answersMap[q.Name]
		if !ok {
			resp.Answers[q.Name] = Answer{
				Name:    q.Name,
				Type:    q.Type,
				Refused: true,
			}
			continue
		}

		ans, err := parseAnswer(q, rawQ)
		if err != nil {
			resp.Answers[q.Name] = Answer{
				Name:    q.Name,
				Type:    q.Type,
				Refused: true,
			}
			continue
		}
		resp.Answers[q.Name] = ans
	}

	return resp, nil
}

type rawAnswerItem struct {
	Refused       bool            `json:"refused"`
	Probability   *float64        `json:"probability"`
	Choice        string          `json:"choice"`
	Probabilities json.RawMessage `json:"probabilities"`
}

func parseAnswer(q Question, rawData json.RawMessage) (Answer, error) {
	var item rawAnswerItem
	if err := json.Unmarshal(rawData, &item); err != nil {
		return Answer{}, err
	}

	switch q.Type {
	case QuestionPredicate:
		ans := Answer{
			Name:    q.Name,
			Type:    QuestionPredicate,
			Refused: item.Refused,
		}

		var prob float64
		if item.Probability != nil {
			prob = *item.Probability
		} else if len(item.Probabilities) > 0 {
			var m map[string]float64
			if err := json.Unmarshal(item.Probabilities, &m); err == nil {
				if p, ok := m["true"]; ok {
					prob = p
				}
			} else {
				var arr []float64
				if err := json.Unmarshal(item.Probabilities, &arr); err == nil && len(arr) > 0 {
					prob = arr[0]
				}
			}
		}

		// Clamp to [0, 1]
		if prob < 0 {
			prob = 0
		} else if prob > 1 {
			prob = 1
		}
		ans.Probability = prob

		if !ans.Refused {
			ans.Confidence = ConfidenceFromProbabilities([]float64{prob, 1.0 - prob})
		}
		return ans, nil

	case QuestionChoice:
		ans := Answer{
			Name:    q.Name,
			Type:    QuestionChoice,
			Refused: item.Refused,
		}

		var rawProbs any
		if len(item.Probabilities) > 0 {
			_ = json.Unmarshal(item.Probabilities, &rawProbs)
		}

		ans.ChoiceProbabilities = parseChoiceProbabilities(rawProbs, q.Choices)

		// Pick choice: highest probability if not set or invalid
		choice := strings.TrimSpace(item.Choice)
		valid := false
		for _, ch := range q.Choices {
			if ch.Value == choice {
				valid = true
				break
			}
		}

		if (!valid || choice == "") && len(ans.ChoiceProbabilities) > 0 {
			bestChoice := ans.ChoiceProbabilities[0].Value
			bestProb := ans.ChoiceProbabilities[0].Probability
			for _, cp := range ans.ChoiceProbabilities[1:] {
				if cp.Probability > bestProb {
					bestProb = cp.Probability
					bestChoice = cp.Value
				}
			}
			choice = bestChoice
		}
		ans.Choice = choice

		probs := make([]float64, len(ans.ChoiceProbabilities))
		for i, cp := range ans.ChoiceProbabilities {
			probs[i] = cp.Probability
		}
		if !ans.Refused {
			ans.Confidence = ConfidenceFromProbabilities(probs)
		}
		return ans, nil

	case QuestionScore:
		ans := Answer{
			Name:    q.Name,
			Type:    QuestionScore,
			Refused: item.Refused,
		}

		var rawProbs any
		if len(item.Probabilities) > 0 {
			_ = json.Unmarshal(item.Probabilities, &rawProbs)
		}

		ans.ScoreProbabilities = parseScoreProbabilities(rawProbs, q.Levels)

		// Compute score as probability-weighted average sum(i * P(i))
		var score float64
		for i, lp := range ans.ScoreProbabilities {
			score += float64(i) * lp.Probability
		}
		ans.Score = score

		probs := make([]float64, len(ans.ScoreProbabilities))
		for i, lp := range ans.ScoreProbabilities {
			probs[i] = lp.Probability
		}
		if !ans.Refused {
			ans.Confidence = ConfidenceFromProbabilities(probs)
		}
		return ans, nil

	default:
		return Answer{
			Name:    q.Name,
			Type:    q.Type,
			Refused: item.Refused,
		}, nil
	}
}

func parseChoiceProbabilities(rawProb any, choices []Option) []OptionProbability {
	n := len(choices)
	result := make([]OptionProbability, n)
	for i, ch := range choices {
		result[i] = OptionProbability{
			Value: ch.Value,
		}
	}
	if n == 0 {
		return result
	}

	rawVals := make([]float64, n)
	switch v := rawProb.(type) {
	case map[string]any:
		for i, ch := range choices {
			if val, ok := v[ch.Value]; ok {
				if num, ok := toFloat64(val); ok && num >= 0 {
					rawVals[i] = num
					continue
				}
			}
			for k, val := range v {
				if strings.EqualFold(k, ch.Value) {
					if num, ok := toFloat64(val); ok && num >= 0 {
						rawVals[i] = num
						break
					}
				}
			}
		}
	case map[string]float64:
		for i, ch := range choices {
			if val, ok := v[ch.Value]; ok && val >= 0 {
				rawVals[i] = val
			}
		}
	case []any:
		for i := 0; i < n && i < len(v); i++ {
			if num, ok := toFloat64(v[i]); ok && num >= 0 {
				rawVals[i] = num
			}
		}
	case []float64:
		for i := 0; i < n && i < len(v); i++ {
			if v[i] >= 0 {
				rawVals[i] = v[i]
			}
		}
	}

	var sum float64
	for _, val := range rawVals {
		sum += val
	}
	if sum > 0 {
		for i := range result {
			result[i].Probability = rawVals[i] / sum
		}
	} else {
		uniform := 1.0 / float64(n)
		for i := range result {
			result[i].Probability = uniform
		}
	}
	return result
}

func parseScoreProbabilities(rawProb any, levels []Level) []LevelProbability {
	n := len(levels)
	result := make([]LevelProbability, n)
	for i, lvl := range levels {
		result[i] = LevelProbability{
			Index: i,
			Label: lvl.Label,
		}
	}
	if n == 0 {
		return result
	}

	rawVals := make([]float64, n)
	switch v := rawProb.(type) {
	case []any:
		for i := 0; i < n && i < len(v); i++ {
			if num, ok := toFloat64(v[i]); ok && num >= 0 {
				rawVals[i] = num
			}
		}
	case []float64:
		for i := 0; i < n && i < len(v); i++ {
			if v[i] >= 0 {
				rawVals[i] = v[i]
			}
		}
	case map[string]any:
		for i, lvl := range levels {
			idxKey := strconv.Itoa(i)
			if val, ok := v[idxKey]; ok {
				if num, ok := toFloat64(val); ok && num >= 0 {
					rawVals[i] = num
					continue
				}
			}
			if val, ok := v[lvl.Label]; ok {
				if num, ok := toFloat64(val); ok && num >= 0 {
					rawVals[i] = num
					continue
				}
			}
			for k, val := range v {
				if strings.EqualFold(k, lvl.Label) {
					if num, ok := toFloat64(val); ok && num >= 0 {
						rawVals[i] = num
						break
					}
				}
			}
		}
	case map[string]float64:
		for i, lvl := range levels {
			idxKey := strconv.Itoa(i)
			if val, ok := v[idxKey]; ok && val >= 0 {
				rawVals[i] = val
				continue
			}
			if val, ok := v[lvl.Label]; ok && val >= 0 {
				rawVals[i] = val
				continue
			}
		}
	}

	var sum float64
	for _, val := range rawVals {
		sum += val
	}
	if sum > 0 {
		for i := range result {
			result[i].Probability = rawVals[i] / sum
		}
	} else {
		uniform := 1.0 / float64(n)
		for i := range result {
			result[i].Probability = uniform
		}
	}
	return result
}

func toFloat64(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	default:
		return 0, false
	}
}

func extractAnswersMap(rawText string, questions []Question) (map[string]json.RawMessage, error) {
	var rawMap map[string]json.RawMessage
	if err := json.Unmarshal([]byte(rawText), &rawMap); err != nil {
		return nil, err
	}

	answersMap := make(map[string]json.RawMessage)
	if ansData, ok := rawMap["answers"]; ok {
		var inner map[string]json.RawMessage
		if err := json.Unmarshal(ansData, &inner); err == nil {
			answersMap = inner
		}
	}

	for _, q := range questions {
		if _, ok := answersMap[q.Name]; !ok {
			if val, exists := rawMap[q.Name]; exists {
				answersMap[q.Name] = val
			}
		}
	}

	// Single question fallback: root object itself represents the answer
	if len(questions) == 1 {
		q := questions[0]
		if _, ok := answersMap[q.Name]; !ok {
			_, hasProb := rawMap["probability"]
			_, hasProbs := rawMap["probabilities"]
			_, hasChoice := rawMap["choice"]
			_, hasRefused := rawMap["refused"]
			if hasProb || hasProbs || hasChoice || hasRefused {
				answersMap[q.Name] = json.RawMessage(rawText)
			}
		}
	}

	return answersMap, nil
}

func buildSchema(questions []Question) *jsonschema.Schema {
	answersProps := make(map[string]*jsonschema.Schema, len(questions))
	answersRequired := make([]string, 0, len(questions))

	for _, q := range questions {
		answersRequired = append(answersRequired, q.Name)
		switch q.Type {
		case QuestionPredicate:
			minVal := 0.0
			maxVal := 1.0
			answersProps[q.Name] = &jsonschema.Schema{
				Type: "object",
				Properties: map[string]*jsonschema.Schema{
					"refused": {
						Type:        "boolean",
						Description: "Set to true if unable to evaluate the question",
					},
					"probability": {
						Type:        "number",
						Minimum:     &minVal,
						Maximum:     &maxVal,
						Description: "Probability between 0.0 and 1.0 that the condition is true",
					},
				},
				Required: []string{"refused", "probability"},
			}

		case QuestionChoice:
			choiceEnums := make([]any, len(q.Choices))
			probProps := make(map[string]*jsonschema.Schema, len(q.Choices))
			probReq := make([]string, len(q.Choices))
			minVal := 0.0
			maxVal := 1.0
			for i, opt := range q.Choices {
				choiceEnums[i] = opt.Value
				probReq[i] = opt.Value
				probProps[opt.Value] = &jsonschema.Schema{
					Type:    "number",
					Minimum: &minVal,
					Maximum: &maxVal,
				}
			}
			answersProps[q.Name] = &jsonschema.Schema{
				Type: "object",
				Properties: map[string]*jsonschema.Schema{
					"refused": {
						Type:        "boolean",
						Description: "Set to true if unable to evaluate the question",
					},
					"choice": {
						Type:        "string",
						Enum:        choiceEnums,
						Description: "The selected choice value",
					},
					"probabilities": {
						Type:        "object",
						Properties:  probProps,
						Required:    probReq,
						Description: "Probabilities assigned to each option (should sum to 1.0)",
					},
				},
				Required: []string{"refused", "choice", "probabilities"},
			}

		case QuestionScore:
			minVal := 0.0
			maxVal := 1.0
			minItems := len(q.Levels)
			maxItems := len(q.Levels)
			answersProps[q.Name] = &jsonschema.Schema{
				Type: "object",
				Properties: map[string]*jsonschema.Schema{
					"refused": {
						Type:        "boolean",
						Description: "Set to true if unable to evaluate the question",
					},
					"probabilities": {
						Type: "array",
						Items: &jsonschema.Schema{
							Type:    "number",
							Minimum: &minVal,
							Maximum: &maxVal,
						},
						MinItems:    &minItems,
						MaxItems:    &maxItems,
						Description: "Probabilities assigned to each rating level from index 0 to N-1 (should sum to 1.0)",
					},
				},
				Required: []string{"refused", "probabilities"},
			}
		}
	}

	return &jsonschema.Schema{
		Type: "object",
		Properties: map[string]*jsonschema.Schema{
			"answers": {
				Type:       "object",
				Properties: answersProps,
				Required:   answersRequired,
			},
		},
		Required: []string{"answers"},
	}
}

func buildPrompt(req *Request) (string, string, error) {
	sysPrompt := "You are an expert decision evaluation system. You evaluate the provided context against a set of structured questions and output calibrated probabilities and decisions in JSON format strictly conforming to the required schema."

	var sb strings.Builder
	stateStr, err := formatState(req.Input.State)
	if err != nil {
		return "", "", err
	}
	if stateStr != "" {
		sb.WriteString("## Context\n")
		sb.WriteString(stateStr)
		sb.WriteString("\n\n")
	}

	sb.WriteString("## Questions to Evaluate\n\n")
	for i, q := range req.Questions {
		fmt.Fprintf(&sb, "### %d. %s (type: %s)\n", i+1, q.Name, q.Type)
		if q.Instructions != "" {
			fmt.Fprintf(&sb, "Instructions: %s\n", q.Instructions)
		}
		switch q.Type {
		case QuestionPredicate:
			if q.TrueDescription != "" {
				fmt.Fprintf(&sb, "True condition: %s\n", q.TrueDescription)
			}
			if q.FalseDescription != "" {
				fmt.Fprintf(&sb, "False condition: %s\n", q.FalseDescription)
			}
		case QuestionChoice:
			sb.WriteString("Options:\n")
			for _, opt := range q.Choices {
				if opt.Description != "" {
					fmt.Fprintf(&sb, "- %s: %s\n", opt.Value, opt.Description)
				} else {
					fmt.Fprintf(&sb, "- %s\n", opt.Value)
				}
			}
		case QuestionScore:
			sb.WriteString("Levels (0 to N-1):\n")
			for idx, lvl := range q.Levels {
				if lvl.Description != "" {
					fmt.Fprintf(&sb, "- Level %d (%s): %s\n", idx, lvl.Label, lvl.Description)
				} else {
					fmt.Fprintf(&sb, "- Level %d (%s)\n", idx, lvl.Label)
				}
			}
		}
		sb.WriteString("\n")
	}

	sb.WriteString("Evaluate all questions objectively. Respond strictly with JSON adhering to the specified schema.")
	return sysPrompt, sb.String(), nil
}

func formatState(state any) (string, error) {
	if state == nil {
		return "", nil
	}
	switch v := state.(type) {
	case string:
		return v, nil
	case []byte:
		return string(v), nil
	default:
		b, err := json.MarshalIndent(v, "", "  ")
		if err != nil {
			return "", fmt.Errorf("failed to marshal input state: %w", err)
		}
		return string(b), nil
	}
}

func cleanJSON(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "```") {
		lines := strings.Split(s, "\n")
		if len(lines) >= 2 && strings.HasPrefix(lines[0], "```") {
			lines = lines[1:]
		}
		if len(lines) >= 1 && strings.HasPrefix(strings.TrimSpace(lines[len(lines)-1]), "```") {
			lines = lines[:len(lines)-1]
		}
		s = strings.TrimSpace(strings.Join(lines, "\n"))
	}
	return s
}

func extractModelInfo(m *llm.Model) (string, llm.Provider, *llm.ModelProfile) {
	if m == nil {
		return "", nil, nil
	}
	return m.Name(), m.Provider(), m.Profile()
}
