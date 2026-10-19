package loomtypesafe

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/masterkeysrd/loom/decision"
	"github.com/masterkeysrd/loom/message"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

var (
	_ decision.Provider        = (*Provider)(nil)
	_ decision.ProfileProvider = (*Provider)(nil)
)

const (
	// DefaultBaseURL is the default TypeSafe AI API base URL.
	DefaultBaseURL = "https://api.typesafe.ai"

	// MaxBodySize is the maximum uncompressed request payload size allowed (32 MiB).
	MaxBodySize = 32 * 1024 * 1024

	// CompressionThreshold is the minimum body size in bytes to trigger gzip compression (1 KiB).
	CompressionThreshold = 1024

	// LongContextExtensionID is the unique identifier for the LongContext extension.
	LongContextExtensionID = "typesafe.long_context"
)

// LongContext enables the TypeSafe-Danger-Zone-Long-Context: true request header,
// raising context limits from 64k per-request / 32k per-question to 262k tokens.
type LongContext struct {
	Enabled bool `json:"enabled"`
}

// ExtensionID returns the extension identifier for LongContext.
func (LongContext) ExtensionID() string { return LongContextExtensionID }

// Provider implements decision.Provider and decision.ProfileProvider for TypeSafe AI.
type Provider struct {
	apiKey     string
	baseURL    *url.URL
	httpClient *http.Client
	compress   bool
	maxRetries int
}

// Option configures a Provider.
type Option func(*Provider)

// WithAPIKey sets the TypeSafe AI API key.
func WithAPIKey(apiKey string) Option {
	return func(p *Provider) {
		p.apiKey = apiKey
	}
}

// WithBaseURL sets the base URL for the TypeSafe AI service.
func WithBaseURL(rawURL string) Option {
	return func(p *Provider) {
		rawURL = strings.TrimSpace(rawURL)
		if rawURL == "" {
			p.baseURL = nil
			return
		}
		u, err := url.Parse(rawURL)
		if err != nil {
			p.baseURL = nil
			return
		}
		p.baseURL = u
	}
}

// WithHTTPClient sets a custom HTTP client for API requests.
func WithHTTPClient(client *http.Client) Option {
	return func(p *Provider) {
		if client != nil {
			p.httpClient = client
		}
	}
}

// WithCompression enables or disables gzip compression for requests >= 1 KiB.
func WithCompression(enabled bool) Option {
	return func(p *Provider) {
		p.compress = enabled
	}
}

// WithMaxRetries sets the maximum retry attempts on HTTP 429 and 529 responses.
func WithMaxRetries(maxRetries int) Option {
	return func(p *Provider) {
		p.maxRetries = maxRetries
	}
}

// NewProvider initializes a new TypeSafe AI decision provider with the given options.
func NewProvider(opts ...Option) (*Provider, error) {
	defaultURL, err := url.Parse(DefaultBaseURL)
	if err != nil {
		return nil, fmt.Errorf("typesafe: invalid default base URL: %w", err)
	}

	p := &Provider{
		baseURL:    defaultURL,
		httpClient: http.DefaultClient,
		compress:   true,
		maxRetries: 3,
	}

	for _, opt := range opts {
		if opt != nil {
			opt(p)
		}
	}

	if p.apiKey == "" {
		return nil, errors.New("typesafe: api key is required")
	}
	if p.baseURL == nil || p.baseURL.String() == "" {
		return nil, errors.New("typesafe: valid base URL is required")
	}
	if p.httpClient == nil {
		p.httpClient = http.DefaultClient
	}
	if p.maxRetries < 0 {
		p.maxRetries = 0
	}

	return p, nil
}

// NewDefaultProvider creates a Provider reading TYPESAFE_API_KEY and optional
// TYPESAFE_BASE_URL from environment variables.
func NewDefaultProvider() (*Provider, error) {
	apiKey := os.Getenv("TYPESAFE_API_KEY")
	if apiKey == "" {
		return nil, errors.New("typesafe: TYPESAFE_API_KEY environment variable is not set")
	}

	var opts []Option
	opts = append(opts, WithAPIKey(apiKey))

	if baseURL := os.Getenv("TYPESAFE_BASE_URL"); baseURL != "" {
		opts = append(opts, WithBaseURL(baseURL))
	}

	return NewProvider(opts...)
}

// Name returns the provider identifier "typesafe".
func (p *Provider) Name() string {
	return "typesafe"
}

