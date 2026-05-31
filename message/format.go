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
		options = &FormatOptions{}
	}

	if options.UserPrefix == "" {
		options.UserPrefix = "User"
	}
	if options.AssistantPrefix == "" {
		options.AssistantPrefix = "Assistant"
	}
	if options.SystemPrefix == "" {
		options.SystemPrefix = "System"
	}
	if options.ToolPrefix == "" {
		options.ToolPrefix = "Tool"
	}
	if options.FormatType == "" {
		options.FormatType = FormatTypePrefix
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
		sb.WriteString(prefix)
		sb.WriteString(": ")
		for i, block := range msg.GetContent() {
			if i > 0 {
				sb.WriteString("\n")
			}
			switch b := block.(type) {
			case *TextBlock:
				sb.WriteString(b.Text)
			case *ThinkingBlock:
				sb.WriteString("[Thinking: ")
				sb.WriteString(b.Thinking)
				sb.WriteString("]")
			case *ImageBlock:
				if b.URL != "" {
					fmt.Fprintf(&sb, "[Image: %s]", b.URL)
				} else {
					fmt.Fprintf(&sb, "[Image: %s (%d bytes)]", b.MIMEType, len(b.Data))
				}
			case *AudioBlock:
				if b.URL != "" {
					fmt.Fprintf(&sb, "[Audio: %s]", b.URL)
				} else {
					fmt.Fprintf(&sb, "[Audio: %s (%d bytes)]", b.MIMEType, len(b.Data))
				}
			case *VideoBlock:
				if b.URL != "" {
					fmt.Fprintf(&sb, "[Video: %s]", b.URL)
				} else {
					fmt.Fprintf(&sb, "[Video: %s (%d bytes)]", b.MIMEType, len(b.Data))
				}
			case *DocumentBlock:
				if b.URL != "" {
					fmt.Fprintf(&sb, "[Document: %s]", b.URL)
				} else {
					fmt.Fprintf(&sb, "[Document: %s (%d bytes)]", b.MIMEType, len(b.Data))
				}
			}
		}
		sb.WriteString("\n")
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
		case *ImageBlock:
			if b.URL != "" {
				fmt.Fprintf(sb, "<image url=\"%s\" mime_type=\"%s\" />", html.EscapeString(b.URL), html.EscapeString(b.MIMEType))
			} else {
				fmt.Fprintf(sb, "<image mime_type=\"%s\" data_length=\"%d\" />", html.EscapeString(b.MIMEType), len(b.Data))
			}
		case *AudioBlock:
			if b.URL != "" {
				fmt.Fprintf(sb, "<audio url=\"%s\" mime_type=\"%s\" />", html.EscapeString(b.URL), html.EscapeString(b.MIMEType))
			} else {
				fmt.Fprintf(sb, "<audio mime_type=\"%s\" data_length=\"%d\" />", html.EscapeString(b.MIMEType), len(b.Data))
			}
		case *VideoBlock:
			if b.URL != "" {
				fmt.Fprintf(sb, "<video url=\"%s\" mime_type=\"%s\" />", html.EscapeString(b.URL), html.EscapeString(b.MIMEType))
			} else {
				fmt.Fprintf(sb, "<video mime_type=\"%s\" data_length=\"%d\" />", html.EscapeString(b.MIMEType), len(b.Data))
			}
		case *DocumentBlock:
			if b.URL != "" {
				fmt.Fprintf(sb, "<document url=\"%s\" mime_type=\"%s\" />", html.EscapeString(b.URL), html.EscapeString(b.MIMEType))
			} else {
				fmt.Fprintf(sb, "<document mime_type=\"%s\" data_length=\"%d\" />", html.EscapeString(b.MIMEType), len(b.Data))
			}
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
