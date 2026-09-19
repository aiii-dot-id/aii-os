// .
// .
// .
// .

package app

import (
	"context"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/conversation"
	"github.com/aiii-dot-id/aii-os/internal/llm"
)

type reviewFoldClient struct {
	call     llm.ToolCall
	requests [][]llm.Message
}

func (c *reviewFoldClient) Chat(_ context.Context, messages []llm.Message, _ llm.ChatOptions) (*llm.Response, error) {
	c.requests = append(c.requests, append([]llm.Message(nil), messages...))
	msg := llm.Message{Role: "assistant", Content: "done"}
	if len(c.requests) == 1 {
		msg.Content = ""
		msg.ToolCalls = []llm.ToolCall{c.call}
	}
	return &llm.Response{Choices: []llm.Choice{{Message: msg, FinishReason: "stop"}}}, nil
}

type reviewFoldTools struct{}

func (reviewFoldTools) Execute(context.Context, llm.ToolCall) conversation.Observation {
	return conversation.Observation{Text: strings.Repeat("observation ", 1000)}
}
func (reviewFoldTools) ToolDefinitions() []llm.ToolDefinition { return nil }

func TestReviewFoldMustNotLicenseMutationReplay(t *testing.T) {
	for _, tc := range []struct{ name, args string }{
		{"work", `{"action":"update","next_move":"publish candidate"}`},
		{"shell", `{"command":"find . -name '*.tmp' -delete -print"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var call llm.ToolCall
			call.ID, call.Type = "call1", "function"
			call.Function.Name, call.Function.Arguments = tc.name, tc.args
			c := &reviewFoldClient{call: call}
			cfg := defaultConfig()
			cfg.Agency = armedNudges(cfg.Agency)
			loop := conversation.New(c, reviewFoldTools{}, reviewFoldTools{}, nil, nil, conversation.Config{
				MaxIterations: 4, ContextBudgetTokens: 1400, IsAct: (&App{}).firstActHook(*cfg),
			})
			if _, err := loop.Run(context.Background(), "review", []llm.Message{{Role: "user", Content: "continue"}}); err != nil {
				t.Fatal(err)
			}
			if len(c.requests) < 2 {
				t.Fatal("no request after tool execution")
			}
			var folded string
			for _, m := range c.requests[1] {
				if m.Role == "tool" {
					folded = m.Content
				}
			}
			if !strings.Contains(folded, "tool result folded") {
				t.Fatalf("test did not reach fold: %q", folded)
			}
			if strings.Contains(folded, "may be repeated exactly") {
				t.Fatalf("side-effecting call was advertised as safe to replay: %s", folded)
			}
		})
	}
}
