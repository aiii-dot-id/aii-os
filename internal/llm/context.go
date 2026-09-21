package llm

import (
	"encoding/json"
	"fmt"

	"github.com/aiii-dot-id/aii-os/internal/tokenestimate"
)

// .
type ContextLimitError struct {
	Required int
	Limit    int
}

func (e *ContextLimitError) Error() string {
	return fmt.Sprintf("LLM context admission: request needs approximately %d input tokens; limit is %d; protected identity, current input, and offered tools must fit", e.Required, e.Limit)
}

// .
// .
func EstimateInputTokens(messages []Message, tools []ToolDefinition) (int, error) {
	// .
	// .
	// .
	estimated := 0
	for _, m := range messages {
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
		}
		b, err := json.Marshal(payload)
		if err != nil {
			return 0, fmt.Errorf("marshal LLM message for token estimate: %w", err)
		}
		n := max(tokenestimate.Estimate(string(b)), m.OutputTokens)
		var ok bool
		estimated, ok = tokenSum(estimated, n)
		if !ok {
			return 0, fmt.Errorf("LLM input token estimate overflow")
		}
	}
	body, err := json.Marshal(struct {
		Tools []ToolDefinition `json:"tools,omitempty"`
	}{tools})
	if err != nil {
		return 0, fmt.Errorf("marshal LLM tools for token estimate: %w", err)
	}
	total, ok := tokenSum(estimated, tokenestimate.Estimate(string(body))+8)
	if !ok {
		return 0, fmt.Errorf("LLM input token estimate overflow")
	}
	return total, nil
}

// .
func ValidateInput(messages []Message, tools []ToolDefinition, limit int) error {
	_, err := AdmitInput(messages, tools, limit)
	return err
}

// .
// .
// .
// .
// .
func AdmitInput(messages []Message, tools []ToolDefinition, limit int) (required int, err error) {
	required, err = EstimateInputTokens(messages, tools)
	if err != nil {
		return 0, err
	}
	if limit > 0 && required > limit {
		return required, &ContextLimitError{Required: required, Limit: limit}
	}
	return required, nil
}
