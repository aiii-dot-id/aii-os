package cognitive

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/ring"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

// WORKING TRUTH LISTS THE OPERATOR'S NUMBER OF RECENT EXPERIENCES, EACH CUT
// AT THE OPERATOR'S EXCERPT. Two of four, each at most sixty characters,
// the one that was cut said to be.
func TestWorkingTruthTakesTheOperatorsExcerptAndCount(t *testing.T) {
	long := strings.Repeat("the tide came in early and the harbor flooded ", 4)
	st, rw := &mockStore{experiences: []store.Experience{
		{ID: "e4", Content: long, Category: "observation", Provenance: "self"},
		{ID: "e3", Content: "short and whole", Category: "observation", Provenance: "self"},
		{ID: "e2", Content: "the third newest", Category: "observation", Provenance: "self"},
		{ID: "e1", Content: "the oldest", Category: "observation", Provenance: "self"},
	}}, &mockRingWriter{}
	c := NewConsolidate(st, &mockLLM{}, &mockLedger{st: st}, rw, ConsolidateConfig{Threshold: 3, ExcerptChars: 60, RecentExperiences: 2})
	c.BeliefRetired()
	text := rw.section(ring.Ring3, "working_truth")
	want := "You recently experienced:\n" +
		"- [observation] a note of yours: " + string([]rune(strings.TrimSpace(long))[:60]) + "…" + shownInPart + "\n" +
		"- [observation] a note of yours: short and whole\n"
	if !strings.Contains(text, want) {
		t.Fatalf("working truth does not list the two newest at sixty characters each:\n%s\nwant within it:\n%s", text, want)
	}
	if strings.Contains(text, "the third newest") {
		t.Fatalf("working truth lists more than the operator's two:\n%s", text)
	}
}

// AN END OF A CONTRADICTION IS CUT AT THE OPERATOR'S EXCERPT.
func TestTheContradictionViewTakesTheOperatorsExcerpt(t *testing.T) {
	note := strings.Repeat("é", 100)
	st := &mockStore{
		tensions:     []store.TensionPair{{EdgeID: "t1", LeftID: "exp_note", RightID: "b_alone"}},
		tensionNotes: []store.Experience{{ID: "exp_note", Content: note, Provenance: "self"}},
	}
	view, err := renderTensions(st, map[string]bool{"b_alone": true}, 0, 30)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(view, `"`+strings.Repeat("é", 30)+`…"`) || strings.Contains(view, strings.Repeat("é", 31)) {
		t.Fatalf("an end is not a thirty-character excerpt:\n%s", view)
	}
}

// cutAt reports whether text quotes s cut at n characters, and no more of
// it: the excerpt, its ellipsis, and nowhere n+1 of its characters.
func cutAt(text, s string, n int) bool {
	r := []rune(s)
	return strings.Contains(text, string(r[:n])+"…") && !strings.Contains(text, string(r[:n+1]))
}

// longEnd is a contradiction's note end of a hundred characters.
func longEnd() *mockStore {
	st := contestedStore()
	st.tensionNotes = []store.Experience{{ID: "exp_note", Content: strings.Repeat("é", 100), Provenance: "self"}}
	return st
}

