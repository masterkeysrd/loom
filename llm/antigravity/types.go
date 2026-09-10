package loomantigravity

import "encoding/json"

// RequestEnvelope represents the outer JSON envelope required by the internal
// Cloud Code / Antigravity streaming endpoint.
type RequestEnvelope struct {
	Project   string                 `json:"project,omitempty"`
	Model     string                 `json:"model"`
	Request   GenerateContentRequest `json:"request"`
	RequestID string                 `json:"requestId,omitempty"`
	UserAgent string                 `json:"userAgent,omitempty"`
}

// GenerateContentRequest represents the inner payload containing chat contents,
// system instructions, generation knobs, and tool declarations.
type GenerateContentRequest struct {
	Contents          []Content         `json:"contents"`
	SystemInstruction *Content          `json:"systemInstruction,omitempty"`
	GenerationConfig  *GenerationConfig `json:"generationConfig,omitempty"`
	Tools             []Tool            `json:"tools,omitempty"`
	ToolConfig        *ToolConfig       `json:"toolConfig,omitempty"`
}

// Content represents a single conversational turn (user, model, or tool response).
type Content struct {
	Role  string `json:"role,omitempty"`
	Parts []Part `json:"parts"`
}

// Part is a polymorphic element of a content turn (text, image, audio, tool call, etc.).
type Part struct {
	Text             string            `json:"text,omitempty"`
	Thought          bool              `json:"thought,omitempty"`
	ThoughtSignature string            `json:"thoughtSignature,omitempty"`
	InlineData       *Blob             `json:"inlineData,omitempty"`
	FileData         *FileData         `json:"fileData,omitempty"`
	FunctionCall     *FunctionCall     `json:"functionCall,omitempty"`
	FunctionResponse *FunctionResponse `json:"functionResponse,omitempty"`
}

// UnmarshalJSON unmarshals a Part, supporting both camelCase (thoughtSignature)
// and snake_case (thought_signature) as emitted by Google endpoints.
func (p *Part) UnmarshalJSON(data []byte) error {
	type Alias Part
	var aux struct {
		Alias
		ThoughtSignatureSnake string `json:"thought_signature"`
	}
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	*p = Part(aux.Alias)
	if p.ThoughtSignature == "" && aux.ThoughtSignatureSnake != "" {
		p.ThoughtSignature = aux.ThoughtSignatureSnake
	}
	return nil
}

// Blob represents inlined binary content (e.g. images, audio) encoded in Base64.
type Blob struct {
	MIMEType string `json:"mimeType"`
	Data     string `json:"data"`
}

// FileData references a file hosted externally or uploaded to Google Cloud storage.
type FileData struct {
	MIMEType string `json:"mimeType"`
	FileURI  string `json:"fileUri"`
}

// FunctionCall represents a tool execution requested by the model.
type FunctionCall struct {
	ID   string         `json:"id,omitempty"`
	Name string         `json:"name"`
	Args map[string]any `json:"args,omitempty"`
}

// FunctionResponse represents the result of a tool execution sent back to the model.
type FunctionResponse struct {
	ID       string         `json:"id,omitempty"`
	Name     string         `json:"name"`
	Response map[string]any `json:"response"`
}

// GenerationConfig holds knobs for controlling the generation process.
type GenerationConfig struct {
	Temperature      *float32        `json:"temperature,omitempty"`
	TopP             *float32        `json:"topP,omitempty"`
	TopK             *float32        `json:"topK,omitempty"`
	MaxOutputTokens  int32           `json:"maxOutputTokens,omitempty"`
	StopSequences    []string        `json:"stopSequences,omitempty"`
	ThinkingConfig   *ThinkingConfig `json:"thinkingConfig,omitempty"`
	ResponseMIMEType string          `json:"responseMimeType,omitempty"`
	ResponseSchema   any             `json:"responseSchema,omitempty"`
}

// ThinkingConfig controls reasoning and chain-of-thought token generation.
type ThinkingConfig struct {
	IncludeThoughts bool   `json:"includeThoughts,omitempty"`
	ThinkingBudget  *int32 `json:"thinkingBudget,omitempty"`
	ThinkingLevel   string `json:"thinkingLevel,omitempty"`
}

// Tool declares function calling capabilities available to the model.
type Tool struct {
	FunctionDeclarations []FunctionDeclaration `json:"functionDeclarations,omitempty"`
}

// FunctionDeclaration defines a callable function's name, description, and input schema.
type FunctionDeclaration struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Parameters  any    `json:"parameters,omitempty"`
}

// ToolConfig specifies runtime constraints on tool usage.
type ToolConfig struct {
	FunctionCallingConfig *FunctionCallingConfig `json:"functionCallingConfig,omitempty"`
}

// FunctionCallingConfig specifies the calling mode (e.g., AUTO, ANY, NONE).
type FunctionCallingConfig struct {
	Mode string `json:"mode,omitempty"`
}

// ResponseEnvelope captures both wrapped and unwrapped SSE chunk envelopes
// returned by the internal endpoint.
type ResponseEnvelope struct {
	Response      *GenerateContentResponse `json:"response,omitempty"`
	Candidates    []*Candidate             `json:"candidates,omitempty"`
	UsageMetadata *UsageMetadata           `json:"usageMetadata,omitempty"`
}

// Result extracts the effective GenerateContentResponse regardless of whether
// the response is nested under "response" or returned at root.
func (e *ResponseEnvelope) Result() *GenerateContentResponse {
	if e.Response != nil {
		return e.Response
	}
	if len(e.Candidates) > 0 || e.UsageMetadata != nil {
		return &GenerateContentResponse{
			Candidates:    e.Candidates,
			UsageMetadata: e.UsageMetadata,
		}
	}
	return nil
}

// GenerateContentResponse represents the core completion chunk from the stream.
type GenerateContentResponse struct {
	Candidates    []*Candidate   `json:"candidates,omitempty"`
	UsageMetadata *UsageMetadata `json:"usageMetadata,omitempty"`
}

// Candidate represents a single generated output candidate.
type Candidate struct {
	Content       *Content `json:"content,omitempty"`
	FinishReason  string   `json:"finishReason,omitempty"`
	FinishMessage string   `json:"finishMessage,omitempty"`
}

// UsageMetadata contains token usage metrics for the prompt and response.
type UsageMetadata struct {
	PromptTokenCount        int `json:"promptTokenCount,omitempty"`
	CandidatesTokenCount    int `json:"candidatesTokenCount,omitempty"`
	TotalTokenCount         int `json:"totalTokenCount,omitempty"`
	CachedContentTokenCount int `json:"cachedContentTokenCount,omitempty"`
	ThoughtsTokenCount      int `json:"thoughtsTokenCount,omitempty"`
}

// CountTokensRequest represents the payload for counting tokens.
type CountTokensRequest struct {
	Contents []Content `json:"contents"`
}

// countTokensEnvelope wraps CountTokensRequest for the internal Cloud Code endpoint.
type countTokensEnvelope struct {
	Request CountTokensRequest `json:"request"`
}

// CountTokensResponse represents the response from the countTokens endpoint.
type CountTokensResponse struct {
	TotalTokens int `json:"totalTokens"`
}

