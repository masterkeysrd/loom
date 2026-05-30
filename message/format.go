package message

import (
	"encoding/json"
	"fmt"
	"html"
	"strings"
)

type FormatOptions struct {
	UserPrefix      string
	AssistantPrefix string
	SystemPrefix    string
	ToolPrefix      string
	FormatType      FormatType
}

type FormatType string

const (
	FormatTypePrefix FormatType = "prefix"
	FormatTypeXML    FormatType = "xml"
)

func FormatMessages(messages MessageList, options *FormatOptions) (string, error) {
	if options == nil {
		options = &FormatOptions{
			UserPrefix:      "User",
			AssistantPrefix: "Assistant",
			SystemPrefix:    "System",
			ToolPrefix:      "Tool",
			FormatType:      FormatTypePrefix,
		}
	}

	if options.FormatType != FormatTypePrefix && options.FormatType != FormatTypeXML {
		return "", fmt.Errorf("unsupported format type: %s", options.FormatType)
	}

	rolePrefixes := map[Role]string{
		RoleSystem:    options.SystemPrefix,
		RoleAssistant: options.AssistantPrefix,
		RoleUser:      options.UserPrefix,
		RoleTool:      options.ToolPrefix,
	}

	if options.FormatType == FormatTypePrefix {
		return formatMessagesWithPrefix(messages, rolePrefixes), nil
	}

	return formatMessagesAsXML(messages, rolePrefixes), nil
}

func formatMessagesWithPrefix(messages MessageList, rolePrefixes map[Role]string) string {
	var sb strings.Builder
	for _, msg := range messages {
		prefix, ok := rolePrefixes[msg.Role()]
		if !ok {
			prefix = string(msg.Role())
		}
		fmt.Fprintf(&sb, "%s: %s\n", prefix, msg.GetContent().Text())
		if assistant, ok := msg.(*Assistant); ok {
			tcs := assistant.ToolCalls()
			if len(tcs) > 0 {
				sb.WriteString(toolCallsToString(tcs))
			}
		}
	}

	return sb.String()
}

func toolCallsToString(toolCalls []*ToolCall) string {
	var sb strings.Builder
	sb.WriteString("[")
	for _, tc := range toolCalls {
		jsonArgs, _ := json.Marshal(tc.Args)
		fmt.Fprintf(&sb, "ToolCall(ID: %s, ToolName: %s, Arguments: %s)", tc.ID, tc.Name, string(jsonArgs))
	}
	sb.WriteString("]")
	return sb.String()
}

func formatMessagesAsXML(messages MessageList, rolePrefixes map[Role]string) string {
	var sb strings.Builder
	for _, msg := range messages {
		var part strings.Builder
		prefix, ok := rolePrefixes[msg.Role()]
		if !ok {
			prefix = string(msg.Role())
		}
		var tcs []*ToolCall
		if assistant, ok := msg.(*Assistant); ok {
			tcs = assistant.ToolCalls()
		}

		fmt.Fprintf(&part, "<message role=\"%s\">\n", prefix)
		part.WriteString("  <content>\n")
		formatContentAsXML(&part, msg.GetContent())
		if len(tcs) > 0 {
			formatToolCallsAsXML(&part, tcs)
		}
		part.WriteString("  </content>\n")
		part.WriteString("</message>\n")

		sb.WriteString(part.String())
	}

	return sb.String()
}

func formatContentAsXML(sb *strings.Builder, content Content) {
	for _, block := range content {
		switch b := block.(type) {
		case *TextBlock:
			sb.WriteString(html.EscapeString(b.Text))
		case *ThinkingBlock:
			fmt.Fprintf(sb, "<thinking>%s</thinking>", html.EscapeString(b.Thinking))
		}
	}
}

func formatToolCallsAsXML(sb *strings.Builder, toolCalls []*ToolCall) {
	for _, tc := range toolCalls {
		jsonArgs, _ := json.Marshal(tc.Args)
		fmt.Fprintf(sb, "  <tool_call id=\"%s\" name=\"%s\">\n", tc.ID, tc.Name)
		sb.WriteString(html.EscapeString(string(jsonArgs)))
		sb.WriteString("  </tool_call>\n")
	}
}
