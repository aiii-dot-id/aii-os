package store

import (
	"strings"
	"testing"
)

func TestReasoningIsKeptPerReplyVerbatimAndSearchable(t *testing.T) {
	st := testStore(t)
	seq, err := st.AddConversationTurnSeq("resident", "The answer is four.")
	if err != nil {
		t.Fatal(err)
	}
	calls := []ReasoningCall{
		{Model: "glm", Text: "Two and two make four; check it: four minus two is two."},
		{Model: "glm", Text: "Say it plainly."},
	}
	if err := st.AddReasoning(seq, calls); err != nil {
		t.Fatal(err)
	}
	r, ok, err := st.ReasoningOfTurn(seq)
	if err != nil || !ok {
		t.Fatalf("the reply's reasoning is not found: ok=%v err=%v", ok, err)
	}
	if r.TurnSeq != seq || r.Models != "glm" {
		t.Fatalf("row: %+v", r)
	}
	i1, i2 := strings.Index(r.Content, calls[0].Text), strings.Index(r.Content, calls[1].Text)
	if i1 < 0 || i2 < 0 || i1 > i2 {
		t.Fatalf("both calls' reasoning, verbatim and in order: %q", r.Content)
	}
	if !strings.Contains(r.Content, "[call 1 · glm]") || !strings.Contains(r.Content, "[call 2 · glm]") {
		t.Fatalf("each call is named: %q", r.Content)
	}
	if got, err := st.ReasoningPage([]string{"MINUS", "check"}, 0, 5); err != nil || len(got) != 1 || got[0].TurnSeq != seq {
		t.Fatalf("reasoning is not found by its words: %+v %v", got, err)
	}
	if got, _ := st.ReasoningPage([]string{"minus", "absent"}, 0, 5); len(got) != 0 {
		t.Fatalf("every word must appear: %+v", got)
	}
	if got, _ := st.ReasoningPage([]string{"50%_"}, 0, 5); len(got) != 0 {
		t.Fatalf("LIKE wildcards in a term must be literal: %+v", got)
	}
	if err := st.AddReasoning(seq, calls); err == nil {
		t.Fatal("a second reasoning row for one reply was accepted")
	}
}

func TestOneCallsReasoningIsStoredExactlyAsItCame(t *testing.T) {
	st := testStore(t)
	seq, _ := st.AddConversationTurnSeq("resident", "ok")
	raw := "  exactly this,\n\nwith its spacing  "
	if err := st.AddReasoning(seq, []ReasoningCall{{Model: "glm", Text: raw}}); err != nil {
		t.Fatal(err)
	}
	r, ok, _ := st.ReasoningOfTurn(seq)
	if !ok || r.Content != raw || r.Models != "glm" {
		t.Fatalf("one call's reasoning must be stored exactly: %+v", r)
	}
}

func TestReasoningIsDeclaredEphemeral(t *testing.T) {
	found := false
	for _, name := range EphemeralTables() {
		if name == "reasoning" {
			found = true
		}
	}
	if !found {
		t.Fatalf("reasoning is not an ephemeral table: %v", EphemeralTables())
	}
	if got := SidecarsOf("reasoning"); len(got) != 0 {
		t.Fatalf("reasoning is not a memory store and carries no sidecars: %v", got)
	}
}

// Newest first, paged by turn: after_seq gives the older page.
func TestReasoningPagesNewestFirst(t *testing.T) {
	st := testStore(t)
	var seqs []uint64
	for i := 0; i < 3; i++ {
		seq, _ := st.AddConversationTurnSeq("resident", "r")
		if err := st.AddReasoning(seq, []ReasoningCall{{Text: "thought"}}); err != nil {
			t.Fatal(err)
		}
		seqs = append(seqs, seq)
	}
	first, _ := st.ReasoningPage(nil, 0, 2)
	if len(first) != 2 || first[0].TurnSeq != seqs[2] || first[1].TurnSeq != seqs[1] {
		t.Fatalf("first page: %+v", first)
	}
	older, _ := st.ReasoningPage(nil, first[1].TurnSeq, 2)
	if len(older) != 1 || older[0].TurnSeq != seqs[0] {
		t.Fatalf("older page: %+v", older)
	}
}

func TestNothingToRecordRecordsNothing(t *testing.T) {
	st := testStore(t)
	seq, _ := st.AddConversationTurnSeq("resident", "ok")
	if err := st.AddReasoning(seq, nil); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := st.ReasoningOfTurn(seq); ok {
		t.Fatal("an empty call list wrote a row")
	}
	if err := st.AddReasoning(0, []ReasoningCall{{Text: "x"}}); err == nil {
		t.Fatal("reasoning for no reply (turn 0) was accepted")
	}
}
