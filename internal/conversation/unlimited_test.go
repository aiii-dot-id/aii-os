package conversation

import (
	"context"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/limits"
	"github.com/aiii-dot-id/aii-os/internal/llm"
)

// A TURN WITH NO TOKEN FENCE PASSES A MILLION TOKENS WITH NO WRAP-UP (a
// local model the operator lifts "unlimited" on). Four calls of 300,000 reported tokens each run to the
// model's own answer; the fence's wrap-up and its closing line never come.
func TestALocalModelsTurnPassesAMillionTokensWithNoWrapUp(t *testing.T) {
	client := &optsLLM{script: []llm.Response{
		usedResp(300_000, toolCall("a", "bash", `{}`)),
		usedResp(300_000, toolCall("b", "bash", `{}`)),
		usedResp(300_000, toolCall("c", "bash", `{}`)),
		usedResp(300_000, toolCall("d", "bash", `{}`)),
		textResp("the whole answer"),
	}}
	loop := New(client, &fakeTools{results: map[string]string{"bash": "ok"}}, oneDefs{}, nil, nil,
		testBounds(Config{MaxIterations: limits.Of(30), TurnTokenBudget: limits.Unlimited}))
	res, err := loop.Run(context.Background(), "system", []llm.Message{{Role: "user", Content: "go"}})
	if err != nil {
		t.Fatal(err)
	}
	if res.Usage.TotalTokens < 1_000_000 || len(client.seen) != 5 {
		t.Fatalf("the turn spent %d tokens over %d requests; want past a million over five", res.Usage.TotalTokens, len(client.seen))
	}
	if res.ContinuedAtTokenBudget || strings.Contains(res.Spoken, "token budget") || res.Spoken != "the whole answer" {
		t.Fatalf("a turn with no fence was wrapped up at one: %q (%+v)", res.Spoken, res)
	}
}

// AN UNLIMITED TURN IS STILL FITTED TO THE WINDOW. Lifting the fence and the
// rounds lifts those two and nothing else: a turn that fills its window
// still ends on the context-pressure ending, as a fenced one does.
func TestAnUnlimitedTurnIsStillFittedToTheWindow(t *testing.T) {
	system := strings.Repeat("s", 670-len(showNote)) + systemAdditions()
	defs := &fakeDefs{defs: []llm.ToolDefinition{{Type: "function", Function: llm.ToolFunction{
		Name: "bash", Description: strings.Repeat("d", 900), Parameters: map[string]any{"type": "object"}}}}}
	client := &scriptLLM{script: []llm.Response{resp("", toolCall("c1", "bash", "{}")), textResp("what I have")}}
	loop := New(client, &fakeTools{results: map[string]string{"bash": strings.Repeat("r", 150)}}, defs, nil, nil, testBounds(Config{
		ContextBudgetTokens: 1000,
		MaxIterations:       limits.Unlimited,
		TurnTokenBudget:     limits.Unlimited,
	}))
	res, err := loop.Run(context.Background(), system, []llm.Message{{Role: "user", Content: "go"}})
	if err != nil {
		t.Fatal(err)
	}
	if !res.ContinuedAtPressure || !strings.Contains(res.Spoken, "The context filled during this turn after") {
		t.Fatalf("an unlimited turn was not fitted to its window: %q (%+v)", res.Spoken, res)
	}
}
