package identity

import (
	"context"
	"strings"
	"testing"
)

func TestVerbProjectEvidenceAndWaive(t *testing.T) {
	e, port := newVerbEngine(t)
	port.lastInfo = ProjectInfo{ID: "alpha", Name: "Alpha",
		ProgressLine: "progress: 0 verified, 1 supported, 1 open, 0 unsupported, 0 waived — next: item two"}

	// a valid worker report reaches the port with all fields.
	if _, err := e.ExecuteAction(context.Background(), "verb", "project", map[string]any{
		"action": "evidence", "project": "alpha", "item": "item one",
		"class": "worker_report_only", "ref": "ws_1", "note": "ran it",
	}); err != nil {
		t.Fatalf("evidence: %v", err)
	}
	if port.evidenceArgs.item != "item one" || port.evidenceArgs.class != "worker_report_only" || port.evidenceArgs.ref != "ws_1" {
		t.Fatalf("evidence args did not reach the port: %+v", port.evidenceArgs)
	}

	// an unknown class is refused BEFORE the port.
	port.evidenceArgs.class = ""
	if _, err := e.ExecuteAction(context.Background(), "verb", "project", map[string]any{
		"action": "evidence", "project": "alpha", "item": "item one", "class": "totally_made_up",
	}); err == nil {
		t.Fatal("unknown evidence class must be refused")
	}
	if port.evidenceArgs.class != "" {
		t.Fatal("a refused evidence action must not reach the port")
	}

	// a verified class without a ref is refused.
	if _, err := e.ExecuteAction(context.Background(), "verb", "project", map[string]any{
		"action": "evidence", "project": "alpha", "item": "item one", "class": "locally_verified",
	}); err == nil {
		t.Fatal("locally_verified without a ref must be refused")
	}

	// waive reaches the port with its reason.
	if _, err := e.ExecuteAction(context.Background(), "verb", "project", map[string]any{
		"action": "waive", "project": "alpha", "item": "item two", "reason": "out of scope this cycle",
	}); err != nil {
		t.Fatalf("waive: %v", err)
	}
	if port.waiveArgs.item != "item two" || !strings.Contains(port.waiveArgs.reason, "out of scope") {
		t.Fatalf("waive args did not reach the port: %+v", port.waiveArgs)
	}
}
