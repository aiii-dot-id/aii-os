package conversation

import (
	"context"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/llm"
)

// .
// .
type answerTools struct {
	fakeTools
	answer string
}

func (y *answerTools) Execute(ctx context.Context, call llm.ToolCall) Observation {
	obs := y.fakeTools.Execute(ctx, call)
	if call.Function.Name == "work" {
		obs.EndTurn = true
		obs.EndTurnAnswer = y.answer
	}
	return obs
}

// .
// .
func TestYieldWithAnswerSpeaksItAndFlagsTheResult(t *testing.T) {
	script := []llm.Response{
		resp("", toolCall("c1", "bash", `{}`)),
		resp("", toolCall("c2", "work", `{"action":"yield","answer":"Established: X. Missing: Y."}`)),
		textResp("standing by"),
	}
	client := &captureLLM{script: script}
	tools := &answerTools{fakeTools: fakeTools{results: map[string]string{"bash": "ok", "work": "Turn yielding with your answer carried to the operator"}}, answer: "Established: X. Missing: Y."}
	res, err := New(client, tools, &fakeDefs{}, nil, nil, Config{MaxIterations: 20}).Run(context.Background(), "system", []llm.Message{{Role: "user", Content: "go"}})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Yielded {
		t.Fatal("Result.Yielded must be set by a yield")
	}
	ai, ci := strings.Index(res.Spoken, "Best current answer: Established: X. Missing: Y."), strings.Index(res.Spoken, "Turn yielded after")
	if ai < 0 || ci < 0 || ai > ci {
		t.Fatalf("the answer must be spoken before the closing line:\n%s", res.Spoken)
	}
	if res.ContinuedAtCap || res.ExhaustedBudget {
		t.Fatal("a yield is neither a cap nor an exhausted run")
	}
}

// .
func TestYieldWithoutAnswerIsUnchanged(t *testing.T) {
	script := []llm.Response{resp("", toolCall("c2", "work", `{"action":"yield"}`)), textResp("standing by")}
	client := &captureLLM{script: script}
	tools := &answerTools{fakeTools: fakeTools{results: map[string]string{"work": "Turn yielding"}}}
	res, err := New(client, tools, &fakeDefs{}, nil, nil, Config{MaxIterations: 20}).Run(context.Background(), "system", []llm.Message{{Role: "user", Content: "go"}})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Yielded || strings.Contains(res.Spoken, "Best current answer") {
		t.Fatalf("unexpected: yielded=%v spoken=%q", res.Yielded, res.Spoken)
	}
}
