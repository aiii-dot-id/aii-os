package cognitive

import (
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/store"
)

// rhythm_dwires_test.go — the D-wires: counters that say "claims",
// an efficacy announcement guarded by the outbox's own idempotence,
// and the probe-nominator that hands the review one inherited
// conclusion to re-derive.

func TestBriefCarriesClaimedCounters(t *testing.T) {
	src := &fakeStagnation{
		items:    []store.StaleIntention{{ID: "i1", Statement: "drifting", Gap: 200}},
		verdicts: [3]int{2, 1, 1},
	}
	door := &mockLedger{}
	r := quietRhythm(src, door, nil)
	r.checkStagnation()
	briefs := attentionBriefs(door)
	if len(briefs) != 1 {
		t.Fatalf("briefs: %d, want 1", len(briefs))
	}
	content, _ := briefs[0]["content"].(string)
	if !strings.Contains(content, "served 2 · partial 1 · unserved 1") {
		t.Fatalf("counters missing from the brief: %q", content)
	}
	if !strings.Contains(content, "self-reported") {
		t.Fatalf("the brief must label counts as claims: %q", content)
	}
}

func TestEfficacyAnnouncesAtTwentyClaims(t *testing.T) {
	var got []string
	outbox := func(id, content string) { got = append(got, id+"|"+content) }

	src := &fakeStagnation{verdicts: [3]int{10, 5, 4}} // 19: quiet
	r := quietRhythm(src, &mockLedger{}, outbox)
	r.checkStagnation()
	if len(got) != 0 {
		t.Fatalf("announced below the floor: %v", got)
	}

	src.verdicts = [3]int{10, 5, 5} // 20: speaks
	r.checkStagnation()
	if len(got) != 1 || !strings.HasPrefix(got[0], "efficacy_data_ready|") {
		t.Fatalf("efficacy announcement wrong: %v", got)
	}
	if !strings.Contains(got[0], "CLAIMS") {
		t.Fatalf("the announcement must say these are claims: %v", got)
	}
	// Once-ness lives in AddOutboxMessageOnce (idempotent by id) —
	// the rhythm may repeat the call; the store is the guard.
}

func TestProbeNominatedOncePerBeliefVersion(t *testing.T) {
	src := &fakeStagnation{probe: &store.StaleBelief{ID: "b_old", Statement: "the old figure", Gap: 150}}
	door := &mockLedger{}
	r := quietRhythm(src, door, nil)

	r.nominateProbe()
	r.nominateProbe() // same belief-version: no second mint
	probes := probeMints(door)
	if len(probes) != 1 {
		t.Fatalf("probe minted %d times for one belief-version", len(probes))
	}
	content, _ := probes[0]["content"].(string)
	if !strings.Contains(content, "b_old") || !strings.Contains(content, "claim, not evidence") {
		t.Fatalf("probe content wrong: %q", content)
	}

	src.probe.Statement = "a revised figure" // new version: nominate again
	r.nominateProbe()
	if len(probeMints(door)) != 2 {
		t.Fatal("a revised belief-version was not re-nominated")
	}

	src.probe = nil // nothing stale: silent
	r.nominateProbe()
	if len(probeMints(door)) != 2 {
		t.Fatal("an empty nomination minted anyway")
	}
}

func probeMints(door *mockLedger) []map[string]interface{} {
	var out []map[string]interface{}
	for i := range door.appended {
		if p, ok := door.payloads[i].(map[string]interface{}); ok {
			if id, _ := p["id"].(string); strings.HasPrefix(id, "exp_probe_") {
				out = append(out, p)
			}
		}
	}
	return out
}
