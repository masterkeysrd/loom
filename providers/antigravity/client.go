package loomantigravity

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net/http"
	"os"
	"runtime"
	"strings"
	"sync"

	"github.com/masterkeysrd/loom/llm"
	"github.com/masterkeysrd/loom/message"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"golang.org/x/oauth2"
)

const (
	// DefaultBaseURL is the production Google Cloud Code / Antigravity API base URL.
	DefaultBaseURL = "https://cloudcode-pa.googleapis.com"

	// DailyBaseURL is the daily staging / canary environment URL.
	DailyBaseURL = "https://daily-cloudcode-pa.googleapis.com"

	// DefaultClientVersion is the emulated Antigravity CLI version reported in DefaultUserAgent.
	DefaultClientVersion = "2.0.0"

	// DefaultUserAgent emulates a modern versioned Antigravity CLI client identifier.
	// Frontier model tiers on daily-cloudcode-pa.googleapis.com gate model visibility
	// and routing on a versioned User-Agent string.
	DefaultUserAgent = "antigravity/cli/" + DefaultClientVersion + " (" + runtime.GOOS + "/" + runtime.GOARCH + ")"
)

// ModelResolver translates a requested model ID and request into a target backend model ID
// and a modified request (e.g. injecting thinking level).
type ModelResolver func(requestedModel string, req GenerateContentRequest, availableModels map[string]bool) (string, GenerateContentRequest)

