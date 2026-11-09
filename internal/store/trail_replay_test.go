package store

import (
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/ledger"
)

// trail_replay_test.go — the record survives replay (host, 2026-10-30
// 16:24Z, item 1).
//
// The trail is a read of the record. If replay rebuilds the projections
// from the signed events and the trail changes, then the trail was
// reading something that is not the record. So: build a belief with its
// evidence and a revision out of PRODUCED EVENTS, read the trail, replay
// those same events into a store that has never seen them, read again,
// and compare with recall left out.
//
// Recall is excluded on purpose and is the one thing the comparison
// leaves out: memory_access is store-only (it is not f(ledger)), so it
// cannot survive a replay and must not be compared. What this proves is
// that the record itself — the belief and its arms — is stable.

// trailRecord is the part of a trail that replay must reproduce: the
// belief and its four arms, without recall.
type trailRecord struct {
	BeliefID          string
	Statement         string
	Ring              int
	Confidence        float64
	Evidence          int
	Archived          bool
	SupersededBy      string
	Origins           []trailRecordLink
	Contradicts       []trailRecordLink
	Supersedes        []trailRecordLink
	SupersededByLinks []trailRecordLink
}

type trailRecordLink struct {
	ID       string
	FromID   string
	ToID     string
	EdgeType string
	Strength float64
}

func recordOf(t *testing.T, s *Store, beliefID string) trailRecord {
	t.Helper()
	b, err := s.BeliefForTrail(beliefID)
	if err != nil {
		t.Fatalf("BeliefForTrail(%s): %v", beliefID, err)
	}
	arms, err := s.TrailArmsForBelief(beliefID, 50)
	if err != nil {
		t.Fatalf("TrailArmsForBelief(%s): %v", beliefID, err)
	}
	conv := func(edges []TrailEdge) []trailRecordLink {
		out := make([]trailRecordLink, 0, len(edges))
		for _, e := range edges {
			out = append(out, trailRecordLink{
				ID: e.ID, FromID: e.FromID, ToID: e.ToID,
				EdgeType: e.EdgeType, Strength: e.Strength,
			})
		}
		return out
	}
	return trailRecord{
		BeliefID: b.ID, Statement: b.Statement, Ring: b.Ring,
		Confidence: b.Confidence, Evidence: b.EvidenceCount,
		Archived: b.Archived, SupersededBy: b.SupersededBy,
		Origins: conv(arms.Origins), Contradicts: conv(arms.Contradicts),
		Supersedes: conv(arms.Supersedes), SupersededByLinks: conv(arms.SupersededBy),
	}
}

func sameRecord(t *testing.T, before, after trailRecord) {
	t.Helper()
	if before.BeliefID != after.BeliefID || before.Statement != after.Statement ||
		before.Ring != after.Ring || before.Confidence != after.Confidence ||
		before.Evidence != after.Evidence || before.Archived != after.Archived ||
		before.SupersededBy != after.SupersededBy {
		t.Fatalf("the belief changed under replay:\n before %+v\n after  %+v", before, after)
	}
	arms := []struct {
		name          string
		before, after []trailRecordLink
	}{
		{"origins", before.Origins, after.Origins},
		{"contradicts", before.Contradicts, after.Contradicts},
		{"supersedes", before.Supersedes, after.Supersedes},
		{"superseded_by", before.SupersededByLinks, after.SupersededByLinks},
	}
	for _, arm := range arms {
		if len(arm.before) != len(arm.after) {
			t.Fatalf("%s: %d links before replay, %d after", arm.name, len(arm.before), len(arm.after))
		}
		for i := range arm.before {
			if arm.before[i] != arm.after[i] {
				t.Fatalf("%s[%d] changed under replay:\n before %+v\n after  %+v", arm.name, i, arm.before[i], arm.after[i])
			}
		}
	}
}

