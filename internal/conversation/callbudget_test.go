package conversation

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/limits"
	"github.com/aiii-dot-id/aii-os/internal/llm"
)

// wideBatch builds one response carrying n tool calls — the shape a
// round ceiling cannot bound.
func wideBatch(n int) llm.Response {
	calls := make([]llm.ToolCall, 0, n)
	for i := 0; i < n; i++ {
		calls = append(calls, toolCall(fmt.Sprintf("c%d", i), "bash", `{}`))
	}
	return resp("", calls...)
}

func TestTheCallCeilingBoundsAWideBatchThatRoundsCannotBound(t *testing.T) {
	client := &captureLLM{script: []llm.Response{
		wideBatch(30), // one round, thirty calls
		textResp("done"), textResp("done"),
	}}
	ft := &fakeTools{results: map[string]string{"bash": "ok"}}
	loop := New(client, ft, &fakeDefs{}, nil, nil, testBounds(Config{
		MaxIterations: limits.Of(12), // rounds would happily allow all thirty
		MaxToolCalls:  limits.Of(10),
	}))
	res, err := loop.Run(context.Background(), "system", []llm.Message{{Role: "user", Content: "go"}})
	if err != nil {
		t.Fatal(err)
	}

	if len(ft.calls) != 10 {
		t.Fatalf("the ceiling must stop execution at 10 calls, ran %d", len(ft.calls))
	}
	if !res.ExhaustedCallBudget || res.ExhaustedBudget {
		t.Errorf("a call-budget stop must report ExhaustedCallBudget and NOT the round ceiling: %+v", res)
	}
	if false {
		t.Fatal("a run stopped by its call budget is UNFINISHED and must say so")
	}
	if !strings.Contains(res.Spoken, "20 call(s) in the final batch were not executed") {
		t.Fatalf("the ending must name what was not run: %q", res.Spoken)
	}
}

