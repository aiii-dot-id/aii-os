package store

import (
	"context"
	"database/sql"
	"fmt"
	"math/rand/v2"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/store/compressvfs"
)

func TestLearnedDatabaseMaintenanceExportAndRestore(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "identity.db")
	s, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { s.Close() }()
	activated, err := s.ActivateFormatAtStartup(ctx, path, "zstd")
	if err != nil {
		t.Fatal(err)
	}
	s = activated.Opened
	rng := rand.New(rand.NewPCG(73, 91))
	shared := make([]byte, 3000)
	for i := range shared {
		shared[i] = byte(33 + rng.IntN(90))
	}
	var first string
	for row := 0; row < 256; row++ {
		b := make([]byte, 4000)
		copy(b, shared)
		for i := len(shared); i < len(b); i++ {
			b[i] = byte(33 + rng.IntN(90))
		}
		if row == 0 {
			first = string(b)
		}
		if _, err := s.DB().Exec("INSERT INTO plugin_kv(plugin_id,key,value,updated_at) VALUES('optimization',?,?,?)", fmt.Sprint(row), string(b), "2026-09-21T00:00:00Z"); err != nil {
			t.Fatal(err)
		}
	}
	note, err := s.HousekeepOptimized(ctx, CompressionOptimization{Levels: true, Dictionary: true}, 0)
	if err != nil {
		t.Fatal(err)
	}
	size, err := s.CompressionDictionaryBytes(ctx)
	if err != nil || size == 0 || !strings.Contains(note, "compressed-container reclaimed") {
		t.Fatalf("no actual learned dictionary: %d %v %s", size, err, note)
	}
	if err := s.QuickCheck(); err != nil {
		t.Fatal(err)
	}
	if err := s.ForeignKeyCheck(); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB().Exec("INSERT INTO plugin_kv(plugin_id,key,value,updated_at) VALUES('optimization','after','after learning','2026-09-21T00:00:00Z')"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Housekeep(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = New(path)
	if err != nil {
		t.Fatal(err)
	}
	var got string
	if err := s.DB().QueryRow("SELECT value FROM plugin_kv WHERE plugin_id='optimization' AND key='0'").Scan(&got); err != nil || got != first {
		t.Fatalf("reopen: %v", err)
	}
	exported := filepath.Join(t.TempDir(), "ordinary.db")
	if published, err := ConvertDatabase(ctx, path, exported, false); err != nil || !published {
		t.Fatalf("export: %v %v", published, err)
	}
	plain, err := sql.Open("sqlite", exported)
	if err != nil {
		t.Fatal(err)
	}
	defer plain.Close()
	if err := plain.QueryRow("SELECT value FROM plugin_kv WHERE plugin_id='optimization' AND key='after'").Scan(&got); err != nil || got != "after learning" {
		t.Fatalf("ordinary reader: %q %v", got, err)
	}
	activated, err = s.ActivateFormatAtStartup(ctx, path, "sqlite")
	if err != nil {
		t.Fatal(err)
	}
	s = activated.Opened
	if size, err := s.CompressionDictionaryBytes(ctx); err != nil || size != 0 {
		t.Fatalf("dictionary survived ordinary conversion: %d %v", size, err)
	}
}

func TestCompressionOptimizationRefusesInvalidModesAndReadOnly(t *testing.T) {
	path := filepath.Join(t.TempDir(), "identity.db")
	s, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"-1", "4", "true", "bad"} {
		if _, err := s.DB().Exec("PRAGMA " + compressvfs.CompactPragma + "='" + mode + "'"); err == nil {
			t.Fatalf("invalid mode accepted: %s", mode)
		}
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	ro, err := OpenReadOnly(path)
	if err != nil {
		t.Fatal(err)
	}
	defer ro.Close()
	if _, err := ro.DB().Exec("PRAGMA " + compressvfs.CompactPragma + "=3"); err == nil {
		t.Fatal("read-only maintenance accepted")
	}
	if _, err := ro.CompressionDictionaryBytes(context.Background()); err != nil {
		t.Fatal(err)
	}
}
