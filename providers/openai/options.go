package loomopenai

import (
	"github.com/masterkeysrd/loom/llm"
)

// Metadata keys for OpenAI response tracking and encrypted reasoning.
const (
	// ResponseIDKey is the metadata key on Assistant messages and chunks storing the OpenAI response ID (e.g. "resp_...").
	ResponseIDKey = "openai.response_id"

	// EncryptedContentKey is the key stored in ThinkingBlock.Extras preserving the opaque encrypted reasoning blob.
	EncryptedContentKey = "openai.encrypted_content"

	// ReasoningIDKey is the key stored in ThinkingBlock.Extras preserving the reasoning item ID (e.g. "rs_...").
	ReasoningIDKey = "openai.reasoning_id"

	// ResponsesExtensionID is the unique extension ID for ResponsesOptions.
	ResponsesExtensionID = "openai.responses"
)

var _ llm.Extension = (*ResponsesOptions)(nil)

// APIMode determines the underlying OpenAI endpoint used for inference.
type APIMode string

const (
	// APIModeChatCompletions routes inference to /v1/chat/completions (default).
	APIModeChatCompletions APIMode = "chat_completions"

	// APIModeResponses routes inference to /v1/responses.
	APIModeResponses APIMode = "responses"
)

// FileSearchConfig specifies parameters for OpenAI's built-in file search tool.
type FileSearchConfig struct {
	// VectorStoreIDs lists the vector store identifiers to query.
	VectorStoreIDs []string `json:"vector_store_ids"`

	// MaxNumResults specifies the maximum number of search results to retrieve (1-50).
	MaxNumResults int64 `json:"max_num_results,omitempty"`
}

// ResponsesOptions configures OpenAI Responses API behavior for an individual request or model.
type ResponsesOptions struct {
	// Enabled forces the request to be routed to the Responses API.
	Enabled bool `json:"enabled,omitempty"`

	// Store determines whether OpenAI stores conversation state on the server.
	// When true (or nil/default), OpenAI stores conversation turns for stateful continuation.
	// When false, Zero Data Retention (ZDR) is honored; full history and encrypted reasoning
	// are managed client-side.
	Store *bool `json:"store,omitempty"`

	// UsePreviousResponseID enables automatic conversation state continuation:
	// it finds the most recent Assistant message with a ResponseIDKey in its metadata or extras,
	// passes its ID as previous_response_id, and prunes preceding messages from the wire payload.
	// Only applicable when Store is not explicitly false.
	UsePreviousResponseID bool `json:"use_previous_response_id,omitempty"`

	// PreviousResponseID manually sets the conversation turn to continue from.
	PreviousResponseID string `json:"previous_response_id,omitempty"`

	// WebSearch enables OpenAI's built-in web search tool.
	WebSearch bool `json:"web_search,omitempty"`

	// FileSearch enables OpenAI's built-in file search tool with vector stores.
	FileSearch *FileSearchConfig `json:"file_search,omitempty"`

	// CodeInterpreter enables OpenAI's built-in code interpreter tool.
	CodeInterpreter bool `json:"code_interpreter,omitempty"`
}

// ExtensionID returns the identifier for ResponsesOptions in llm.Request extensions.
func (r ResponsesOptions) ExtensionID() string {
	return ResponsesExtensionID
}

// WithWebSearch returns a ResponsesOptions extension enabling built-in web search.
func WithWebSearch() ResponsesOptions {
	return ResponsesOptions{WebSearch: true}
}

// WithFileSearch returns a ResponsesOptions extension enabling built-in file search with the given vector store IDs.
func WithFileSearch(vectorStoreIDs ...string) ResponsesOptions {
	return ResponsesOptions{
		FileSearch: &FileSearchConfig{
			VectorStoreIDs: vectorStoreIDs,
		},
	}
}

// WithPreviousResponse returns a ResponsesOptions extension with an explicit previous response ID.
func WithPreviousResponse(responseID string) ResponsesOptions {
	return ResponsesOptions{
		PreviousResponseID: responseID,
	}
}

// Option configures a Provider.
type Option func(*Provider)

// WithAPIMode sets the default API mode for the Provider (ChatCompletions or Responses).
func WithAPIMode(mode APIMode) Option {
	return func(p *Provider) {
		p.defaultAPIMode = mode
	}
}

// WithResponsesAPI enables or disables the Responses API globally as the default for the Provider.
func WithResponsesAPI(enabled bool) Option {
	return func(p *Provider) {
		if enabled {
			p.defaultAPIMode = APIModeResponses
		} else {
			p.defaultAPIMode = APIModeChatCompletions
		}
	}
}
