package cognitive

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/aiii-dot-id/aii-os/internal/ledger"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

// .
// .
// .

const sealedWords = "THE SEALED WORDS nobody but the identity may read"

// .
// .
func contestedStore() *mockStore {
	return &mockStore{
		unprocessedCnt: 3,
		experiences: []store.Experience{
			{ID: "e1", Content: "one", Raw: 1}, {ID: "e2", Content: "two", Raw: 1}, {ID: "e3", Content: "three", Raw: 1},
		},
		beliefs: []store.Belief{
			{ID: "b_alone", Statement: "I work best alone", Ring: 3},
			{ID: "b_other", Statement: "something already known", Ring: 3},
		},
		standings:    map[string]string{"b_alone": "suspect", "b_other": "new"},
		tensions:     []store.TensionPair{{EdgeID: "t1", LeftID: "exp_note", RightID: "b_alone"}},
		tensionNotes: []store.Experience{{ID: "exp_note", Content: "pairing with the operator went faster than working alone", Provenance: "self"}},
	}
}

func consolidateOver(t *testing.T, st *mockStore, reply string) (*mockLLM, *mockLedger, *mockRingWriter) {
	t.Helper()
	m := &mockLLM{override: reply}
	lg := &mockLedger{st: st}
	rw := &mockRingWriter{}
	c := NewConsolidate(st, m, lg, rw, ConsolidateConfig{Threshold: 3})
	c.SetTensions(st)
	if err := c.Execute(context.Background()); err != nil {
		t.Fatalf("execute: %v", err)
	}
	return m, lg, rw
}

// .
// .
func TestConsolidateIsShownWhatAContestedBeliefStandsAgainst(t *testing.T) {
	m, _, _ := consolidateOver(t, contestedStore(), backedEnvelope("the view"))
	for _, want := range []string{
		"[b_alone, suspect] I work best alone",
		"Contradictions standing in the record",
		`- [exp_note] a note of yours: "pairing with the operator went faster than working alone" stands against [b_alone]`,
	} {
		if !strings.Contains(m.lastUser, want) {
			t.Errorf("the metabolism pass was not shown %q:\n%s", want, m.lastUser)
		}
	}
	if strings.Count(m.lastUser, "I work best alone") != 1 {
		t.Errorf("a belief already on the table by id was said again in the contradiction view:\n%s", m.lastUser)
	}
}

// .
// .
func TestTheRenderOnlyPassIsShownTheContradictionInWords(t *testing.T) {
	st := contestedStore()
	st.unprocessedCnt, st.experiences = 0, nil
	m, lg, _ := consolidateOver(t, st, "You believe you work best alone, and a note of yours stands against it.")
	if len(lg.appended) != 0 {
		t.Fatalf("a render-only pass minted: %v", lg.appended)
	}
	if !strings.Contains(m.lastUser, `stands against [b_alone] "I work best alone"`) {
		t.Fatalf("the render-only pass was not shown the contradiction in words:\n%s", m.lastUser)
	}
}

// .
// .
// .
func TestDreamReadsANoteMintedContradictionAsWords(t *testing.T) {
	st := contestedStore()
	m := &mockLLM{override: "You may be noticing a rhythm."}
	d := NewDream(st, m, &mockLedger{st: st}, &mockRingWriter{}, DreamConfig{Threshold: 1})
	d.SetTensions(st)
	if err := d.Execute(context.Background()); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(m.lastUser, "exp_note stands against b_alone") {
		t.Fatalf("DREAM still reads two bare ids:\n%s", m.lastUser)
	}
	for _, want := range []string{"pairing with the operator went faster", `[b_alone] "I work best alone"`} {
		if !strings.Contains(m.lastUser, want) {
			t.Errorf("DREAM was not shown %q:\n%s", want, m.lastUser)
		}
	}
}

