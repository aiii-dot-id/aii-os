package conversation

import (
	"context"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/limits"
	"github.com/aiii-dot-id/aii-os/internal/llm"
)

// windowProbe answers a call with a result of exactly the window its
// context says, as a tool that pages its answer from the top does.
type windowProbe struct{ seen []int }

func (p *windowProbe) Execute(ctx context.Context, _ llm.ToolCall) Observation {
	n := limits.ToolResultChars(ctx)
	p.seen = append(p.seen, n)
	return Observation{Text: "HEAD" + strings.Repeat("x", n-4)}
}

func TestEachToolCallIsToldTheWindowItsResultIsShownIn(t *testing.T) {
	probe := &windowProbe{}
	client := &scriptLLM{script: []llm.Response{resp("", toolCall("1", "pager", "{}")), textResp("done")}}
	loop := New(client, probe, &fakeDefs{}, nil, nil, testBounds(Config{MaxToolResultChars: 180}))
	if _, err := loop.Run(context.Background(), "system", []llm.Message{{Role: "user", Content: "one"}}); err != nil {
		t.Fatal(err)
	}
	if len(probe.seen) != 1 || probe.seen[0] != 180 {
		t.Fatalf("the call was told %v, want the turn's 180", probe.seen)
	}
	var shown string
	for _, m := range client.requests[len(client.requests)-1] {
		if m.Role == "tool" {
			shown = m.Content
		}
	}
	if !strings.HasPrefix(shown, "HEAD") || strings.Contains(shown, "truncated") {
		t.Fatalf("a result that fills its window is shown whole: %.60q", shown)
	}
	if limits.ToolResultChars(context.Background()) != limits.DefaultToolResultChars {
		t.Fatal("outside a turn a call has the default window")
	}
}
