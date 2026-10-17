package store

import (
	"reflect"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/interaction"
)

// WHAT A TURN DID IS WHAT WAS RECORDED UNDER IT: each call's tool and how
// it ended, in the order made — a failed call as failed, one with no
// result as unfinished, another turn's calls never — and a turn that made
// no call answers with an empty list, not with nothing.
func TestTurnToolCallsAreTheRecordedCallsInOrder(t *testing.T) {
	s := testStore(t)
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(s.RecordToolStart("t1", 1, "main", "m", "", "recall", `{"query":"lesson"}`))
	must(recordToolDone(s, "t1", 1, "recall", `{"query":"lesson"}`, "2 hits", interaction.Succeeded, false))
	must(s.RecordToolStart("t1", 2, "main", "m", "", "recall", `{"query":"plan"}`))
	must(recordToolDone(s, "t1", 2, "recall", `{"query":"plan"}`, "1 hit", interaction.Succeeded, false))
	must(s.RecordToolStart("t1", 3, "main", "m", "", "note", `{"content":""}`))
	must(recordToolDone(s, "t1", 3, "note", `{"content":""}`, "refused: empty", interaction.Failed, false))
	must(s.RecordToolStart("t1", 4, "main", "m", "", "shell", `{"cmd":"ls"}`))
	must(s.RecordToolStart("t9", 1, "main", "m", "", "note", `{"content":"elsewhere"}`))
	must(recordToolDone(s, "t9", 1, "note", `{"content":"elsewhere"}`, "Noted.", interaction.Succeeded, false))

	got, err := s.TurnToolCalls([]string{"t1", "t2", "", "t1"})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string][]ToolCall{
		"t1": {{Tool: "recall", Outcome: "succeeded"}, {Tool: "recall", Outcome: "succeeded"}, {Tool: "note", Outcome: "failed"}, {Tool: "shell"}},
		"t2": {},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("turn tool calls = %#v\nwant %#v", got, want)
	}
}

// DREAM's intake carries each turn's id — the id the loop recorded the
// turn's tool calls under — and "" for a turn recorded before turns had
// ids.
func TestTheConversationIntakeCarriesEachTurnsID(t *testing.T) {
	s := testStore(t)
	for _, in := range []interaction.Input{
		{ID: "old", Kind: interaction.Message, Role: interaction.Resident, Content: "An answer from before turn ids."},
		{ID: "q", Kind: interaction.Message, Role: interaction.Operator, TurnID: "t1", Content: "Remember this."},
		{ID: "a", Kind: interaction.Message, Role: interaction.Resident, TurnID: "t1", Content: "Recorded permanently."},
	} {
		if _, err := s.RecordInteraction(t.Context(), in); err != nil {
			t.Fatal(err)
		}
	}
	batch, err := s.NextConversation(100000, "")
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, p := range batch.Parts {
		got[p.ID] = p.TurnID
	}
	if want := map[string]string{"old": "", "q": "t1", "a": "t1"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("intake turn ids = %v, want %v", got, want)
	}
}
