package speecheval

import (
	"math"
	"strings"
	"testing"
)

// speecheval_test.go — known answers, because a metric nobody can check
// is a number nobody should act on.
//
// Every case here is one a person can verify by hand in a few seconds.
// That is the point: this package's whole job is to decide whether a
// speech endpoint is good enough to put in front of an identity, and a
// scorer with a quiet bug would answer that question confidently and
// wrongly.

func TestNormalizeIsExactlyTheRulesInTheContract(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want string
	}{
		{"The ledger and the outbox disagree.", "the ledger and the outbox disagree"},
		{"Don't stop.", "do not stop"},
		{"Don’t stop.", "do not stop"}, // curly apostrophe folds to straight
		{"It's the ledger", "it is the ledger"},
		{"port 8180", "port eight one eight zero"},
		{"10.200.200.2", "one zero two zero zero two zero zero two"},
		{"AII OS", "aii os"},
		{"  spaced   out  ", "spaced out"},
		{"hyphen-word", "hyphen word"},

		// THE COLLISION CASES. Each of these is a real English word whose
		// apostrophe-stripped contraction twin means something else. An
		// earlier draft of the map expanded them and would have rewritten
		// the first line here into "we we are there".
		{"we were there", "we were there"},
		{"the well is deep", "the well is deep"},
		{"its ledger is frozen", "its ledger is frozen"},
		{"he was ill", "he was ill"},
		{"the gate lets it through", "the gate lets it through"},
	} {
		got := strings.Join(Normalize(tc.in), " ")
		if got != tc.want {
			t.Errorf("Normalize(%q)\n got: %q\nwant: %q", tc.in, got, tc.want)
		}
	}
}

func TestAlignCountsEachKindOfErrorOnce(t *testing.T) {
	for _, tc := range []struct {
		name           string
		ref, hyp       string
		sub, del, ins  int
		wantRate       float64
		wantRateExists bool
	}{
		{"identical", "a b c", "a b c", 0, 0, 0, 0, true},
		{"one substitution", "a b c", "a x c", 1, 0, 0, 1.0 / 3, true},
		{"one deletion", "a b c", "a c", 0, 1, 0, 1.0 / 3, true},
		{"one insertion", "a b c", "a b x c", 0, 0, 1, 1.0 / 3, true},
		{"nothing heard", "a b c", "", 0, 3, 0, 1.0, true},
		{"every word wrong", "a b c", "x y z", 3, 0, 0, 1.0, true},
		// A reference with no words has no rate. Returning 0 would let
		// each silent clip pull the corpus average DOWN, which is the
		// exact opposite of what a hallucination should do to a score.
		{"empty reference", "", "thank you", 0, 0, 2, 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := Align(Normalize(tc.ref), Normalize(tc.hyp))
			if e.Sub != tc.sub || e.Del != tc.del || e.Ins != tc.ins {
				t.Fatalf("got S=%d D=%d I=%d, want S=%d D=%d I=%d",
					e.Sub, e.Del, e.Ins, tc.sub, tc.del, tc.ins)
			}
			rate, ok := e.Rate()
			if ok != tc.wantRateExists {
				t.Fatalf("rate exists = %v, want %v", ok, tc.wantRateExists)
			}
			if ok && math.Abs(rate-tc.wantRate) > 1e-9 {
				t.Fatalf("rate = %v, want %v", rate, tc.wantRate)
			}
		})
	}
}

// A REAL SENTENCE, COUNTED BY HAND. Reference is six words; the
// hypothesis mishears one, drops one and adds one, so S+D+I = 3 over
// N = 6 and the rate is 0.5.
func TestWERAgainstASentenceCountedByHand(t *testing.T) {
	ref := Normalize("the ledger and the outbox disagree")
	hyp := Normalize("the leisure and outbox strongly disagree")
	e := Align(ref, hyp)
	if e.RefWords != 6 {
		t.Fatalf("reference is %d words, expected 6", e.RefWords)
	}
	if got := e.Sub + e.Del + e.Ins; got != 3 {
		t.Fatalf("S=%d D=%d I=%d (total %d), expected 3 edits", e.Sub, e.Del, e.Ins, got)
	}
	rate, ok := e.Rate()
	if !ok || math.Abs(rate-0.5) > 1e-9 {
		t.Fatalf("WER = %v (ok=%v), want 0.5", rate, ok)
	}
}

func TestTermCountFindsMultiWordTerms(t *testing.T) {
	toks := Normalize("in safe mode the safe mode banner holds")
	if n := TermCount(toks, Normalize("safe mode")); n != 2 {
		t.Fatalf("safe mode counted %d times, want 2", n)
	}
	if n := TermCount(toks, Normalize("degraded witness")); n != 0 {
		t.Fatalf("a term that was never said counted %d times", n)
	}
	if n := TermCount(toks, Normalize("banner")); n != 1 {
		t.Fatalf("banner counted %d times, want 1", n)
	}
}

// THE METRIC THAT OVERALL WER CANNOT PROVIDE. Both hypotheses below
// score the same handful of edits, but one of them lost the word that
// decides whether the operator can be understood at all.
func TestTermRecallSeesWhatWERAveragesAway(t *testing.T) {
	refs := [][]string{
		Normalize("the ledger is frozen"),
		Normalize("aeon entered safe mode"),
	}
	hyps := [][]string{
		Normalize("the ledger is frozen"),
		Normalize("ian entered safe mode"), // "aeon" lost
	}
	scores := ScoreTerms([]string{"aeon", "ledger", "safe mode"}, refs, hyps)

	byTerm := map[string]TermScore{}
	for _, s := range scores {
		byTerm[s.Term] = s
	}
	if got := byTerm["aeon"]; got.InRef != 1 || got.InHyp != 0 || got.Recall != 0 {
		t.Fatalf("aeon: %+v — the identity's own name was lost and recall did not say so", got)
	}
	if got := byTerm["ledger"]; got.Recall != 1 {
		t.Fatalf("ledger: %+v, want full recall", got)
	}
	if got := byTerm["safe mode"]; got.InRef != 1 || got.Recall != 1 {
		t.Fatalf("safe mode: %+v, want one occurrence fully recovered", got)
	}
	// Worst first, so the report leads with what is broken.
	if scores[0].Term != "aeon" {
		t.Fatalf("report leads with %q, want the worst term first", scores[0].Term)
	}
}

// A term the endpoint invents cannot earn credit it was not owed.
func TestInventedTermsEarnNoCredit(t *testing.T) {
	refs := [][]string{Normalize("check the ledger")}
	hyps := [][]string{Normalize("check the ledger ledger ledger")}
	s := ScoreTerms([]string{"ledger"}, refs, hyps)
	if s[0].InRef != 1 || s[0].InHyp != 1 || s[0].Recall != 1 {
		t.Fatalf("%+v — credit exceeded what was actually said", s[0])
	}
}

func TestPercentileIsNearestRank(t *testing.T) {
	vals := []float64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}
	for _, tc := range []struct {
		p    float64
		want float64
	}{
		{0, 1}, {0.5, 5}, {0.95, 10}, {1, 10},
	} {
		if got := Percentile(vals, tc.p); got != tc.want {
			t.Errorf("Percentile(p=%v) = %v, want %v", tc.p, got, tc.want)
		}
	}
	if got := Percentile(nil, 0.95); got != 0 {
		t.Errorf("empty percentile = %v, want 0", got)
	}
	// And it must not disturb the caller's slice.
	unsorted := []float64{3, 1, 2}
	_ = Percentile(unsorted, 0.5)
	if unsorted[0] != 3 {
		t.Fatal("Percentile sorted the caller's slice in place")
	}
}
