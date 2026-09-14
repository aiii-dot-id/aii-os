package store

import (
	"path/filepath"
	"testing"
)

// .
// .
// .
// .
// .
// .
func TestAnOlderDatabaseSurvivesANewerBinary(t *testing.T) {
	path := filepath.Join(t.TempDir(), "aii.db")

	// .
	// .
	s, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB().Exec(
		`INSERT INTO conversations (id, session_id, role, content, turn_seq, created_at)
		 VALUES ('turn_1','default','operator','remember this exact phrase',1,'2026-08-28')`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB().Exec(
		`INSERT INTO work_sessions (id, description, status) VALUES ('ws1','find the plans','active')`); err != nil {
		t.Fatal(err)
	}
	// .
	for _, col := range []string{"focus", "next_move", "plan"} {
		if _, err := s.DB().Exec(`ALTER TABLE work_sessions DROP COLUMN ` + col); err != nil {
			t.Fatalf("aging the database (%s): %v", col, err)
		}
	}
	s.Close()

	// .
	s2, err := New(path)
	if err != nil {
		t.Fatalf("an older database did not survive the newer binary: %v", err)
	}
	defer s2.Close()

	// .
	// .
	var content string
	if err := s2.DB().QueryRow(
		`SELECT content FROM conversations WHERE id='turn_1'`).Scan(&content); err != nil {
		t.Fatalf("the conversation did not survive the upgrade: %v", err)
	}
	if content != "remember this exact phrase" {
		t.Fatalf("conversation content changed: %q", content)
	}

	// .
	var desc, focus string
	if err := s2.DB().QueryRow(
		`SELECT description, focus FROM work_sessions WHERE id='ws1'`).Scan(&desc, &focus); err != nil {
		t.Fatalf("the work session did not survive the upgrade: %v", err)
	}
	if desc != "find the plans" {
		t.Fatalf("work session content changed: %q", desc)
	}
	if focus != "" {
		t.Fatalf("the added column should hold its declared DEFAULT, got %q", focus)
	}
}
