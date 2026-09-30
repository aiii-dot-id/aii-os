package wire

import (
	"encoding/json"
	"fmt"
	"strconv"
	"time"
	"unicode/utf8"
)

type ThinkingBlock struct {
	Kind      string
	Text      string
	Signature string
	Data      string
}

type Message struct {
	Role       string     `json:"role"`
	Content    string     `json:"content"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	StableLen  int        `json:"-"`

	CacheBefore bool `json:"-"`

	Abridged bool `json:"-"`

	Reasoning string `json:"-"`

	Thinking []ThinkingBlock `json:"-"`

	NativeContent json.RawMessage `json:"-"`
	NativeDialect string          `json:"-"`

	OutputTokens int `json:"-"`
}

type ToolDefinition struct {
	Type     string       `json:"type"`
	Function ToolFunction `json:"function"`
}

type ToolFunction struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	Parameters  map[string]interface{} `json:"parameters"`
}

type ToolCall struct {
	ID   string `json:"id"`
	Type string `json:"type"`

	EmissionOrdinal int `json:"-"`
	Function        struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type Choice struct {
	Message      Message `json:"message"`
	FinishReason string  `json:"finish_reason"`

	Reasoning string `json:"-"`
}

type Response struct {
	Choices   []Choice `json:"choices"`
	Usage     Usage    `json:"usage"`
	ModelID   string   `json:"-"`
	ID        string   `json:"id,omitempty"`
	RequestID string   `json:"-"`

	CallID      string          `json:"-"`
	Diagnostics json.RawMessage `json:"-"`
	StopDetails json.RawMessage `json:"-"`
	Duration    time.Duration   `json:"-"`
}

type ChatOptions struct {
	Tools          []ToolDefinition
	ThinkingBudget int

	Source string

	RequireTool        bool
	DisableTools       bool
	PreviousResponseID string
}

type IncompleteResponseError struct {
	Reason string
	Got    int
}

func (e *IncompleteResponseError) Error() string {
	why := "the provider ended it with " + strconv.Quote(e.Reason)
	switch e.Reason {
	case "length":
		why = "it was cut off at the output limit"
	case "refusal":
		why = "the provider declined the request"
	}
	return fmt.Sprintf("the model did not finish its reply — %s (%d characters had arrived): an unfinished reply is not a product", why, e.Got)
}

func Finished(c Choice) error {
	switch c.FinishReason {
	case "", "stop":
		return nil
	case "tool_calls":
		if len(c.Message.ToolCalls) > 0 {
			return nil
		}
	}
	return &IncompleteResponseError{Reason: c.FinishReason, Got: utf8.RuneCountInString(c.Message.Content)}
}
