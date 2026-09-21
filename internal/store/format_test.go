package store

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/store/compressvfs"
)

func TestDatabaseFormatReadsActualStore(t *testing.T) {
	dir := t.TempDir()
	plain := filepath.Join(dir, "source.db")
	s, err := New(plain)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := s.DatabaseFormat(t.Context()); err != nil || got != compressvfs.FormatSQLite {
		t.Fatalf("registering the compression VFS must not claim activation: %q %v", got, err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	compressed := filepath.Join(dir, "converted.db")
	if published, err := ConvertDatabase(t.Context(), plain, compressed, true); err != nil || !published {
		t.Fatalf("convert: %v %v", published, err)
	}
	for _, tc := range []struct{ path, format string }{{plain, compressvfs.FormatSQLite}, {compressed, compressvfs.FormatZstd}} {
		ro, err := OpenReadOnly(tc.path)
		if err != nil {
			t.Fatal(err)
		}
		if got, err := ro.DatabaseFormat(t.Context()); err != nil || got != tc.format {
			t.Fatalf("actual format: %q %v", got, err)
		}
		if err := ro.Close(); err != nil {
			t.Fatal(err)
		}
		if _, err := ro.DatabaseFormat(t.Context()); err == nil {
			t.Fatal("closed store returned a stale format")
		}
	}
}

func TestDatabaseFormatDoesNotInventPersistentFormat(t *testing.T) {
	s, err := NewMemory()
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err := s.DatabaseFormat(t.Context()); err == nil {
		t.Fatal("memory store claimed a persistent format")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := s.DatabaseFormat(ctx); err == nil {
		t.Fatal("cancelled read reported success")
	}
}
