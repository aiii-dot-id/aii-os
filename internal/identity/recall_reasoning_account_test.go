package identity

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/store"
)

func recallOut(t *testing.T, engine *Engine, args map[string]any) string {
	t.Helper()
	out, err := engine.ExecuteAction(context.Background(), "verb", "recall", args)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestAProvidersAccountIsRecalledAsWhatItIs(t *testing.T) {
	engine, st, _, _, _ := setupEngine(t)
	summary := "Weighed the osprey sighting against the tide table and trusted the table."
	seq := recordReply(t, st, "The tide table is the better source.",
		[]store.ReasoningCall{{Model: "claude", Text: summary, Account: true}})

	listed := recallOut(t, engine, map[string]any{"source": "reasoning", "query": "osprey"})
	if !strings.Contains(listed, fmt.Sprintf("turn %d", seq)) || !strings.Contains(listed, "claude ("+store.AccountMark+")") {
		t.Fatalf("the listing must name the turn and mark the model:\n%s", listed)
	}
	whole := recallOut(t, engine, map[string]any{"source": "reasoning", "query": fmt.Sprintf("turn:%d", seq)})
	if !strings.Contains(whole, "[call 1 · claude · "+store.AccountMark+"]\n"+summary) {
		t.Fatalf("the whole read must show the account under its marked header:\n%s", whole)
	}
	for name, out := range map[string]string{"listing": listed, "whole read": whole} {
		if !strings.Contains(out, reasoningLegend) {
			t.Fatalf("the %s shows a provider's account and does not say what the mark means:\n%s", name, out)
		}
		if strings.Contains(out, "raw deliberation") {
			t.Fatalf("the %s calls a provider's account raw:\n%s", name, out)
		}
		if !strings.Contains(out, "not something you said") {
			t.Fatalf("the %s is not labelled as thinking:\n%s", name, out)
		}
	}
	// The body of a whole read still begins after the head, and ends with
	// its label, as a raw one does.
	if !strings.Contains(whole, "\n\n[call 1 · claude") || !strings.Contains(whole, fmt.Sprintf("[end of turn %d's reasoning, part 1 of 1", seq)) {
		t.Fatalf("the whole read lost its shape:\n%s", whole)
	}
}

// Raw reasoning reads as it did: no mark, and no sentence about one.
func TestRawReasoningCarriesNoMarkAndNoLegend(t *testing.T) {
	engine, st, _, _, _ := setupEngine(t)
	seq := recordReply(t, st, "done", []store.ReasoningCall{{Model: "glm", Text: "The curlew call came from the east marsh."}})
	for name, out := range map[string]string{
		"listing":    recallOut(t, engine, map[string]any{"source": "reasoning", "query": "curlew"}),
		"whole read": recallOut(t, engine, map[string]any{"source": "reasoning", "query": fmt.Sprintf("turn:%d", seq)}),
	} {
		if strings.Contains(out, store.AccountMark) || strings.Contains(out, reasoningLegend) {
			t.Fatalf("the %s marks raw reasoning as a provider's account:\n%s", name, out)
		}
		if !strings.Contains(out, "east marsh") {
			t.Fatalf("the %s lost the reasoning:\n%s", name, out)
		}
	}
}

// An answer that finds none says when reasoning is kept at all: raw, or a
// provider's own version.
func TestAnEmptyAnswerSaysWhenReasoningIsKept(t *testing.T) {
	engine, _, _, _, _ := setupEngine(t)
	for name, args := range map[string]map[string]any{
		"nothing recorded":         {"source": "reasoning"},
		"a turn without reasoning": {"source": "reasoning", "query": "turn:99999"},
	} {
		out := recallOut(t, engine, args)
		if !strings.Contains(out, reasoningKept) || !strings.Contains(out, "summary") {
			t.Fatalf("%s: the answer does not say when reasoning is kept:\n%s", name, out)
		}
		if strings.Contains(out, "only when your model returns it raw") {
			t.Fatalf("%s: the answer still says only raw reasoning is kept:\n%s", name, out)
		}
	}
}

// A conversation hit that is a reply whose thinking is recorded says so
// and names the one call that reads it, in a recall across the record
// and in the conversation's own listing. A hit with none says nothing.
func TestAConversationHitNamesTheRouteToItsThinking(t *testing.T) {
	engine, st, _, _, _ := setupEngine(t)
	with := recordReply(t, st, "The lapwing nests are past the second gate.",
		[]store.ReasoningCall{{Model: "glm", Text: "The second gate was named in the survey."}})
	without, err := st.AddConversationTurnSeq("resident", "The lapwing count is twelve.")
	if err != nil {
		t.Fatal(err)
	}
	asked, err := st.AddConversationTurnSeq("operator", "Where are the lapwing nests?")
	if err != nil {
		t.Fatal(err)
	}
	route := func(seq uint64) string {
		return fmt.Sprintf(`[its thinking is kept: recall source=reasoning query="turn:%d"]`, seq)
	}
	for name, out := range map[string]string{
		"a recall across the record": recallOut(t, engine, map[string]any{"query": "lapwing"}),
		"the conversation's listing": recallOut(t, engine, map[string]any{"source": "conversation", "query": "lapwing"}),
	} {
		if !strings.Contains(out, "second gate") || !strings.Contains(out, "count is twelve") {
			t.Fatalf("%s: setup: both replies must be hits:\n%s", name, out)
		}
		if !strings.Contains(out, route(with)) {
			t.Fatalf("%s: the reply whose thinking is kept does not name the way to it:\n%s", name, out)
		}
		for _, seq := range []uint64{without, asked} {
			if strings.Contains(out, route(seq)) {
				t.Fatalf("%s: turn %d has no thinking recorded and its hit points at some:\n%s", name, seq, out)
			}
		}
		// The route is on the reply's own line, after its text.
		for _, line := range strings.Split(out, "\n") {
			if strings.Contains(line, route(with)) && !strings.Contains(line, "second gate") {
				t.Fatalf("%s: the route is not on its reply's line: %q", name, line)
			}
		}
		// The thinking itself is still reached only when named.
		if strings.Contains(out, "named in the survey") {
			t.Fatalf("%s: a recall that did not name reasoning surfaced it:\n%s", name, out)
		}
	}
	// And the route is good: it reads that reply's thinking.
	if out := recallOut(t, engine, map[string]any{"source": "reasoning", "query": fmt.Sprintf("turn:%d", with)}); !strings.Contains(out, "named in the survey") {
		t.Fatalf("the route a hit names did not read the thinking:\n%s", out)
	}
}

// The description says what is kept, how it is marked, and that a
// conversation hit points the way.
func TestTheRecallDescriptionNamesTheAccountAndTheRoute(t *testing.T) {
	v := lookupVerb("recall")
	if v == nil {
		t.Fatal("no recall verb")
	}
	props, _ := v.Params["properties"].(map[string]any)
	src, _ := props["source"].(map[string]any)
	srcText, _ := src["description"].(string)
	for where, text := range map[string]string{"the description": v.Description, "the source parameter": srcText} {
		if !strings.Contains(text, store.AccountMark) {
			t.Fatalf("%s does not name the mark a provider's account carries: %s", where, text)
		}
		if strings.Contains(text, "recorded when your model returns it raw") {
			t.Fatalf("%s still says reasoning is kept only when raw: %s", where, text)
		}
	}
	for _, want := range []string{"source=reasoning", `query="turn:N"`, "says when its thinking is kept"} {
		if !strings.Contains(v.Description, want) {
			t.Fatalf("the description lacks %q: %s", want, v.Description)
		}
	}
}