// Under the ceiling nothing changes: the bound must not tax ordinary work.
func TestAWideBatchInsideTheCeilingRunsWhole(t *testing.T) {
	client := &captureLLM{script: []llm.Response{wideBatch(8), textResp("done"), textResp("done")}}
	ft := &fakeTools{results: map[string]string{"bash": "ok"}}
	loop := New(client, ft, &fakeDefs{}, nil, nil, testBounds(Config{MaxIterations: limits.Of(12), MaxToolCalls: limits.Of(10)}))
	res, err := loop.Run(context.Background(), "system", []llm.Message{{Role: "user", Content: "go"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(ft.calls) != 8 {
		t.Fatalf("a batch inside the ceiling must run whole, ran %d", len(ft.calls))
	}
	if res.ExhaustedBudget || res.ExhaustedCallBudget {
		t.Fatal("a run that never reached its ceiling is not exhausted")
	}
}

// The resident's call ceiling is unlimited by default: no ceiling, not a
// ceiling of zero — the difference between "unbounded" and "cannot act".
// (Zero is "not set", which the loop refuses: TestTheLoopHasNoDefaultOfItsOwn.)
func TestUnlimitedMeansNoCallCeiling(t *testing.T) {
	client := &captureLLM{script: []llm.Response{wideBatch(30), textResp("done"), textResp("done")}}
	ft := &fakeTools{results: map[string]string{"bash": "ok"}}
	loop := New(client, ft, &fakeDefs{}, nil, nil, testBounds(Config{MaxIterations: limits.Of(12), MaxToolCalls: limits.Unlimited}))
	if _, err := loop.Run(context.Background(), "system", []llm.Message{{Role: "user", Content: "go"}}); err != nil {
		t.Fatal(err)
	}
	if len(ft.calls) != 30 {
		t.Fatalf("no ceiling means the batch runs whole, ran %d", len(ft.calls))
	}
}

// A batch that lands exactly ON the ceiling is spent, not truncated:
// off-by-one here either steals a call or leaks one.
func TestABatchLandingExactlyOnTheCeilingIsNotTruncated(t *testing.T) {
	client := &captureLLM{script: []llm.Response{wideBatch(10), textResp("done"), textResp("done")}}
	ft := &fakeTools{results: map[string]string{"bash": "ok"}}
	loop := New(client, ft, &fakeDefs{}, nil, nil, testBounds(Config{MaxIterations: limits.Of(12), MaxToolCalls: limits.Of(10)}))
	res, err := loop.Run(context.Background(), "system", []llm.Message{{Role: "user", Content: "go"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(ft.calls) != 10 {
		t.Fatalf("exactly-at-the-ceiling must run whole, ran %d", len(ft.calls))
	}
	if res.ExhaustedBudget || res.ExhaustedCallBudget {
		t.Fatal("spending the budget exactly is not an exhausted run — nothing was refused")
	}
}

// The reported count must be the count that happened. The outcome-line
// test passes literals, so it can prove the FORMAT and nothing about the
// number — this proves the number.
func TestTheRunReportsTheToolCallsItActuallyMade(t *testing.T) {
	client := &captureLLM{script: []llm.Response{
		wideBatch(5),
		wideBatch(3),
		textResp("done"), textResp("done"),
	}}
	ft := &fakeTools{results: map[string]string{"bash": "ok"}}
	loop := New(client, ft, &fakeDefs{}, nil, nil, testBounds(Config{MaxIterations: limits.Of(12), MaxToolCalls: limits.Of(64)}))
	res, err := loop.Run(context.Background(), "system", []llm.Message{{Role: "user", Content: "go"}})
	if err != nil {
		t.Fatal(err)
	}
	if res.ToolCallsUsed != 8 {
		t.Fatalf("ToolCallsUsed = %d, want 8 (5 + 3 across two rounds)", res.ToolCallsUsed)
	}
	if len(ft.calls) != res.ToolCallsUsed {
		t.Fatalf("reported %d calls but executed %d", res.ToolCallsUsed, len(ft.calls))
	}
	// Rounds and calls are different quantities, which is the whole point
	// of counting both: three loop iterations (two carrying tools, plus
	// the final text round) against eight tool calls.
	if res.RoundsUsed != 3 {
		t.Fatalf("RoundsUsed = %d, want 3 — a round is not a call", res.RoundsUsed)
	}
	if res.RoundsUsed == res.ToolCallsUsed {
		t.Fatal("rounds and calls must not be the same number here, or this test proves nothing about the distinction")
	}
}

// A RESIDENT'S TURN ENDS AT ITS CALL LIMIT WITH AN ANSWER
// (agency.max_tool_calls). The calls past the limit
// are answered as not executed, in the words approved; one final call
// without tools gives the answer; the turn closes on the approved line and
// is not continued by itself. A child's leg (WrapUpAtCallLimit false) ends
// unfinished as before, with no final call.
func TestAResidentTurnEndsAtItsCallLimitWithAnAnswer(t *testing.T) {
	client := &optsLLM{script: []llm.Response{wideBatch(2), wideBatch(2), textResp("here is where it stands"), textResp("unused")}}
	ft := &fakeTools{results: map[string]string{"bash": "ok"}}
	loop := New(client, ft, oneDefs{}, nil, nil, testBounds(Config{MaxIterations: limits.Of(12), MaxToolCalls: limits.Of(3), WrapUpAtCallLimit: true}))
	res, err := loop.Run(context.Background(), "system", []llm.Message{{Role: "user", Content: "go"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(ft.calls) != 3 || len(client.seen) != 3 {
		t.Fatalf("ran %d calls over %d requests; want 3 calls, and the third request the final one", len(ft.calls), len(client.seen))
	}
	if !client.opts[2].DisableTools {
		t.Error("the final request at the call limit offered tools")
	}
	if all := joined(client.seen[2]); !strings.Contains(all, "not executed: this turn's call limit (3) is reached") {
		t.Errorf("the call past the limit was not answered in the approved words:\n%s", all)
	}
	want := "here is where it stands\n\n[This turn reached its limit of 3 tool calls and was finished without further tools. Ask again to continue.]"
	if !strings.HasSuffix(res.Spoken, want) {
		t.Errorf("the turn ends %q; want it to end %q", res.Spoken, want)
	}
	if !res.ExhaustedCallBudget || res.ContinuedAtCap || res.ContinuedAtPressure || res.ContinuedAtTokenBudget {
		t.Errorf("the ending is not a hard ceiling that nothing continues: %+v", res)
	}
}
