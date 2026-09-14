package store

import (
	"path/filepath"
	"testing"
)

// .
// .
// .
func TestTurnAnnotationsAmendARecordedTurn(t *testing.T) {
	st, err := New(filepath.Join(t.TempDir(), "aii.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	seq, err := st.AddConversationTurnSeq("operator", "[voice] hello there")
	if err != nil || seq == 0 {
		t.Fatalf("record with seq: %d %v", seq, err)
	}
	seq2, _ := st.AddConversationTurnSeq("resident", "hello")
	if seq2 != seq+1 {
		t.Fatalf("seqs are the conversation's: %d after %d", seq2, seq)
	}
	if err := st.AnnotateTurn(seq, "voice", "vs-1/7", `{"session":"vs-1","sequence":7}`); err != nil {
		t.Fatal(err)
	}
	got, ok, err := st.TurnSeqByAnnotation("voice", "vs-1/7")
	if err != nil || !ok || got != seq {
		t.Fatalf("lookup by key: %d %v %v", got, ok, err)
	}
	if _, ok, _ := st.TurnSeqByAnnotation("voice", "vs-1/8"); ok {
		t.Fatal("an unknown key finds nothing")
	}
	if err := st.AnnotateTurn(seq, "speaker", "vs-1/7", `{"speaker":"Sam","decision":"known","score":0.9}`); err != nil {
		t.Fatal(err)
	}
	if err := st.AnnotateTurn(seq, "speaker", "vs-1/7", `{"speaker":"Sam","decision":"uncertain","score":0.6}`); err != nil {
		t.Fatal(err)
	}
	ann, err := st.TurnAnnotations("speaker", []uint64{seq, seq2})
	if err != nil || len(ann) != 1 || ann[seq] != `{"speaker":"Sam","decision":"uncertain","score":0.6}` {
		t.Fatalf("the later annotation of a kind replaces the earlier: %v %v", ann, err)
	}
	if err := st.AnnotateTurn(999, "speaker", "x", "{}"); err == nil {
		t.Fatal("an annotation needs a recorded turn")
	}
	if ann, _ := st.TurnAnnotations("speaker", nil); len(ann) != 0 {
		t.Fatal("no seqs, no rows")
	}
}
