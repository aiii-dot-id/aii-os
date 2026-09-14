package llm

import (
	"encoding/json"
)

// .
// .
type ParsedAction struct {
	Type       string
	Name       string
	Args       map[string]interface{}
	ToolCallID string
}

// .
// .
// .
// .
// .
// .
// .
// .
// .
func ParseResponse(resp *Response) ([]ParsedAction, string) {
	if len(resp.Choices) == 0 {
		return nil, ""
	}

	choice := resp.Choices[0]
	var actions []ParsedAction
	textOutput := choice.Message.Content

	// .
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

	return actions, textOutput
}

// .
func FormatToolResult(toolCallID, content string) Message {
	return Message{
		Role:       "tool",
		Content:    content,
		ToolCallID: toolCallID,
	}
}

// .
func BuildMessages(systemPrompt string, conversation []Message, toolResults []Message) []Message {
	msgs := []Message{{Role: "system", Content: systemPrompt}}
	msgs = append(msgs, conversation...)
	msgs = append(msgs, toolResults...)
	return msgs
}

// .
