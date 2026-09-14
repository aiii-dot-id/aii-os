package store

import (
	"strings"
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
func TestEvidenceDerivationDistinguishesClasses(t *testing.T) {
	ambiguous := []string{
		EvidenceRejectedNoEffect,
		EvidenceExternalUnknown,
		EvidenceWorkerReportOnly,
		EvidencePartialOrMixed,
	}
	seen := map[string]string{}
	for _, c := range ambiguous {
		rec := EvidenceRecovery(c)
		if strings.TrimSpace(rec) == "" {
			t.Fatalf("ambiguous class %q has no recovery check", c)
		}
		if prev, ok := seen[rec]; ok {
			t.Fatalf("classes %q and %q share a recovery string — recovery must differ per class", prev, c)
		}
		seen[rec] = c
		if EvidenceScopeLabel(c) == "" {
			t.Fatalf("class %q has no scope label", c)
		}
	}
	// .
	if r := EvidenceRecovery(EvidenceExternalUnknown); !strings.Contains(r, "inspect") {
		t.Fatalf("external_effect_unknown recovery must inspect first: %q", r)
	}
	// .
	for _, c := range []string{EvidenceNotRun, EvidenceCompletedLocally, EvidenceLocallyVerified, EvidenceHostReceipted} {
		if EvidenceRecovery(c) != "" {
			t.Fatalf("settled class %q should have no recovery, got %q", c, EvidenceRecovery(c))
		}
	}
	// .
	if !EvidenceVerifiedTier(EvidenceLocallyVerified) || !EvidenceVerifiedTier(EvidenceHostReceipted) {
		t.Fatal("verified tier not recognized")
	}
	if EvidenceVerifiedTier(EvidenceWorkerReportOnly) || EvidenceVerifiedTier(EvidenceCompletedLocally) {
		t.Fatal("a reported class must not be treated as verified")
	}
	if !IsEvidenceClass(EvidenceWorkerReportOnly) || IsEvidenceClass("") || IsEvidenceClass("bogus") {
		t.Fatal("IsEvidenceClass vocabulary check failed")
	}
}

// .
// .
// .
func TestDeliverPersistsEvidenceAndGatesVerifiedTier(t *testing.T) {
	s := testStore(t)

	if err := s.StartWorkSession("ws_wr", "worker leg"); err != nil {
		t.Fatalf("start: %v", err)
	}
	if err := s.DeliverWorkSession("ws_wr", "served: did the thing", EvidenceWorkerReportOnly, ""); err != nil {
		t.Fatalf("deliver worker report: %v", err)
	}
	ws, err := s.WorkSessionByID("ws_wr")
	if err != nil || ws == nil {
		t.Fatalf("readback: %v", err)
	}
	if ws.Evidence != EvidenceWorkerReportOnly {
		t.Fatalf("evidence not persisted: %q", ws.Evidence)
	}
	if ws.Result != "served: did the thing" {
		t.Fatalf("original result not preserved: %q", ws.Result)
	}
	if EvidenceScopeLabel(ws.Evidence) != "worker-reported" {
		t.Fatalf("a served: worker delivery must read as worker-reported, got %q", EvidenceScopeLabel(ws.Evidence))
	}

	// .
	if err := s.StartWorkSession("ws_v", "verify leg"); err != nil {
		t.Fatalf("start: %v", err)
	}
	if err := s.DeliverWorkSession("ws_v", "served: checked", EvidenceLocallyVerified, ""); err == nil {
		t.Fatal("expected refusal: locally_verified without a readback")
	}
	if err := s.DeliverWorkSession("ws_v", "served: checked", EvidenceLocallyVerified, "ran go test ./... and read PASS"); err != nil {
		t.Fatalf("deliver verified with readback: %v", err)
	}
	ws, _ = s.WorkSessionByID("ws_v")
	if ws.Evidence != EvidenceLocallyVerified || ws.EvidenceReadback == "" {
		t.Fatalf("verified class/readback not persisted: %+v", ws)
	}

	// .
	if err := s.StartWorkSession("ws_bad", "bad leg"); err != nil {
		t.Fatalf("start: %v", err)
	}
	if err := s.DeliverWorkSession("ws_bad", "served: x", "made_up_class", ""); err == nil {
		t.Fatal("expected refusal: unknown evidence class")
	}
}

// .
// .
// .
func TestOrphanSweepClassifiesExternalUnknown(t *testing.T) {
	s := testStore(t)
	if err := s.StartWorkSession("ws_orphan", "long external op"); err != nil {
		t.Fatalf("start: %v", err)
	}
	if _, err := s.SweepOrphanWorkSessions(); err != nil {
		t.Fatalf("sweep: %v", err)
	}
	ws, err := s.WorkSessionByID("ws_orphan")
	if err != nil || ws == nil {
		t.Fatalf("readback: %v", err)
	}
	if ws.Evidence != EvidenceExternalUnknown {
		t.Fatalf("interrupted session must be external_effect_unknown, got %q", ws.Evidence)
	}
	if !strings.Contains(EvidenceRecovery(ws.Evidence), "inspect") {
		t.Fatalf("recovery must be a read-only inspection: %q", EvidenceRecovery(ws.Evidence))
	}
}

// .
// .
// .
func TestEachAmbiguousClassPersistsWithDistinctRecovery(t *testing.T) {
	s := testStore(t)
	cases := []struct{ id, class string }{
		{"ws_c0", EvidenceRejectedNoEffect},
		{"ws_c1", EvidenceExternalUnknown},
		{"ws_c2", EvidenceWorkerReportOnly},
		{"ws_c3", EvidencePartialOrMixed},
	}
	recoveries := map[string]bool{}
	for _, c := range cases {
		if err := s.StartWorkSession(c.id, "leg "+c.id); err != nil {
			t.Fatalf("start %s: %v", c.id, err)
		}
		if err := s.DeliverWorkSession(c.id, "partial: mixed outcome", c.class, ""); err != nil {
			t.Fatalf("deliver %s: %v", c.id, err)
		}
		ws, err := s.WorkSessionByID(c.id)
		if err != nil || ws == nil {
			t.Fatalf("readback %s: %v", c.id, err)
		}
		if ws.Evidence != c.class {
			t.Fatalf("class not persisted for %s: %q", c.id, ws.Evidence)
		}
		rec := EvidenceRecovery(ws.Evidence)
		if rec == "" {
			t.Fatalf("no recovery for persisted class %q", c.class)
		}
		if recoveries[rec] {
			t.Fatalf("recovery for %q collides with another class", c.class)
		}
		recoveries[rec] = true
	}
	if len(recoveries) != len(cases) {
		t.Fatalf("expected %d distinct recoveries, got %d", len(cases), len(recoveries))
	}
}

// .
// .
// .
func TestUnharvestedDeliveryCarriesEvidenceScope(t *testing.T) {
	s := testStore(t)
	id := "ws_child"
	if err := s.StartWorkSession(id, SubagentDescription("count the beans")); err != nil {
		t.Fatalf("start: %v", err)
	}
	if err := s.DeliverWorkSession(id, "unserved: could not reach the jar", EvidenceWorkerReportOnly, ""); err != nil {
		t.Fatalf("deliver: %v", err)
	}
	subs, err := s.UnharvestedDeliveries(8)
	if err != nil {
		t.Fatalf("unharvested: %v", err)
	}
	var found *WorkSession
	for i := range subs {
		if subs[i].ID == id {
			found = &subs[i]
			break
		}
	}
	if found == nil {
		t.Fatal("delivered child not present in the parent's outcome surface")
	}
	if found.Result != "unserved: could not reach the jar" {
		t.Fatalf("child result not retained: %q", found.Result)
	}
	if found.Evidence != EvidenceWorkerReportOnly {
		t.Fatalf("child scope not retained: %q", found.Evidence)
	}
	scope := RenderEvidenceScope(found.Evidence)
	if !strings.Contains(scope, "worker-reported") || !strings.Contains(scope, "recovery") {
		t.Fatalf("rendered scope must show worker-reported + recovery: %q", scope)
	}
}
