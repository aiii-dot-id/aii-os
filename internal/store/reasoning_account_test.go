package store

import (
	"strings"
	"testing"
)

func TestAProvidersAccountIsKeptMarked(t *testing.T) {
	st := testStore(t)
	seq, _ := st.AddConversationTurnSeq("resident", "ok")
	summary := "Weighed the two readings and chose the later date."
	if err := st.AddReasoning(seq, []ReasoningCall{{Model: "claude", Text: summary, Account: true}}); err != nil {
		t.Fatal(err)
	}
	r, ok, err := st.ReasoningOfTurn(seq)
	if err != nil || !ok {
		t.Fatalf("not found: ok=%v err=%v", ok, err)
	}
	if want := "[call 1 · claude · " + AccountMark + "]\n" + summary; r.Content != want {
		t.Fatalf("a single account is kept under its marked header:\n got %q\nwant %q", r.Content, want)
	}
	if want := "claude (" + AccountMark + ")"; r.Models != want {
		t.Fatalf("the model does not carry the mark: %q, want %q", r.Models, want)
	}
	if !r.AccountMarked() {
		t.Fatal("a row that holds a provider's account does not say so")
	}
	// It is found by its words like any reasoning.
	if got, err := st.ReasoningPage([]string{"later", "readings"}, 0, 5); err != nil || len(got) != 1 || got[0].TurnSeq != seq {
		t.Fatalf("an account is not found by its words: %+v %v", got, err)
	}
}

func TestARawCallBesideAnAccountKeepsNoMark(t *testing.T) {
	st := testStore(t)
	seq, _ := st.AddConversationTurnSeq("resident", "ok")
	calls := []ReasoningCall{
		{Call: 1, Model: "glm", Text: "raw tokens of the first call"},
		{Call: 3, Model: "claude", Text: "a summary of the third", Account: true},
		{Call: 4, Model: "glm", Text: "raw tokens of the fourth"},
		{Call: 5, Text: "a summary from a model that was not named", Account: true},
	}
	if err := st.AddReasoning(seq, calls); err != nil {
		t.Fatal(err)
	}
	r, _, _ := st.ReasoningOfTurn(seq)
	for _, want := range []string{
		"[call 1 · glm]\nraw tokens of the first call",
		"[call 3 · claude · " + AccountMark + "]\na summary of the third",
		"[call 4 · glm]\nraw tokens of the fourth",
		"[call 5 · " + AccountMark + "]\na summary from a model that was not named",
	} {
		if !strings.Contains(r.Content, want) {
			t.Fatalf("content lacks %q:\n%s", want, r.Content)
		}
	}
	if want := "glm, claude (" + AccountMark + "), (" + AccountMark + ")"; r.Models != want {
		t.Fatalf("models: %q, want %q", r.Models, want)
	}
}

// One raw call is kept exactly as it came, with no header and no mark:
// what was true of every row before an account could be kept.
func TestOneRawCallIsStillKeptBare(t *testing.T) {
	st := testStore(t)
	seq, _ := st.AddConversationTurnSeq("resident", "ok")
	raw := "  exactly this,\n\nwith its spacing  "
	if err := st.AddReasoning(seq, []ReasoningCall{{Model: "glm", Text: raw}}); err != nil {
		t.Fatal(err)
	}
	r, _, _ := st.ReasoningOfTurn(seq)
	if r.Content != raw || r.Models != "glm" {
		t.Fatalf("a single raw call changed: content %q, models %q", r.Content, r.Models)
	}
	if r.AccountMarked() {
		t.Fatal("a row of raw reasoning says it holds a provider's account")
	}
}

func TestTheStoreSaysWhetherAndWhereReasoningIsKept(t *testing.T) {
	st := testStore(t)
	if kept, err := st.HasReasoning(); err != nil || kept {
		t.Fatalf("an empty store has no reasoning: kept=%v err=%v", kept, err)
	}
	if got, err := st.ReasoningTurns(nil); err != nil || len(got) != 0 {
		t.Fatalf("no turns asked: %v %v", got, err)
	}
	first, _ := st.AddConversationTurnSeq("resident", "one")
	second, _ := st.AddConversationTurnSeq("resident", "two")
	third, _ := st.AddConversationTurnSeq("resident", "three")
	// A reply whose calls returned nothing records nothing.
	if err := st.AddReasoning(second, []ReasoningCall{{Model: "glm", Text: "  "}}); err != nil {
		t.Fatal(err)
	}
	if kept, _ := st.HasReasoning(); kept {
		t.Fatal("a reply with no reasoning made the store say some is kept")
	}
	if err := st.AddReasoning(first, []ReasoningCall{{Model: "glm", Text: "thought"}}); err != nil {
		t.Fatal(err)
	}
	if err := st.AddReasoning(third, []ReasoningCall{{Model: "claude", Text: "a summary", Account: true}}); err != nil {
		t.Fatal(err)
	}
	if kept, err := st.HasReasoning(); err != nil || !kept {
		t.Fatalf("reasoning is recorded and the store says none: kept=%v err=%v", kept, err)
	}
	got, err := st.ReasoningTurns([]uint64{first, second, third, 99999, 1 << 63})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || !got[first] || !got[third] || got[second] {
		t.Fatalf("turns with reasoning: %v, want %d and %d", got, first, third)
	}
	// A yes is kept. Nothing deletes these rows, so the table is asked
	// until it holds one and never after: emptied behind the store's back,
	// the answer does not change.
	if _, err := st.DB().Exec(`DELETE FROM reasoning`); err != nil {
		t.Fatal(err)
	}
	if kept, err := st.HasReasoning(); err != nil || !kept {
		t.Fatalf("the store asked its table again after a yes: kept=%v err=%v", kept, err)
	}
}
