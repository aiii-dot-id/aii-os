package llm

import (
	"encoding/json"
	"fmt"

	"github.com/aiii-dot-id/aii-os/internal/llm/wire"
	"github.com/aiii-dot-id/aii-os/internal/tokenestimate"
)

type ContextLimitError struct {
	Required int
	Limit    int
}

func (e *ContextLimitError) Error() string {
	return fmt.Sprintf("LLM context admission: request needs approximately %d input tokens; limit is %d; protected identity, current input, and offered tools must fit", e.Required, e.Limit)
}

func EstimateInputTokens(messages []Message, tools []ToolDefinition) (int, error) {
	return AdmitInput(messages, tools, 0)
}

func EstimateMessageTokens(m Message) (int, error) {

	var payload any = m
	if len(m.NativeContent) > 0 {
		payload = struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		}{m.Role, m.NativeContent}
	} else if len(m.Thinking) > 0 {
		payload = struct {
			Message
			Thinking []ThinkingBlock `json:"thinking"`
		}{m, m.Thinking}
	} else if m.Reasoning != "" {
		payload = wireMessage{m, m.Reasoning}
	}
	b, err := json.Marshal(payload)
	if err != nil {
		return 0, fmt.Errorf("marshal LLM message for token estimate: %w", err)
	}
	return max(tokenestimate.Estimate(string(b)), m.OutputTokens), nil
}

func EstimateToolTokens(tools []ToolDefinition) (int, error) {
	body, err := json.Marshal(struct {
		Tools []ToolDefinition `json:"tools,omitempty"`
	}{tools})
	if err != nil {
		return 0, fmt.Errorf("marshal LLM tools for token estimate: %w", err)
	}
	return tokenestimate.Estimate(string(body)) + 8, nil
}

func ValidateInput(messages []Message, tools []ToolDefinition, limit int) error {
	_, err := AdmitInput(messages, tools, limit)
	return err
}

func AdmitInput(messages []Message, tools []ToolDefinition, limit int) (required int, err error) {
	toolTokens, err := EstimateToolTokens(tools)
	if err != nil {
		return 0, err
	}
	return AdmitPriced(messages, toolTokens, limit)
}

func AdmitPriced(messages []Message, toolTokens, limit int) (required int, err error) {
	required = toolTokens
	for _, m := range messages {
		n, err := EstimateMessageTokens(m)
		if err != nil {
			return 0, err
		}
		var ok bool
		if required, ok = wire.TokenSum(required, n); !ok {
			return 0, fmt.Errorf("LLM input token estimate overflow")
		}
	}
	if limit > 0 && required > limit {
		return required, &ContextLimitError{Required: required, Limit: limit}
	}
	return required, nil
}