// .
// .
// .
func TestASealedNotesWordsReachNoFacility(t *testing.T) {
	sealed := func() *mockStore {
		st := contestedStore()
		st.tensionNotes = []store.Experience{{ID: "exp_note", Content: sealedWords, Provenance: "self", Private: 1}}
		return st
	}
	t.Run("consolidate, metabolism pass", func(t *testing.T) {
		m, _, _ := consolidateOver(t, sealed(), backedEnvelope("the view"))
		if strings.Contains(m.lastUser, sealedWords) || strings.Contains(m.lastSystem, sealedWords) {
			t.Fatal("A PRIVATE NOTE'S WORDS REACHED CONSOLIDATE")
		}
		if !strings.Contains(m.lastUser, "[exp_note] a private note (sealed: its content is not shown) stands against [b_alone]") {
			t.Fatalf("the sealed note is not named as what it is:\n%s", m.lastUser)
		}
	})
	t.Run("consolidate, render-only pass", func(t *testing.T) {
		st := sealed()
		st.unprocessedCnt, st.experiences = 0, nil
		m, _, _ := consolidateOver(t, st, "the view")
		if strings.Contains(m.lastUser, sealedWords) {
			t.Fatal("A PRIVATE NOTE'S WORDS REACHED CONSOLIDATE'S RENDER PASS")
		}
	})
	t.Run("dream", func(t *testing.T) {
		st := sealed()
		m := &mockLLM{override: "You may be noticing a rhythm."}
		d := NewDream(st, m, &mockLedger{st: st}, &mockRingWriter{}, DreamConfig{Threshold: 1})
		d.SetTensions(st)
		if err := d.Execute(context.Background()); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(m.lastUser, sealedWords) {
			t.Fatal("A PRIVATE NOTE'S WORDS REACHED DREAM")
		}
	})
	t.Run("the identity review", func(t *testing.T) {
		buf := captureLog(t)
		r := NewIdentityReview(sealed(), IdentityReviewConfig{})
		if err := r.Execute(context.Background()); err != nil {
			t.Fatal(err)
		}
		issues := strings.Join(r.LastReview().Issues, "\n")
		if strings.Contains(issues, sealedWords) || strings.Contains(buf.String(), sealedWords) {
			t.Fatal("A PRIVATE NOTE'S WORDS REACHED THE LOG OR THE OPERATOR'S STRIP")
		}
		if !strings.Contains(issues, "standing contradiction: [exp_note] a private note (sealed") {
			t.Fatalf("the review does not name the contradiction: %s", issues)
		}
	})
}

// .
// .
func TestEachEndIsDescribedAsWhatItIs(t *testing.T) {
	st := contestedStore()
	st.retired = []store.Belief{{ID: "b_gone", Statement: "I never pair"}}
	st.tensions = []store.TensionPair{
		{EdgeID: "t1", LeftID: "b_gone", RightID: "b_alone"},
		{EdgeID: "t2", LeftID: "nothing_here", RightID: "b_alone"},
	}
	view, err := renderTensions(st, map[string]bool{"b_alone": true}, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`- [b_gone] a belief since retired: "I never pair" stands against [b_alone]`,
		`- [nothing_here] (resolves to nothing in the record) stands against [b_alone]`,
	} {
		if !strings.Contains(view, want) {
			t.Errorf("missing %q in:\n%s", want, view)
		}
	}
}

// .
// .
func TestAnUnreadableViewIsLeftOutAndThePassGoesOn(t *testing.T) {
	buf := captureLog(t)
	st := contestedStore()
	st.tensionEndsErr = errors.New("database is locked")
	m, lg, _ := consolidateOver(t, st, backedEnvelope("the view"))
	if strings.Contains(m.lastUser, "Contradictions standing") {
		t.Fatalf("a view was shown from a read that failed:\n%s", m.lastUser)
	}
	if len(lg.appended) == 0 {
		t.Fatal("an unreadable contradiction view stopped the pass")
	}
	if !strings.Contains(buf.String(), "database is locked") {
		t.Fatalf("the failure was not logged: %q", buf.String())
	}
}

