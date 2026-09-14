package store

import (
	"path/filepath"
	"testing"
)

// .
// .
// .
func TestOperatorASCIIFoldIsOptInAndOperatorOnly(t *testing.T) {
	s, err := New(filepath.Join(t.TempDir(), "aii.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	text := "done \u2014 see \u00a76 \u2192 \u201cnext\u201d\u2026"
	read := func(id string) string {
		var c string
		if err := s.db.QueryRow(`SELECT content FROM outbox WHERE id = ?`, id).Scan(&c); err != nil {
			t.Fatal(err)
		}
		return c
	}
	if err := s.AddOutboxMessage("off", "operator", "", text, nil); err != nil {
		t.Fatal(err)
	}
	if got := read("off"); got != text {
		t.Fatalf("fold applied while off: %q", got)
	}
	s.SetOperatorASCII(true)
	if err := s.AddOutboxMessage("on", "operator", "", text, nil); err != nil {
		t.Fatal(err)
	}
	if got, want := read("on"), `done - see S.6 -> "next"...`; got != want {
		t.Fatalf("fold: got %q want %q", got, want)
	}
	if err := s.AddOutboxMessage("peer", "peer", "ivy", text, nil); err != nil {
		t.Fatal(err)
	}
	if got := read("peer"); got != text {
		t.Fatalf("fold applied to a non-operator message: %q", got)
	}
}
