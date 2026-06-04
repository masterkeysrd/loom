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
	WithModel         = semconv.GenAIRequestModel
	WithResponseModel = semconv.GenAIResponseModel
	WithSystem        = KeyGenAISystem.String

	WithMaxTokens        = semconv.GenAIRequestMaxTokens
	WithTemperature      = semconv.GenAIRequestTemperature
	WithTopP             = semconv.GenAIRequestTopP
	WithTopK             = semconv.GenAIRequestTopK
	WithStopSequences    = semconv.GenAIRequestStopSequences
	WithPresencePenalty  = semconv.GenAIRequestPresencePenalty
	WithFrequencyPenalty = semconv.GenAIRequestFrequencyPenalty
	WithToolName         = semconv.GenAIToolName
	WithToolCallID       = semconv.GenAIToolCallID
	WithToolType         = semconv.GenAIToolType
	WithInputTokens      = semconv.GenAIUsageInputTokens
	WithOutputTokens     = semconv.GenAIUsageOutputTokens

	// RPC and JSON-RPC helpers
	WithRPCMethod = semconv.RPCMethod
	WithJSONRPCID = semconv.JSONRPCRequestID
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

	// GenAI Keys (shorthands for standard ones)
	KeyGenAISystem       = attribute.Key("gen_ai.system")
	KeyGenAIRequestModel = attribute.Key("gen_ai.request.model")
)

// Loom-specific helpers

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