// .
// .
func TestTheContradictionViewIsBoundedInWholePairsAndSaysWhatItLeftOut(t *testing.T) {
	st := contestedStore()
	st.tensions, st.tensionNotes = nil, nil
	for i := 0; i < 40; i++ {
		id := fmt.Sprintf("exp_%02d", i)
		st.tensions = append(st.tensions, store.TensionPair{EdgeID: "t" + id, LeftID: id, RightID: "b_alone"})
		st.tensionNotes = append(st.tensionNotes, store.Experience{ID: id, Content: strings.Repeat("é", 400), Provenance: "self"})
	}
	view, err := renderTensions(st, map[string]bool{"b_alone": true}, 1000)
	if err != nil {
		t.Fatal(err)
	}
	if n := utf8.RuneCountInString(view); n > 1000 {
		t.Fatalf("THE VIEW IS %d CHARACTERS, bound 1000", n)
	}
	lines := strings.Split(view, "\n")
	kept := len(lines) - 1
	if kept < 1 || !strings.HasPrefix(lines[0], "- [exp_00]") {
		t.Fatalf("the oldest contradiction must come first and at least one must fit: %q", lines[0])
	}
	// .
	// .
	// .
	perPair := utf8.RuneCountInString(lines[0]) + 1
	reserve := utf8.RuneCountInString(lines[kept]) + 1
	if want := (1000 - reserve) / perPair; kept != want || len(lines[0]) <= perPair {
		t.Fatalf("kept %d pairs, want %d (each %d characters, %d bytes): the bound is not counted in characters", kept, want, perPair, len(lines[0]))
	}
	if want := fmt.Sprintf("- and %d more standing contradiction(s) not shown here", 40-kept); !strings.HasPrefix(lines[kept], want) {
		t.Fatalf("what was left out is not declared: last line %q, want it to begin %q", lines[kept], want)
	}
	for _, l := range lines[:kept] {
		if !strings.HasSuffix(l, "stands against [b_alone]") {
			t.Fatalf("a pair was cut: %q", l)
		}
		// .
		if !strings.Contains(l, strings.Repeat("é", tensionExcerptChars)+"…") || strings.Contains(l, strings.Repeat("é", tensionExcerptChars+1)) {
			t.Fatalf("an end is not a %d-character excerpt: %q", tensionExcerptChars, l)
		}
	}
	// .
	all, _ := renderTensions(st, map[string]bool{"b_alone": true}, 0)
	if n := utf8.RuneCountInString(all); n > defaultTensionsMaxChars || n <= 1000 {
		t.Fatalf("the default bound did not govern: %d characters", n)
	}
	few := contestedStore()
	if v, _ := renderTensions(few, nil, 0); strings.Contains(v, "more standing contradiction") {
		t.Fatalf("a view that fits declared an overflow: %s", v)
	}
}

// .
// .
// .
// .
// .
func TestConsolidateCannotRetireAContestedBelief(t *testing.T) {
	envelope := func(oldID string) string {
		return `{"operations": [` +
			`{"op": "upsert", "id": "n1", "statement": "Three experiences share a pattern", "confidence": 0.6, "evidence": ["e1", "e2", "e3"]},` +
			`{"op": "supersede", "old_id": "` + oldID + `", "new_id": "n1", "reason": "converging"}` +
			`], "ring3_view": "the view"}`
	}
	supersedes := func(lg *mockLedger) int {
		n := 0
		for _, et := range lg.appended {
			if et == ledger.EventBeliefSupersede {
				n++
			}
		}
		return n
	}
	t.Run("contested: dropped, and the rest mints", func(t *testing.T) {
		buf := captureLog(t)
		_, lg, _ := consolidateOver(t, contestedStore(), envelope("b_alone"))
		if supersedes(lg) != 0 {
			t.Fatal("A CONTESTED BELIEF WAS RETIRED BY CONSOLIDATION: the contradiction leaves working truth unresolved")
		}
		var upserts int
		for _, et := range lg.appended {
			if et == ledger.EventBeliefUpsert {
				upserts++
			}
		}
		if upserts != 1 {
			t.Fatalf("the refused supersede took the rest of the envelope with it: %v", lg.appended)
		}
		if !strings.Contains(buf.String(), "CONTESTED") || !strings.Contains(buf.String(), "b_alone") {
			t.Fatalf("the refusal is not logged with its reason: %q", buf.String())
		}
	})
	t.Run("uncontested: still mints", func(t *testing.T) {
		_, lg, _ := consolidateOver(t, contestedStore(), envelope("b_other"))
		if supersedes(lg) != 1 {
			t.Fatalf("an uncontested supersede was refused: %v", lg.appended)
		}
	})
	t.Run("standing cannot be derived: dropped", func(t *testing.T) {
		st := contestedStore()
		st.standingErr = map[string]error{"b_other": errors.New("evidence unreadable")}
		_, lg, _ := consolidateOver(t, st, envelope("b_other"))
		if supersedes(lg) != 0 {
			t.Fatal("a belief whose standing could not be derived was retired as though uncontested")
		}
	})
}

// .
func TestTheConsolidatePromptSaysAContradictionIsNotItsToSettle(t *testing.T) {
	for _, p := range []string{consolidateSystemPrompt, consolidateViewSystemPrompt} {
		for _, want := range []string{"shows what it stands against", "do not supersede a contested belief", "identity's own deliberate act"} {
			if !strings.Contains(p, want) {
				t.Errorf("the prompt lost %q", want)
			}
		}
	}
}
