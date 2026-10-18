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

// tensions_test.go — CONTESTED is an explanation, not a label; what is
// shown is context and is never consumed; and a note kept out of DREAM and
// CONSOLIDATE is not quoted to those two, whatever it stands against, while
// the operator's lines quote it.

const keptOutWords = "the words of a very hard day, kept out of DREAM and CONSOLIDATE"

// contestedStore is three raw experiences on the table, two Ring 3
// beliefs, and one of them contested the ordinary way: by a note.
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

// A STANDING CONTRADICTION APPEARS IN CONSOLIDATE'S EVIDENCE, as words: the
// note by its content, the belief by the id it is already listed under.
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

// THE RENDER-ONLY PASS READS IT TOO, in words: it lists beliefs without
// their ids, and working truth is where a contradiction is said to stand.
func TestTheRenderOnlyPassIsShownTheContradictionInWords(t *testing.T) {
	st := contestedStore()
	st.unprocessedCnt, st.experiences = 0, nil // nothing to metabolize: the view refreshes
	m, lg, _ := consolidateOver(t, st, "You believe you work best alone, and a note of yours stands against it.")
	if len(lg.appended) != 0 {
		t.Fatalf("a render-only pass minted: %v", lg.appended)
	}
	if !strings.Contains(m.lastUser, `stands against [b_alone] "I work best alone"`) {
		t.Fatalf("the render-only pass was not shown the contradiction in words:\n%s", m.lastUser)
	}
}

// DREAM READ TWO BARE IDS for the commonest contradiction there is — a note
// against a belief — because its renderer gave up when either end was not
// a belief. It reads words now, through the same renderer.
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

func TestAnUnmetabolizedNoteIsNotQuotedToTheFacilitiesAndIsQuotedToTheOperator(t *testing.T) {
	keptOut := func() *mockStore {
		st := contestedStore()
		st.tensionNotes = []store.Experience{{ID: "exp_note", Content: keptOutWords, Provenance: "self", Unmetabolized: 1}}
		return st
	}
	t.Run("consolidate, metabolism pass", func(t *testing.T) {
		m, _, _ := consolidateOver(t, keptOut(), backedEnvelope("the view"))
		if strings.Contains(m.lastUser, keptOutWords) || strings.Contains(m.lastSystem, keptOutWords) {
			t.Fatal("A NOTE KEPT OUT OF CONSOLIDATE WAS QUOTED TO IT")
		}
		if !strings.Contains(m.lastUser, "[exp_note] "+unmetabolizedEnd+" stands against [b_alone]") {
			t.Fatalf("the note is not named as what it is:\n%s", m.lastUser)
		}
	})
	t.Run("consolidate, render-only pass", func(t *testing.T) {
		st := keptOut()
		st.unprocessedCnt, st.experiences = 0, nil
		m, _, _ := consolidateOver(t, st, "the view")
		if strings.Contains(m.lastUser, keptOutWords) {
			t.Fatal("A NOTE KEPT OUT OF CONSOLIDATE WAS QUOTED TO ITS RENDER PASS")
		}
	})
	t.Run("dream", func(t *testing.T) {
		st := keptOut()
		m := &mockLLM{override: "You may be noticing a rhythm."}
		d := NewDream(st, m, &mockLedger{st: st}, &mockRingWriter{}, DreamConfig{Threshold: 1})
		d.SetTensions(st)
		if err := d.Execute(context.Background()); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(m.lastUser, keptOutWords) {
			t.Fatal("A NOTE KEPT OUT OF DREAM WAS QUOTED TO IT")
		}
	})
	t.Run("the identity review", func(t *testing.T) {
		buf := captureLog(t)
		r := NewIdentityReview(keptOut())
		if err := r.Execute(context.Background()); err != nil {
			t.Fatal(err)
		}
		issues := strings.Join(r.LastReview().Issues, "\n")
		// Quoted, with the mark the note carries wherever it appears.
		want := `standing contradiction: [exp_note] a note of yours, kept out of DREAM and CONSOLIDATE: "` + keptOutWords + `"`
		if !strings.Contains(issues, want) {
			t.Fatalf("THE REVIEW'S FINDING DOES NOT QUOTE THE NOTE WITH ITS MARK: want %q in\n%s", want, issues)
		}
		if !strings.Contains(buf.String(), keptOutWords) {
			t.Fatalf("A MARKED NOTE'S WORDS ARE KEPT FROM THE OPERATOR'S LOG: %q", buf.String())
		}
		if strings.Contains(issues, "sealed") || strings.Contains(issues, "not shown") || strings.Contains(issues, "not given") {
			t.Fatalf("the review tells the operator something is not shown to them: %s", issues)
		}
	})
}

