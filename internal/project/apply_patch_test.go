package project

import (
	"path/filepath"
	"testing"
)

// TestApplyPatchClearsFocus pins §8.6's open half: a pointer to the
// empty string CLEARS the field, a nil pointer leaves it untouched.
// The stringly path could not express the difference, which is why
// the focus note could never be cleared while the UI said "saved" —
// an inertness failure on an operator-authorable surface.
func TestApplyPatchClearsFocus(t *testing.T) {
	root := t.TempDir()
	m := NewManager(root)
	p, err := m.Create("Alpha", "a description", "operator", nil)
	if err != nil {
		t.Fatal(err)
	}

	// Set, then nil keeps it, then a pointer to "" clears it — the
	// whole PATCH contract in three moves.
	set := "the active concern"
	if _, err := m.ApplyPatch(p.ID, nil, nil, &set, nil); err != nil {
		t.Fatalf("set focus: %v", err)
	}
	got, err := m.Load(p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Focus != "the active concern" {
		t.Fatalf("set focus did not write: %q", got.Focus)
	}
	if _, err := m.ApplyPatch(p.ID, nil, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	got, _ = m.Load(p.ID)
	if got.Focus != "the active concern" {
		t.Fatalf("nil patch cleared focus: %q", got.Focus)
	}
	clearFocus := ""
	if _, err := m.ApplyPatch(p.ID, nil, nil, &clearFocus, nil); err != nil {
		t.Fatalf("clear focus: %v", err)
	}
	got, _ = m.Load(p.ID)
	if got.Focus != "" {
		t.Fatalf("focus was not cleared: %q", got.Focus)
	}
	clearDesc := ""
	if _, err := m.ApplyPatch(p.ID, nil, &clearDesc, nil, nil); err != nil {
		t.Fatalf("clear description: %v", err)
	}
	got, _ = m.Load(p.ID)
	if got.Description != "" {
		t.Fatalf("description was not cleared: %q", got.Description)
	}

	// Name is NOT clearable — an unnamed project is an empty-state
	// vocabulary defect on every surface that lists projects.
	empty := ""
	if _, err := m.ApplyPatch(p.ID, &empty, nil, nil, nil); err == nil {
		t.Fatal("empty name was accepted — it must be refused")
	}
	got, err = m.Load(p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "Alpha" {
		t.Fatalf("name was changed despite refusal: %q", got.Name)
	}

	// A set name still updates (regression: the app bridge passes nil
	// for absent, a pointer for present).
	newName := "Beta"
	if _, err := m.ApplyPatch(p.ID, &newName, nil, nil, nil); err != nil {
		t.Fatalf("set name: %v", err)
	}
	got, _ = m.Load(p.ID)
	if got.Name != "Beta" {
		t.Fatalf("name not updated: %q", got.Name)
	}
	_ = filepath.Join(root, p.ID)
}

// TestStringlyUpdateStillWorks pins the identity-tool bridge: the
// stringly Update keeps empty-means-no-change semantics — a resident
// asking to update without naming a field is asking to change nothing.
func TestStringlyUpdateStillWorks(t *testing.T) {
	m := NewManager(t.TempDir())
	p, err := m.Create("Alpha", "a description", "operator", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Update(p.ID, "", "", "", nil); err != nil {
		t.Fatalf("empty update: %v", err)
	}
	got, err := m.Load(p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Focus != "" {
		t.Fatalf("stringly empty update changed focus: %q", got.Focus)
	}
	if got.Name != "Alpha" {
		t.Fatalf("stringly empty update changed name: %q", got.Name)
	}
	// A set value still writes.
	if _, err := m.Update(p.ID, "", "", "new focus", nil); err != nil {
		t.Fatal(err)
	}
	got, _ = m.Load(p.ID)
	if got.Focus != "new focus" {
		t.Fatalf("stringly set did not write: %q", got.Focus)
	}
}
