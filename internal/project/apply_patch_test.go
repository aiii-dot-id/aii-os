package project

import (
	"path/filepath"
	"testing"
)

// .
// .
// .
// .
// .
func TestApplyPatchClearsFocus(t *testing.T) {
	root := t.TempDir()
	m := NewManager(root)
	p, err := m.Create("Alpha", "a description", "operator", nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	// .
	// .
	set := "the active concern"
	if _, err := m.ApplyPatch(p.ID, nil, nil, &set, nil, nil, nil); err != nil {
		t.Fatalf("set focus: %v", err)
	}
	got, err := m.Load(p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Focus != "the active concern" {
		t.Fatalf("set focus did not write: %q", got.Focus)
	}
	if _, err := m.ApplyPatch(p.ID, nil, nil, nil, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	got, _ = m.Load(p.ID)
	if got.Focus != "the active concern" {
		t.Fatalf("nil patch cleared focus: %q", got.Focus)
	}
	clearFocus := ""
	if _, err := m.ApplyPatch(p.ID, nil, nil, &clearFocus, nil, nil, nil); err != nil {
		t.Fatalf("clear focus: %v", err)
	}
	got, _ = m.Load(p.ID)
	if got.Focus != "" {
		t.Fatalf("focus was not cleared: %q", got.Focus)
	}
	clearDesc := ""
	if _, err := m.ApplyPatch(p.ID, nil, &clearDesc, nil, nil, nil, nil); err != nil {
		t.Fatalf("clear description: %v", err)
	}
	got, _ = m.Load(p.ID)
	if got.Description != "" {
		t.Fatalf("description was not cleared: %q", got.Description)
	}

	// .
	// .
	empty := ""
	if _, err := m.ApplyPatch(p.ID, &empty, nil, nil, nil, nil, nil); err == nil {
		t.Fatal("empty name was accepted — it must be refused")
	}
	got, err = m.Load(p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "Alpha" {
		t.Fatalf("name was changed despite refusal: %q", got.Name)
	}

	// .
	// .
	newName := "Beta"
	if _, err := m.ApplyPatch(p.ID, &newName, nil, nil, nil, nil, nil); err != nil {
		t.Fatalf("set name: %v", err)
	}
	got, _ = m.Load(p.ID)
	if got.Name != "Beta" {
		t.Fatalf("name not updated: %q", got.Name)
	}
	_ = filepath.Join(root, p.ID)
}

// .
// .
// .
func TestStringlyUpdateStillWorks(t *testing.T) {
	m := NewManager(t.TempDir())
	p, err := m.Create("Alpha", "a description", "operator", nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Update(p.ID, "", "", "", nil, nil, nil); err != nil {
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
	// .
	if _, err := m.Update(p.ID, "", "", "new focus", nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	got, _ = m.Load(p.ID)
	if got.Focus != "new focus" {
		t.Fatalf("stringly set did not write: %q", got.Focus)
	}
}
