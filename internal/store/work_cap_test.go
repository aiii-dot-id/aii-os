package store

import (
	"fmt"
	"path/filepath"
	"testing"
)

func workCapStore(t *testing.T) *Store {
	t.Helper()
	s, err := New(filepath.Join(t.TempDir(), "aii.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// .
// .
// .
// .
// .
func TestWorkSessionsByProjectDeclaresCap(t *testing.T) {
	s := workCapStore(t)
	if err := s.SetActiveProject("proj-x"); err != nil {
		t.Fatal(err)
	}
	// .
	for i := 0; i < 22; i++ {
		id := fmt.Sprintf("s%02d", i)
		if err := s.StartWorkSession(id, "task "+id); err != nil {
			t.Fatal(err)
		}
		if err := s.DeliverWorkSession(id, "served: done "+id, "", ""); err != nil {
			t.Fatal(err)
		}
	}
	got, capped, err := s.WorkSessionsByProject("proj-x", 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 20 {
		t.Fatalf("cap: got %d sessions, want 20", len(got))
	}
	if !capped {
		t.Fatal("capped flag missing — the caller cannot declare what it was not told")
	}
}

// .
// .
// .
// .
func TestWorkSessionsByProjectExactBoundary(t *testing.T) {
	s := workCapStore(t)
	if err := s.SetActiveProject("proj-b"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 20; i++ {
		id := fmt.Sprintf("e%02d", i)
		if err := s.StartWorkSession(id, "task "+id); err != nil {
			t.Fatal(err)
		}
	}
	got, capped, err := s.WorkSessionsByProject("proj-b", 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 20 || capped {
		t.Fatalf("exact cap: got %d sessions, capped=%v — want 20, false (complete record)", len(got), capped)
	}
	// .
	if err := s.StartWorkSession("e20", "task e20"); err != nil {
		t.Fatal(err)
	}
	got, capped, err = s.WorkSessionsByProject("proj-b", 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 20 || !capped {
		t.Fatalf("past cap: got %d sessions, capped=%v — want 20, true (there is more)", len(got), capped)
	}
}
