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

const firstActMark = "first call that changes something"

func firstActCount(msgs []llm.Message) int {
	n := 0
	for _, m := range msgs {
		n += strings.Count(m.Content, firstActMark)
	}
	return n
}

func notRead(tc llm.ToolCall) bool { return tc.Function.Name != "read" }

func runFirstActTurn(t *testing.T, names []string, predicted func() int, isAct func(llm.ToolCall) bool, planState func() (bool, bool)) *captureLLM {
	t.Helper()
	script := make([]llm.Response, 0, len(names)+1)
	for _, n := range names {
		script = append(script, resp("", toolCall("c", n, `{}`)))
	}
	script = append(script, textResp("done"))
	client := &captureLLM{script: script}
	loop := New(client, &fakeTools{results: map[string]string{"read": "ok", "write": "ok", "bash": "ok"}}, &fakeDefs{}, nil, nil,
		Config{HeuristicNudges: true, MaxIterations: len(names) + 2, PredictedThisTurn: predicted, IsAct: isAct, PlanState: planState})
	if _, err := loop.Run(context.Background(), "system", []llm.Message{{Role: "user", Content: "go"}}); err != nil {
		t.Fatal(err)
	}
	return client
}

func zero() int { return 0 }

// .
// .
func TestFirstActAskFiresWithTheFirstActAfterReads(t *testing.T) {
	client := runFirstActTurn(t, []string{"read", "read", "write", "write"}, zero, notRead, func() (bool, bool) { return false, true })
	last := client.seen[len(client.seen)-1]
	if got := firstActCount(last); got != 1 {
		t.Fatalf("first-act ask appeared %d times, want exactly 1", got)
	}
	// .
	// .
	// .
	if n := firstActCount(client.seen[2]); n != 0 {
		t.Fatalf("asked after a read round: %d", n)
	}
	if n := firstActCount(client.seen[3]); n != 1 {
		t.Fatalf("the round that carried the first act was not asked (count %d)", n)
	}
}

// .
// .
func TestFirstActAskSilentWhenAnEstimateStands(t *testing.T) {
	client := runFirstActTurn(t, []string{"read", "write"}, func() int { return 5 }, notRead, func() (bool, bool) { return true, true })
	for i, msgs := range client.seen {
		if n := firstActCount(msgs); n != 0 {
			t.Fatalf("request %d: asked %d time(s) though steps= stands", i, n)
		}
	}
}

// .
// .
func TestFirstActAskSilentOnAnAllReadTurn(t *testing.T) {
	client := runFirstActTurn(t, []string{"read", "read", "read"}, zero, notRead, func() (bool, bool) { return false, true })
	for i, msgs := range client.seen {
		if n := firstActCount(msgs); n != 0 {
			t.Fatalf("request %d: an all-read turn was asked %d time(s)", i, n)
		}
	}
}

func TestFirstActAskOncePerTurn(t *testing.T) {
	client := runFirstActTurn(t, []string{"write", "write", "write", "write"}, zero, notRead, func() (bool, bool) { return false, true })
	if got := firstActCount(client.seen[len(client.seen)-1]); got != 1 {
		t.Fatalf("asked %d times across four act rounds, want 1", got)
	}
}

// .
// .
func TestFirstActAskOffWithoutClassifierOrAccessor(t *testing.T) {
	for name, c := range map[string]struct {
		predicted func() int
		isAct     func(llm.ToolCall) bool
	}{
		"nil classifier": {zero, nil},
		"nil accessor":   {nil, notRead},
	} {
		client := runFirstActTurn(t, []string{"write", "write"}, c.predicted, c.isAct, func() (bool, bool) { return false, true })
		for i, msgs := range client.seen {
			if n := firstActCount(msgs); n != 0 {
				t.Fatalf("%s: request %d asked %d time(s) — absent must mean off", name, i, n)
			}
		}
	}
}

// .
// .
func TestFirstActAskNamesTheKeysAndTheSession(t *testing.T) {
	active := runFirstActTurn(t, []string{"write"}, zero, notRead, func() (bool, bool) { return false, true })
	text := joined(active.seen[len(active.seen)-1])
	// .
	// .
	// .
	// .
	for _, want := range []string{"work update plan=", "steps=", "independent=", "falsifier", "what it IS", "then act"} {
		if !strings.Contains(text, want) {
			t.Errorf("active-session ask is missing %q", want)
		}
	}
	if strings.Contains(text, "work start") {
		t.Error("active-session ask must not name `work start` — the wording that taught the identity to stack sessions")
	}
	none := runFirstActTurn(t, []string{"write"}, zero, notRead, func() (bool, bool) { return false, false })
	text = joined(none.seen[len(none.seen)-1])
	for _, want := range []string{"work start", "work update plan=", "steps=", "independent=", "falsifier"} {
		if !strings.Contains(text, want) {
			t.Errorf("no-session ask is missing %q", want)
		}
	}
}

// .
// .
// .
func TestFirstActAskStandsInForTheScalarAsk(t *testing.T) {
	client := runFirstActTurn(t, []string{"read", "write", "write"}, zero, notRead, func() (bool, bool) { return true, true })
	last := client.seen[len(client.seen)-1]
	if got := firstActCount(last); got != 1 {
		t.Fatalf("first-act ask count %d, want 1", got)
	}
	if strings.Contains(joined(last), "under a standing plan") {
		t.Fatal("the scalar ask fired beside the first-act ask — two asks for the same keys")
	}
}

// .
// .
// .
func TestCheckpointNoteNamesTheThreeOutcomes(t *testing.T) {
	for _, note := range []string{checkpointNote(3, 6, 4), checkpointNote(0, 32, 4)} {
		for _, want := range []string{checkpointMark, "next_move=", "done with its scope", "continue from that point", "stop with why"} {
			if !strings.Contains(note, want) {
				t.Errorf("checkpoint note missing %q: %s", want, note)
			}
		}
	}
	if !strings.Contains(checkpointNote(0, 32, 4), "steps=") {
		t.Error("the undeclared variant must still ask for steps=")
	}
}

// .
// .
func TestPlanNudgeBackstopNamesTheBriefOnlyUnderASession(t *testing.T) {
	if !strings.Contains(planNudgeNote(8, true), "brief above") {
		t.Error("active-session plan ask must name the brief it backs up")
	}
	if strings.Contains(planNudgeNote(8, false), "brief above") {
		t.Error("no-session plan ask names a brief the turn never rendered")
	}
}
