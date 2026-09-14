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

const fanoutMark = "independent steps this turn and have spawned none"

func fanoutCount(msgs []llm.Message) int {
	n := 0
	for _, m := range msgs {
		n += strings.Count(m.Content, fanoutMark)
	}
	return n
}

func runFanoutTurn(t *testing.T, rounds int, fanout func() (int, int)) *captureLLM {
	t.Helper()
	script := make([]llm.Response, 0, rounds+1)
	for i := 0; i < rounds; i++ {
		script = append(script, resp("", toolCall("c", "bash", `{}`)))
	}
	script = append(script, textResp("done"))
	client := &captureLLM{script: script}
	loop := New(client, &fakeTools{results: map[string]string{"bash": "ok"}}, &fakeDefs{}, nil, nil,
		Config{HeuristicNudges: true, MaxIterations: rounds + 2, FanoutState: fanout})
	if _, err := loop.Run(context.Background(), "system", []llm.Message{{Role: "user", Content: "go"}}); err != nil {
		t.Fatal(err)
	}
	return client
}

// .
// .
func TestFanoutNudgeFiresOnDeclaredButNotSpawned(t *testing.T) {
	client := runFanoutTurn(t, fanoutNudgeAfterRounds+4, func() (int, int) { return 3, 0 })
	last := client.seen[len(client.seen)-1]
	if got := fanoutCount(last); got != 1 {
		t.Fatalf("fan-out note appeared %d times, want exactly 1", got)
	}
	text := joined(last)
	if !strings.Contains(text, "declared 3 independent steps") {
		t.Error("the note must echo the resident's own number back")
	}
	if !strings.Contains(text, "work spawn") {
		t.Error("the note must name the verb — an ask the model cannot act on is a complaint")
	}
	if !strings.Contains(text, "coupled") {
		t.Error("the note must leave the honest out: coupled steps stay serial by saying so")
	}
}

// .
func TestFanoutNudgeSilentOnceSpawned(t *testing.T) {
	client := runFanoutTurn(t, fanoutNudgeAfterRounds+4, func() (int, int) { return 3, 1 })
	for i, msgs := range client.seen {
		if n := fanoutCount(msgs); n != 0 {
			t.Fatalf("round %d: nudged %d time(s) though a spawn already happened", i, n)
		}
	}
}

// .
func TestFanoutNudgeNeedsAtLeastTwoDeclared(t *testing.T) {
	for _, declared := range []int{0, 1} {
		client := runFanoutTurn(t, fanoutNudgeAfterRounds+4, func() (int, int) { return declared, 0 })
		for i, msgs := range client.seen {
			if n := fanoutCount(msgs); n != 0 {
				t.Fatalf("declared=%d round %d: nudged %d time(s) — below the fan-out floor", declared, i, n)
			}
		}
	}
}

// .
func TestFanoutNudgeSilentShortTurnAndNilAccessor(t *testing.T) {
	client := runFanoutTurn(t, 3, func() (int, int) { return 5, 0 })
	for i, msgs := range client.seen {
		if n := fanoutCount(msgs); n != 0 {
			t.Fatalf("short turn round %d: nudged %d time(s)", i, n)
		}
	}
	client = runFanoutTurn(t, fanoutNudgeAfterRounds+4, nil)
	for i, msgs := range client.seen {
		if n := fanoutCount(msgs); n != 0 {
			t.Fatalf("nil accessor round %d: nudged %d time(s) — absent must mean off", i, n)
		}
	}
}

// .
// .
func TestFanoutNudgeArmsMidTurn(t *testing.T) {
	calls := 0
	client := runFanoutTurn(t, fanoutNudgeAfterRounds+6, func() (int, int) {
		calls++
		if calls < 3 {
			return 0, 0
		}
		return 2, 0
	})
	last := client.seen[len(client.seen)-1]
	if got := fanoutCount(last); got != 1 {
		t.Fatalf("fan-out note appeared %d times, want exactly 1 after a mid-turn declaration", got)
	}
}
