package cognitive

import (
	"context"
	"errors"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/aiii-dot-id/aii-os/internal/store"
)

// retired_test.go — what CONSOLIDATE's composed input says of the beliefs
// the record retired: which, when, and what replaced each; nothing at all
// when nothing was retired since its oldest note; and the same words on
// both channels the model can be asked through. These are source proofs of
// the input. A scripted model shows only what it is shown: how a real one
// reads it is not measured here.

// dawnStore is a pass over three notes, two of which still say the ferry
// leaves at dawn, and a belief the identity retired between the second and
// the third. The mock keeps its experiences newest first, as the store's
// recent list does; the raw queue reads them back oldest first.
func dawnStore() *mockStore {
	return &mockStore{
		unprocessedCnt: 3,
		experiences: []store.Experience{
			{ID: "e3", Content: "the ferry left at noon today, not dawn", Raw: 1, CreatedSeq: 30, CreatedAt: "2026-09-28T15:20:00Z"},
			{ID: "e2", Content: "caught the dawn ferry from the harbor", Raw: 1, CreatedSeq: 12, CreatedAt: "2026-09-27T16:40:09.123Z"},
			{ID: "e1", Content: "the ferry left the harbor at dawn again", Raw: 1, CreatedSeq: 11, CreatedAt: "2026-09-27T08:10:33Z"},
		},
		beliefs: []store.Belief{
			{ID: "b_noon", Statement: "The ferry to the island leaves the harbor at noon", Ring: 3},
			{ID: "b_core", Statement: "I test before I ship", Ring: 2},
		},
		standings: map[string]string{"b_noon": "new"},
		retiredView: []store.RetiredBelief{{
			ID: "b_dawn", Statement: "The ferry to the island leaves the harbor at dawn", RetiredAt: "2026-09-28T14:05:07.5Z",
			Replacement: store.TensionEnd{ID: "b_noon", Kind: "belief", Text: "The ferry to the island leaves the harbor at noon"},
		}},
	}
}

const retiredHeader = "Beliefs retired since the oldest experience above (an experience recorded before a belief was retired is not evidence for it now; one recorded after may be):"

func shownToMetabolism(t *testing.T, st *mockStore) string {
	t.Helper()
	m, _, _ := consolidateOver(t, st, backedEnvelope("the view"))
	return m.lastUser
}

// THE INPUT, WORD FOR WORD: the retired belief with its words, the second
// it was retired and what replaced it — after the beliefs that stand, and
// every note dated so the two can be compared. What replaced it is on the
// table by id, so it is not said twice.
func TestConsolidateIsShownTheBeliefsRetiredSinceItsOldestNote(t *testing.T) {
	st := dawnStore()
	want := strings.Join([]string{
		"Experiences to consolidate (cite their ids as evidence):",
		"- 2026-09-27T08:10:33Z [e1] a note of yours: the ferry left the harbor at dawn again",
		"- 2026-09-27T16:40:09Z [e2] a note of yours: caught the dawn ferry from the harbor",
		"- 2026-09-28T15:20:00Z [e3] a note of yours: the ferry left at noon today, not dawn",
		"Current beliefs (for merge/supersede decisions):",
		"  [b_noon, new] The ferry to the island leaves the harbor at noon",
		retiredHeader,
		`  [b_dawn] "The ferry to the island leaves the harbor at dawn" — retired 2026-09-28T14:05:07Z, replaced by [b_noon]`,
	}, "\n")
	if got := shownToMetabolism(t, st); got != want {
		t.Fatalf("the composed input:\n%s\n\nwant:\n%s", got, want)
	}
	// It asked for the retirements after its oldest note, and for the
	// number it shows.
	if len(st.retiredAsked) != 1 || st.retiredAsked[0] != (retiredAsk{after: 11, limit: retiredShown}) {
		t.Fatalf("the facility asked %+v, want the span after its oldest note (seq 11) and %d retirements", st.retiredAsked, retiredShown)
	}
}

