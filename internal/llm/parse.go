package llm

import (
	"encoding/json"
	"strings"
)

// ParsedAction is a single action extracted from the LLM response.
// An action is either a verb call or a tool call.
type ParsedAction struct {
	Type       string // "verb" or "tool"
	Name       string // verb name (note, recall, send, work, commit, tools) or tool name (read, bash, etc.)
	Args       map[string]interface{}
	ToolCallID string // For tool results that need to be sent back
}

// ParseResponse extracts verb calls and tool calls from an LLM response.
//
// The LLM communicates in two ways:
// 1. Tool calls (function calling) — structured, preferred for tools
// 2. Text content with verb directives — for identity verbs (note, recall, etc.)
//
// We parse both: tool calls become tool actions, and text content is
// scanned for verb directives in the format:
//
//	note("content")
//	recall(query="...")
//	send(to="operator", message="...")
//	work(action="start", description="...")
//	commit(variant="belief.promote", id="...")
//	tools(depth=2)
func ParseResponse(resp *Response) ([]ParsedAction, string) {
	if len(resp.Choices) == 0 {
		return nil, ""
	}

	choice := resp.Choices[0]
	var actions []ParsedAction
	textOutput := choice.Message.Content

	// Parse tool calls (function calling)
	for _, tc := range choice.Message.ToolCalls {
		var args map[string]interface{}
		if tc.Function.Arguments != "" {
			json.Unmarshal([]byte(tc.Function.Arguments), &args)
		}
		actions = append(actions, ParsedAction{
			Type:       "tool",
			Name:       tc.Function.Name,
			Args:       args,
			ToolCallID: tc.ID,
		})
	}

	// Parse verb directives from text content
	if textOutput != "" {
		verbActions := parseVerbDirectives(textOutput)
		actions = append(actions, verbActions...)
	}

	return actions, textOutput
}

// parseVerbDirectives scans text for verb() patterns.
//
// A directive is recognized ONLY when it stands alone on its own line
// (optionally indented): "note(...)" as a complete line is an act;
// "note(...)" mentioned inside a sentence ("to save something, write
// note(\"like this\")") is prose, not an act. The text path exists for
// providers without function calling — the structured tool-call path is
// always preferred. Line-anchoring prevents accidental ledger mints from
// the resident explaining its own verbs (2026-08-17 review).
func parseVerbDirectives(text string) []ParsedAction {
	var actions []ParsedAction

	// Look for verb patterns: verb_name(args)
	verbs := []string{"note", "recall", "send", "work", "commit", "tools"}

	for _, verb := range verbs {
		pattern := verb + "("
		idx := 0
		for {
			pos := strings.Index(text[idx:], pattern)
			if pos == -1 {
				break
			}
			pos += idx

			// Line anchor: the directive must start a line (only
			// whitespace before it on the line). An inline mention is
			// prose about the verb, not the verb.
			lineStart := strings.LastIndexByte(text[:pos], '\n') + 1
			if strings.TrimLeft(text[lineStart:pos], " \t") != "" {
				idx = pos + len(pattern)
				continue
			}

			// Find matching close paren
			depth := 1
			start := pos + len(pattern)
			end := start
			for end < len(text) && depth > 0 {
				if text[end] == '(' {
					depth++
				} else if text[end] == ')' {
					depth--
				}
				if depth > 0 {
					end++
				}
			}

			if end < len(text) {
				argStr := text[start:end]
				args := parseArgs(argStr)
				actions = append(actions, ParsedAction{
					Type: "verb",
					Name: verb,
					Args: args,
				})
				idx = end + 1
			} else {
				break
			}
		}
	}

	return actions
}

// parseArgs parses key=value, key="value", or positional "value" arguments.
func parseArgs(s string) map[string]interface{} {
	args := make(map[string]interface{})

	s = strings.TrimSpace(s)
	if s == "" {
		return args
	}

	// Try JSON object first
	if strings.HasPrefix(s, "{") {
		var m map[string]interface{}
		if json.Unmarshal([]byte(s), &m) == nil {
			return m
		}
	}

	// Parse key=value pairs
	parts := splitArgs(s)
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}

		eq := strings.Index(part, "=")
		if eq == -1 {
			// Positional argument — store as "_positional"
			args["_positional"] = unquote(part)
			continue
		}

		key := strings.TrimSpace(part[:eq])
		val := strings.TrimSpace(part[eq+1:])
		args[key] = unquote(val)
	}

	return args
}

// splitArgs splits on commas not inside quotes.
func splitArgs(s string) []string {
	var parts []string
	depth := 0
	inString := false
	start := 0

	for i, c := range s {
		switch c {
		case '"':
			inString = !inString
		case '(', '[', '{':
			if !inString {
				depth++
			}
		case ')', ']', '}':
			if !inString {
				depth--
			}
		case ',':
			if !inString && depth == 0 {
				parts = append(parts, s[start:i])
				start = i + 1
			}
		}
	}
	parts = append(parts, s[start:])

	return parts
}

// unquote removes surrounding quotes from a string.
func unquote(s string) interface{} {
	s = strings.TrimSpace(s)
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		return s[1 : len(s)-1]
	}
	return s
}

// FormatToolResult formats a tool result as a message for the next LLM call.
func FormatToolResult(toolCallID, content string) Message {
	return Message{
		Role:       "tool",
		Content:    content,
		ToolCallID: toolCallID,
	}
}

// BuildMessages constructs the message list for an LLM call.
func BuildMessages(systemPrompt string, conversation []Message, toolResults []Message) []Message {
	msgs := []Message{{Role: "system", Content: systemPrompt}}
	msgs = append(msgs, conversation...)
	msgs = append(msgs, toolResults...)
	return msgs
}

// Ensure fmt is used