// Client handles direct HTTP communication with the Cloud Code streamGenerateContent endpoint.
type Client struct {
	baseURL          string
	httpClient       *http.Client
	tokenSource      oauth2.TokenSource
	projectID        string
	companionProject string
	userAgent        string
	aliases          map[string]string
	resolver         ModelResolver

	companionMu                sync.RWMutex
	companionLoaded            bool
	companionToken             string
	discoveredCompanionProject string

	availableInitMu      sync.Mutex
	availableMu          sync.RWMutex
	availableModels      map[string]bool
	availableLoadedToken string
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

// WithProjectID sets an explicit Google Cloud user billing project ID sent via the
// X-Goog-User-Project HTTP header (and used as a fallback body project if no
// CompanionProject is configured or discovered).
func WithProjectID(projectID string) ClientOption {
	return func(c *Client) {
		c.projectID = projectID
	}
}

// WithCompanionProject sets an explicit Cloud AI Companion project sent only in the
// JSON request body ("project" field), bypassing /v1internal:loadCodeAssist discovery
// without setting the X-Goog-User-Project header.
func WithCompanionProject(projectID string) ClientOption {
	return func(c *Client) {
		c.companionProject = projectID
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
		maps.Copy(c.aliases, aliases)
	}
}

// WithResolver configures a custom ModelResolver function.
func WithResolver(resolver ModelResolver) ClientOption {
	return func(c *Client) {
		c.resolver = resolver
	}
}

// NewClient creates a new Client with the given TokenSource and optional ClientOptions.
func NewClient(tokenSource oauth2.TokenSource, opts ...ClientOption) *Client {
	baseURL := os.Getenv("ANTIGRAVITY_BASE_URL")
	if baseURL == "" {
		baseURL = DailyBaseURL
	}

	userAgent := os.Getenv("ANTIGRAVITY_USER_AGENT")
	if userAgent == "" {
		userAgent = DefaultUserAgent
	}

	c := &Client{
		baseURL:          strings.TrimRight(baseURL, "/"),
		httpClient:       &http.Client{Transport: otelhttp.NewTransport(http.DefaultTransport)},
		tokenSource:      tokenSource,
		companionProject: os.Getenv("ANTIGRAVITY_COMPANION_PROJECT"),
		userAgent:        userAgent,
		availableModels:  make(map[string]bool),
		aliases:          make(map[string]string),
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// LoadCodeAssist calls POST /v1internal:loadCodeAssist once per token/account to
// resolve and cache the server-assigned cloudaicompanionProject.
func (c *Client) LoadCodeAssist(ctx context.Context) (string, error) {
	if c.companionProject != "" {
		return c.companionProject, nil
	}

	tok, err := c.tokenSource.Token()
	if err != nil {
		return "", fmt.Errorf("antigravity: acquire token: %w", err)
	}
	token := tok.AccessToken

	c.companionMu.RLock()
	if c.companionLoaded && c.companionToken == token {
		proj := c.discoveredCompanionProject
		c.companionMu.RUnlock()
		return proj, nil
	}
	c.companionMu.RUnlock()

	c.companionMu.Lock()
	if c.companionLoaded && c.companionToken == token {
		proj := c.discoveredCompanionProject
		c.companionMu.Unlock()
		return proj, nil
	}

	proj, _, err := c.doLoadCodeAssist(ctx, token)
	if err != nil {
		c.companionMu.Unlock()
		return "", err
	}

	invalidateModels := c.companionLoaded && c.discoveredCompanionProject != proj
	c.discoveredCompanionProject = proj
	c.companionToken = token
	c.companionLoaded = true
	c.companionMu.Unlock()

	if invalidateModels {
		c.availableMu.Lock()
		c.availableModels = make(map[string]bool)
		c.availableLoadedToken = ""
		c.availableMu.Unlock()
	}

	return proj, nil
}

func (c *Client) resolveBodyProject(ctx context.Context, token string) string {
	if c.companionProject != "" {
		return c.companionProject
	}

	c.companionMu.RLock()
	if c.companionLoaded && c.companionToken == token {
		proj := c.discoveredCompanionProject
		c.companionMu.RUnlock()
		if proj != "" {
			return proj
		}
		return c.projectID
	}
	c.companionMu.RUnlock()

	c.companionMu.Lock()
	if c.companionLoaded && c.companionToken == token {
		proj := c.discoveredCompanionProject
		c.companionMu.Unlock()
		if proj != "" {
			return proj
		}
		return c.projectID
	}

	proj, statusCode, err := c.doLoadCodeAssist(ctx, token)
	var invalidateModels bool
	if err == nil {
		invalidateModels = c.companionLoaded && c.discoveredCompanionProject != proj
		c.discoveredCompanionProject = proj
		c.companionToken = token
		c.companionLoaded = true
	} else if statusCode == http.StatusNotFound || statusCode == http.StatusMethodNotAllowed || statusCode == http.StatusNotImplemented {
		c.discoveredCompanionProject = ""
		c.companionToken = token
		c.companionLoaded = true
	}
	c.companionMu.Unlock()

	if invalidateModels {
		c.availableMu.Lock()
		c.availableModels = make(map[string]bool)
		c.availableLoadedToken = ""
		c.availableMu.Unlock()
	}

	if proj != "" {
		return proj
	}
	return c.projectID
}

func (c *Client) doLoadCodeAssist(ctx context.Context, token string) (string, int, error) {
	url := fmt.Sprintf("%s/v1internal:loadCodeAssist", c.baseURL)
	const payload = `{"metadata":{"ideType":"ANTIGRAVITY"}}`

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader(payload))
	if err != nil {
		return "", 0, fmt.Errorf("antigravity: create loadCodeAssist request: %w", err)
	}

	httpReq.Header.Set("Authorization", "Bearer "+token)
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("User-Agent", c.userAgent)
	if c.projectID != "" {
		httpReq.Header.Set("X-Goog-User-Project", c.projectID)
	}

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return "", 0, fmt.Errorf("antigravity: loadCodeAssist: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", resp.StatusCode, fmt.Errorf("antigravity: loadCodeAssist error (status %d): %s", resp.StatusCode, string(body))
	}

	var rawResp struct {
		CloudaicompanionProject json.RawMessage `json:"cloudaicompanionProject"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&rawResp); err != nil {
		return "", resp.StatusCode, fmt.Errorf("antigravity: decode loadCodeAssist response: %w", err)
	}

	return parseCompanionProject(rawResp.CloudaicompanionProject), http.StatusOK, nil
}

func parseCompanionProject(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return strings.TrimSpace(s)
	}
	var obj struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(raw, &obj); err == nil {
		return strings.TrimSpace(obj.ID)
	}
	return ""
}

func (c *Client) ensureAvailableModels(ctx context.Context, token string) {
	c.availableMu.RLock()
	ready := len(c.availableModels) > 0 || (c.availableLoadedToken != "" && c.availableLoadedToken == token)
	c.availableMu.RUnlock()
	if ready {
		return
	}

	c.availableInitMu.Lock()
	defer c.availableInitMu.Unlock()

	c.availableMu.RLock()
	ready = len(c.availableModels) > 0 || (c.availableLoadedToken != "" && c.availableLoadedToken == token)
	c.availableMu.RUnlock()
	if ready {
		return
	}

	_, _ = c.fetchAvailableModelsWithToken(ctx, token)
}

// StreamGenerateContent sends a streaming generation request to the Cloud Code endpoint
// and returns an iterator over message.AssistantChunk.
func (c *Client) StreamGenerateContent(ctx context.Context, model string, req GenerateContentRequest) (llm.StreamResponse, error) {
	return c.streamGenerateContent(ctx, model, req, "")
}

func (c *Client) streamGenerateContent(ctx context.Context, model string, req GenerateContentRequest, userPromptID string) (llm.StreamResponse, error) {
	tok, err := c.tokenSource.Token()
	if err != nil {
		return nil, fmt.Errorf("antigravity: acquire token: %w", err)
	}
	token := tok.AccessToken

	project := c.resolveBodyProject(ctx, token)
	c.ensureAvailableModels(ctx, token)
	model, req = c.resolveModel(model, req)

	envelope := RequestEnvelope{
		Project:      project,
		Model:        model,
		Request:      req,
		UserAgent:    c.userAgent,
		UserPromptID: userPromptID,
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
	tok, err := c.tokenSource.Token()
	if err != nil {
		return nil, fmt.Errorf("antigravity: acquire token: %w", err)
	}
	return c.fetchAvailableModelsWithToken(ctx, tok.AccessToken)
}

func (c *Client) fetchAvailableModelsWithToken(ctx context.Context, token string) ([]AvailableModel, error) {
	project := c.resolveBodyProject(ctx, token)

	url := fmt.Sprintf("%s/v1internal:fetchAvailableModels", c.baseURL)
	payload := "{}"
	if project != "" {
		bodyBytes, err := json.Marshal(struct {
			Project string `json:"project"`
		}{Project: project})
		if err != nil {
			return nil, fmt.Errorf("antigravity: marshal models request: %w", err)
		}
		payload = string(bodyBytes)
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
		if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusMethodNotAllowed || resp.StatusCode == http.StatusNotImplemented {
			c.availableMu.Lock()
			c.availableLoadedToken = token
			c.availableMu.Unlock()
		}
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
	c.availableLoadedToken = token
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
		if before, ok := strings.CutSuffix(id, "-tiered"); ok {
			baseID := before
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

	if req.GenerationConfig != nil && req.GenerationConfig.ThinkingConfig != nil && req.GenerationConfig.ThinkingConfig.ThinkingLevel != "" {
		req = withThinkingLevel(req, req.GenerationConfig.ThinkingConfig.ThinkingLevel)
	}

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

	// If no effort was requested and the model had no effort suffix:
	if effort == "" && suffixEffort == "" {
		if len(avail) == 0 || avail[base] {
			return base, req
		}
		if avail[base+"-tiered"] {
			return base + "-tiered", req
		}
		for _, suffix := range []string{"-medium", "-low", "-high"} {
			if candidate := base + suffix; avail[candidate] {
				return candidate, req
			}
		}
		return model, req
	}

	// An effort was specified (via suffix or request config):
	// 1. If base-tiered is available (or cache not yet initialized), route to base-tiered
	tieredID := base + "-tiered"
	if len(avail) == 0 || avail[tieredID] {
		return tieredID, withThinkingLevel(req, effort)
	}

	// 2. If separate endpoint base-[effort] exists (e.g. gemini-4-carbon-high), use it
	effortID := base + "-" + strings.ToLower(effort)
	if avail[effortID] {
		return effortID, req
	}

	// 3. Fallback to base if available
	if avail[base] {
		return base, withThinkingLevel(req, effort)
	}

	// 4. Fallback to another available effort variant in the same family
	for _, suffix := range []string{"-medium", "-low", "-high"} {
		if candidate := base + suffix; avail[candidate] {
			return candidate, req
		}
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
	} else {
		cfgCopy := *req.GenerationConfig
		req.GenerationConfig = &cfgCopy
	}
	if req.GenerationConfig.ThinkingConfig == nil {
		req.GenerationConfig.ThinkingConfig = &ThinkingConfig{
			IncludeThoughts: true,
		}
	} else {
		tcCopy := *req.GenerationConfig.ThinkingConfig
		req.GenerationConfig.ThinkingConfig = &tcCopy
	}
	if req.GenerationConfig.ThinkingConfig.ThinkingLevel == "" {
		req.GenerationConfig.ThinkingConfig.ThinkingLevel = strings.ToUpper(level)
	} else {
		req.GenerationConfig.ThinkingConfig.ThinkingLevel = strings.ToUpper(req.GenerationConfig.ThinkingConfig.ThinkingLevel)
	}
	return req
}

// CountTokens sends a request to the internal Cloud Code countTokens endpoint
// and returns the total number of tokens for the given contents.
func (c *Client) CountTokens(ctx context.Context, req CountTokensRequest) (*CountTokensResponse, error) {
	tok, err := c.tokenSource.Token()
	if err != nil {
		return nil, fmt.Errorf("antigravity: acquire token: %w", err)
	}
	token := tok.AccessToken

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