// THE REQUEST THAT RENDERS WORKING TRUTH LISTS THE OPERATOR'S NUMBER OF
// RECENT EXPERIENCES (prompt.ring3_recent_experiences, writeRing3).
func TestTheRenderRequestListsTheOperatorsNumberOfRecentExperiences(t *testing.T) {
	st := &mockStore{experiences: []store.Experience{
		{ID: "e3", Content: "the newest", Category: "observation", Provenance: "self"},
		{ID: "e2", Content: "the second newest", Category: "observation", Provenance: "self"},
		{ID: "e1", Content: "the third newest", Category: "observation", Provenance: "self"},
	}}
	m := &mockLLM{override: "a view in prose"}
	c := NewConsolidate(st, m, &mockLedger{st: st}, &mockRingWriter{}, ConsolidateConfig{Threshold: 3, RecentExperiences: 2})
	if err := c.Execute(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(m.lastUser, "Recent experiences:\n") || !strings.Contains(m.lastUser, "the second newest") {
		t.Fatalf("rig: the render request does not list the two newest:\n%s", m.lastUser)
	}
	if strings.Contains(m.lastUser, "the third newest") {
		t.Fatalf("the render request lists more than the operator's two:\n%s", m.lastUser)
	}
}

// A CONSOLIDATION PASS QUOTES A CONTRADICTION'S END AT THE OPERATOR'S
// EXCERPT (prompt.excerpt_chars, the pass's material).
func TestAConsolidationPassQuotesAtTheOperatorsExcerpt(t *testing.T) {
	st := longEnd()
	m := &sizedModel{reply: func(string) (string, error) { return "", errors.New("the provider is down") }}
	c := NewConsolidate(st, m, &mockLedger{st: st}, &mockRingWriter{}, ConsolidateConfig{Threshold: 3, ExcerptChars: 30})
	c.SetTensions(st)
	_ = c.Execute(context.Background())
	if len(m.sent) == 0 || !strings.Contains(m.sent[0], "Contradictions standing in the record") {
		t.Fatalf("rig: the pass did not put the contradiction to the model: %q", m.sent)
	}
	if !cutAt(m.sent[0], strings.Repeat("é", 100), 30) {
		t.Fatalf("the pass does not quote the end at thirty characters:\n%s", m.sent[0])
	}
}

// DREAM QUOTES A CONTRADICTION'S END AT THE OPERATOR'S EXCERPT.
func TestDreamQuotesAtTheOperatorsExcerpt(t *testing.T) {
	st := longEnd()
	m := &mockLLM{override: "You may be noticing a rhythm."}
	d := NewDream(st, m, &mockLedger{st: st}, &mockRingWriter{}, DreamConfig{Threshold: 1, ExcerptChars: 30})
	d.SetTensions(st)
	if err := d.Execute(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(m.lastUser, "Contradictions currently standing in your record:") {
		t.Fatalf("rig: DREAM was not shown the contradiction:\n%s", m.lastUser)
	}
	if !cutAt(m.lastUser, strings.Repeat("é", 100), 30) {
		t.Fatalf("DREAM does not quote the end at thirty characters:\n%s", m.lastUser)
	}
}

// longOutcome is an outcome whose text runs to a hundred characters.
type longOutcome struct{ *fakeOutcomes }

func (longOutcome) NextOutcomes(int) (store.OutcomeBatch, error) {
	return store.OutcomeBatch{From: 0, Through: 9, Outcomes: []store.Outcome{{
		Seq: 9, EntryHash: strings.Repeat("ab", 32), Kind: "intention", ID: "i1", State: "completed", Was: strings.Repeat("é", 100), Said: "done",
	}}}, nil
}

// THE OUTCOME INTAKE QUOTES AN OUTCOME AT THE OPERATOR'S EXCERPT, in the
// request it sends as in the one it fits.
func TestTheOutcomeIntakeQuotesAtTheOperatorsExcerpt(t *testing.T) {
	m := &mockLLM{err: errors.New("the provider is down")}
	c := NewConsolidate(nil, m, &captureDoor{}, nil, ConsolidateConfig{ExcerptChars: 30})
	c.SetOutcomes(longOutcome{&fakeOutcomes{self: strings.Repeat("cd", 32)}})
	_ = c.ObserveOutcomes(context.Background())
	if m.calls != 1 || !strings.Contains(m.lastUser, "[record 9] intention completed") {
		t.Fatalf("rig: the intake did not send the outcome (%d call(s)):\n%s", m.calls, m.lastUser)
	}
	if !cutAt(m.lastUser, strings.Repeat("é", 100), 30) {
		t.Fatalf("the intake does not quote the outcome at thirty characters:\n%s", m.lastUser)
	}
}
