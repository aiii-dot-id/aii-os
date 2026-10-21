package conversation

import (
	"context"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/llm"
)

func TestACallWithOnlyTheProvidersAccountKeepsItMarked(t *testing.T) {
	first := resp("", toolCall("1", "read", "{}"))
	first.ModelID = "claude"
	first.Choices[0].ReasoningAccount = "Reading the file before answering."
	second := resp("", toolCall("2", "read", "{}"))
	second.ModelID = "glm"
	second.Choices[0].Reasoning = "raw tokens of the second call"
	second.Choices[0].ReasoningAccount = "a shorter version of the same" // a dialect never sets both; the loop still keeps one
	third := resp("", toolCall("3", "read", "{}"))
	third.ModelID = "claude"
	third.Choices[0].ReasoningAccount = " \n " // blank: no account
	last := textResp("done")
	last.ModelID = "claude"
	last.Choices[0].ReasoningAccount = "The file says done."
	client := &scriptLLM{script: []llm.Response{first, second, third, last}}
	loop := New(client, &fakeTools{results: map[string]string{"read": "ok"}}, &fakeDefs{}, nil, nil, testBounds(Config{ContextBudgetTokens: 100000}))
	res, err := loop.Run(context.Background(), "system", []llm.Message{{Role: "user", Content: "go"}})
	if err != nil {
		t.Fatal(err)
	}
	want := []CallReasoning{
		{Call: 1, Model: "claude", Text: "Reading the file before answering.", Account: true},
		{Call: 2, Model: "glm", Text: "raw tokens of the second call"},
		{Call: 4, Model: "claude", Text: "The file says done.", Account: true},
	}
	if len(res.Reasoning) != len(want) {
		t.Fatalf("reasoning of %d calls came back, want %d: %+v", len(res.Reasoning), len(want), res.Reasoning)
	}
	for i := range want {
		if res.Reasoning[i] != want[i] {
			t.Fatalf("entry %d: got %+v, want %+v", i, res.Reasoning[i], want[i])
		}
	}
	// Nothing of an account goes back to the provider: replay carries raw
	// reasoning a client put on the message, and no client puts this there.
	for i, req := range client.requests {
		for _, m := range req {
			if m.Reasoning != "" {
				t.Fatalf("request %d carried reasoning back that no client put on a message: %q", i+1, m.Reasoning)
			}
		}
	}
}

// A provider's account of a call made after a heard read is kept as any
// account is, marked as an account: nothing stands in for it.
func TestAnAccountAfterAHeardReadIsKept(t *testing.T) {
	ask := resp("", toolCall("1", "recall", `{"source":"heard","query":""}`))
	ask.Choices[0].ReasoningAccount = "Checking what was said nearby."
	after := textResp("done")
	after.Choices[0].ReasoningAccount = "A bystander said the PIN is 4471."
	client := &scriptLLM{script: []llm.Response{ask, after}}
	loop := New(client, &fakeTools{results: map[string]string{"recall": "[heard] the PIN is 4471"}}, &fakeDefs{}, nil, nil, testBounds(Config{ContextBudgetTokens: 100000}))
	res, err := loop.Run(context.Background(), "system", []llm.Message{{Role: "user", Content: "go"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Reasoning) != 2 || !res.Reasoning[0].Account || res.Reasoning[0].Text != "Checking what was said nearby." {
		t.Fatalf("the asking call's account is kept, marked: %+v", res.Reasoning)
	}
	if got := res.Reasoning[1]; got.Text != "A bystander said the PIN is 4471." || !got.Account {
		t.Fatalf("an account after a heard read was not kept as the provider gave it: %+v", got)
	}
}

// The route to a reply's thinking rides the history note, beside the
// route to the turns, when some thinking is recorded: and only then, and
// only where that note already stands, so a turn with all its history in
// view still closes on the words alone.
func TestTheHistoryNoteNamesTheRouteToThinking(t *testing.T) {
	with := TurnBlock("", 4, 0, false, true)
	if !strings.Contains(with, HistoryOmissionNote(4)) || !strings.Contains(with, ThinkingRouteNote) {
		t.Fatalf("turns out of view and thinking recorded: the block must name both routes:\n%s", with)
	}
	if strings.Index(with, ThinkingRouteNote) < strings.Index(with, HistoryOmissionNote(4)) {
		t.Fatalf("the thinking route comes after the turns' own:\n%s", with)
	}
	abridged := TurnBlock("", 0, 2, false, true)
	if !strings.Contains(abridged, "shown abridged") || !strings.Contains(abridged, ThinkingRouteNote) {
		t.Fatalf("turns abridged and thinking recorded: the block must name both routes:\n%s", abridged)
	}
	if without := TurnBlock("", 4, 0, false, false); strings.Contains(without, "source=reasoning") {
		t.Fatalf("no thinking is recorded and the block points at it:\n%s", without)
	}
	if quiet := TurnBlock("", 0, 0, false, true); quiet != "" {
		t.Fatalf("a turn with all its history in view closes on the words alone: %q", quiet)
	}
	for _, want := range []string{"recall(source=reasoning)", `source=reasoning) finds it by its words`, `query="turn:N"`} {
		if !strings.Contains(ThinkingRouteNote, want) {
			t.Fatalf("the note does not name %q: %s", want, ThinkingRouteNote)
		}
	}
	// The frame the app reserves is the block the loop sends.
	const facts = "### Rhythm — 1 turn"
	for _, omitted := range []int{0, 7} {
		block := TurnBlock(facts, omitted, 0, false, true)
		if got, frame := strings.Replace(block, facts, "", 1), TurnBlockFrame(omitted, true); got != frame {
			t.Fatalf("omitted=%d: block minus its facts %q, frame %q", omitted, got, frame)
		}
	}
}

// The loop asks once as a turn begins, and what it sends says so: with
// turns out of view, the current message closes on both routes when
// thinking is recorded and on the turns' route alone when it is not.
func TestTheLoopSendsTheThinkingRouteWhenThinkingIsKept(t *testing.T) {
	for _, tc := range []struct {
		name string
		kept func() bool
		want bool
	}{
		{"kept", func() bool { return true }, true},
		{"none kept", func() bool { return false }, false},
		{"a loop that is not the resident's", nil, false},
	} {
		asked := 0
		cfg := testBounds(Config{ContextBudgetTokens: 100000})
		if tc.kept != nil {
			kept := tc.kept
			cfg.ThinkingKept = func() bool { asked++; return kept() }
		}
		client := &scriptLLM{script: []llm.Response{textResp("done")}}
		loop := New(client, &fakeTools{}, &fakeDefs{}, nil, nil, cfg)
		history := []llm.Message{{Role: "user", Content: "earlier"}, {Role: "assistant", Content: "noted"}, {Role: "user", Content: "and now?"}}
		if _, err := loop.RunTurn(context.Background(), llm.Message{Role: "system", Content: "system"}, history, 9, ""); err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if len(client.requests) != 1 {
			t.Fatalf("%s: %d calls, want 1", tc.name, len(client.requests))
		}
		req := client.requests[0]
		last := req[len(req)-1].Content
		if !strings.Contains(last, HistoryOmissionNote(9)) {
			t.Fatalf("%s: the current message lacks the turns' route:\n%s", tc.name, last)
		}
		if got := strings.Contains(last, ThinkingRouteNote); got != tc.want {
			t.Fatalf("%s: thinking route present=%v, want %v:\n%s", tc.name, got, tc.want, last)
		}
		if tc.kept != nil && asked != 1 {
			t.Fatalf("%s: the store was asked %d times in one turn, want once", tc.name, asked)
		}
	}
}
