package app

import (
	"context"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/conversation"
	"github.com/aiii-dot-id/aii-os/internal/llm"
	"github.com/aiii-dot-id/aii-os/internal/tools"
)

func TestAFoldedReadIsRecoverableOnADefaultConfiguration(t *testing.T) {
	reg := tools.NewRegistry(t.TempDir(), nil, tools.Timeouts{})
	notice := func(name, args string) string {
		var call llm.ToolCall
		call.ID, call.Type = "call1", "function"
		call.Function.Name, call.Function.Arguments = name, args
		c := &reviewFoldClient{call: call}
		loop := conversation.New(c, reviewFoldTools{}, reviewFoldTools{}, nil, nil, conversation.Config{
			MaxIterations: 4, ContextBudgetTokens: 1400, ReplaySafe: replaySafeHook(reg),
		})
		if _, err := loop.Run(context.Background(), "review", []llm.Message{{Role: "user", Content: "continue"}}); err != nil {
			t.Fatal(err)
		}
		for _, m := range c.requests[1] {
			if m.Role == "tool" {
				if !strings.Contains(m.Content, "tool result folded") {
					t.Fatalf("the fixture did not reach a fold: %q", m.Content)
				}
				return m.Content
			}
		}
		t.Fatal("no tool message in the second request")
		return ""
	}
	if n := notice("read", `{"file_path":"notes.txt"}`); !strings.Contains(n, "may be repeated exactly") {
		t.Errorf("a genuine read was not told it can be repeated, on a default configuration: %s", n)
	}
	// The two the old wiring licensed are still refused, with a write.
	for name, args := range map[string]string{
		"work":  `{"action":"update","next_move":"publish candidate"}`,
		"shell": `{"command":"find . -name '*.tmp' -delete -print"}`,
		"write": `{"file_path":"x","content":"y"}`,
	} {
		if n := notice(name, args); strings.Contains(n, "may be repeated exactly") {
			t.Errorf("%s was licensed for replay: %s", name, n)
		}
	}
}
