package conversation

import (
	"context"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/llm"
)

// Every call's raw reasoning in a turn comes back with the turn, in call
// order, with the model that thought it; the identity records it beside
// the reply. A provider's thinking blocks are not raw (they arrive empty
// or summarized) and are not collected here.
func TestTheTurnCarriesEveryCallsRawReasoning(t *testing.T) {
	first := resp("", toolCall("1", "read", "{}"))
	first.ModelID = "glm"
	first.Choices[0].Reasoning = "I should read the file first."
	last := textResp("done")
	last.ModelID = "glm"
	last.Choices[0].Reasoning = "The file says done; say so."
	last.Choices[0].Message.Thinking = []llm.ThinkingBlock{{Kind: "thinking", Text: "a provider's summary"}}
	client := &scriptLLM{script: []llm.Response{first, last}}
	loop := New(client, &fakeTools{results: map[string]string{"read": "ok"}}, &fakeDefs{}, nil, nil, testBounds(Config{ContextBudgetTokens: 100000}))
	res, err := loop.Run(context.Background(), "system", []llm.Message{{Role: "user", Content: "go"}})
	if err != nil {
		t.Fatal(err)
	}
	want := []CallReasoning{
		{Call: 1, Model: "glm", Text: "I should read the file first."},
		{Call: 2, Model: "glm", Text: "The file says done; say so."},
	}
	if len(res.Reasoning) != len(want) {
		t.Fatalf("reasoning of %d calls came back, want %d: %+v", len(res.Reasoning), len(want), res.Reasoning)
	}
	for i := range want {
		if res.Reasoning[i] != want[i] {
			t.Fatalf("call %d: got %+v, want %+v", i+1, res.Reasoning[i], want[i])
		}
	}
}

