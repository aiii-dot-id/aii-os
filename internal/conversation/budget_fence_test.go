package conversation

import (
	"context"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/llm"
)

// budget_fence_test.go — the turn token fence (external review P2-6).
// Live motivation 2026-08-26: 1,945,593 tokens over 101 calls ran to
// the context wall because the accounting summed and nothing refused.

type optsLLM struct {
	script []llm.Response
	i      int
	opts   []llm.ChatOptions
	seen   [][]llm.Message
}

func (c *optsLLM) Chat(_ context.Context, msgs []llm.Message, o llm.ChatOptions) (*llm.Response, error) {
	c.seen = append(c.seen, append([]llm.Message(nil), msgs...))
	c.opts = append(c.opts, o)
	r := c.script[c.i%len(c.script)]
	c.i++
	return &r, nil
}

type oneDefs struct{}

func (oneDefs) ToolDefinitions() []llm.ToolDefinition {
	return []llm.ToolDefinition{{Type: "function", Function: llm.ToolFunction{Name: "bash"}}}
}

func usedResp(total int, calls ...llm.ToolCall) llm.Response {
	r := resp("", calls...)
	r.Usage = llm.Usage{Reported: true, PromptTokens: total, TotalTokens: total}
	return r
}

func TestTurnTokenBudgetFencesTheTurn(t *testing.T) {
	client := &optsLLM{script: []llm.Response{
		usedResp(300_000, toolCall("a", "bash", `{}`)),
		usedResp(300_000, toolCall("b", "bash", `{}`)),
		textResp("wrapped up what I had"),
	}}
	loop := New(client, &fakeTools{results: map[string]string{"bash": "ok"}}, oneDefs{}, nil, nil,
		Config{MaxIterations: 10, TurnTokenBudget: 500_000})
	res, err := loop.Run(context.Background(), "system", []llm.Message{{Role: "user", Content: "go"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(client.opts) != 3 {
		t.Fatalf("made %d calls, want 2 working + 1 bounded wrap-up", len(client.opts))
	}
	if len(client.opts[0].Tools) == 0 {
		t.Fatal("working calls must offer tools (harness self-check)")
	}
	if len(client.opts[2].Tools) != 0 {
		t.Fatal("the wrap-up call still offered tools — the fence must end the tool loop")
	}
	if !strings.Contains(res.Spoken, "token budget (500000) is spent") {
		t.Fatalf("the turn did not declare its fence: %q", res.Spoken)
	}
}

// A provider that reports no usage still draws against the fence, by
// estimate: unknown spend is never free.
func TestSilentUsageCountsAgainstTheFence(t *testing.T) {
	big := resp(strings.Repeat("y", 40_000), toolCall("a", "bash", `{}`))
	// Usage deliberately unreported.
	client := &optsLLM{script: []llm.Response{big, textResp("done what I could")}}
	loop := New(client, &fakeTools{results: map[string]string{"bash": "ok"}}, oneDefs{}, nil, nil,
		Config{MaxIterations: 10, TurnTokenBudget: 1_000})
	res, err := loop.Run(context.Background(), "system", []llm.Message{{Role: "user", Content: "go"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(client.opts) != 2 {
		t.Fatalf("made %d calls, want 1 silent working call + 1 wrap-up", len(client.opts))
	}
	if !strings.Contains(res.Spoken, "is spent") {
		t.Fatalf("silent spend did not trip the fence: %q", res.Spoken)
	}
}

// Under budget, nothing changes: no declaration, all calls offered tools.
func TestUnderBudgetTurnsAreUntouched(t *testing.T) {
	client := &optsLLM{script: []llm.Response{
		usedResp(1_000, toolCall("a", "bash", `{}`)),
		usedResp(1_000, toolCall("b", "bash", `{}`)),
		textResp("done"),
	}}
	loop := New(client, &fakeTools{results: map[string]string{"bash": "ok"}}, oneDefs{}, nil, nil,
		Config{MaxIterations: 10, TurnTokenBudget: 600_000})
	res, err := loop.Run(context.Background(), "system", []llm.Message{{Role: "user", Content: "go"}})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(res.Spoken, "token budget") {
		t.Fatalf("an under-budget turn declared a fence: %q", res.Spoken)
	}
}