// A RETIRED END IS SAID TO BE RETIRED, and an id that names nothing is said
// to name nothing — a belief is never shown standing against a blank.
func TestEachEndIsDescribedAsWhatItIs(t *testing.T) {
	st := contestedStore()
	st.retired = []store.Belief{{ID: "b_gone", Statement: "I never pair"}}
	st.tensions = []store.TensionPair{
		{EdgeID: "t1", LeftID: "b_gone", RightID: "b_alone"},
		{EdgeID: "t2", LeftID: "nothing_here", RightID: "b_alone"},
	}
	view, err := renderTensions(st, map[string]bool{"b_alone": true}, 0, 0)
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

// A VIEW THAT CANNOT BE READ IS LEFT OUT, LOUDLY — no view rather than a
// wrong one — and the pass goes on.
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

// BOUNDED AT THE RENDERER: whole pairs, oldest first, and what did not fit
// is declared — in characters, and by the operator's number.
func TestTheContradictionViewIsBoundedInWholePairsAndSaysWhatItLeftOut(t *testing.T) {
	st := contestedStore()
	st.tensions, st.tensionNotes = nil, nil
	for i := 0; i < 40; i++ {
		id := fmt.Sprintf("exp_%02d", i)
		st.tensions = append(st.tensions, store.TensionPair{EdgeID: "t" + id, LeftID: id, RightID: "b_alone"})
		st.tensionNotes = append(st.tensionNotes, store.Experience{ID: id, Content: strings.Repeat("é", 400), Provenance: "self"})
	}
	view, err := renderTensions(st, map[string]bool{"b_alone": true}, 1000, 0)
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
	// IN CHARACTERS: every pair here is the same length and mostly
	// two-byte characters, so a bound counted in bytes keeps about half
	// as many. As many whole pairs as the characters allow are kept.
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
		// An end is an excerpt: 240 characters and the mark that says so.
		if !strings.Contains(l, strings.Repeat("é", DefaultExcerptChars)+"…") || strings.Contains(l, strings.Repeat("é", DefaultExcerptChars+1)) {
			t.Fatalf("an end is not a %d-character excerpt: %q", DefaultExcerptChars, l)
		}
	}
	// The unset bound is the default, and everything that fits is shown.
	all, _ := renderTensions(st, map[string]bool{"b_alone": true}, 0, 0)
	if n := utf8.RuneCountInString(all); n > DefaultTensionsMaxChars || n <= 1000 {
		t.Fatalf("the default bound did not govern: %d characters", n)
	}
	few := contestedStore()
	if v, _ := renderTensions(few, nil, 0, 0); strings.Contains(v, "more standing contradiction") {
		t.Fatalf("a view that fits declared an overflow: %s", v)
	}
}

// A CONTRADICTION IS NEVER CONSUMED. A supersede that retires a contested
// belief would drop the contradiction from working truth — the edge stays
// on the retired belief and its successor reads uncontested — so it is
// dropped, the rest of the envelope mints, and an uncontested supersede is
// untouched.
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

// THE PROMPT SAYS WHAT THE VIEW IS FOR, in both of CONSOLIDATE's passes.
func TestTheConsolidatePromptSaysAContradictionIsNotItsToSettle(t *testing.T) {
	for _, p := range []string{consolidatePrompt(DefaultRing3MaxChars, DefaultConsolidateMaxOps), consolidateViewPrompt(DefaultRing3MaxChars)} {
		for _, want := range []string{"shows what it stands against", "do not supersede a contested belief", "identity's own deliberate act"} {
			if !strings.Contains(p, want) {
				t.Errorf("the prompt lost %q", want)
			}
		}
	}
}