// ModelInfo describes a model returned by the TypeSafe AI models endpoint.
type ModelInfo struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	ReleaseDate string `json:"release_date"`
}

type listModelsResponse struct {
	Models []ModelInfo `json:"models"`
}

// ListModels queries the TypeSafe AI /v1/models endpoint to list available models.
func (p *Provider) ListModels(ctx context.Context) ([]ModelInfo, error) {
	if p == nil {
		return nil, errors.New("typesafe: provider is not initialized")
	}

	endpoint := p.endpointURL("/v1/models")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create list models request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+p.apiKey)

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to execute list models request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		var errResp struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(body, &errResp) == nil && errResp.Error != "" {
			return nil, fmt.Errorf("typesafe API error (status %d): %s", resp.StatusCode, errResp.Error)
		}
		return nil, fmt.Errorf("typesafe API error (status %d): %s", resp.StatusCode, string(body))
	}

	var res listModelsResponse
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return nil, fmt.Errorf("failed to decode list models response: %w", err)
	}
	return res.Models, nil
}

type systemOneQuestion struct {
	Type         string          `json:"type"`
	Instructions string          `json:"instructions"`
	Criteria     json.RawMessage `json:"criteria,omitempty"`
}

type systemOneRequest struct {
	State     any                          `json:"state"`
	Model     string                       `json:"model"`
	Questions map[string]systemOneQuestion `json:"questions"`
}

type systemOneResponse struct {
	Model   string                     `json:"model"`
	Answers map[string]json.RawMessage `json:"answers"`
	Usage   struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
}

// Evaluate executes a decision evaluation request for the given questions using
// TypeSafe AI's System One API endpoint (/v1/systemone).
func (p *Provider) Evaluate(ctx context.Context, request *decision.Request) (*decision.Response, error) {
	if p == nil {
		return nil, errors.New("typesafe: provider is not initialized")
	}
	if request == nil {
		return nil, errors.New("typesafe: decision request cannot be nil")
	}
	if len(request.Input.Images) > 0 {
		return nil, errors.New("typesafe provider does not support image inputs")
	}

	questions := make(map[string]systemOneQuestion, len(request.Questions))
	for _, q := range request.Questions {
		switch q.Type {
		case decision.QuestionPredicate:
			soq := systemOneQuestion{
				Type:         "noul",
				Instructions: q.Instructions,
			}
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
			questions[q.Name] = soq

		case decision.QuestionChoice:
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
			questions[q.Name] = systemOneQuestion{
				Type:         "choice",
				Instructions: q.Instructions,
				Criteria:     json.RawMessage(buf.Bytes()),
			}

		case decision.QuestionScore:
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
			questions[q.Name] = systemOneQuestion{
				Type:         "score",
				Instructions: q.Instructions,
				Criteria:     json.RawMessage(critBytes),
			}

		default:
			return nil, fmt.Errorf("unsupported question type %q for question %q", q.Type, q.Name)
		}
	}

	stateVal := request.Input.State
	if stateVal == nil {
		stateVal = ""
	}

	sysReq := systemOneRequest{
		State:     stateVal,
		Model:     request.Model,
		Questions: questions,
	}

	uncompressedBody, err := json.Marshal(sysReq)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal systemone request: %w", err)
	}

	if len(uncompressedBody) > MaxBodySize {
		return nil, fmt.Errorf("request body size (%d bytes) exceeds maximum allowed size (%d bytes)", len(uncompressedBody), MaxBodySize)
	}

	var bodyBytes []byte
	isGzip := false
	if p.compress && len(uncompressedBody) >= CompressionThreshold {
		var gzBuf bytes.Buffer
		gw := gzip.NewWriter(&gzBuf)
		if _, err := gw.Write(uncompressedBody); err != nil {
			return nil, fmt.Errorf("failed to compress request body: %w", err)
		}
		if err := gw.Close(); err != nil {
			return nil, fmt.Errorf("failed to close gzip writer: %w", err)
		}
		bodyBytes = gzBuf.Bytes()
		isGzip = true
	} else {
		bodyBytes = uncompressedBody
	}

	isLongContext := false
	if request.Extensions != nil {
		if ext, ok := request.Extensions[LongContextExtensionID]; ok {
			if lc, ok := ext.(LongContext); ok && lc.Enabled {
				isLongContext = true
			} else if lcPtr, ok := ext.(*LongContext); ok && lcPtr != nil && lcPtr.Enabled {
				isLongContext = true
			}
		}
	}
	if !isLongContext && request.Options != nil {
		if lc, ok := request.Options["long_context"].(bool); ok && lc {
			isLongContext = true
		}
	}

	span := trace.SpanFromContext(ctx)
	attrs := []attribute.KeyValue{
		attribute.String("typesafe.api.type", "systemone"),
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

	endpoint := p.endpointURL("/v1/systemone")

	var resp *http.Response
	for attempt := 0; attempt <= p.maxRetries; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(bodyBytes))
		if err != nil {
			return nil, fmt.Errorf("failed to create systemone request: %w", err)
		}
		req.Header.Set("Authorization", "Bearer "+p.apiKey)
		req.Header.Set("Content-Type", "application/json")
		if isGzip {
			req.Header.Set("Content-Encoding", "gzip")
		}
		if isLongContext {
			req.Header.Set("TypeSafe-Danger-Zone-Long-Context", "true")
		}

		resp, err = p.httpClient.Do(req)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			return nil, fmt.Errorf("failed to execute systemone request: %w", err)
		}

		if (resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == 529) && attempt < p.maxRetries {
			var delay time.Duration
			hasRetryAfter := false
			if retryAfter := resp.Header.Get("Retry-After"); retryAfter != "" {
				if sec, err := strconv.Atoi(retryAfter); err == nil && sec >= 0 {
					delay = time.Duration(sec) * time.Second
					hasRetryAfter = true
				}
			}
			if !hasRetryAfter {
				delay = 100 * time.Millisecond * (1 << attempt)
			}

			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()

			select {
			case <-time.After(delay):
				continue
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}

		break
	}

	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		var errResp struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(body, &errResp) == nil && errResp.Error != "" {
			return nil, fmt.Errorf("typesafe API error (status %d): %s", resp.StatusCode, errResp.Error)
		}
		return nil, fmt.Errorf("typesafe API error (status %d): %s", resp.StatusCode, string(body))
	}

	var sysResp systemOneResponse
	if err := json.NewDecoder(resp.Body).Decode(&sysResp); err != nil {
		return nil, fmt.Errorf("failed to decode systemone response: %w", err)
	}

	return toDecisionResponse(&sysResp, request)
}

