package conversation

import (
	"context"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/llm"
)

// repeat_nudge_test.go — the turn-scale sibling of the announced-intent
// nudge. Live incident 2026-08-26: nine iterations re-fetching one
// complete 4KB diff; the harness was traced and exonerated (zero folds,
// full results delivered), so the detector watches what the model
// RECEIVES — result hashes — not the shapes of its calls, which varied
// every time.

// captureLLM scripts replies and keeps every message set it was sent,
// so tests can inspect exactly what the model would have seen.
type captureLLM struct {
	script []llm.Response
	i      int
	seen   [][]llm.Message
}

func (c *captureLLM) Chat(_ context.Context, msgs []llm.Message, _ llm.ChatOptions) (*llm.Response, error) {
	c.seen = append(c.seen, append([]llm.Message(nil), msgs...))
	r := c.script[c.i%len(c.script)]
	c.i++
	return &r, nil
}

func bigResult(seed string) string {
	return seed + ": " + strings.Repeat("x", 300)
}

func nudgeCount(msgs []llm.Message) int {
	n := 0
	for _, m := range msgs {
		n += strings.Count(m.Content, "[loop note — repeated result")
	}
	return n
}

func runRepeatTurn(t *testing.T, rounds int, results map[string]string, argVary bool) *captureLLM {
	t.Helper()
	script := make([]llm.Response, 0, rounds+1)
	for i := 0; i < rounds; i++ {
		args := `{}`
		if argVary {
			args = `{"n":` + strings.Repeat("9", i+1) + `}`
		}
		script = append(script, resp("", toolCall("c", "bash", args)))
	}
	script = append(script, textResp("done"))
	client := &captureLLM{script: script}
	loop := New(client, &fakeTools{results: results}, &fakeDefs{}, nil, nil, Config{MaxIterations: rounds + 2})
	if _, err := loop.Run(context.Background(), "system", []llm.Message{{Role: "user", Content: "go"}}); err != nil {
		t.Fatal(err)
	}
	return client
}

// THE INCIDENT'S SHAPE: varied calls, identical substantial results.
// On the third identical arrival the note welds to that result — once.
func TestRepeatedSubstantialResultsDrawOneNudge(t *testing.T) {
	client := runRepeatTurn(t, 5, map[string]string{"bash": bigResult("diff")}, true)
	final := client.seen[len(client.seen)-1]
	if got := nudgeCount(final); got != 1 {
		t.Fatalf("nudge appeared %d times across the turn, want exactly 1", got)
	}
	// Welded to a tool result, not floated as its own voice.
	var carrier llm.Message
	for _, m := range final {
		if strings.Contains(m.Content, "[loop note — repeated result") {
			carrier = m
		}
	}
	if !strings.Contains(carrier.Content, "diff: ") {
		t.Fatalf("the note is not welded to the repeated result: %.80q", carrier.Content)
	}
	// And it fired at the third arrival, not later: the fourth round's
	// request already carried it.
	if len(client.seen) < 4 || nudgeCount(client.seen[3]) != 1 {
		t.Fatalf("the note did not arrive at the third repeat")
	}
}

// One-word statuses repeat innocently forever.
func TestSmallRepeatsNeverNudge(t *testing.T) {
	client := runRepeatTurn(t, 6, map[string]string{"bash": "EXIT=0"}, false)
	if got := nudgeCount(client.seen[len(client.seen)-1]); got != 0 {
		t.Fatalf("a one-word status drew the nudge %d times", got)
	}
}

// Distinct substantial results are progress, not a loop.
func TestDistinctResultsNeverNudge(t *testing.T) {
	script := []llm.Response{
		resp("", toolCall("c", "a", `{}`)),
		resp("", toolCall("c", "b", `{}`)),
		resp("", toolCall("c", "d", `{}`)),
		textResp("done"),
	}
	client := &captureLLM{script: script}
	tools := &fakeTools{results: map[string]string{
		"a": bigResult("alpha"), "b": bigResult("beta"), "d": bigResult("delta"),
	}}
	loop := New(client, tools, &fakeDefs{}, nil, nil, Config{MaxIterations: 6})
	if _, err := loop.Run(context.Background(), "system", []llm.Message{{Role: "user", Content: "go"}}); err != nil {
		t.Fatal(err)
	}
	if got := nudgeCount(client.seen[len(client.seen)-1]); got != 0 {
		t.Fatalf("distinct results drew the nudge %d times", got)
	}
}
