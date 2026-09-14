package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// .
// .
// .
// .
// .
// .
func TestChooseBootNeverFirstbootsOverIdentityEvidence(t *testing.T) {
	dir := t.TempDir()
	cfg := &Config{}
	cfg.Identity.LedgerPath = filepath.Join(dir, "data", "ledger.jsonl")
	cfg.Identity.DBPath = filepath.Join(dir, "data", "aii.db")
	cfg.Identity.KeyPath = filepath.Join(dir, "data", "identity.sec")
	a := New(cfg)

	put := func(path, content string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	// .
	if choice, err := a.chooseBoot(); err != nil || choice != bootFirstboot {
		t.Fatalf("blank container: want firstboot, got %v (err %v)", choice, err)
	}

	// .
	put(cfg.Identity.KeyPath, "the-key")
	if _, err := a.chooseBoot(); err == nil {
		t.Fatal("a signing key without a ledger did not refuse — this is the identity fork")
	} else if !strings.Contains(err.Error(), "refusing FIRSTBOOT") {
		t.Fatalf("refusal does not name itself: %v", err)
	}

	// .
	if err := os.Remove(cfg.Identity.KeyPath); err != nil {
		t.Fatal(err)
	}
	put(cfg.Identity.DBPath, "the-projection")
	if _, err := a.chooseBoot(); err == nil {
		t.Fatal("a projection db without a ledger did not refuse")
	}

	// .
	put(cfg.Identity.LedgerPath, "{}")
	if choice, err := a.chooseBoot(); err != nil || choice != bootLive {
		t.Fatalf("ledger present: want live, got %v (err %v)", choice, err)
	}
}

// .
// .
// .
// .
func TestStandardLayoutIsEvidence(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	cfg := &Config{}
	cfg.Identity.LedgerPath = filepath.Join(dir, "vault", "ledger.jsonl")
	cfg.Identity.DBPath = filepath.Join(dir, "vault", "aii.db")
	cfg.Identity.KeyPath = filepath.Join(dir, "vault", "identity.sec")
	a := New(cfg)

	// .
	if choice, err := a.chooseBoot(); err != nil || choice != bootFirstboot {
		t.Fatalf("blank world: want firstboot, got %v (err %v)", choice, err)
	}

	// .
	if err := os.MkdirAll(filepath.Join(dir, "data"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "data", "ledger.jsonl"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := a.chooseBoot(); err == nil {
		t.Fatal("a standard-layout ledger the config lost did not refuse FIRSTBOOT — this is the quarantine fork")
	} else if !strings.Contains(err.Error(), "refusing FIRSTBOOT") {
		t.Fatalf("refusal does not name itself: %v", err)
	}
}
