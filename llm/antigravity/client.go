package loomantigravity

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"

	"github.com/masterkeysrd/loom/llm"
	"github.com/masterkeysrd/loom/message"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

const (
	// DefaultBaseURL is the production Google Cloud Code / Antigravity API base URL.
	DefaultBaseURL = "https://cloudcode-pa.googleapis.com"

	// DailyBaseURL is the daily staging / canary environment URL.
	DailyBaseURL = "https://daily-cloudcode-pa.googleapis.com"

	// DefaultUserAgent emulates the standard Antigravity client identifier.
	DefaultUserAgent = "antigravity"
)

// ModelResolver translates a requested model ID and request into a target backend model ID
// and a modified request (e.g. injecting thinking level).
type ModelResolver func(requestedModel string, req GenerateContentRequest, availableModels map[string]bool) (string, GenerateContentRequest)

// Client handles direct HTTP communication with the Cloud Code streamGenerateContent endpoint.
type Client struct {
	baseURL         string
	httpClient      *http.Client
	tokenProvider   TokenProvider
	projectID       string
	userAgent       string
	aliases         map[string]string
	resolver        ModelResolver
	availableMu     sync.RWMutex
	availableModels map[string]bool
}

// ClientOption configures a Client.
type ClientOption func(*Client)

// WithBaseURL overrides the default API base URL.
func WithBaseURL(url string) ClientOption {
	return func(c *Client) {
		c.baseURL = strings.TrimRight(url, "/")
	}
}

// WithHTTPClient overrides the HTTP client.
func WithHTTPClient(client *http.Client) ClientOption {
	return func(c *Client) {
		c.httpClient = client
	}
}

// WithProjectID sets the Google Cloud project ID.
func WithProjectID(projectID string) ClientOption {
	return func(c *Client) {
		c.projectID = projectID
	}
}

// WithUserAgent sets a custom User-Agent header.
func WithUserAgent(userAgent string) ClientOption {
	return func(c *Client) {
		c.userAgent = userAgent
	}
}

// WithAliases sets custom model aliases (e.g. "fast" -> "gemini-3.8-flash-low").
func WithAliases(aliases map[string]string) ClientOption {
	return func(c *Client) {
		if c.aliases == nil {
			c.aliases = make(map[string]string)
		}
		for k, v := range aliases {
			c.aliases[k] = v
		}
	}
}

// WithResolver configures a custom ModelResolver function.
func WithResolver(resolver ModelResolver) ClientOption {
	return func(c *Client) {
		c.resolver = resolver
	}
}

