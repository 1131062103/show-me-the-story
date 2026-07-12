package agent

import (
	"encoding/json"
	"strings"

	"showmethestory/internal/app/chat"
)

// ParseToolCall accepts the historical XML envelope, bare JSON, and function.name
// fallback. It returns only the first call, preserving the one-tool-call protocol.
func ParseToolCall(content string) *chat.ToolCall {
	content = strings.TrimSpace(content)
	if start := strings.Index(content, "<tool_call>"); start >= 0 {
		rest := content[start+len("<tool_call>"):]
		if end := strings.Index(rest, "</tool_call>"); end >= 0 {
			rest = rest[:end]
		}
		if call := parseJSONCall(strings.TrimSpace(rest)); call != nil {
			return call
		}
		if call := parseXMLCall(rest); call != nil {
			return call
		}
	}
	if call := parseJSONCall(content); call != nil {
		return call
	}
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "function.") {
			continue
		}
		rest := strings.TrimPrefix(line, "function.")
		open := strings.IndexByte(rest, '(')
		if open <= 0 {
			continue
		}
		args := strings.TrimSpace(strings.TrimSuffix(rest[open+1:], ")"))
		if args == "" {
			args = "{}"
		}
		if !json.Valid([]byte(args)) {
			args = "{}"
		}
		return &chat.ToolCall{Name: rest[:open], Arguments: json.RawMessage(args)}
	}
	return nil
}

func parseXMLCall(content string) *chat.ToolCall {
	nameStart, nameEnd := strings.Index(content, "<name>"), strings.Index(content, "</name>")
	if nameStart < 0 || nameEnd <= nameStart {
		return nil
	}
	name := strings.TrimSpace(content[nameStart+len("<name>") : nameEnd])
	if name == "" {
		return nil
	}
	args := json.RawMessage("{}")
	argStart, argEnd := strings.Index(content, "<arguments>"), strings.Index(content, "</arguments>")
	if argStart >= 0 && argEnd > argStart {
		candidate := strings.TrimSpace(content[argStart+len("<arguments>") : argEnd])
		if json.Valid([]byte(candidate)) {
			args = json.RawMessage(candidate)
		}
	}
	return &chat.ToolCall{Name: name, Arguments: args}
}

func parseJSONCall(content string) *chat.ToolCall {
	for remaining := content; ; {
		start := strings.IndexByte(remaining, '{')
		if start < 0 {
			return nil
		}
		remaining = remaining[start:]
		candidate := extractJSON(remaining)
		if candidate == "" {
			return nil
		}
		var raw map[string]json.RawMessage
		if json.Unmarshal([]byte(candidate), &raw) == nil {
			name := raw["name"]
			if name == nil {
				name = raw["tool"]
			}
			var parsed string
			if json.Unmarshal(name, &parsed) == nil && parsed != "" {
				args := raw["arguments"]
				if args == nil {
					args = json.RawMessage("{}")
				}
				return &chat.ToolCall{Name: parsed, Arguments: args}
			}
		}
		remaining = remaining[len(candidate):]
	}
}

func extractJSON(content string) string {
	depth, end := 0, -1
	quoted, escaped := false, false
	for i := 0; i < len(content); i++ {
		c := content[i]
		if escaped {
			escaped = false
			continue
		}
		if quoted && c == '\\' {
			escaped = true
			continue
		}
		if c == '"' {
			quoted = !quoted
			continue
		}
		if quoted {
			continue
		}
		if c == '{' {
			depth++
		}
		if c == '}' {
			depth--
			if depth == 0 {
				end = i + 1
				break
			}
		}
	}
	if end < 0 {
		return ""
	}
	return content[:end]
}

func StripToolCallTags(content string) string {
	var result strings.Builder
	for {
		start := strings.Index(content, "<tool_call>")
		if start < 0 {
			result.WriteString(content)
			break
		}
		result.WriteString(content[:start])
		end := strings.Index(content[start:], "</tool_call>")
		if end < 0 {
			break
		}
		content = content[start+end+len("</tool_call>"):]
	}
	return strings.TrimSpace(result.String())
}

func outputTruncated(reason, content string, call *chat.ToolCall) bool {
	if reason != "length" || !strings.Contains(content, "<tool_call>") {
		return false
	}
	start := strings.Index(content, "<tool_call>")
	return !strings.Contains(content[start+len("<tool_call>"):], "</tool_call>") || call == nil
}
