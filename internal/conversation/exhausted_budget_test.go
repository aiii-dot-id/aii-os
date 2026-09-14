package conversation

import (
	"context"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/llm"
)

// .
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

// .
// .
// .
// .
// .
// .
// .
// .
// .
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
	// .
	// .
	// .
	// .
	if res.RoundsUsed != 3 {
		t.Errorf("RoundsUsed = %d, want 3 — the loop ran three rounds", res.RoundsUsed)
	}
	if res.Usage.Calls == res.RoundsUsed {
		t.Errorf("provider calls (%d) equal rounds (%d); this run makes a wrap-up call after the last round, so the two must differ here — if they stopped differing, the units were conflated again",
			res.Usage.Calls, res.RoundsUsed)
	}
}

// .
// .
type yieldingTools struct{}

func (yieldingTools) Execute(ctx context.Context, call llm.ToolCall) Observation {
	return Observation{Text: "gate released", EndTurn: true}
}

// .
// .
// .
// .
// .
// .
// .
// .
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

// .
// .
// .
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
