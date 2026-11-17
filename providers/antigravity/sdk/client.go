package sdk

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"iter"
	"net/http"
	"os"
	"runtime"
	"strings"
	"sync"

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

// Client handles raw HTTP communication with Cloud Code / Antigravity endpoints.
type Client struct {
	baseURL          string
	httpClient       *http.Client
	tokenSource      oauth2.TokenSource
	projectID        string
	companionProject string
	userAgent        string

	companionMu                sync.RWMutex
	companionLoaded            bool
	companionToken             string
	discoveredCompanionProject string
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

// StreamOption configures optional fields on the outer RequestEnvelope for StreamGenerateContent.
type StreamOption func(*RequestEnvelope)

// WithUserPromptID sets user_prompt_id on the outer RequestEnvelope.
func WithUserPromptID(id string) StreamOption {
	return func(env *RequestEnvelope) {
		env.UserPromptID = id
	}
}

// NewClient creates a new Antigravity SDK Client with the given TokenSource and options.
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
		httpClient:       &http.Client{Transport: http.DefaultTransport},
		tokenSource:      tokenSource,
		companionProject: os.Getenv("ANTIGRAVITY_COMPANION_PROJECT"),
		userAgent:        userAgent,
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// UserAgent returns the User-Agent string configured on this client.
func (c *Client) UserAgent() string {
	return c.userAgent
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
	defer c.companionMu.Unlock()

	if c.companionLoaded && c.companionToken == token {
		return c.discoveredCompanionProject, nil
	}

	proj, _, err := c.doLoadCodeAssist(ctx, token)
	if err != nil {
		return "", err
	}

	c.discoveredCompanionProject = proj
	c.companionToken = token
	c.companionLoaded = true
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
	defer c.companionMu.Unlock()

	if c.companionLoaded && c.companionToken == token {
		if c.discoveredCompanionProject != "" {
			return c.discoveredCompanionProject
		}
		return c.projectID
	}

	proj, statusCode, err := c.doLoadCodeAssist(ctx, token)
	if err == nil {
		c.discoveredCompanionProject = proj
		c.companionToken = token
		c.companionLoaded = true
	} else if statusCode == http.StatusNotFound || statusCode == http.StatusMethodNotAllowed || statusCode == http.StatusNotImplemented {
		c.discoveredCompanionProject = ""
		c.companionToken = token
		c.companionLoaded = true
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

	c.setCommonHeaders(httpReq, token)

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

func (c *Client) setCommonHeaders(httpReq *http.Request, token string) {
	httpReq.Header.Set("Authorization", "Bearer "+token)
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("User-Agent", c.userAgent)
	if c.projectID != "" {
		httpReq.Header.Set("X-Goog-User-Project", c.projectID)
	}
}

func (c *Client) postInternalProjectJSON(ctx context.Context, token, endpoint string) (*http.Response, error) {
	project := c.resolveBodyProject(ctx, token)

	url := fmt.Sprintf("%s%s", c.baseURL, endpoint)
	payload := "{}"
	if project != "" {
		bodyBytes, err := json.Marshal(struct {
			Project string `json:"project"`
		}{Project: project})
		if err != nil {
			return nil, fmt.Errorf("antigravity: marshal %s request: %w", endpoint, err)
		}
		payload = string(bodyBytes)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("antigravity: create %s request: %w", endpoint, err)
	}

	c.setCommonHeaders(httpReq, token)
	return c.httpClient.Do(httpReq)
}

// StreamGenerateContent sends req for the exact backend model ID to
// `/v1internal:streamGenerateContent?alt=sse` and yields raw ResponseEnvelope events.
func (c *Client) StreamGenerateContent(
	ctx context.Context,
	model string,
	req GenerateContentRequest,
	opts ...StreamOption,
) (iter.Seq2[*ResponseEnvelope, error], error) {
	tok, err := c.tokenSource.Token()
	if err != nil {
		return nil, fmt.Errorf("antigravity: acquire token: %w", err)
	}
	token := tok.AccessToken

	project := c.resolveBodyProject(ctx, token)

	envelope := RequestEnvelope{
		Project:   project,
		Model:     model,
		Request:   req,
		UserAgent: c.userAgent,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(&envelope)
		}
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

	c.setCommonHeaders(httpReq, token)
	httpReq.Header.Set("Accept", "text/event-stream")

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("antigravity: post request: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("antigravity: API error (status %d): %s", resp.StatusCode, string(body))
	}

	return func(yield func(*ResponseEnvelope, error) bool) {
		defer resp.Body.Close()

		scanner := bufio.NewScanner(resp.Body)
		// Allocate a generous buffer for large JSON chunks (up to 10MB).
		buf := make([]byte, 64*1024)
		scanner.Buffer(buf, 10*1024*1024)

		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
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
				if !yield(nil, fmt.Errorf("antigravity: unmarshal sse chunk: %w", err)) {
					return
				}
				continue
			}

			if !yield(&env, nil) {
				return
			}
		}

		if err := scanner.Err(); err != nil && err != io.EOF {
			yield(nil, fmt.Errorf("antigravity: stream read error: %w", err))
		}
	}, nil
}

// FetchAvailableModels queries `/v1internal:fetchAvailableModels` and returns the raw
// model entries exposed by the Antigravity backend without virtual alias expansion.
func (c *Client) FetchAvailableModels(ctx context.Context) ([]AvailableModel, error) {
	tok, err := c.tokenSource.Token()
	if err != nil {
		return nil, fmt.Errorf("antigravity: acquire token: %w", err)
	}

	resp, err := c.postInternalProjectJSON(ctx, tok.AccessToken, "/v1internal:fetchAvailableModels")
	if err != nil {
		return nil, fmt.Errorf("antigravity: fetch models: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("antigravity: fetch models error (status %d): %s", resp.StatusCode, string(body))
	}

	var rawResp struct {
		Models map[string]AvailableModel `json:"models"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&rawResp); err != nil {
		return nil, fmt.Errorf("antigravity: decode models response: %w", err)
	}

	models := make([]AvailableModel, 0, len(rawResp.Models))
	for id, m := range rawResp.Models {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		m.ID = id
		if m.DisplayName == "" {
			m.DisplayName = id
		}
		models = append(models, m)
	}
	return models, nil
}

// RetrieveUserQuotaSummary queries `/v1internal:retrieveUserQuotaSummary` and returns
// the grouped multi-window quota buckets (such as 5-hour and weekly pools).
func (c *Client) RetrieveUserQuotaSummary(ctx context.Context) (*UserQuotaSummaryResponse, error) {
	tok, err := c.tokenSource.Token()
	if err != nil {
		return nil, fmt.Errorf("antigravity: acquire token: %w", err)
	}

	resp, err := c.postInternalProjectJSON(ctx, tok.AccessToken, "/v1internal:retrieveUserQuotaSummary")
	if err != nil {
		return nil, fmt.Errorf("antigravity: retrieveUserQuotaSummary: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("antigravity: retrieveUserQuotaSummary status %d: %s", resp.StatusCode, string(body))
	}

	var out UserQuotaSummaryResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("antigravity: decode retrieveUserQuotaSummary response: %w", err)
	}
	return &out, nil
}

// CountTokens sends a request to `/v1internal:countTokens` and returns the total token count.
func (c *Client) CountTokens(ctx context.Context, req CountTokensRequest) (*CountTokensResponse, error) {
	tok, err := c.tokenSource.Token()
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

	c.setCommonHeaders(httpReq, tok.AccessToken)

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
