package conversation

import (
	"context"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/llm"
)

// .
// .
// .
// .

// .
// .
// .
type yieldTools struct {
	fakeTools
	yieldOn string
}

func (y *yieldTools) Execute(ctx context.Context, call llm.ToolCall) Observation {
	obs := y.fakeTools.Execute(ctx, call)
	if call.Function.Name == y.yieldOn {
		obs.EndTurn = true
	}
	return obs
}

func runYieldTurn(t *testing.T, script []llm.Response, predicted func() int) (*captureLLM, Result) {
	t.Helper()
	client := &captureLLM{script: script}
	tools := &yieldTools{fakeTools: fakeTools{results: map[string]string{"bash": "ok", "work": "Turn yielding — your gate frees when this round ends."}}, yieldOn: "work"}
	loop := New(client, tools, &fakeDefs{}, nil, nil,
		Config{MaxIterations: 20, PredictedThisTurn: predicted})
	res, err := loop.Run(context.Background(), "system", []llm.Message{{Role: "user", Content: "go"}})
	if err != nil {
		t.Fatal(err)
	}
	return client, res
}

func TestYieldEndsTheTurnWithoutContinuation(t *testing.T) {
	script := []llm.Response{
		resp("", toolCall("c1", "bash", `{}`)),
		resp("", toolCall("c2", "work", `{"action":"yield"}`)),
		textResp("yielded; waiting on workers"),
	}
	client, res := runYieldTurn(t, script, nil)

	if res.ContinuedAtCap {
		t.Fatal("a yielded turn flagged ContinuedAtCap — the continuation would re-take the freed gate")
	}
	if got := len(client.seen); got != 3 {
		t.Fatalf("turn took %d requests, want 3 — yield must end the turn at its round", got)
	}
	last := joined(client.seen[len(client.seen)-1])
	if !strings.Contains(last, "You yielded this turn") {
		t.Error("the yield boundary message is missing — the model was not told why the turn is ending")
	}
	if !strings.Contains(res.Spoken, "Turn yielded after 2 tool calls") {
		t.Errorf("the ending does not declare the yield: %q", res.Spoken)
	}
}

// .
// .
// .
// .
func TestYieldOutranksAnArmedSoftCap(t *testing.T) {
	script := []llm.Response{
		resp("", toolCall("c1", "bash", `{}`)),
		resp("", toolCall("c2", "bash", `{}`)),
		resp("", toolCall("c3", "bash", `{}`)),
		resp("", toolCall("c4", "bash", `{}`)),
		resp("", toolCall("c5", "bash", `{}`)),
		resp("", toolCall("c6", "work", `{"action":"yield"}`)),
		textResp("yielded at the cap boundary"),
	}
	client, res := runYieldTurn(t, script, func() int { return 1 })

	if res.ContinuedAtCap {
		t.Fatal("yield on the forced-final round still flagged ContinuedAtCap — the continuation would re-take the freed gate")
	}
	if got := len(client.seen); got != 7 {
		t.Fatalf("turn took %d requests, want 7", got)
	}
	if !strings.Contains(res.Spoken, "Turn yielded") {
		t.Errorf("yield ending lost to the cap ending: %q", res.Spoken)
	}
}