// A call that returned no reasoning adds nothing, and the ordinal still
// names the call it came from.
func TestACallWithoutReasoningAddsNothing(t *testing.T) {
	first := resp("", toolCall("1", "read", "{}"))
	last := textResp("done")
	last.Choices[0].Reasoning = "only the second call thought aloud"
	client := &scriptLLM{script: []llm.Response{first, last}}
	loop := New(client, &fakeTools{results: map[string]string{"read": "ok"}}, &fakeDefs{}, nil, nil, testBounds(Config{ContextBudgetTokens: 100000}))
	res, err := loop.Run(context.Background(), "system", []llm.Message{{Role: "user", Content: "go"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Reasoning) != 1 || res.Reasoning[0].Call != 2 {
		t.Fatalf("want the second call's reasoning alone: %+v", res.Reasoning)
	}
}

// What the identity thought is the operator's to read whatever they had just
// read: a call made after the turn read heard speech keeps its reasoning as
// every call does, quoting what it read if it does. No notice stands in.
func TestReasoningAfterAHeardReadIsKept(t *testing.T) {
	ask := resp("", toolCall("1", "recall", `{"source":"heard","query":""}`))
	ask.Choices[0].Reasoning = "I should check what was just said nearby."
	after := textResp("done")
	after.Choices[0].Reasoning = "A bystander said the PIN is 4471."
	client := &scriptLLM{script: []llm.Response{ask, after}}
	loop := New(client, &fakeTools{results: map[string]string{"recall": "[heard] the PIN is 4471"}}, &fakeDefs{}, nil, nil, testBounds(Config{ContextBudgetTokens: 100000}))
	res, err := loop.Run(context.Background(), "system", []llm.Message{{Role: "user", Content: "go"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Reasoning) != 2 || res.Reasoning[0].Text != "I should check what was just said nearby." {
		t.Fatalf("the asking call's reasoning is kept: %+v", res.Reasoning)
	}
	if got := res.Reasoning[1]; got.Text != "A bystander said the PIN is 4471." || got.Account {
		t.Fatalf("reasoning after a heard read was not kept as the model gave it: %+v", got)
	}
}

// Within a turn the loop sends back whatever reasoning a message
// carries: a client whose provider entry replays puts it there, and one
// that does not leaves it off. The record comes from the choice either
// way, and the provider's response is left as it came.
func TestReasoningIsReplayedWithinTheTurnOnlyWhenTheClientPutsItThere(t *testing.T) {
	const thought = "I should read the file first."
	for _, replay := range []bool{false, true} {
		first := resp("", toolCall("1", "read", "{}"))
		first.Choices[0].Reasoning = thought
		if replay {
			first.Choices[0].Message.Reasoning = thought // as a replaying client hands it over
		}
		client := &scriptLLM{script: []llm.Response{first, textResp("done")}}
		loop := New(client, &fakeTools{results: map[string]string{"read": "ok"}}, &fakeDefs{}, nil, nil, testBounds(Config{ContextBudgetTokens: 100000}))
		res, err := loop.Run(context.Background(), "system", []llm.Message{{Role: "user", Content: "go"}})
		if err != nil {
			t.Fatal(err)
		}
		if len(client.requests) != 2 {
			t.Fatalf("replay=%v: %d calls, want 2", replay, len(client.requests))
		}
		sentBack, found := "", false
		for _, m := range client.requests[1] {
			if len(m.ToolCalls) > 0 { // the scripted response names no role
				sentBack, found = m.Reasoning, true
			}
		}
		if !found {
			t.Fatalf("replay=%v: the second call did not carry the first call's message", replay)
		}
		want := ""
		if replay {
			want = thought
		}
		if sentBack != want {
			t.Fatalf("replay=%v: the second call carried reasoning %q, want %q", replay, sentBack, want)
		}
		if len(res.Reasoning) != 1 || res.Reasoning[0].Text != thought {
			t.Fatalf("replay=%v: the turn's record lost the reasoning: %+v", replay, res.Reasoning)
		}
		if client.script[0].Choices[0].Reasoning != thought {
			t.Fatalf("replay=%v: the loop rewrote the provider's response", replay)
		}
	}
}

// REPLAY NEVER COSTS HISTORY. Under pressure the reasoning carried for
// replay goes before any history, replay stays off for the rest of the
// turn (a later call's reasoning is dropped silently), and the context
// line on the newest result says so once the request fits.
func TestReplayedReasoningGoesBeforeHistory(t *testing.T) {
	thought := strings.Repeat("the file must be read before the answer. ", 100)
	msgs := []llm.Message{
		{Role: "system", Content: "stable"},
		{Role: "user", Content: "an older question"},
		{Role: "assistant", Content: "an older answer"},
		{Role: "user", Content: "now"},
		{Role: "assistant", ToolCalls: []llm.ToolCall{toolCall("c1", "read", `{"file_path":"/tmp/x"}`)}, Reasoning: thought},
		{Role: "tool", ToolCallID: "c1", Content: "ok"},
	}
	without := append([]llm.Message(nil), msgs...)
	without[4].Reasoning = ""
	st := fitState{current: 3}
	if err := fitRequest(&msgs, &st, "stable", nil, estimate(t, without)+400, nil); err != nil {
		t.Fatalf("fit: %v", err)
	}
	if msgs[4].Reasoning != "" || !st.replayOff {
		t.Fatalf("the reasoning carried for replay stayed under pressure (replayOff=%v)", st.replayOff)
	}
	if st.abridged != 0 || st.omitted != 0 || msgs[1].Content != "an older question" {
		t.Fatalf("history yielded while replayed reasoning could go: abridged=%d omitted=%d", st.abridged, st.omitted)
	}
	if !strings.Contains(msgs[5].Content, replayOffLine) {
		t.Fatalf("the drop was not declared on the newest result: %q", msgs[5].Content)
	}

	msgs = append(msgs,
		llm.Message{Role: "assistant", ToolCalls: []llm.ToolCall{toolCall("c2", "read", `{"file_path":"/tmp/y"}`)}, Reasoning: "next"},
		llm.Message{Role: "tool", ToolCallID: "c2", Content: "ok"})
	if err := fitRequest(&msgs, &st, "stable", nil, 1000000, nil); err != nil {
		t.Fatalf("second fit: %v", err)
	}
	if msgs[6].Reasoning != "" {
		t.Fatal("replay resumed within the turn after pressure turned it off")
	}
	if !strings.Contains(msgs[7].Content, replayOffLine) {
		t.Fatalf("the newest result does not say replay is off: %q", msgs[7].Content)
	}
}

const replayOffLine = "your reasoning from earlier calls this turn was dropped to fit"

// A fold after the drop cannot erase the declaration: it is written once
// the request fits, onto whatever the newest result has become.
func TestADropIsDeclaredAfterAFoldInTheSameFit(t *testing.T) {
	msgs := []llm.Message{
		{Role: "system", Content: "stable"},
		{Role: "user", Content: "now"},
		{Role: "assistant", ToolCalls: []llm.ToolCall{toolCall("c1", "read", `{"file_path":"/tmp/x"}`)},
			Reasoning: strings.Repeat("plan the read. ", 200)},
		{Role: "tool", ToolCallID: "c1", Content: strings.Repeat("evidence line\n", 400)},
	}
	without := append([]llm.Message(nil), msgs...)
	without[2].Reasoning = ""
	st := fitState{current: 1}
	if err := fitRequest(&msgs, &st, "stable", nil, estimate(t, without)/2, nil); err != nil {
		t.Fatalf("fit: %v", err)
	}
	if msgs[2].Reasoning != "" || st.folded != 1 {
		t.Fatalf("want the reasoning dropped and the result folded: replayOff=%v folded=%d", st.replayOff, st.folded)
	}
	if !strings.Contains(msgs[3].Content, replayOffLine) {
		t.Fatalf("the fold erased the declaration: %q", msgs[3].Content)
	}
}

// The context warning tells the model tools may be withdrawn; reasoning
// carried for replay goes before that, so it does not make the context
// "nearly full".
func TestReplayedReasoningDoesNotRaiseTheTightWarning(t *testing.T) {
	msgs := []llm.Message{
		{Role: "system", Content: "stable"},
		{Role: "user", Content: "now"},
		{Role: "assistant", ToolCalls: []llm.ToolCall{toolCall("c1", "read", "{}")}, Reasoning: strings.Repeat("plan the read. ", 400)},
		{Role: "tool", ToolCallID: "c1", Content: "ok"},
	}
	without := append([]llm.Message(nil), msgs...)
	without[2].Reasoning = ""
	// Room for the warning itself, so without the rule it would fire.
	budget := estimate(t, msgs) + 150
	if estimate(t, msgs)*100 < budget*contextTightPercent || estimate(t, without)*100 >= budget*contextTightPercent {
		t.Fatal("setup: only the reasoning may push the request past the warning threshold")
	}
	st := fitState{current: 1}
	warnIfTight(&msgs, &st, estimate(t, msgs), budget)
	if st.warned {
		t.Fatal("reasoning that goes first raised the context-full warning")
	}
}
