package tools

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// TestSuspiciousPathCounter pins the P3 corrupted-content extension: a
// read-family call whose resolved target does not exist increments the
// engine-side corruption counter, while existing targets, write-family
// calls, and gate-denied calls do not. The observed instances this
// counter makes visible (2026-08-21/22 forensics): replay.py read where
// replay.go exists, ioi-os where aii-os exists, truncated paths — every
// one schema-valid, fluent, and pointing at nothing.
func TestSuspiciousPathCounter(t *testing.T) {
	dir := t.TempDir()
	r := NewRegistry(dir, nil, Timeouts{})
	realFile := filepath.Join(dir, "replay.go")
	os.WriteFile(realFile, []byte("package main"), 0644)

	// The corrupted-content signature: read of a sibling that doesn't
	// exist while the real file does — replay.py for replay.go.
	if _, err := r.Execute(context.Background(), "read", map[string]interface{}{
		"file_path": filepath.Join(dir, "replay.py"),
	}); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if got := r.SuspiciousPathCount(); got != 1 {
		t.Fatalf("corrupted read not counted: got %d, want 1", got)
	}

	// Reading the real file is not corruption.
	if _, err := r.Execute(context.Background(), "read", map[string]interface{}{
		"file_path": realFile,
	}); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if got := r.SuspiciousPathCount(); got != 1 {
		t.Fatalf("legitimate read counted as corruption: got %d, want 1", got)
	}

	// Writing to a not-yet-existing path is legitimate workflow — the
	// write family is excluded by design.
	if _, err := r.Execute(context.Background(), "write", map[string]interface{}{
		"file_path": filepath.Join(dir, "newdir", "fresh.txt"),
		"content":   "x",
	}); err != nil {
		t.Fatalf("execute write: %v", err)
	}
	if got := r.SuspiciousPathCount(); got != 1 {
		t.Fatalf("write-family miss counted as corruption: got %d, want 1", got)
	}

	// Gate denials are not corruption. A read outside the sandbox is
	// denied by the sandbox check before telemetry runs.
	if _, err := r.Execute(context.Background(), "read", map[string]interface{}{
		"file_path": "/etc/passwd",
	}); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if got := r.SuspiciousPathCount(); got != 1 {
		t.Fatalf("denied read counted as corruption: got %d, want 1", got)
	}

	// grep/ls share the family: a grep of a nonexistent dir counts.
	if _, err := r.Execute(context.Background(), "grep", map[string]interface{}{
		"pattern": "x",
		"path":    filepath.Join(dir, "ioi-os"),
	}); err != nil {
		t.Fatalf("execute grep: %v", err)
	}
	if got := r.SuspiciousPathCount(); got != 2 {
		t.Fatalf("grep miss not counted: got %d, want 2", got)
	}
}
