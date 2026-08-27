package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// FIRSTBOOT mints an identity, so the decision to run it must be
// airtight: a container holding a signing key or a projection db with
// no readable ledger is an EXISTING identity whose record failed to
// resolve, and minting over it forks the resident (Sev 2026-08-26,
// P0). Three outcomes, each proven: a ledger resumes, a blank world
// births, evidence without a ledger refuses.
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

	// A blank container: a true first boot.
	if choice, err := a.chooseBoot(); err != nil || choice != bootFirstboot {
		t.Fatalf("blank container: want firstboot, got %v (err %v)", choice, err)
	}

	// A signing key and no ledger: an existing identity in trouble.
	put(cfg.Identity.KeyPath, "the-key")
	if _, err := a.chooseBoot(); err == nil {
		t.Fatal("a signing key without a ledger did not refuse — this is the identity fork")
	} else if !strings.Contains(err.Error(), "refusing FIRSTBOOT") {
		t.Fatalf("refusal does not name itself: %v", err)
	}

	// Same for a projection db alone.
	if err := os.Remove(cfg.Identity.KeyPath); err != nil {
		t.Fatal(err)
	}
	put(cfg.Identity.DBPath, "the-projection")
	if _, err := a.chooseBoot(); err == nil {
		t.Fatal("a projection db without a ledger did not refuse")
	}

	// The ledger present: resume — regardless of what else exists.
	put(cfg.Identity.LedgerPath, "{}")
	if choice, err := a.chooseBoot(); err != nil || choice != bootLive {
		t.Fatalf("ledger present: want live, got %v (err %v)", choice, err)
	}
}

// The standard layout is evidence in its own right: when the config
// lost its identity paths (mobile quarantine, hand damage) the files
// at data/ still prove a resident lives here — FIRSTBOOT must refuse,
// not build over them (D02 residual, Sev 2026-08-26).
func TestStandardLayoutIsEvidence(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	cfg := &Config{}
	cfg.Identity.LedgerPath = filepath.Join(dir, "vault", "ledger.jsonl")
	cfg.Identity.DBPath = filepath.Join(dir, "vault", "aii.db")
	cfg.Identity.KeyPath = filepath.Join(dir, "vault", "identity.sec")
	a := New(cfg)

	// Nothing anywhere: a true first boot even with lost paths.
	if choice, err := a.chooseBoot(); err != nil || choice != bootFirstboot {
		t.Fatalf("blank world: want firstboot, got %v (err %v)", choice, err)
	}

	// A ledger at the standard layout, unknown to the config: refuse.
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
