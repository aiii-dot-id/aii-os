package app

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/ledger"
)

func TestBirthDecisionRetainsSealedIdentityEvidence(t *testing.T) {
	for _, standard := range []bool{false, true} {
		t.Run(map[bool]string{false: "configured", true: "lost-config"}[standard], func(t *testing.T) {
			dir := t.TempDir()
			t.Chdir(dir)
			cfg := &Config{}
			cfg.Identity.LedgerPath = filepath.Join(dir, "configured", "ledger.jsonl")
			cfg.Identity.KeyPath = filepath.Join(dir, "configured", "identity.sec")
			cfg.Identity.DBPath = filepath.Join(dir, "configured", "aii.db")
			a := New(cfg)
			if choice, err := a.chooseBoot(); err != nil || choice != bootFirstboot {
				t.Fatalf("empty container cannot birth: %v %v", choice, err)
			}
			where := filepath.Dir(cfg.Identity.LedgerPath)
			if standard {
				where = filepath.Join(dir, "data")
			}
			if err := os.MkdirAll(where, 0700); err != nil {
				t.Fatal(err)
			}
			segment := filepath.Join(where, "segment-1-4.jsonl.gz")
			if err := os.WriteFile(segment, []byte("surviving record"), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := a.chooseBoot(); !errors.Is(err, ledger.ErrRecordExists) {
				t.Fatalf("sealed identity admitted to FIRSTBOOT: %v", err)
			}
			if body, err := os.ReadFile(segment); err != nil || string(body) != "surviving record" {
				t.Fatalf("birth decision changed surviving record: %q %v", body, err)
			}
		})
	}
}
