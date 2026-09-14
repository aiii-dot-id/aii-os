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

const scalarMark = "this turn's numbers are undeclared"

func scalarCount(msgs []llm.Message) int {
	n := 0
	for _, m := range msgs {
		n += strings.Count(m.Content, scalarMark)
	}
	return n
}

type scalarCfg struct {
	planned   bool
	predicted int
	declared  int
	calib     func() (int, int, int)
}

func runScalarTurn(t *testing.T, rounds int, c scalarCfg) *captureLLM {
	t.Helper()
	script := make([]llm.Response, 0, rounds+1)
	for i := 0; i < rounds; i++ {
		script = append(script, resp("", toolCall("c", "bash", `{}`)))
	}
	script = append(script, textResp("done"))
	client := &captureLLM{script: script}
	loop := New(client, &fakeTools{results: map[string]string{"bash": "ok"}}, &fakeDefs{}, nil, nil, Config{HeuristicNudges: true,
		MaxIterations:     rounds + 2,
		PlanState:         func() (bool, bool) { return c.planned, true },
		FanoutState:       func() (int, int) { return c.declared, 0 },
		PredictedThisTurn: func() int { return c.predicted },
		Calibration:       c.calib,
	})
	if _, err := loop.Run(context.Background(), "system", []llm.Message{{Role: "user", Content: "go"}}); err != nil {
		t.Fatal(err)
	}
	return client
}

// .
// .
func TestScalarAskNamesOnlyTheMissingNumber(t *testing.T) {
	client := runScalarTurn(t, planNudgeAfterRounds+3, scalarCfg{
		planned: true, predicted: 0, declared: 3,
		calib: func() (int, int, int) { return 3, 39, 179 },
	})
	last := client.seen[len(client.seen)-1]
	if got := scalarCount(last); got != 1 {
		t.Fatalf("scalar ask appeared %d times, want exactly 1", got)
	}
	text := joined(last)
	if !strings.Contains(text, "steps=") {
		t.Error("missing steps= was not requested")
	}
	if strings.Contains(text, "independent=`") && strings.Contains(text, "declare `independent=") {
		t.Error("independent= was requested though 3 were already declared — the ask must name only what lapsed")
	}
	// .
	if !strings.Contains(text, "your last 3 estimate(s) totalled 39 calls; the turns took 179") {
		t.Error("the ask does not mirror the identity's measured record")
	}
}

func TestScalarAskOtherHalfAndBoth(t *testing.T) {
	// .
	client := runScalarTurn(t, planNudgeAfterRounds+3, scalarCfg{planned: true, predicted: 9, declared: 0})
	text := joined(client.seen[len(client.seen)-1])
	if !strings.Contains(text, "independent=") {
		t.Error("missing independent= was not requested")
	}
	if strings.Contains(text, "declare `steps=") {
		t.Error("steps= requested though already declared")
	}
	// .
	client = runScalarTurn(t, planNudgeAfterRounds+3, scalarCfg{planned: true, predicted: 0, declared: 0})
	text = joined(client.seen[len(client.seen)-1])
	if !strings.Contains(text, "steps=") || !strings.Contains(text, "independent=") {
		t.Error("both scalars missing; both must be requested")
	}
}

// .
// .
func TestScalarAskSilences(t *testing.T) {
	client := runScalarTurn(t, planNudgeAfterRounds+3, scalarCfg{planned: true, predicted: 9, declared: 2})
	for i, msgs := range client.seen {
		if scalarCount(msgs) != 0 {
			t.Fatalf("round %d: asked though both scalars are declared", i)
		}
	}
	client = runScalarTurn(t, planNudgeAfterRounds+3, scalarCfg{planned: false, predicted: 0, declared: 0})
	for i, msgs := range client.seen {
		if scalarCount(msgs) != 0 {
			t.Fatalf("round %d: scalar ask fired on an UNPLANNED turn — that case belongs to the plan ask", i)
		}
	}
	client = runScalarTurn(t, planNudgeAfterRounds+3, scalarCfg{planned: true, predicted: 0, declared: 3,
		calib: func() (int, int, int) { return 0, 0, 0 }})
	if strings.Contains(joined(client.seen[len(client.seen)-1]), "Mirror:") {
		t.Error("a mirror rendered with no record behind it")
	}
}

// .
// .
// .
// .
// .
// .
func TestScalarAskFiresAfterTheFirstRoundEvenForBigBatches(t *testing.T) {
	calls := make([]llm.ToolCall, 0, 30)
	for i := 0; i < 30; i++ {
		calls = append(calls, toolCall("c", "bash", `{}`))
	}
	client := &captureLLM{script: []llm.Response{
		{Choices: []llm.Choice{{Message: llm.Message{Role: "assistant", ToolCalls: calls}}}},
		textResp("done"),
	}}
	loop := New(client, &fakeTools{results: map[string]string{"bash": "ok"}}, &fakeDefs{}, nil, nil, Config{HeuristicNudges: true,
		MaxIterations:     4,
		PlanState:         func() (bool, bool) { return true, true },
		FanoutState:       func() (int, int) { return 2, 0 },
		PredictedThisTurn: func() int { return 0 },
	})
	if _, err := loop.Run(context.Background(), "system", []llm.Message{{Role: "user", Content: "go"}}); err != nil {
		t.Fatal(err)
	}
	if len(client.seen) < 2 {
		t.Fatal("second request never happened")
	}
	if scalarCount(client.seen[1]) != 1 {
		t.Fatalf("a 30-call first round did not produce the ask in request 2 — the deep threshold would have slept through it")
	}
}
