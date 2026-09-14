package identity

import (
	"context"
	"strings"
	"testing"
)

// .
// .
// .
func TestVerbProjectEvidenceAndWaive(t *testing.T) {
	e, port := newVerbEngine(t)
	port.lastInfo = ProjectInfo{ID: "alpha", Name: "Alpha",
		ProgressLine: "progress: 0 verified, 1 supported, 1 open, 0 unsupported, 0 waived — next: item two"}

	// .
	if _, err := e.ExecuteAction(context.Background(), "verb", "project", map[string]interface{}{
		"action": "evidence", "project": "alpha", "item": "item one",
		"class": "worker_report_only", "ref": "ws_1", "note": "ran it",
	}); err != nil {
		t.Fatalf("evidence: %v", err)
	}
	if port.evidenceArgs.item != "item one" || port.evidenceArgs.class != "worker_report_only" || port.evidenceArgs.ref != "ws_1" {
		t.Fatalf("evidence args did not reach the port: %+v", port.evidenceArgs)
	}

	// .
	port.evidenceArgs.class = ""
	if _, err := e.ExecuteAction(context.Background(), "verb", "project", map[string]interface{}{
		"action": "evidence", "project": "alpha", "item": "item one", "class": "totally_made_up",
	}); err == nil {
		t.Fatal("unknown evidence class must be refused")
	}
	if port.evidenceArgs.class != "" {
		t.Fatal("a refused evidence action must not reach the port")
	}

	// .
	if _, err := e.ExecuteAction(context.Background(), "verb", "project", map[string]interface{}{
		"action": "evidence", "project": "alpha", "item": "item one", "class": "locally_verified",
	}); err == nil {
		t.Fatal("locally_verified without a ref must be refused")
	}

	// .
	if _, err := e.ExecuteAction(context.Background(), "verb", "project", map[string]interface{}{
		"action": "waive", "project": "alpha", "item": "item two", "reason": "out of scope this cycle",
	}); err != nil {
		t.Fatalf("waive: %v", err)
	}
	if port.waiveArgs.item != "item two" || !strings.Contains(port.waiveArgs.reason, "out of scope") {
		t.Fatalf("waive args did not reach the port: %+v", port.waiveArgs)
	}
}