func toDecisionResponse(sysResp *systemOneResponse, request *decision.Request) (*decision.Response, error) {
	questionMap := make(map[string]decision.Question, len(request.Questions))
	for _, q := range request.Questions {
		questionMap[q.Name] = q
	}

	answers := make(map[string]decision.Answer, len(sysResp.Answers))

	for name, rawJSON := range sysResp.Answers {
		var base struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(rawJSON, &base); err != nil {
			return nil, fmt.Errorf("failed to unmarshal answer %q: %w", name, err)
		}

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
		if ansType == "predicate" {
			ansType = "noul"
		}

		switch ansType {
		case "noul":
			var raw struct {
				Type       string  `json:"type"`
				Noul       float64 `json:"noul"`
				Confidence float64 `json:"confidence,omitempty"`
			}
			if err := json.Unmarshal(rawJSON, &raw); err != nil {
				return nil, fmt.Errorf("failed to unmarshal noul answer %q: %w", name, err)
			}
			ans := decision.Answer{
				Name:        name,
				Type:        decision.QuestionPredicate,
				Probability: raw.Noul,
				Confidence:  raw.Confidence,
			}
			answers[name] = ans

		case "choice":
			var raw struct {
				Type          string             `json:"type"`
				Choice        string             `json:"choice"`
				Probabilities map[string]float64 `json:"probabilities"`
				Confidence    float64            `json:"confidence"`
			}
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
			var raw struct {
				Type          string             `json:"type"`
				Score         float64            `json:"score"`
				Legend        map[string]string  `json:"legend"`
				Probabilities map[string]float64 `json:"probabilities"`
				Confidence    float64            `json:"confidence"`
			}
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

	metrics := &message.TokenMetrics{
		TotalTokens: sysResp.Usage.InputTokens + sysResp.Usage.OutputTokens,
		Tokens: message.TokenDetails{
			Input:  sysResp.Usage.InputTokens,
			Output: sysResp.Usage.OutputTokens,
		},
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

func (p *Provider) endpointURL(path string) string {
	u := *p.baseURL
	if strings.HasSuffix(u.Path, "/") && strings.HasPrefix(path, "/") {
		u.Path += path[1:]
	} else if !strings.HasSuffix(u.Path, "/") && !strings.HasPrefix(path, "/") {
		u.Path += "/" + path
	} else {
		u.Path += path
	}
	return u.String()
}