// A belief with its evidence and a revision: the trail reads the same
// after the projections are rebuilt from the events.
func TestABeliefTrailAnswersTheSameAfterReplay(t *testing.T) {
	s := testStore(t)

	ev := []ledger.Event{
		*seqEvent(t, s, 1, ledger.EventExperienceCreate, map[string]any{
			"id": "exp_evidence", "content": "the kettle boiled at noon", "provenance": "self",
		}),
		*seqEvent(t, s, 2, ledger.EventBeliefUpsert, map[string]any{
			"id": "b_kettle", "statement": "the kettle boils before noon", "ring": 3,
			"confidence": 0.6, "origin_refs": []map[string]string{{"kind": "experience", "id": "exp_evidence"}},
		}),
		*seqEvent(t, s, 3, ledger.EventBeliefUpsert, map[string]any{
			"id": "b_kettle", "statement": "the kettle boils before noon, usually", "ring": 3,
			"confidence": 0.7, "origin_refs": []map[string]string{{"kind": "experience", "id": "exp_evidence"}},
		}),
	}

	before := recordOf(t, s, "b_kettle")

	// A store that has never seen these events, rebuilt by replay.
	fresh := testStore(t)
	if err := fresh.ReplayAll(EventSlice(ev)); err != nil {
		t.Fatalf("ReplayAll: %v", err)
	}
	after := recordOf(t, fresh, "b_kettle")

	sameRecord(t, before, after)
}

// The starvation fixture: a contradiction created LATER than every origin
// must survive a bounded read. A bound applied before ordering, or applied
// to the union rather than per arm, drops the newest — which is the one
// that matters most.
func TestTheNewestContradictionSurvivesAFullOriginArm(t *testing.T) {
	s := testStore(t)

	var ev []ledger.Event
	var refs []map[string]string
	seq := uint64(1)
	for i := 0; i < 50; i++ {
		id := "exp_origin_" + string(rune('a'+i%26)) + string(rune('a'+i/26))
		ev = append(ev, *seqEvent(t, s, seq, ledger.EventExperienceCreate, map[string]any{
			"id": id, "content": "observation " + id, "provenance": "self",
		}))
		refs = append(refs, map[string]string{"kind": "experience", "id": id})
		seq++
	}
	ev = append(ev, *seqEvent(t, s, seq, ledger.EventBeliefUpsert, map[string]any{
		"id": "b_busy", "statement": "a belief with fifty origins", "ring": 3,
		"confidence": 0.5, "origin_refs": refs,
	}))
	seq++
	ev = append(ev, *seqEvent(t, s, seq, ledger.EventBeliefUpsert, map[string]any{
		"id": "b_counter", "statement": "the belief is wrong", "ring": 3, "confidence": 0.4,
	}))
	seq++
	ev = append(ev, *seqEvent(t, s, seq, ledger.EventEdgeCreate, map[string]any{
		"id": "e_contra", "from_id": "b_counter", "to_id": "b_busy",
		"edge_type": "CONTRADICTS", "strength": 0.9,
	}))

	fresh := testStore(t)
	if err := fresh.ReplayAll(EventSlice(ev)); err != nil {
		t.Fatalf("ReplayAll: %v", err)
	}

	arms, err := fresh.TrailArmsForBelief("b_busy", 50)
	if err != nil {
		t.Fatalf("TrailArmsForBelief: %v", err)
	}
	if len(arms.Contradicts) != 1 {
		t.Fatalf("the newest contradiction did not survive a full origin arm: %d contradictions", len(arms.Contradicts))
	}
	if arms.Contradicts[0].ID != "e_contra" {
		t.Fatalf("the contradiction is %q, want e_contra", arms.Contradicts[0].ID)
	}
	if len(arms.Origins) != 50 {
		t.Fatalf("origins = %d, want 50", len(arms.Origins))
	}
	// The per-arm truncation flags are computed where the bound is
	// applied (internal/app/dashboard_reads.go), not here: TrailArms
	// carries the links and the caller decides what a full arm means.
	// TestBeliefTrailBoundsEachArmAndSaysSo asserts those flag values.
}
