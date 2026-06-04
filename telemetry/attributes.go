package telemetry

import (
	"go.opentelemetry.io/otel/attribute"
	semconv "go.opentelemetry.io/otel/semconv/v1.41.0"
	"go.opentelemetry.io/otel/semconv/v1.41.0/genaiconv"
)

// Re-export standard GenAI attributes and helpers from OTel semconv
var (
	// Operations
	OpChat            = genaiconv.OperationNameChat
	OpGenerateContent = genaiconv.OperationNameGenerateContent
	OpTextCompletion  = genaiconv.OperationNameTextCompletion
	OpEmbeddings      = genaiconv.OperationNameEmbeddings
	OpRetrieval       = genaiconv.OperationNameRetrieval
	OpExecuteTool     = genaiconv.OperationNameExecuteTool

	// Providers
	ProviderOpenAI    = genaiconv.ProviderNameOpenAI
	ProviderAnthropic = genaiconv.ProviderNameAnthropic
	ProviderGCPGemini = genaiconv.ProviderNameGCPGemini

	// Token Types
	TokenTypeInput  = genaiconv.TokenTypeInput
	TokenTypeOutput = genaiconv.TokenTypeOutput

	// Helpers from standard semconv
	WithModel         = KeyGenAIRequestModel.String
	WithResponseModel = KeyGenAIResponseModel.String
	WithProvider      = KeyGenAIProvider.String

	WithMaxTokens        = KeyGenAIRequestMaxTokens.Int
	WithTemperature      = KeyGenAIRequestTemperature.Float64
	WithTopP             = KeyGenAIRequestTopP.Float64
	WithTopK             = KeyGenAIRequestTopK.Int
	WithStopSequences    = KeyGenAIRequestStopSequences.StringSlice
	WithPresencePenalty  = KeyGenAIRequestPresencePenalty.Float64
	WithFrequencyPenalty = KeyGenAIRequestFrequencyPenalty.Float64
	WithToolName         = semconv.GenAIToolNameKey.String
	WithToolCallID       = semconv.GenAIToolCallIDKey.String
	WithToolType         = semconv.GenAIToolTypeKey.String
	WithInputTokens      = KeyGenAIUsageInputTokens.Int
	WithOutputTokens     = KeyGenAIUsageOutputTokens.Int

	// RPC and JSON-RPC helpers
	WithRPCMethod = semconv.RPCMethodKey.String
	WithJSONRPCID = semconv.JSONRPCRequestIDKey.String
)

// Attribute Keys
const (
	// MCP Keys
	KeyMCPMethodName = attribute.Key("mcp.method.name")
	KeyMCPSessionID  = attribute.Key("mcp.session.id")

	// Loom Keys
	KeyLoomGraphName      = attribute.Key("loom.graph.name")
	KeyLoomThreadID       = attribute.Key("loom.thread_id")
	KeyLoomCheckpointID   = attribute.Key("loom.checkpoint_id")
	KeyLoomNodeName       = attribute.Key("loom.node.name")
	KeyLoomToolType       = attribute.Key("loom.tool.type")
	KeyLoomMemoryStrategy = attribute.Key("loom.memory.strategy")

	// Sensitive content Keys
	KeyContentInputMessages  = attribute.Key("gen_ai.input.messages")
	KeyContentOutputMessages = attribute.Key("gen_ai.output.messages")
	KeyContentSystemPrompt   = attribute.Key("gen_ai.system_instructions")
	KeyContentToolArguments  = attribute.Key("gen_ai.tool.call.arguments")
	KeyContentToolResult     = attribute.Key("gen_ai.tool.call.result")

	// GenAI Keys (standard ones as defined in latest conventions)
	KeyGenAIProvider                = attribute.Key("gen_ai.provider.name")
	KeyGenAIOperation               = attribute.Key("gen_ai.operation.name")
	KeyGenAIRequestModel            = attribute.Key("gen_ai.request.model")
	KeyGenAIRequestStream           = attribute.Key("gen_ai.request.stream")
	KeyGenAIRequestSeed             = attribute.Key("gen_ai.request.seed")
	KeyGenAIConversationID          = attribute.Key("gen_ai.conversation.id")
	KeyGenAIResponseType            = attribute.Key("gen_ai.output.type")
	KeyGenAIResponseModel           = attribute.Key("gen_ai.response.model")
	KeyGenAIRequestMaxTokens        = attribute.Key("gen_ai.request.max_tokens")
	KeyGenAIRequestTemperature      = attribute.Key("gen_ai.request.temperature")
	KeyGenAIRequestTopP             = attribute.Key("gen_ai.request.top_p")
	KeyGenAIRequestTopK             = attribute.Key("gen_ai.request.top_k")
	KeyGenAIRequestStopSequences    = attribute.Key("gen_ai.request.stop_sequences")
	KeyGenAIRequestPresencePenalty  = attribute.Key("gen_ai.request.presence_penalty")
	KeyGenAIRequestFrequencyPenalty = attribute.Key("gen_ai.request.frequency_penalty")
	KeyGenAIUsageInputTokens        = attribute.Key("gen_ai.usage.input_tokens")
	KeyGenAIUsageOutputTokens       = attribute.Key("gen_ai.usage.output_tokens")

	KeyErrorType = attribute.Key("error.type")
)

// Loom-specific helpers

func WithOperation(op genaiconv.OperationNameAttr) attribute.KeyValue {
	return KeyGenAIOperation.String(string(op))
}

func WithStream(stream bool) attribute.KeyValue {
	return KeyGenAIRequestStream.Bool(stream)
}

func WithConversationID(id string) attribute.KeyValue {
	return KeyGenAIConversationID.String(id)
}

func WithErrorType(err string) attribute.KeyValue {
	return KeyErrorType.String(err)
}

func WithLoomGraph(name string) attribute.KeyValue {
	return KeyLoomGraphName.String(name)
}

func WithLoomThread(id string) attribute.KeyValue {
	return KeyLoomThreadID.String(id)
}

func WithLoomNode(name string) attribute.KeyValue {
	return KeyLoomNodeName.String(name)
}

func WithLoomCheckpoint(id string) attribute.KeyValue {
	return KeyLoomCheckpointID.String(id)
}

func WithLoomMemoryStrategy(strategy string) attribute.KeyValue {
	return KeyLoomMemoryStrategy.String(strategy)
}

// MCP-specific helpers

func WithMCPMethod(method string) attribute.KeyValue {
	return KeyMCPMethodName.String(method)
}

func WithMCPSession(id string) attribute.KeyValue {
	return KeyMCPSessionID.String(id)
}
