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

const checkpointMark = "Checkpoint NOW"

// .
// .
// .
func checkpointCount(client *captureLLM) int {
	if len(client.seen) == 0 {
		return 0
	}
	last := client.seen[len(client.seen)-1]
	n := 0
	for _, m := range last {
		n += strings.Count(m.Content, checkpointMark)
	}
	return n
}

// .
// .
func runCapTurn(t *testing.T, toolRounds, maxIter int, predicted func() int) (*captureLLM, Result) {
	t.Helper()
	script := make([]llm.Response, 0, toolRounds+2)
	for i := 0; i < toolRounds; i++ {
		script = append(script, resp("", toolCall("c", "bash", `{}`)))
	}
	script = append(script, textResp("done"), textResp("done"))
	client := &captureLLM{script: script}
	loop := New(client, &fakeTools{results: map[string]string{"bash": "ok"}}, &fakeDefs{}, nil, nil,
		Config{HeuristicNudges: true, MaxIterations: maxIter, PredictedThisTurn: predicted})
	res, err := loop.Run(context.Background(), "system", []llm.Message{{Role: "user", Content: "go"}})
	if err != nil {
		t.Fatal(err)
	}
	return client, res
}

// .
// .
// .
// .
func TestDeclaredBudgetCapsTheTurnAndFlagsContinuation(t *testing.T) {
	client, res := runCapTurn(t, 10, 60, func() int { return 3 })

	if !res.ContinuedAtCap {
		t.Fatal("a turn ended by its declared budget did not flag ContinuedAtCap")
	}
	// .
	// .
	if got := len(client.seen); got != 11 {
		t.Errorf("turn took %d requests, want 11 — the declared budget did not bound it", got)
	}
	if got := checkpointCount(client); got != 1 {
		t.Errorf("checkpoint ask appeared %d times, want exactly 1", got)
	}
	all := joined(client.seen[len(client.seen)-1])
	for _, key := range []string{"work update next_move=", "plan="} {
		if !strings.Contains(all, key) {
			t.Errorf("the checkpoint ask omits %q — the machine key its consumer parses", key)
		}
	}
	if !strings.Contains(res.Spoken, "declared budget") || !strings.Contains(res.Spoken, "continues automatically in a fresh turn") {
		t.Errorf("the ending does not say what happened and what happens next: %q", res.Spoken)
	}
}

// .
// .
func TestUndeclaredTurnGetsTheFloor(t *testing.T) {
	client, res := runCapTurn(t, 36, 60, func() int { return 0 })

	if !res.ContinuedAtCap {
		t.Fatal("an undeclared deep turn did not flag ContinuedAtCap at the floor")
	}
	// .
	if got := len(client.seen); got != 37 {
		t.Errorf("turn took %d requests, want 37 — the floor did not bound it", got)
	}
	all := joined(client.seen[len(client.seen)-1])
	if !strings.Contains(all, "no declared budget") || !strings.Contains(all, "declare steps=") {
		t.Error("the floor's ask does not tell the resident that declaring buys the budget")
	}
}

// .
// .
// .
func TestNoCapWithoutTheResidentWiring(t *testing.T) {
	client, res := runCapTurn(t, 40, 60, nil)

	if res.ContinuedAtCap {
		t.Fatal("a loop with no prediction wiring flagged ContinuedAtCap")
	}
	if got := len(client.seen); got != 41 {
		t.Errorf("turn took %d requests, want 41 (natural end) — an unwired loop was capped", got)
	}
	if got := checkpointCount(client); got != 0 {
		t.Errorf("checkpoint ask appeared %d times in an unwired loop, want 0", got)
	}
}

// .
// .
// .
func TestNaturalFinishInsideGraceDoesNotFlag(t *testing.T) {
	client, res := runCapTurn(t, 8, 60, func() int { return 3 })

	if res.ContinuedAtCap {
		t.Fatal("a turn that ended naturally inside the grace window flagged ContinuedAtCap")
	}
	if got := checkpointCount(client); got != 1 {
		t.Errorf("checkpoint ask appeared %d times, want 1 — it should have fired before the natural end", got)
	}
}

// .
// .
func TestSoftCapYieldsToTheHardCap(t *testing.T) {
	client, res := runCapTurn(t, 8, 8, func() int { return 3 })

	if res.ContinuedAtCap {
		t.Fatal("a hard-capped turn flagged ContinuedAtCap")
	}
	if got := checkpointCount(client); got != 0 {
		t.Errorf("checkpoint ask appeared %d times beside the hard cap, want 0", got)
	}
	if !strings.Contains(res.Spoken, "reached its limit") {
		t.Errorf("the hard cap lost its ask-again ending: %q", res.Spoken)
	}
}

// .
// .
func runCapTurnFamilyOff(t *testing.T, toolRounds, maxIter int, predicted func() int) (*captureLLM, Result) {
	t.Helper()
	script := make([]llm.Response, 0, toolRounds+2)
	for i := 0; i < toolRounds; i++ {
		script = append(script, resp("", toolCall("c", "bash", `{}`)))
	}
	script = append(script, textResp("done"), textResp("done"))
	client := &captureLLM{script: script}
	loop := New(client, &fakeTools{results: map[string]string{"bash": "ok"}}, &fakeDefs{}, nil, nil,
		Config{HeuristicNudges: false, MaxIterations: maxIter, PredictedThisTurn: predicted})
	res, err := loop.Run(context.Background(), "system", []llm.Message{{Role: "user", Content: "go"}})
	if err != nil {
		t.Fatal(err)
	}
	return client, res
}

// .
// .
// .
// .
// .
// .
func TestTheStructuralCapSurvivesTheHeuristicSwitch(t *testing.T) {
	client, res := runCapTurnFamilyOff(t, 10, 60, func() int { return 3 })

	if !res.ContinuedAtCap {
		t.Fatal("the declared-budget cap is loop control — switching heuristic prose off must not disable it")
	}
	if n := checkpointCount(client); n != 1 {
		t.Fatalf("the checkpoint receipt is part of the structural ending, want 1 got %d", n)
	}
}

// .
// .
// .
func TestTheHeuristicSwitchOffSilencesEveryProseFamily(t *testing.T) {
	s := &scriptLLM{script: []llm.Response{
		textResp("I found the config section. Now let me read the ledger schema."),
		resp("Reading it.", toolCall("1", "read", `{"file_path":"x"}`)),
		textResp("Done."),
	}}
	ft := &fakeTools{results: map[string]string{"read": "schema..."}}
	l := New(s, ft, &fakeDefs{}, &fakeTranscript{limit: 4000}, &fakeEmitter{}, Config{
		HeuristicNudges: false, MaxIterations: 10,
	})
	if _, err := l.Run(context.Background(), "sys", []llm.Message{{Role: "user", Content: "continue"}}); err != nil {
		t.Fatal(err)
	}
	if s.calls != 1 {
		t.Fatalf("family off: the announced-intent nudge must not fire, want 1 LLM call got %d", s.calls)
	}
}

// .
// .
// .
// .
func TestAMissingPredictionRunsToTheHardCapDeterministically(t *testing.T) {
	client, res := runCapTurn(t, 10, 60, nil)

	if res.ContinuedAtCap {
		t.Fatal("no prediction accessor means no declared budget to cap against — the hard cap owns the ending")
	}
	if n := checkpointCount(client); n != 0 {
		t.Fatalf("no checkpoint without a prediction accessor, got %d", n)
	}
}
