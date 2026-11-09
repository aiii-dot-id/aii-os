package identity

import (
	"fmt"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/store"
	"github.com/aiii-dot-id/aii-os/internal/untrusted"
)

// THE IDENTITY READS A NAMED VOICE THE SAME WAY WHEREVER IT READS A TURN. A participant's
// row, recorded before anyone named its voice, reads through recall as it does in the
// prompt: the words alone, under the operator's attribution, with no "authority" and no
// sentinel. The record does not move, and another voice's row does not move by a byte.
func TestRecallReadsANamedVoiceAsThePromptDoes(t *testing.T) {
	const named, other = "6f0c2c1e-8a41-4f6e-9a55-0d1c2b3a4e5f", "9078c44a-6fbe-4f24-a20e-bd212f520530"
	engine, st, _, _, _ := setupEngine(t)
	said := func(uuid, words string) uint64 {
		t.Helper()
		seq, err := st.AddConversationTurnSeq("participant", store.HeardFromSomeone("Same label", words))
		if err != nil {
			t.Fatal(err)
		}
		observed := `{"decision":"uncertain","speaker_uuid":"` + uuid + `","continuity":"matched","registry_revision":"9"}`
		if err := st.AnnotateTurn(seq, store.AnnotationSpeaker, "vs-1/1", observed); err != nil {
			t.Fatal(err)
		}
		return seq
	}
	first := said(named, "the harbour at dawn")
	second := said(other, "the mountain at noon")
	reads := map[string]func(uint64) string{
		"turn:N": func(seq uint64) string {
			return recallOut(t, engine, map[string]any{"source": "conversation", "query": fmt.Sprintf("turn:%d", seq)})
		},
		"a search hit": func(seq uint64) string {
			words := map[uint64]string{first: "harbour", second: "mountain"}[seq]
			return recallOut(t, engine, map[string]any{"query": words})
		},
		"a page of the source": func(seq uint64) string {
			words := map[uint64]string{first: "harbour", second: "mountain"}[seq]
			return recallOut(t, engine, map[string]any{"source": "conversation", "query": words})
		},
	}
	before := map[string]string{}
	for name, read := range reads {
		before[name+" other"] = read(second)
		got := read(first)
		if !strings.Contains(got, "authority") && name == "turn:N" {
			t.Fatalf("%s before any name reads %s", name, got)
		}
	}
	st.SetOperatorVoices([]string{named})
	for name, read := range reads {
		got := read(first)
		for _, gone := range []string{"authority", "EXTERNAL_UNTRUSTED", "Someone spoke"} {
			if strings.Contains(got, gone) {
				t.Errorf("%s of a named voice still holds %q: %s", name, gone, got)
			}
		}
		if !strings.Contains(got, "your operator's voice") || !strings.Contains(got, "the harbour at dawn") || !strings.Contains(got, ", operator,") {
			t.Errorf("%s of a named voice reads %s", name, got)
		}
		if after := read(second); after != before[name+" other"] {
			t.Errorf("%s of another voice moved:\n%s\nwas:\n%s", name, after, before[name+" other"])
		}
	}
	turn, err := st.GetTurnBySeq(first)
	if err != nil || turn == nil || turn.Role != "participant" || !strings.Contains(turn.Content, "authority") {
		t.Fatalf("the record moved: %+v %v", turn, err)
	}
}

// A ROW THAT CANNOT BE CREDITED READS EXACTLY AS IT WAS RECORDED, on every part of a long
// read and in a preview cut before the frame's end: a participant's, never the operator's.
func TestAnUncreditedRowIsRecalledAsRecorded(t *testing.T) {
	const named = "6f0c2c1e-8a41-4f6e-9a55-0d1c2b3a4e5f"
	engine, st, _, _, _ := setupEngine(t)
	prose := store.HeardFromSomeone("", "x")
	long := strings.Repeat("the long label of an earlier build\n", 800)
	framed := prose[:strings.Index(prose, untrusted.Open)] + untrusted.Open + " source: voice, speaker labelled: " + long + "the end of it\n" + untrusted.Close
	seq, err := st.AddConversationTurnSeq("participant", framed)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.AnnotateTurn(seq, store.AnnotationSpeaker, "vs-1/1", `{"speaker_uuid":"`+named+`","continuity":"matched","registry_revision":"9"}`); err != nil {
		t.Fatal(err)
	}
	st.SetOperatorVoices([]string{named})
	for name, args := range map[string]map[string]any{
		"the first part":  {"source": "conversation", "query": fmt.Sprintf("turn:%d", seq)},
		"the second part": {"source": "conversation", "query": fmt.Sprintf("turn:%d part:2", seq)},
		"a search hit":    {"query": "earlier build"},
		"a page":          {"source": "conversation", "query": "earlier build"},
	} {
		got := recallOut(t, engine, args)
		if strings.Contains(got, "your operator's voice") || strings.Contains(got, ", operator,") {
			t.Errorf("%s credits the operator: %s", name, got)
		}
		if !strings.Contains(got, ", participant,") {
			t.Errorf("%s is not a participant's: %s", name, got)
		}
	}
}
