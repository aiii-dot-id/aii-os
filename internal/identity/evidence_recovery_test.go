package identity

import (
	"context"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/store"
)

// .
// .
func startAndActiveID(t *testing.T, engine *Engine, st *store.Store, desc string) string {
	t.Helper()
	if _, err := engine.ExecuteAction(context.Background(), "verb", "work",
		map[string]interface{}{"action": "start", "description": desc}); err != nil {
		t.Fatalf("work start: %v", err)
	}
	ws, err := st.ActiveWorkSession()
	if err != nil || ws == nil {
		t.Fatalf("no active session after start: %v", err)
	}
	return ws.ID
}

// .
// .
// .
// .
func TestDeliverDefaultsBySeat(t *testing.T) {
	// .
	engine, st, _, _, _ := setupEngine(t)
	id := startAndActiveID(t, engine, st, "worker leg")
	ctx := context.WithValue(context.Background(), SubagentWorkSession{}, id)
	if _, err := engine.ExecuteAction(ctx, "verb", "work",
		map[string]interface{}{"action": "deliver", "result": "served: did the thing"}); err != nil {
		t.Fatalf("worker deliver: %v", err)
	}
	ws, _ := st.WorkSessionByID(id)
	if ws.Evidence != store.EvidenceWorkerReportOnly {
		t.Fatalf("worker seat default = %q, want worker_report_only", ws.Evidence)
	}

	// .
	engine2, st2, _, _, _ := setupEngine(t)
	id2 := startAndActiveID(t, engine2, st2, "primary leg")
	if _, err := engine2.ExecuteAction(context.Background(), "verb", "work",
		map[string]interface{}{"action": "deliver", "result": "served: finished it here"}); err != nil {
		t.Fatalf("primary deliver: %v", err)
	}
	ws2, _ := st2.WorkSessionByID(id2)
	if ws2.Evidence != store.EvidenceCompletedLocally {
		t.Fatalf("primary seat default = %q, want completed_locally", ws2.Evidence)
	}
}

// .
// .
func TestDeliverGatesVerifiedTierAtVerb(t *testing.T) {
	engine, st, _, _, _ := setupEngine(t)
	id := startAndActiveID(t, engine, st, "verify leg")

	// .
	if _, err := engine.ExecuteAction(context.Background(), "verb", "work",
		map[string]interface{}{"action": "deliver", "result": "served: checked", "evidence": store.EvidenceLocallyVerified}); err == nil {
		t.Fatal("expected refusal: locally_verified without evidence_readback")
	}
	// .
	if _, err := engine.ExecuteAction(context.Background(), "verb", "work",
		map[string]interface{}{"action": "deliver", "result": "served: checked", "evidence": store.EvidenceLocallyVerified, "evidence_readback": "ran the check and read PASS"}); err != nil {
		t.Fatalf("verified deliver with readback: %v", err)
	}
	ws, _ := st.WorkSessionByID(id)
	if ws.Evidence != store.EvidenceLocallyVerified || ws.EvidenceReadback == "" {
		t.Fatalf("verified class/readback not persisted: %+v", ws)
	}

	// .
	engine2, st2, _, _, _ := setupEngine(t)
	id2 := startAndActiveID(t, engine2, st2, "worker verify leg")
	ctx := context.WithValue(context.Background(), SubagentWorkSession{}, id2)
	if _, err := engine2.ExecuteAction(ctx, "verb", "work",
		map[string]interface{}{"action": "deliver", "result": "served: checked", "evidence": store.EvidenceLocallyVerified, "evidence_readback": "trust me"}); err == nil {
		t.Fatal("expected refusal: a worker seat cannot declare locally_verified")
	}
}

// .
// .
// .
func TestDeliverMalformedIsRejectedBeforeEffect(t *testing.T) {
	engine, st, _, _, _ := setupEngine(t)
	id := startAndActiveID(t, engine, st, "malformed leg")
	_, err := engine.ExecuteAction(context.Background(), "verb", "work",
		map[string]interface{}{"action": "deliver", "result": "all done, looks great"})
	if err == nil {
		t.Fatal("expected refusal for a delivery without an outcome line")
	}
	msg := err.Error()
	if !strings.Contains(msg, "rejected_before_effect") || !strings.Contains(msg, "deliver.outcome_line") {
		t.Fatalf("refusal must name the rejected class and boundary: %q", msg)
	}
	// .
	ws, _ := st.WorkSessionByID(id)
	if ws.Status == "delivered" {
		t.Fatalf("a rejected delivery must not land: status=%q", ws.Status)
	}
	if ws.Evidence != "" {
		t.Fatalf("a rejected delivery must not classify: %q", ws.Evidence)
	}
}
