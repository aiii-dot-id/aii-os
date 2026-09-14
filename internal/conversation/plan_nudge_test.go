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
// .
// .

const planNoteMark = "rounds into this turn"

func planNoteCount(msgs []llm.Message) int {
	n := 0
	for _, m := range msgs {
		n += strings.Count(m.Content, planNoteMark)
	}
	return n
}

func joined(msgs []llm.Message) string {
	var b strings.Builder
	for _, m := range msgs {
		b.WriteString(m.Content)
	}
	return b.String()
}

// .
// .
func runPlanTurn(t *testing.T, rounds int, planState func() (bool, bool)) *captureLLM {
	t.Helper()
	script := make([]llm.Response, 0, rounds+1)
	for i := 0; i < rounds; i++ {
		script = append(script, resp("", toolCall("c", "bash", `{}`)))
	}
	script = append(script, textResp("done"))
	client := &captureLLM{script: script}
	loop := New(client, &fakeTools{results: map[string]string{"bash": "ok"}}, &fakeDefs{}, nil, nil,
		Config{HeuristicNudges: true, MaxIterations: rounds + 2, PlanState: planState})
	if _, err := loop.Run(context.Background(), "system", []llm.Message{{Role: "user", Content: "go"}}); err != nil {
		t.Fatal(err)
	}
	return client
}

// .
// .
func TestPlanNudgeNoSessionAsksToStartThenPlan(t *testing.T) {
	client := runPlanTurn(t, planNudgeAfterRounds+4, func() (bool, bool) { return false, false })

	last := client.seen[len(client.seen)-1]
	if got := planNoteCount(last); got != 1 {
		t.Fatalf("plan note appeared %d times, want exactly 1", got)
	}
	text := joined(last)
	// .
	// .
	// .
	// .
	// .
	for _, want := range []string{"work start", "work update plan=", "steps=", "independent=", "need to know"} {
		if !strings.Contains(text, want) {
			t.Errorf("no-session note is missing %q — the ask must name the exact key the meter parses", want)
		}
	}
}

// .
// .
// .
func TestPlanNudgeActiveSessionAsksToUpdateNotStart(t *testing.T) {
	client := runPlanTurn(t, planNudgeAfterRounds+4, func() (bool, bool) { return false, true })

	last := client.seen[len(client.seen)-1]
	if got := planNoteCount(last); got != 1 {
		t.Fatalf("plan note appeared %d times, want exactly 1", got)
	}
	text := joined(last)
	if !strings.Contains(text, "work update plan=") {
		t.Error("active-session note must name `work update plan=`")
	}
	for _, want := range []string{"steps=", "independent="} {
		if !strings.Contains(text, want) {
			t.Errorf("active-session note is missing %q — both declared scalars, key-for-key", want)
		}
	}
	if !strings.Contains(text, "Do not start another session") {
		t.Error("active-session note must forbid a second `work start` — stacking is the failure it exists to prevent")
	}
	if strings.Contains(text, "`work start` one") {
		t.Error("active-session note still carries the start instruction — the exact wording that taught the identity to stack sessions")
	}
}

// .
func TestPlanNudgeSilentWhenThePlanExists(t *testing.T) {
	client := runPlanTurn(t, planNudgeAfterRounds+4, func() (bool, bool) { return true, true })
	for i, msgs := range client.seen {
		if n := planNoteCount(msgs); n != 0 {
			t.Fatalf("round %d: asked for a plan %d time(s) though one exists", i, n)
		}
	}
}

// .
// .
func TestPlanNudgeSilentOnAShortTurn(t *testing.T) {
	client := runPlanTurn(t, 3, func() (bool, bool) { return false, false })
	for i, msgs := range client.seen {
		if n := planNoteCount(msgs); n != 0 {
			t.Fatalf("round %d: a 3-round turn was asked for a plan %d time(s)", i, n)
		}
	}
}

// .
// .
func TestPlanNudgeOffWhenAccessorAbsent(t *testing.T) {
	client := runPlanTurn(t, planNudgeAfterRounds+4, nil)
	for i, msgs := range client.seen {
		if n := planNoteCount(msgs); n != 0 {
			t.Fatalf("round %d: nil PlanState still asked %d time(s) — absent must mean off", i, n)
		}
	}
}

// .
// .
func TestPlanNudgeReadsThePlanLive(t *testing.T) {
	planned := false
	client := runPlanTurn(t, planNudgeAfterRounds+4, func() (bool, bool) {
		defer func() { planned = true }()
		return planned, true
	})
	last := client.seen[len(client.seen)-1]
	if got := planNoteCount(last); got != 1 {
		t.Fatalf("plan note appeared %d times, want exactly 1 — the live read should ask once and then stop", got)
	}
}
