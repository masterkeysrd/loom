package loomcodex

import (
	"slices"

	"github.com/openai/openai-go/v3/packages/param"
	"github.com/openai/openai-go/v3/responses"
)

// adaptRequest prepares encoded developer messages and function tools for direct Codex inference.
func adaptRequest(params responses.ResponseNewParams) responses.ResponseNewParams {
	params.MaxOutputTokens = param.Opt[int64]{}
	params.Temperature = param.Opt[float64]{}
	params.TopP = param.Opt[float64]{}
	params.Include = append(params.Include, responses.ResponseIncludableReasoningEncryptedContent)
	var functions, hosted []responses.ToolUnionParam
	for _, tool := range params.Tools {
		if tool.OfFunction != nil {
			functions = append(functions, tool)
		} else {
			hosted = append(hosted, tool)
		}
	}
	params.Tools = hosted
	if len(functions) > 0 {
		items := params.Input.OfInputItemList
		// Keep the reusable developer prefix ahead of request-specific tools.
		pos := 0
		for pos < len(items) && items[pos].OfMessage != nil && items[pos].OfMessage.Role == responses.EasyInputMessageRoleDeveloper {
			pos++
		}
		params.Input.OfInputItemList = slices.Insert(items, pos, responses.ResponseInputItemParamOfAdditionalTools(functions))
	}
	if params.Input.OfInputItemList == nil {
		params.Input.OfInputItemList = []responses.ResponseInputItemUnionParam{}
	}
	return params
}