// NOTHING RETIRED, NOTHING SAID: no header, no dates on the notes, and the
// input is what it was before the section existed.
func TestNothingRetiredLeavesTheInputAsItWas(t *testing.T) {
	st := dawnStore()
	st.retiredView = nil
	want := strings.Join([]string{
		"Experiences to consolidate (cite their ids as evidence):",
		"- [e1] a note of yours: the ferry left the harbor at dawn again",
		"- [e2] a note of yours: caught the dawn ferry from the harbor",
		"- [e3] a note of yours: the ferry left at noon today, not dawn",
		"Current beliefs (for merge/supersede decisions):",
		"  [b_noon, new] The ferry to the island leaves the harbor at noon",
	}, "\n")
	if got := shownToMetabolism(t, st); got != want {
		t.Fatalf("the composed input:\n%s\n\nwant:\n%s", got, want)
	}
}

func TestARetiredBeliefSaysWhatReplacedItOrThatNothingDid(t *testing.T) {
	st := dawnStore()
	st.retiredView = []store.RetiredBelief{
		{ID: "b_oil", Statement: "The lantern oil is kept aft", RetiredAt: "2026-09-28T09:05:00Z"},
		{ID: "b_six", Statement: "The ferry leaves at six", RetiredAt: "2026-09-27T20:00:00Z",
			Replacement: store.TensionEnd{ID: "b_core", Kind: "belief", Text: "I test before I ship"}},
		{ID: "b_x", Statement: "claim x", RetiredAt: "2026-09-27T19:00:00Z",
			Replacement: store.TensionEnd{ID: "b_y", Kind: "belief", Text: "claim y", Retired: true}},
	}
	got := shownToMetabolism(t, st)
	for _, want := range []string{
		`  [b_oil] "The lantern oil is kept aft" — retired 2026-09-28T09:05:00Z, nothing replaced it`,
		`  [b_six] "The ferry leaves at six" — retired 2026-09-27T20:00:00Z, replaced by [b_core] "I test before I ship"`,
		`  [b_x] "claim x" — retired 2026-09-27T19:00:00Z, replaced by [b_y] a belief since retired: "claim y"`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the input lacks %q:\n%s", want, got)
		}
	}
}

// A REPLACEMENT THAT IS ITSELF RETIRED AND IN THE SECTION is named by its id:
// its words are on the table on its own line, and one owner says them.
func TestARetiredReplacementInTheSectionIsNamedByItsId(t *testing.T) {
	st := dawnStore()
	st.retiredView = []store.RetiredBelief{
		{ID: "b_q", Statement: "claim q", RetiredAt: "2026-09-28T10:00:00Z"},
		{ID: "b_p", Statement: "claim p", RetiredAt: "2026-09-27T10:00:00Z",
			Replacement: store.TensionEnd{ID: "b_q", Kind: "belief", Text: "claim q", Retired: true}},
	}
	got := shownToMetabolism(t, st)
	if !strings.HasSuffix(got, `  [b_p] "claim p" — retired 2026-09-27T10:00:00Z, replaced by [b_q]`) {
		t.Fatalf("a replacement on the table by id was not named by it:\n%s", got)
	}
	if n := strings.Count(got, "claim q"); n != 1 {
		t.Fatalf("a retired replacement's words are in the input %d times:\n%s", n, got)
	}
}

func TestARetiredBeliefIsShownAsAnExcerpt(t *testing.T) {
	id := func(c string) string { return "belief_" + strings.Repeat(c, 32) }
	st := dawnStore()
	st.retiredView = []store.RetiredBelief{{
		ID: id("a"), Statement: strings.Repeat("é", 500), RetiredAt: "2026-09-28T09:05:00Z",
		Replacement: store.TensionEnd{ID: id("b"), Kind: "belief", Text: strings.Repeat("ü", 500), Retired: true},
	}}
	got := shownToMetabolism(t, st)
	at := strings.Index(got, "  [belief_a")
	if at < 0 {
		t.Fatalf("the retired belief is not in the input:\n%s", got)
	}
	line := got[at:]
	for _, r := range []string{"é", "ü"} {
		if !strings.Contains(line, strings.Repeat(r, DefaultExcerptChars)+"…") || strings.Contains(line, strings.Repeat(r, DefaultExcerptChars+1)) {
			t.Fatalf("a belief in the line is not a %d-character excerpt: %q", DefaultExcerptChars, line)
		}
	}
	if n := utf8.RuneCountInString(line); n > 700 {
		t.Fatalf("a retirement line is %d characters; retiredShown's bound assumes at most about 700", n)
	}
}

