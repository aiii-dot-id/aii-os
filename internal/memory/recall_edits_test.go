package memory

import (
	"context"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/memory/trigram"
)

// .
// .
// .
// .
// .
func TestTheSecondLookKeepsAWordOneTypoAway(t *testing.T) {
	s := newStore(t)
	now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	seedLedger(t, s, 2, now.Add(-48*time.Hour))
	const text = "the pilot sounded the shoals at dawn"
	addExperience(t, s, "e1", text, 1, now.Add(-24*time.Hour), false)
	addExperience(t, s, "e2", "a quiet day of routine observations", 2, now.Add(-2*time.Hour), false)
	if cov := trigram.Coverage("sohals", text); cov >= FuzzyFloor {
		t.Fatalf("the case must be one the windows refuse: coverage %v", cov)
	}
	f := New(s)
	recall := func(q string) []Hit {
		t.Helper()
		res, err := f.Recall(context.Background(), Query{Text: q, Now: now})
		if err != nil {
			t.Fatal(err)
		}
		return res.Hits
	}
	hits := recall("sohals")
	if len(hits) != 1 || hits[0].ID != "e1" || hits[0].Match != MatchFuzzy {
		t.Fatalf("hits = %v, want e1 by the second look", hitIDs(hits))
	}
	if hits[0].Fuzz < 0.8 {
		t.Errorf("one edit in six letters scores %v, want 1 - 1/6", hits[0].Fuzz)
	}
	// .
	if hits := recall("the pliot shaols"); len(hits) != 1 || hits[0].ID != "e1" {
		t.Fatalf("every word within its allowance must hit: %v", hitIDs(hits))
	}
	// .
	if hits := recall("sohlas"); len(hits) != 0 {
		t.Fatalf("two edits in six letters must not hit: %v", hitIDs(hits))
	}
	// .
	if hits := recall("sohals dusk"); len(hits) != 0 {
		t.Fatalf("a word the text has nothing near must refuse: %v", hitIDs(hits))
	}
	// .
	if hits := recall("sohals ta"); len(hits) != 0 {
		t.Fatalf("a short word is never edited: %v", hitIDs(hits))
	}
	// .
	res, err := f.Recall(context.Background(), Query{Text: "sohals", Exact: true, Now: now})
	if err != nil || len(res.Hits) != 0 {
		t.Fatalf("exact must not take a near-miss: %v %v", err, hitIDs(res.Hits))
	}
}
