package store

import (
	"crypto/sha256"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// .
// .
// .
func TestRestoreProofKeepsCommittedWALInBothFormats(t *testing.T) {
	for _, format := range []string{"sqlite", "zstd"} {
		t.Run(format, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "live.db")
			s, err := New(path)
			if err != nil {
				t.Fatal(err)
			}
			result, err := s.ActivateFormatAtStartup(t.Context(), path, format)
			if err != nil {
				s.Close()
				t.Fatal(err)
			}
			s = result.Opened
			defer s.Close()
			s.DB().SetMaxOpenConns(1)
			if _, err := s.DB().Exec("PRAGMA wal_autocheckpoint=0; PRAGMA wal_checkpoint(TRUNCATE)"); err != nil {
				t.Fatal(err)
			}
			addTurns(t, s, 7)
			set := t.TempDir()
			copyPath := filepath.Join(set, "aii.db")
			before := map[string][32]byte{}
			// .
			// .
			for _, suffix := range []string{"", "-wal"} {
				if err := copyFileSynced(path+suffix, copyPath+suffix); err != nil {
					t.Fatal(err)
				}
				b, err := os.ReadFile(copyPath + suffix)
				if err != nil {
					t.Fatal(err)
				}
				if len(b) == 0 {
					t.Fatal("empty fixture member: " + suffix)
				}
				before[suffix] = sha256.Sum256(b)
			}
			record := filepath.Join(set, "ledger.jsonl")
			if _, _, err := InspectCopy(t.Context(), copyPath); err == nil {
				t.Fatal("immutable inspection silently ignored the committed WAL")
			}
			// .
			if err := os.WriteFile(record, nil, 0600); err != nil {
				t.Fatal(err)
			}
			control := filepath.Join(t.TempDir(), "main-only.db")
			if err := copyFileSynced(copyPath, control); err != nil {
				t.Fatal(err)
			}
			_, empty, err := ProveRestore(t.Context(), control, record, filepath.Join(t.TempDir(), "control"))
			if err != nil || empty.Ephemeral["conversations"] != 0 {
				t.Fatalf("fixture was already checkpointed: %+v %v", empty, err)
			}
			_, facts, err := ProveRestore(t.Context(), copyPath, record, filepath.Join(t.TempDir(), "proof"))
			if err != nil {
				t.Fatal(err)
			}
			if facts.Ephemeral["conversations"] != 7 || facts.LastTurnSeq != 7 {
				t.Fatalf("restore proof lost committed WAL conversations: %+v", facts)
			}
			for suffix, want := range before {
				b, err := os.ReadFile(copyPath + suffix)
				if err != nil || sha256.Sum256(b) != want {
					t.Fatalf("proof changed its source %s: %v", suffix, err)
				}
			}
		})
	}
}

func TestInspectCopyReadsClosedImagesInBothFormats(t *testing.T) {
	for _, format := range []string{"sqlite", "zstd"} {
		t.Run(format, func(t *testing.T) {
			name := "identity #?%.db"
			if runtime.GOOS == "windows" {
				name = "identity #%.db"
			}
			path := filepath.Join(t.TempDir(), name)
			s, err := New(path)
			if err != nil {
				t.Fatal(err)
			}
			result, err := s.ActivateFormatAtStartup(t.Context(), path, format)
			if err != nil {
				s.Close()
				t.Fatal(err)
			}
			s = result.Opened
			addTurns(t, s, 3)
			if err := s.Close(); err != nil {
				t.Fatal(err)
			}
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			_, facts, err := InspectCopy(t.Context(), path)
			if err != nil || facts.LastTurnSeq != 3 {
				t.Fatalf("inspection: %+v %v", facts, err)
			}
			after, err := os.ReadFile(path)
			if err != nil || sha256.Sum256(before) != sha256.Sum256(after) {
				t.Fatalf("inspection wrote its source: %v", err)
			}
		})
	}
}