// A TIME OF THE RECORD is stated in UTC to the second, an absent one is not
// invented, and one in another shape is shown as written, never dropped.
func TestAStampIsUTCToTheSecondAndNeverDropsATime(t *testing.T) {
	for in, want := range map[string]string{
		"2026-09-28T14:05:07.987654321Z": "2026-09-28T14:05:07Z",
		"2026-09-28T16:05:07+02:00":      "2026-09-28T14:05:07Z",
		"":                               "",
		"last Tuesday":                   "last Tuesday",
	} {
		if got := stamp(in); got != want {
			t.Errorf("stamp(%q) = %q, want %q", in, got, want)
		}
	}
}

// WHAT THE SECTION LEFT OUT IS SAID, and nothing is said when it left
// nothing out.
func TestTheRetiredSectionSaysWhatItLeftOut(t *testing.T) {
	st := dawnStore()
	st.retiredMore = 3
	if got := shownToMetabolism(t, st); !strings.HasSuffix(got, "\n  (and 3 earlier retirement(s) not shown here)") {
		t.Fatalf("the retirements left out are not declared:\n%s", got)
	}
	st = dawnStore()
	if got := shownToMetabolism(t, st); strings.Contains(got, "not shown here") {
		t.Fatalf("a section that left nothing out declared an omission:\n%s", got)
	}
}

func TestARetiredBeliefIsNotSaidTwice(t *testing.T) {
	const dawn = "The ferry to the island leaves the harbor at dawn"
	st := dawnStore()
	st.tensions = []store.TensionPair{{EdgeID: "t1", LeftID: "exp_fix", RightID: "b_dawn"}}
	st.tensionNotes = []store.Experience{{ID: "exp_fix", Content: "the operator says noon now", Provenance: "operator", OperatorVerbatim: true}}
	st.retired = []store.Belief{{ID: "b_dawn", Statement: dawn}}
	got := shownToMetabolism(t, st)
	if n := strings.Count(got, dawn); n != 1 {
		t.Fatalf("the retired belief's words are in the input %d times:\n%s", n, got)
	}
	if !strings.Contains(got, `- [exp_fix] the operator's words: "the operator says noon now" stands against [b_dawn]`) {
		t.Fatalf("the contradiction does not name the retired belief by id:\n%s", got)
	}

	st.retiredView = nil
	got = shownToMetabolism(t, st)
	if !strings.Contains(got, `stands against [b_dawn] a belief since retired: "`+dawn+`"`) {
		t.Fatalf("a retired belief outside the section lost contract's wording:\n%s", got)
	}
}

// AN UNREADABLE RECORD OF RETIREMENTS IS LEFT OUT, LOUDLY, and the pass goes
// on with the input it had before: no view rather than a wrong one.
func TestAnUnreadableRetiredViewIsLeftOutAndThePassGoesOn(t *testing.T) {
	buf := captureLog(t)
	st := dawnStore()
	st.retiredErr = errors.New("database is locked")
	m, lg, _ := consolidateOver(t, st, backedEnvelope("the view"))
	if strings.Contains(m.lastUser, "retired") || strings.Contains(m.lastUser, "2026-") {
		t.Fatalf("a view was shown from a read that failed:\n%s", m.lastUser)
	}
	if len(lg.appended) == 0 {
		t.Fatal("an unreadable retirement record stopped the pass")
	}
	if !strings.Contains(buf.String(), "database is locked") {
		t.Fatalf("the failure was not logged: %q", buf.String())
	}
}

// THE RENDER-ONLY PASS has no notes on the table, so it has no retired
// section either: nothing is asked of the record.
func TestTheRenderOnlyPassIsNotShownTheRetiredSection(t *testing.T) {
	st := dawnStore()
	st.unprocessedCnt, st.experiences = 0, nil
	m := &mockLLM{override: "You believe the ferry leaves at noon."}
	c := NewConsolidate(st, m, &mockLedger{st: st}, &mockRingWriter{}, ConsolidateConfig{Threshold: 3})
	if err := c.Execute(context.Background()); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(m.lastUser, "retired") || len(st.retiredAsked) != 0 {
		t.Fatalf("the render-only pass was shown, or asked for, the retired section:\n%s", m.lastUser)
	}
}
