package app

import (
	"context"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/conversation"
	"github.com/aiii-dot-id/aii-os/internal/llm"
	"github.com/aiii-dot-id/aii-os/internal/tools"
)

// .
// .
// .
// .
// .
// .
func TestAFoldedReadIsRecoverableOnADefaultConfiguration(t *testing.T) {
	reg := tools.NewRegistry(t.TempDir(), nil, tools.Timeouts{})
	cfg := defaultConfig()
	if (&App{}).firstActHook(*cfg) != nil {
		t.Fatal("fixture: the default configuration is expected to have the planning nudge off")
	}
	notice := func(name, args string, isAct func(llm.ToolCall) bool) string {
		var call llm.ToolCall
		call.ID, call.Type = "call1", "function"
		call.Function.Name, call.Function.Arguments = name, args
		c := &reviewFoldClient{call: call}
		loop := conversation.New(c, reviewFoldTools{}, reviewFoldTools{}, nil, nil, conversation.Config{
			MaxIterations: 4, ContextBudgetTokens: 1400, IsAct: isAct, ReplaySafe: replaySafeHook(reg),
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
	if n := notice("read", `{"file_path":"notes.txt"}`, nil); !strings.Contains(n, "may be repeated exactly") {
		t.Errorf("a genuine read was not told it can be repeated, on a default configuration: %s", n)
	}
	// .
	// .
	// .
	armed := defaultConfig()
	armed.Agency = armedNudges(armed.Agency)
	isAct := (&App{}).firstActHook(*armed)
	if n := notice("read", `{"file_path":"notes.txt"}`, isAct); !strings.Contains(n, "may be repeated exactly") {
		t.Errorf("armed: %s", n)
	}
	for name, args := range map[string]string{
		"work":  `{"action":"update","next_move":"publish candidate"}`,
		"shell": `{"command":"find . -name '*.tmp' -delete -print"}`,
		"write": `{"file_path":"x","content":"y"}`,
	} {
		if n := notice(name, args, isAct); strings.Contains(n, "may be repeated exactly") {
			t.Errorf("%s was licensed for replay: %s", name, n)
		}
	}
}
