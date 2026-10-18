package store

import (
	"path/filepath"
	"testing"
)

func TestThePluginCheckVerdictsAreKeptAcrossAReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "aii.db")
	s, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := s.PluginCheckVerdicts(); err != nil || got != nil {
		t.Fatalf("a fresh store holds %q, %v — want nothing, no error", got, err)
	}
	doc := `[{"id":"org.example.checked","package_hash":"sha256:ab","variant":"large","build":"0.1.15 0123456789ab"}]`
	if err := s.SetPluginCheckVerdicts([]byte(doc)); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = New(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if got, err := s.PluginCheckVerdicts(); err != nil || string(got) != doc {
		t.Fatalf("read back %q, %v", got, err)
	}
}