// NewClient creates a new Client with the given TokenProvider and optional ClientOptions.
func NewClient(tokenProvider TokenProvider, opts ...ClientOption) *Client {
	baseURL := os.Getenv("ANTIGRAVITY_BASE_URL")
	if baseURL == "" {
		baseURL = DailyBaseURL
	}

	c := &Client{
		baseURL:         baseURL,
		httpClient:      &http.Client{Transport: otelhttp.NewTransport(http.DefaultTransport)},
		tokenProvider:   tokenProvider,
		userAgent:       DefaultUserAgent,
		availableModels: make(map[string]bool),
		aliases:         make(map[string]string),
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// StreamGenerateContent sends a streaming generation request to the Cloud Code endpoint
// and returns an iterator over message.AssistantChunk.
func (c *Client) StreamGenerateContent(ctx context.Context, model string, req GenerateContentRequest) (llm.StreamResponse, error) {
	model, req = c.resolveModel(model, req)
	token, err := c.tokenProvider.Token(ctx)
	if err != nil {
		return nil, fmt.Errorf("antigravity: acquire token: %w", err)
	}

	envelope := RequestEnvelope{
		Project:   c.projectID,
		Model:     model,
		Request:   req,
		UserAgent: c.userAgent,
	}

	bodyBytes, err := json.Marshal(envelope)
	if err != nil {
		return nil, fmt.Errorf("antigravity: marshal request: %w", err)
	}

	url := fmt.Sprintf("%s/v1internal:streamGenerateContent?alt=sse", c.baseURL)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("antigravity: create request: %w", err)
	}

	httpReq.Header.Set("Authorization", "Bearer "+token)
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")
	httpReq.Header.Set("User-Agent", c.userAgent)
	if c.projectID != "" {
		httpReq.Header.Set("X-Goog-User-Project", c.projectID)
	}

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("antigravity: post request: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("antigravity: API error (status %d): %s", resp.StatusCode, string(body))
	}

	return func(yield func(message.AssistantChunk, error) bool) {
		defer resp.Body.Close()

		scanner := bufio.NewScanner(resp.Body)
		// Allocate a generous buffer for large JSON chunks (up to 10MB)
		buf := make([]byte, 64*1024)
		scanner.Buffer(buf, 10*1024*1024)

		for scanner.Scan() {
			line := scanner.Text()
			line = strings.TrimSpace(line)

			if line == "" || strings.HasPrefix(line, ":") || strings.HasPrefix(line, "event:") {
				continue
			}

			if !strings.HasPrefix(line, "data:") {
				continue
			}

			payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			if payload == "" || payload == "[DONE]" {
				continue
			}

			var env ResponseEnvelope
			if err := json.Unmarshal([]byte(payload), &env); err != nil {
				if !yield(message.AssistantChunk{}, fmt.Errorf("antigravity: unmarshal sse chunk: %w", err)) {
					return
				}
				continue
			}

			result := env.Result()
			if result == nil {
				continue
			}

			chunk, err := toAssistantChunk(result, model)
			if err != nil {
				if !yield(message.AssistantChunk{}, fmt.Errorf("antigravity: convert chunk: %w", err)) {
					return
				}
				continue
			}

			if !yield(chunk, nil) {
				return
			}
		}

		if err := scanner.Err(); err != nil && err != io.EOF {
			yield(message.AssistantChunk{}, fmt.Errorf("antigravity: stream read error: %w", err))
		}
	}, nil
}

// QuotaInfo describes the remaining quota and reset window for a model.
// Type alias to [llm.QuotaInfo].
type QuotaInfo = llm.QuotaInfo

// AvailableModel describes an AI model and its capabilities returned dynamically by the Antigravity API.
type AvailableModel struct {
	ID                 string
	DisplayName        string
	Description        string
	MaxTokens          int
	MaxOutputTokens    int
	SupportsImages     bool
	SupportsVideo      bool
	SupportsThinking   bool
	ThinkingBudget     int
	MinThinkingBudget  int
	ThinkingLevel      int
	SupportedMimeTypes map[string]bool
	Quota              *QuotaInfo
	APIProvider        string
	ModelProvider      string
}

// FetchAvailableModels queries the /v1internal:fetchAvailableModels endpoint
// and returns the models currently exposed by the Antigravity backend.
func (c *Client) FetchAvailableModels(ctx context.Context) ([]AvailableModel, error) {
	token, err := c.tokenProvider.Token(ctx)
	if err != nil {
		return nil, fmt.Errorf("antigravity: acquire token: %w", err)
	}

	url := fmt.Sprintf("%s/v1internal:fetchAvailableModels", c.baseURL)
	payload := "{}"
	if c.projectID != "" {
		payload = fmt.Sprintf(`{"project":%q}`, c.projectID)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("antigravity: create models request: %w", err)
	}

	httpReq.Header.Set("Authorization", "Bearer "+token)
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("User-Agent", c.userAgent)
	if c.projectID != "" {
		httpReq.Header.Set("X-Goog-User-Project", c.projectID)
	}

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("antigravity: fetch models: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("antigravity: fetch models error (status %d): %s", resp.StatusCode, string(body))
	}

	var rawResp struct {
		Models map[string]struct {
			DisplayName        string          `json:"displayName"`
			Description        string          `json:"description"`
			MaxTokens          int             `json:"maxTokens"`
			MaxOutputTokens    int             `json:"maxOutputTokens"`
			SupportsImages     bool            `json:"supportsImages"`
			SupportsVideo      bool            `json:"supportsVideo"`
			SupportsThinking   bool            `json:"supportsThinking"`
			ThinkingBudget     int             `json:"thinkingBudget"`
			MinThinkingBudget  int             `json:"minThinkingBudget"`
			ThinkingLevel      int             `json:"thinkingLevel"`
			SupportedMimeTypes map[string]bool `json:"supportedMimeTypes"`
			QuotaInfo          *QuotaInfo      `json:"quotaInfo"`
			APIProvider        string          `json:"apiProvider"`
			ModelProvider      string          `json:"modelProvider"`
		} `json:"models"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&rawResp); err != nil {
		return nil, fmt.Errorf("antigravity: decode models response: %w", err)
	}

	c.availableMu.Lock()
	c.availableModels = make(map[string]bool, len(rawResp.Models))
	for id := range rawResp.Models {
		c.availableModels[id] = true
	}
	c.availableMu.Unlock()

	var models []AvailableModel
	for id, m := range rawResp.Models {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		// Skip internal or completion-preview endpoints
		switch id {
		case "chat_20706", "chat_23310", "tab_flash_lite_preview", "tab_jump_flash_lite_preview":
			continue
		}

		name := m.DisplayName
		if name == "" {
			name = id
		}

		baseModel := AvailableModel{
			ID:                 id,
			DisplayName:        name,
			Description:        m.Description,
			MaxTokens:          m.MaxTokens,
			MaxOutputTokens:    m.MaxOutputTokens,
			SupportsImages:     m.SupportsImages,
			SupportsVideo:      m.SupportsVideo,
			SupportsThinking:   m.SupportsThinking,
			ThinkingBudget:     m.ThinkingBudget,
			MinThinkingBudget:  m.MinThinkingBudget,
			ThinkingLevel:      m.ThinkingLevel,
			SupportedMimeTypes: m.SupportedMimeTypes,
			Quota:              m.QuotaInfo,
			APIProvider:        m.APIProvider,
			ModelProvider:      m.ModelProvider,
		}
		models = append(models, baseModel)

		// Dynamic heuristic expansion for "-tiered" gateway models
		if strings.HasSuffix(id, "-tiered") {
			baseID := strings.TrimSuffix(id, "-tiered")
			baseName := cleanDisplayName(name, id, baseID)
			for _, eff := range []struct {
				suffix string
				label  string
				level  int
			}{
				{"-high", " (High)", 3},
				{"-medium", " (Medium)", 2},
				{"-low", " (Low)", 1},
				{"", "", 0},
			} {
				am := baseModel
				am.ID = baseID + eff.suffix
				am.DisplayName = strings.TrimSpace(baseName + eff.label)
				am.ThinkingLevel = eff.level
				models = append(models, am)
			}
		}
	}

	return models, nil
}

// FetchQuotas queries the /v1internal:fetchAvailableModels endpoint and returns
// a map of model identifiers to their respective QuotaInfo.
// This executes a direct live request against the backend without caching.
func (c *Client) FetchQuotas(ctx context.Context) (map[string]QuotaInfo, error) {
	models, err := c.FetchAvailableModels(ctx)
	if err != nil {
		return nil, err
	}

	quotas := make(map[string]QuotaInfo)
	for _, m := range models {
		if m.Quota != nil {
			quotas[m.ID] = *m.Quota
		}
	}
	return quotas, nil
}

// ResolveTargetModel resolves a requested model ID to its concrete backend endpoint ID,
// applying configured aliases, custom resolvers, and dynamic heuristic suffix mappings.
func (c *Client) ResolveTargetModel(model string) string {
	target, _ := c.resolveModel(model, GenerateContentRequest{})
	return target
}

func (c *Client) resolveModel(model string, req GenerateContentRequest) (string, GenerateContentRequest) {
	c.availableMu.RLock()
	avail := c.availableModels
	c.availableMu.RUnlock()

	// 1. Custom user resolver (Option 4)
	if c.resolver != nil {
		return c.resolver(model, req, avail)
	}

	// 2. Custom user aliases (Option 4)
	if target, ok := c.aliases[model]; ok {
		model = target
	}

	// 3. Exact server match passthrough (Option 1)
	if len(avail) > 0 && avail[model] {
		return model, req
	}

	// 4. Generic heuristic pattern resolver (Option 2)
	effort := ""
	if req.GenerationConfig != nil && req.GenerationConfig.ThinkingConfig != nil {
		effort = strings.ToUpper(req.GenerationConfig.ThinkingConfig.ThinkingLevel)
	}

	base, suffixEffort := parseEffortSuffix(model)
	if suffixEffort != "" {
		effort = suffixEffort
	}

	// If no effort was requested and the model had no effort suffix, preserve as-is
	if effort == "" && suffixEffort == "" {
		if len(avail) == 0 || avail[base] {
			return base, req
		}
		if avail[base+"-tiered"] {
			return base + "-tiered", req
		}
		return model, req
	}

	// An effort was specified (via suffix or request config):
	// 1. If base-tiered is available (or cache not yet initialized), route to base-tiered
	tieredID := base + "-tiered"
	if len(avail) == 0 || avail[tieredID] {
		return tieredID, withThinkingLevel(req, effort)
	}

	// 2. If separate endpoint base-[effort] exists (e.g. gemini-3.6-flash-low), use it
	effortID := base + "-" + strings.ToLower(effort)
	if avail[effortID] {
		return effortID, req
	}

	// 3. Fallback to base or original model
	if avail[base] {
		return base, withThinkingLevel(req, effort)
	}

	return model, req
}

func parseEffortSuffix(model string) (base string, effort string) {
	switch {
	case strings.HasSuffix(model, "-extra-low"):
		return strings.TrimSuffix(model, "-extra-low"), "LOW"
	case strings.HasSuffix(model, "-high"):
		return strings.TrimSuffix(model, "-high"), "HIGH"
	case strings.HasSuffix(model, "-medium"):
		return strings.TrimSuffix(model, "-medium"), "MEDIUM"
	case strings.HasSuffix(model, "-low"):
		return strings.TrimSuffix(model, "-low"), "LOW"
	default:
		return model, ""
	}
}

func cleanDisplayName(displayName, id, base string) string {
	if displayName == "" || displayName == id {
		return base
	}
	for _, s := range []string{
		" (Extra Low)", " (High)", " (Medium)", " (Low)", " (Tiered)",
		"-extra-low", "-high", "-medium", "-low", "-tiered",
	} {
		displayName = strings.TrimSuffix(displayName, s)
	}
	return strings.TrimSpace(displayName)
}

func withThinkingLevel(req GenerateContentRequest, level string) GenerateContentRequest {
	if req.GenerationConfig == nil {
		req.GenerationConfig = &GenerationConfig{}
	}
	if req.GenerationConfig.ThinkingConfig == nil {
		req.GenerationConfig.ThinkingConfig = &ThinkingConfig{
			IncludeThoughts: true,
		}
	}
	if req.GenerationConfig.ThinkingConfig.ThinkingLevel == "" {
		req.GenerationConfig.ThinkingConfig.ThinkingLevel = level
	}
	return req
}

// CountTokens sends a request to the internal Cloud Code countTokens endpoint
// and returns the total number of tokens for the given contents.
func (c *Client) CountTokens(ctx context.Context, req CountTokensRequest) (*CountTokensResponse, error) {
	token, err := c.tokenProvider.Token(ctx)
	if err != nil {
		return nil, fmt.Errorf("antigravity: acquire token: %w", err)
	}

	envelope := countTokensEnvelope{
		Request: req,
	}

	bodyBytes, err := json.Marshal(envelope)
	if err != nil {
		return nil, fmt.Errorf("antigravity: marshal count tokens request: %w", err)
	}

	url := fmt.Sprintf("%s/v1internal:countTokens", c.baseURL)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("antigravity: create count tokens request: %w", err)
	}

	httpReq.Header.Set("Authorization", "Bearer "+token)
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("User-Agent", c.userAgent)
	if c.projectID != "" {
		httpReq.Header.Set("X-Goog-User-Project", c.projectID)
	}

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("antigravity: post count tokens request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("antigravity: count tokens API error (status %d): %s", resp.StatusCode, string(body))
	}

	var countResp CountTokensResponse
	if err := json.NewDecoder(resp.Body).Decode(&countResp); err != nil {
		return nil, fmt.Errorf("antigravity: decode count tokens response: %w", err)
	}

	return &countResp, nil
}

