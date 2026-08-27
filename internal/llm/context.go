package llm

import (
	"encoding/json"
	"fmt"

	"github.com/aiii-dot-id/aii-os/internal/tokenestimate"
)

// ContextLimitError refuses a request that cannot fit the active model.
type ContextLimitError struct {
	Required int
	Limit    int
}

func (e *ContextLimitError) Error() string {
	return fmt.Sprintf("LLM context admission: request needs approximately %d input tokens; limit is %d; protected identity, current input, and offered tools must fit", e.Required, e.Limit)
}

// EstimateInputTokens counts the serialized message and tool portion of the
// input, including schemas and structured tool calls.
func EstimateInputTokens(messages []Message, tools []ToolDefinition) (int, error) {
	body, err := json.Marshal(struct {
		Messages []Message        `json:"messages"`
		Tools    []ToolDefinition `json:"tools,omitempty"`
	}{messages, tools})
	if err != nil {
		return 0, fmt.Errorf("marshal LLM input for token estimate: %w", err)
	}
	return tokenestimate.Estimate(string(body)), nil
}

// ValidateInput applies the configured pre-dispatch ceiling.
func ValidateInput(messages []Message, tools []ToolDefinition, limit int) error {
	if limit <= 0 {
		return nil
	}
	required, err := EstimateInputTokens(messages, tools)
	if err != nil {
		return err
	}
	if required > limit {
		return &ContextLimitError{Required: required, Limit: limit}
	}
	return nil
}
