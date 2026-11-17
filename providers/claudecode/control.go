package loomclaudecode

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/masterkeysrd/loom/message"
	"github.com/masterkeysrd/loom/providers/claudecode/internal/protocol"
	"github.com/masterkeysrd/loom/tool"
)

// pendingMCPCall records an intercepted in-process MCP `tools/call` control
// request that is parked waiting for Loom's Tools node to return *message.Tool.
type pendingMCPCall struct {
	ControlRequestID string
	JSONRPCID        json.RawMessage
	ToolCallID       string
	ToolName         string
	Args             map[string]any
}

// handleIncomingControlRequest processes a `control_request` / `sdk_control_request`
// emitted by the CLI. It returns either an immediate `protocol.ControlResponse`
// to send back on stdin, or a `pendingMCPCall` when `tools/call` must be
// yielded back to Loom's Tools node.
func handleIncomingControlRequest(
	frame protocol.Frame,
	tools []tool.Definition,
	allowedNativeTools []string,
) (resp *protocol.ControlResponse, parked *pendingMCPCall, err error) {
	if frame.Request == nil {
		return nil, nil, fmt.Errorf("claudecode: control_request missing request body")
	}
	reqID := frame.RequestID
	if reqID == "" {
		reqID = frame.Request.RequestID
	}

	switch frame.Request.Subtype {
	case "mcp_message":
		return handleMCPMessage(reqID, frame.Request, tools)

	case "can_use_tool":
		allowed := isToolPermitted(frame.Request.ToolName, tools, allowedNativeTools)
		var body any
		if allowed {
			var parsedInput any = map[string]any{}
			if len(frame.Request.Input) > 0 {
				_ = json.Unmarshal(frame.Request.Input, &parsedInput)
			}
			body = map[string]any{
				"behavior":     "allow",
				"updatedInput": parsedInput,
			}
		} else {
			body = map[string]any{
				"behavior": "deny",
				"message":  fmt.Sprintf("tool %q is not enabled; use Loom-bound tools", frame.Request.ToolName),
			}
		}
		return protocol.NewSuccessControlResponse(reqID, body), nil, nil

	default:
		// Acknowledge unknown/hook control requests cleanly so the CLI does not stall.
		return protocol.NewSuccessControlResponse(reqID, map[string]any{}), nil, nil
	}
}

func handleMCPMessage(
	controlReqID string,
	req *protocol.IncomingControlRequest,
	tools []tool.Definition,
) (*protocol.ControlResponse, *pendingMCPCall, error) {
	var rpc protocol.JSONRPCRequest
	if err := json.Unmarshal(req.Message, &rpc); err != nil {
		return protocol.NewErrorControlResponse(controlReqID, fmt.Sprintf("invalid mcp jsonrpc payload: %v", err)), nil, nil
	}

	switch rpc.Method {
	case "initialize":
		return protocol.NewMCPSuccessControlResponse(controlReqID, rpc.ID, map[string]any{
			"protocolVersion": "2024-11-05",
			"capabilities": map[string]any{
				"tools": map[string]any{},
			},
			"serverInfo": map[string]any{
				"name":    DefaultMCPServerName,
				"version": "1.0.0",
			},
		}), nil, nil

	case "notifications/initialized":
		return protocol.NewMCPSuccessControlResponse(controlReqID, rpc.ID, map[string]any{}), nil, nil

	case "tools/list":
		defs := make([]protocol.MCPToolDef, 0, len(tools))
		for _, t := range tools {
			var schema any = map[string]any{"type": "object", "properties": map[string]any{}}
			if t.InputSchema != nil {
				schema = t.InputSchema
			}
			defs = append(defs, protocol.MCPToolDef{
				Name:        t.Name,
				Description: t.Description,
				InputSchema: schema,
			})
		}
		return protocol.NewMCPSuccessControlResponse(controlReqID, rpc.ID, map[string]any{"tools": defs}), nil, nil

	case "tools/call":
		var params protocol.MCPToolsCallParams
		if len(rpc.Params) > 0 {
			if err := json.Unmarshal(rpc.Params, &params); err != nil {
				return protocol.NewMCPErrorControlResponse(controlReqID, rpc.ID, -32602, err.Error()), nil, nil
			}
		}
		if params.Arguments == nil {
			params.Arguments = map[string]any{}
		}
		return nil, &pendingMCPCall{
			ControlRequestID: controlReqID,
			JSONRPCID:        slices.Clone(rpc.ID),
			ToolName:         normalizeToolName(params.Name),
			Args:             params.Arguments,
		}, nil

	default:
		return protocol.NewMCPErrorControlResponse(controlReqID, rpc.ID, -32601, fmt.Sprintf("method %q not found", rpc.Method)), nil, nil
	}
}

func buildMCPToolResultControlResponse(pending pendingMCPCall, toolMsg *message.Tool) *protocol.ControlResponse {
	var content []map[string]any
	if toolMsg != nil {
		for _, block := range toolMsg.Content {
			switch b := block.(type) {
			case *message.TextBlock:
				content = append(content, map[string]any{
					"type": "text",
					"text": b.Text,
				})
			}
		}
		if len(content) == 0 {
			if txt := toolMsg.Content.Text(); txt != "" {
				content = append(content, map[string]any{
					"type": "text",
					"text": txt,
				})
			} else if toolMsg.StructuredContent != nil {
				if data, err := toolMsg.CanonicalStructuredContent(); err == nil && len(data) > 0 {
					content = append(content, map[string]any{
						"type": "text",
						"text": string(data),
					})
				}
			}
		}
	}
	if len(content) == 0 {
		content = []map[string]any{{"type": "text", "text": ""}}
	}

	isErr := toolMsg != nil && toolMsg.IsError
	return protocol.NewMCPSuccessControlResponse(pending.ControlRequestID, pending.JSONRPCID, map[string]any{
		"content": content,
		"isError": isErr,
	})
}

func isToolPermitted(toolName string, loomTools []tool.Definition, allowedNative []string) bool {
	if strings.HasPrefix(toolName, "mcp__"+DefaultMCPServerName+"__") {
		normalized := normalizeToolName(toolName)
		for _, t := range loomTools {
			if t.Name == normalized {
				return true
			}
		}
		return false
	}
	return slices.Contains(allowedNative, toolName)
}

func normalizeToolName(name string) string {
	prefix := "mcp__" + DefaultMCPServerName + "__"
	return strings.TrimPrefix(name, prefix)
}
