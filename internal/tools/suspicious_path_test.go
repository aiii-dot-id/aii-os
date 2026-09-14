package tools

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// .
// .
// .
// .
// .
// .
// .
func TestSuspiciousPathCounter(t *testing.T) {
	dir := t.TempDir()
	r := NewRegistry(dir, nil, Timeouts{})
	realFile := filepath.Join(dir, "replay.go")
	os.WriteFile(realFile, []byte("package main"), 0644)

	// .
	// .
	if _, err := r.Execute(context.Background(), "read", map[string]interface{}{
		"file_path": filepath.Join(dir, "replay.py"),
	}); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if got := r.SuspiciousPathCount(); got != 1 {
		t.Fatalf("corrupted read not counted: got %d, want 1", got)
	}

	// .
	if _, err := r.Execute(context.Background(), "read", map[string]interface{}{
		"file_path": realFile,
	}); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if got := r.SuspiciousPathCount(); got != 1 {
		t.Fatalf("legitimate read counted as corruption: got %d, want 1", got)
	}

	// .
	// .
	if _, err := r.Execute(context.Background(), "write", map[string]interface{}{
		"file_path": filepath.Join(dir, "newdir", "fresh.txt"),
		"content":   "x",
	}); err != nil {
		t.Fatalf("execute write: %v", err)
	}
	if got := r.SuspiciousPathCount(); got != 1 {
		t.Fatalf("write-family miss counted as corruption: got %d, want 1", got)
	}

	// .
	// .
	if _, err := r.Execute(context.Background(), "read", map[string]interface{}{
		"file_path": "/etc/passwd",
	}); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if got := r.SuspiciousPathCount(); got != 1 {
		t.Fatalf("denied read counted as corruption: got %d, want 1", got)
	}

	// .
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
