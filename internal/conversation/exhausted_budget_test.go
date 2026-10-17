package conversation

import (
	"context"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/llm"
)

// callResp is a response that asks for one tool and never stops.
func callResp(name string) llm.Response {
	var tc llm.ToolCall
	tc.ID = "c"
	tc.Type = "function"
	tc.Function.Name = name
	tc.Function.Arguments = `{}`
	return llm.Response{Choices: []llm.Choice{{
		Message:      llm.Message{ToolCalls: []llm.ToolCall{tc}},
		FinishReason: "tool_calls",
	}}}
}

// A run that ends because it ran OUT OF ROUNDS must say so as a typed
// fact, not only in prose.
//
// The loop has always spoken the cap ("reached its limit of N tool
// calls"), so a caller could read it — by parsing English out of the
// spoken text. A sub-agent's outcome is delivered to the parent as
// evidence it acts on, and "unfinished" is exactly the part it must not
// have to infer. This is the same defect the grep and read work spent
// the day removing: declared in text, absent from the record.
func TestARunThatRanOutOfRoundsSaysSoTyped(t *testing.T) {
	client := &scriptLLM{script: []llm.Response{
		callResp("grep"), callResp("grep"), callResp("grep"),
		textResp("here is what I managed"),
	}}
	loop := New(client, &fakeTools{results: map[string]string{"grep": "found"}}, oneDefs{}, nil, nil,
		Config{MaxIterations: 3})
	res, err := loop.Run(context.Background(), "system", []llm.Message{{Role: "user", Content: "go"}})
	if err != nil {
		t.Fatal(err)
	}
	if !res.ExhaustedBudget {
		t.Error("a run stopped by its round ceiling did not report ExhaustedBudget")
	}
	if res.ContinuedAtCap {
		t.Error("the HARD ceiling is not the declared-budget ending; they must not be confused")
	}
	// ROUNDS AND PROVIDER CALLS ARE DIFFERENT UNITS, and this run proves
	// it: three tool rounds were allowed and taken, and a FOURTH provider
	// call was made afterwards to force the closing text. Reporting
	// Usage.Calls against the round ceiling read as "4 of 3".
	if res.RoundsUsed != 3 {
		t.Errorf("RoundsUsed = %d, want 3 — the loop ran three rounds", res.RoundsUsed)
	}
	if res.Usage.Calls == res.RoundsUsed {
		t.Errorf("provider calls (%d) equal rounds (%d); this run makes a wrap-up call after the last round, so the two must differ here — if they stopped differing, the units were conflated again",
			res.Usage.Calls, res.RoundsUsed)
	}
}

// The round ceiling and the token fence move with the operator's config
// (SetTurnBounds, on every reload): the next whole turn runs under the new
// pair, and zero takes the default, as at construction.
func TestSetTurnBoundsAppliesToTheNextWholeTurn(t *testing.T) {
	client := &scriptLLM{script: []llm.Response{
		callResp("grep"), textResp("one round was all"),
		callResp("grep"), callResp("grep"), textResp("two rounds were all"),
	}}
	loop := New(client, &fakeTools{results: map[string]string{"grep": "found"}}, oneDefs{}, nil, nil,
		Config{MaxIterations: 1, TurnTokenBudget: 1000})
	first, err := loop.Run(context.Background(), "system", []llm.Message{{Role: "user", Content: "go"}})
	if err != nil {
		t.Fatal(err)
	}
	loop.SetTurnBounds(2, 0, Checkpoint{})
	second, err := loop.Run(context.Background(), "system", []llm.Message{{Role: "user", Content: "go again"}})
	if err != nil {
		t.Fatal(err)
	}
	if first.RoundsUsed != 1 || !first.ExhaustedBudget || second.RoundsUsed != 2 || !second.ExhaustedBudget {
		t.Fatalf("rounds %d then %d; want the ceiling of each turn, 1 then 2", first.RoundsUsed, second.RoundsUsed)
	}
	if got := loop.cfg.TurnTokenBudget; got != 600_000 {
		t.Fatalf("a zero fence is the default, as at construction: %d", got)
	}
}

// yieldingTools ends the turn from inside a tool, the way `work yield`
// does.
type yieldingTools struct{}

func (yieldingTools) Execute(ctx context.Context, call llm.ToolCall) Observation {
	return Observation{Text: "gate released", EndTurn: true}
}

// A YIELD reaches the same closing branch as the hard cap — the loop
// enters it on `i == MaxIterations-1 || i == softCapFinal || yielded` —
// and a yield is a CHOSEN ending, not an exhausted one. Without this
// case the flag could be set unconditionally in that branch and every
// test would still pass: I know, because my first "completed run" test
// did not catch exactly that mutation. It returns before the branch is
// ever reached, so it was guarding a different path than its name
// claimed.
func TestAYieldIsNotAnExhaustedRun(t *testing.T) {
	client := &scriptLLM{script: []llm.Response{
		callResp("work"),
		textResp("yielded, here is the status"),
	}}
	loop := New(client, yieldingTools{}, oneDefs{}, nil, nil, Config{MaxIterations: 10})
	res, err := loop.Run(context.Background(), "system", []llm.Message{{Role: "user", Content: "go"}})
	if err != nil {
		t.Fatal(err)
	}
	if res.ExhaustedBudget {
		t.Error("a yielded turn reported ExhaustedBudget — a chosen ending is not a ceiling")
	}
}

// A run that finishes on its own must NOT claim exhaustion — otherwise
// the flag says nothing, which is the failure mode a permanently-true
// flag always has.
func TestACompletedRunDoesNotClaimExhaustion(t *testing.T) {
	client := &scriptLLM{script: []llm.Response{textResp("done in one")}}
	loop := New(client, &fakeTools{}, oneDefs{}, nil, nil, Config{MaxIterations: 10})
	res, err := loop.Run(context.Background(), "system", []llm.Message{{Role: "user", Content: "go"}})
	if err != nil {
		t.Fatal(err)
	}
	if res.ExhaustedBudget {
		t.Error("a run that finished on its own reported ExhaustedBudget")
	}
}
