package store

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// NewForTest is worth having only if what it returns is what New returns: the
// same database, byte for byte once closed, mounted with the schema audited, and
// usable for a write and a reopen by New.
func TestNewForTestMakesWhatNewMakes(t *testing.T) {
	dir := t.TempDir()
	viaNew := filepath.Join(dir, "new", "aii.db")
	viaCopy := filepath.Join(dir, "copy", "aii.db")
	a, err := New(viaNew)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	b, err := NewForTest(viaCopy)
	if err != nil {
		t.Fatal(err)
	}
	if err := b.auditSchema(); err != nil {
		t.Fatalf("a store made from the fresh image fails the schema audit: %v", err)
	}
	if err := b.Close(); err != nil {
		t.Fatal(err)
	}
	want, err := os.ReadFile(viaNew)
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(viaCopy)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(want, got) {
		t.Fatalf("the fresh image (%d bytes) is not what New makes (%d bytes): a test store would not be the store a person gets", len(got), len(want))
	}
}

// A database already at the path is New's to open — its schema reconciled to
// this binary's, not merely audited: one that lost an index gets it back — and
// is never overwritten by a copy.
func TestNewForTestOpensADatabaseThatIsThere(t *testing.T) {
	const index = "idx_conversations_turn_seq"
	path := filepath.Join(t.TempDir(), "aii.db")
	s, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := setRuntimeMeta(s.w(), "zz_marker", "kept"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.w().Exec("DROP INDEX " + index); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = NewForTest(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if got, err := s.getRuntimeMeta("zz_marker"); err != nil || got != "kept" {
		t.Fatalf("NewForTest replaced the database that was there: %q, %v", got, err)
	}
	var n int
	if err := s.h().QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE name = ?", index).Scan(&n); err != nil || n != 1 {
		t.Fatalf("NewForTest did not open the existing database as New does: %s is missing (%d, %v)", index, n, err)
	}
}

// Seeding never writes over a database: a test that starts a host on a data
// directory that already holds a projection keeps it.
func TestSeedForTestLeavesADatabaseThatIsThere(t *testing.T) {
	path := filepath.Join(t.TempDir(), "aii.db")
	if err := os.WriteFile(path, []byte("not the fresh image"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := SeedForTest(path); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(path); err != nil || string(got) != "not the fresh image" {
		t.Fatalf("SeedForTest replaced what was there: %q, %v", got, err)
	}
}
