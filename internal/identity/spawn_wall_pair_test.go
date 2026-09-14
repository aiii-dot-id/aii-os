package identity

import (
	"context"
	"encoding/json"
	"testing"
)

// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
func TestASpawnBindsItsWallToItsLease(t *testing.T) {
	for _, wall := range []int{600, 1800} {
		engine, st, _, _, _ := setupEngine(t)
		engine.SetAgencyLimits(3, 3, 20, wall)

		if _, err := engine.ExecuteAction(context.Background(), "verb", "work",
			map[string]interface{}{"action": "spawn", "goal": "bind the pair"}); err != nil {
			t.Fatalf("spawn at wall %d: %v", wall, err)
		}

		var raw string
		var leaseMs int64
		if err := st.DB().QueryRow(
			`SELECT payload, lease_ms FROM work_queue WHERE kind = ?`, SubagentWorkKind,
		).Scan(&raw, &leaseMs); err != nil {
			t.Fatal(err)
		}
		var request SubagentRequest
		if err := json.Unmarshal([]byte(raw), &request); err != nil {
			t.Fatal(err)
		}

		if request.WallSeconds != wall {
			t.Fatalf("the request must carry the wall its lease was built from: carried %d, engine had %d",
				request.WallSeconds, wall)
		}
		// .
		// .
		if wantLease := int64(request.WallSeconds+60) * 1000; leaseMs != wantLease {
			t.Fatalf("lease %dms is not derived from the carried wall %ds (want %dms) — "+
				"a lease shorter than its run is a second concurrent copy",
				leaseMs, request.WallSeconds, wantLease)
		}
		if leaseMs <= int64(request.WallSeconds)*1000 {
			t.Fatalf("lease %dms does not outlive the %ds wall it protects", leaseMs, request.WallSeconds)
		}
	}
}
