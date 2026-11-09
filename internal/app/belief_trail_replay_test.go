package app

import (
	"context"
	"fmt"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/dashboard"
	"github.com/aiii-dot-id/aii-os/internal/identity"
	"github.com/aiii-dot-id/aii-os/internal/ledger"
	"github.com/aiii-dot-id/aii-os/internal/ring"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

// The spec's acceptance test for the trail (spec 1a "Test", v27 line 343):
// a belief built through the identity's own doors — evidence, a revision
// and a contradiction — is asked for its trail; the record is replayed
// into a fresh store; the same question gets the same answer. Recall is
// the one part set aside: it is read at read time over memory_access,
// not carried in the record, so a replay cannot be asked to reproduce a
// measurement (host, 2026-10-30, item 1; a review 1653).
func TestTheTrailAnswersTheSameAfterReplay(t *testing.T) {
	b := newCognitionBench(t)
	engine := identity.NewEngine(b.st, b.door, ring.NewManager(), nil, identity.Ports{})
	commit := func(args map[string]any) {
		t.Helper()
		if _, err := engine.ExecuteAction(context.Background(), "verb", "commit", args); err != nil {
			t.Fatalf("commit %v: %v", args["variant"], err)
		}
	}

	// Evidence, the belief resting on it, a revision of the belief's
	// words, and something that contradicts it — each written through
	// the door the identity itself writes through, not a fixture that
	// calls the materializer directly.
	if _, err := b.door.Append(ledger.EventExperienceCreate, 3, map[string]any{
		"id": "exp_seen", "content": "the sky was grey at noon", "provenance": "self",
	}, ""); err != nil {
		t.Fatal(err)
	}
	commit(map[string]any{
		"variant": "belief.upsert", "id": "b_sky", "statement": "The sky is blue.",
		"evidence_refs": "exp_seen",
	})
	// A revision is a successor and a supersede naming both, because the
	// record refuses a statement changed under one id: the first draft
	// of this fixture was refused by the door for exactly that.
	commit(map[string]any{
		"variant": "belief.upsert", "id": "b_sky_noon", "statement": "The sky is blue at noon.",
		"evidence_refs": "exp_seen",
	})
	commit(map[string]any{
		"variant": "belief.supersede", "old_id": "b_sky", "new_id": "b_sky_noon",
	})
	if _, err := b.door.Append(ledger.EventExperienceCreate, 3, map[string]any{
		"id": "exp_grey", "content": "a grey noon", "provenance": "self",
	}, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := b.door.Append(ledger.EventEdgeCreate, 3, map[string]any{
		"id": "ed_grey", "from_id": "exp_grey", "to_id": "b_sky_noon", "edge_type": "CONTRADICTS",
	}, ""); err != nil {
		t.Fatal(err)
	}

	before, err := (&App{store: b.st, cfg: defaultConfig()}).beliefTrailForDashboard("b_sky_noon")
	if err != nil {
		t.Fatalf("trail before replay: %v", err)
	}

	// The answer must carry what the fixture built, or two empty lists
	// would compare equal and pass for the wrong reason.
	if before.Belief.Statement != "The sky is blue at noon." {
		t.Fatalf("statement = %q, want the successor's words", before.Belief.Statement)
	}
	if len(before.Origins) == 0 {
		t.Fatalf("origins is empty before replay, want the evidence the record holds")
	}
	if len(before.Contradicts) == 0 {
		t.Fatalf("contradicts is empty before replay, want the contradiction the record holds")
	}
	if len(before.Supersedes) == 0 {
		t.Fatalf("supersedes is empty before replay, want the belief it replaced")
	}
	// Presence as well as equality: comparing before with after cannot
	// catch a field that is empty in both. A control that emptied
	// HeldSince passed this test until these lines existed (a review 1653:
	// assert the expected nonempty fields before comparing).
	if before.Belief.HeldSince == "" {
		t.Fatalf("held_since is empty, want the record's own birth time")
	}
	if before.Belief.EvidenceCount == 0 {
		t.Fatalf("evidence_count = 0, want the evidence the record holds")
	}
	for _, links := range [][]dashboard.TrailLink{before.Origins, before.Contradicts, before.Supersedes} {
		for _, l := range links {
			if l.ID == "" || l.Text == "" || l.Kind == "" {
				t.Fatalf("link %+v has no id, text or kind, want the record's own words", l)
			}
			if l.Strength == nil {
				t.Fatalf("link %s has null strength, want the weight the record holds", l.ID)
			}
			if l.CreatedAt == "" {
				t.Fatalf("link %s has no created_at, want the record's own time", l.ID)
			}
		}
	}
	// The far ends are read from the record, not carried by the edge.
	if before.Origins[0].Text == "" || before.Origins[0].Kind == "" {
		t.Fatalf("origin %+v has no text or kind, want the record's own words", before.Origins[0])
	}

	events, err := ledger.ReadAll(b.ledgerP)
	if err != nil {
		t.Fatal(err)
	}
	fresh, err := store.NewForTest(filepath.Join(t.TempDir(), "replayed.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { fresh.Close() })
	if err := fresh.ReplayAll(store.EventSlice(events)); err != nil {
		t.Fatalf("replay: %v", err)
	}

	after, err := (&App{store: fresh, cfg: defaultConfig()}).beliefTrailForDashboard("b_sky_noon")
	if err != nil {
		t.Fatalf("trail after replay: %v", err)
	}

	setAsideRecall(before)
	setAsideRecall(after)

	if diff := trailDifference(before, after); diff != "" {
		t.Errorf("the trail changed across a replay:\n%s", diff)
	}
}

// setAsideRecall clears the part of the answer that is measured at read
// time rather than carried in the record. Nothing else is cleared: the
// whole rest of the answer is compared.
func setAsideRecall(tr *dashboard.BeliefTrail) {
	tr.Belief.Recall = nil
	tr.Belief.RecallCount = nil
	tr.Belief.RecalledAt = nil
	tr.Belief.Fading = false
}

func trailDifference(before, after *dashboard.BeliefTrail) string {
	if reflect.DeepEqual(before, after) {
		return ""
	}
	return fmt.Sprintf("before: %+v\nafter:  %+v", *before, *after)
}
