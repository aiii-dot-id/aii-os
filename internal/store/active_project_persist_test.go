package store

import (
	"path/filepath"
	"testing"
)

// R62 focus persistence: SetActiveProject must survive a close/reopen
// of the store. Before this, activeProject was a bare struct field —
// restart silently dropped the operator into no-project focus while
// they still believed one was chosen (operator report, 2026-08-27:
// "chose Project menu, WORK shows no active session" after a rebuild).
func TestActiveProjectPersistsAcrossReopen(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.db")

	s1, err := New(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if got := s1.ActiveProjectID(); got != "" {
		t.Fatalf("fresh store has focus %q, want empty", got)
	}
	if err := s1.SetActiveProject("proj-menu"); err != nil {
		t.Fatalf("set: %v", err)
	}
	if got := s1.ActiveProjectID(); got != "proj-menu" {
		t.Fatalf("after set: %q, want proj-menu", got)
	}
	if err := s1.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	s2, err := New(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer s2.Close()
	if got := s2.ActiveProjectID(); got != "proj-menu" {
		t.Fatalf("after reopen: %q, want proj-menu — focus was not persisted", got)
	}

	// Clearing focus must also persist: "" is a real state, not absence
	// of state (closing the focused project writes it).
	if err := s2.SetActiveProject(""); err != nil {
		t.Fatalf("clear: %v", err)
	}
	if err := s2.Close(); err != nil {
		t.Fatalf("close2: %v", err)
	}
	s3, err := New(path)
	if err != nil {
		t.Fatalf("reopen2: %v", err)
	}
	defer s3.Close()
	if got := s3.ActiveProjectID(); got != "" {
		t.Fatalf("after clear+reopen: %q, want empty", got)
	}
}
