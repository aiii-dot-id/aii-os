package app

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/ledger"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

// The identity frame carries the beliefs no longer held (F-DASH-043): newest
// retirement first, each with the record's own time and the belief that
// stands in its place; an archived belief replaced by nothing; a Ring 2
// archive is not working truth and is not listed; the members by name on
// the wire; an empty list, not null, when none.
func TestTheIdentityFrameCarriesTheBeliefsNoLongerHeld(t *testing.T) {
	st, err := store.NewForTest(filepath.Join(t.TempDir(), "aii.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	a := &App{store: st}
	seq := uint64(0)
	mat := func(ts string, et ledger.EventType, payload map[string]any) {
		t.Helper()
		seq++
		b, _ := json.Marshal(payload)
		if err := st.Materialize(&ledger.Event{Seq: seq, Type: et, Payload: b, Timestamp: ts}); err != nil {
			t.Fatalf("materialize %s: %v", et, err)
		}
	}
	belief := func(id, statement string, ring int) {
		mat("2026-09-20T08:00:00Z", ledger.EventBeliefUpsert, map[string]any{"id": id, "statement": statement, "ring": ring, "confidence": 0.6})
	}

	// None retired: an empty list, not null.
	belief("b_sun", "The sun rises in the east.", 3)
	state, err := a.identityState()
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(state)
	var frame map[string]any
	if err := json.Unmarshal(raw, &frame); err != nil {
		t.Fatal(err)
	}
	if list, ok := frame["retired"].([]any); !ok || len(list) != 0 {
		t.Fatalf("with nothing retired the frame must carry an empty list, got %v", frame["retired"])
	}
	if frame["retired_more"] != float64(0) {
		t.Fatalf("retired_more must be 0, got %v", frame["retired_more"])
	}

	// A supersession, an archive, and a Ring 2 archive that is not working truth.
	belief("b_dawn", "Deploys go straight to production.", 3)
	belief("b_six", "A deploy goes through staging first.", 3)
	belief("b_oil", "Olive oil is the only oil.", 3)
	belief("b_core", "Honesty first.", 2)
	mat("2026-09-26T10:00:00Z", ledger.EventBeliefSupersede, map[string]any{"old_id": "b_dawn", "new_id": "b_six", "reason": "two deploys went wrong"})
	mat("2026-09-28T09:05:00Z", ledger.EventBeliefArchive, map[string]any{"id": "b_oil", "reason": "overstated"})
	mat("2026-09-28T09:06:00Z", ledger.EventBeliefArchive, map[string]any{"id": "b_core", "reason": "not working truth"})

	// Evidence recorded after the archive must not move its date or its place.
	mat("2026-09-29T07:00:00Z", ledger.EventExperienceCreate, map[string]any{"id": "exp_late", "content": "oil again", "category": "observation"})
	mat("2026-09-29T07:00:01Z", ledger.EventEdgeCreate, map[string]any{"id": "edge_late", "from_id": "exp_late", "to_id": "b_oil", "edge_type": "SUPPORTS"})

	state, err = a.identityState()
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Retired) != 2 || state.RetiredMore != 0 {
		t.Fatalf("two Ring 3 beliefs were retired, got %d (more %d): %+v", len(state.Retired), state.RetiredMore, state.Retired)
	}
	oil, dawn := state.Retired[0], state.Retired[1]
	if oil.ID != "b_oil" || oil.RetiredAt != "2026-09-28T09:05:00Z" || oil.ReplacedBy != nil {
		t.Fatalf("the newest retirement first, archived, replaced by nothing, at the record's time: %+v", oil)
	}
	if dawn.ID != "b_dawn" || dawn.RetiredAt != "2026-09-26T10:00:00Z" || dawn.ReplacedBy == nil ||
		dawn.ReplacedBy.ID != "b_six" || dawn.ReplacedBy.Statement != "A deploy goes through staging first." || dawn.ReplacedBy.Retired {
		t.Fatalf("the superseded belief names the belief that stands in its place: %+v %+v", dawn, dawn.ReplacedBy)
	}
	raw, _ = json.Marshal(state)
	if err := json.Unmarshal(raw, &frame); err != nil {
		t.Fatal(err)
	}
	first := frame["retired"].([]any)[1].(map[string]any)
	if first["statement"] != "Deploys go straight to production." || first["retired_at"] != "2026-09-26T10:00:00Z" || first["replaced_by"].(map[string]any)["statement"] != "A deploy goes through staging first." {
		t.Fatalf("the members by name on the wire: %v", first)
	}
	for _, r := range state.Retired {
		if r.ID == "b_core" {
			t.Fatal("a Ring 2 archive is not working truth and must not be listed")
		}
	}
}
