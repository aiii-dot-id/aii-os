package app

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/ledger"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

// changedRecord is a real record the tests below write events into, each at
// the ledger time given.
func changedRecord(t *testing.T) (*App, *store.Store, func(ts string, et ledger.EventType, payload map[string]any)) {
	t.Helper()
	st, err := store.NewForTest(filepath.Join(t.TempDir(), "aii.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	seq := uint64(0)
	return &App{store: st}, st, func(ts string, et ledger.EventType, payload map[string]any) {
		t.Helper()
		seq++
		b, _ := json.Marshal(payload)
		if err := st.Materialize(&ledger.Event{Seq: seq, Type: et, Payload: b, Timestamp: ts}); err != nil {
			t.Fatalf("materialize %s: %v", et, err)
		}
	}
}

// A HELD BELIEF IS DATED BY ITS NEWEST EVENT (About → Beliefs, "Changed this
// month"): formed on 2 October and given evidence on 20 October, it changed
// on 20 October, as the record states it; one never touched again changed
// when it formed.
func TestAHeldBeliefIsDatedByItsNewestEvent(t *testing.T) {
	a, _, mat := changedRecord(t)
	mat("2026-10-02T08:00:00Z", ledger.EventBeliefUpsert, map[string]any{"id": "b_stage", "statement": "A deploy goes to staging first.", "ring": 3, "confidence": 0.6})
	mat("2026-10-03T08:00:00Z", ledger.EventBeliefUpsert, map[string]any{"id": "b_short", "statement": "Short reports are read.", "ring": 3, "confidence": 0.6})
	mat("2026-10-20T09:29:59Z", ledger.EventExperienceCreate, map[string]any{"id": "exp_rollback", "content": "the rollback took an hour", "category": "observation"})
	mat("2026-10-20T09:30:00Z", ledger.EventEdgeCreate, map[string]any{"id": "edge_rollback", "from_id": "exp_rollback", "to_id": "b_stage", "edge_type": "SUPPORTS"})

	state, err := a.identityState()
	if err != nil {
		t.Fatal(err)
	}
	changed := map[string]string{}
	for _, b := range state.Beliefs {
		changed[b.ID] = b.ChangedAt
	}
	if changed["b_stage"] != "2026-10-20T09:30:00Z" {
		t.Errorf("a belief given evidence on 20 October changed at %q, want the evidence's time 2026-10-20T09:30:00Z", changed["b_stage"])
	}
	if changed["b_short"] != "2026-10-03T08:00:00Z" {
		t.Errorf("a belief never touched again changed at %q, want when it formed, 2026-10-03T08:00:00Z", changed["b_short"])
	}
	raw, _ := json.Marshal(state.Beliefs[0])
	var wire map[string]any
	if err := json.Unmarshal(raw, &wire); err != nil {
		t.Fatal(err)
	}
	if _, ok := wire["changed_at"]; !ok {
		t.Errorf("the wire does not carry changed_at by name: %s", raw)
	}
}

// A time the record states and this cannot read fails the frame: unreadable
// is not empty, and a belief must not drop out of "Changed this month"
// because its entry could not be read.
func TestABeliefsUnreadableChangeFailsTheFrame(t *testing.T) {
	a, st, mat := changedRecord(t)
	mat("2026-10-02T08:00:00Z", ledger.EventBeliefUpsert, map[string]any{"id": "b_stage", "statement": "A deploy goes to staging first.", "ring": 3, "confidence": 0.6})
	if _, err := st.DB().Exec(`UPDATE ledger SET ts = 'the second of October' WHERE seq = 1`); err != nil {
		t.Fatal(err)
	}
	if state, err := a.identityState(); err == nil {
		t.Fatalf("an unreadable ledger time built a frame: %+v", state.Beliefs)
	}
}

// WHY? ANSWERS FOR A BELIEF NO LONGER HELD: the trail reads an archived and a
// superseded belief as the record holds them, so the page's rows for them
// keep Why?.
func TestTheTrailAnswersForABeliefNoLongerHeld(t *testing.T) {
	a, _, mat := changedRecord(t)
	mat("2026-10-02T08:00:00Z", ledger.EventBeliefUpsert, map[string]any{"id": "b_prod", "statement": "Deploys go straight to production.", "ring": 3, "confidence": 0.6})
	mat("2026-10-02T08:00:01Z", ledger.EventBeliefUpsert, map[string]any{"id": "b_stage", "statement": "A deploy goes to staging first.", "ring": 3, "confidence": 0.6})
	mat("2026-10-02T08:00:02Z", ledger.EventBeliefUpsert, map[string]any{"id": "b_oil", "statement": "Olive oil is the only oil.", "ring": 3, "confidence": 0.6})
	mat("2026-10-21T10:00:00Z", ledger.EventBeliefSupersede, map[string]any{"old_id": "b_prod", "new_id": "b_stage", "reason": "two deploys went wrong"})
	mat("2026-10-22T10:00:00Z", ledger.EventBeliefArchive, map[string]any{"id": "b_oil", "reason": "overstated"})

	archived, err := a.beliefTrailForDashboard("b_oil")
	if err != nil {
		t.Fatalf("the trail of an archived belief: %v", err)
	}
	if archived.Belief.ID != "b_oil" || archived.Belief.Statement != "Olive oil is the only oil." || !archived.Belief.Archived {
		t.Errorf("the trail does not read the archived belief as the record holds it: %+v", archived.Belief)
	}
	superseded, err := a.beliefTrailForDashboard("b_prod")
	if err != nil {
		t.Fatalf("the trail of a superseded belief: %v", err)
	}
	if superseded.Belief.ID != "b_prod" || superseded.SupersededBy == nil || superseded.SupersededBy.ID != "b_stage" {
		t.Errorf("the trail does not say what replaced the superseded belief: %+v %+v", superseded.Belief, superseded.SupersededBy)
	}
}
