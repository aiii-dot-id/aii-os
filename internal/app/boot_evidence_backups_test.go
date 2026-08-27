package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The maintenance pass keeps up to eight FULL ledger copies under
// data/backups/, so a partial reset — the three obvious files deleted,
// or a restore that stopped halfway — leaves the previous resident's
// ENTIRE record in the container with no ledger, key or db in sight.
// The evidence rule predates that layer: it read a blank world, and
// FIRSTBOOT minted a new identity on top of a recoverable one, which is
// precisely the fork the refusal exists to prevent (Beta journey #3).
func TestALedgerBackupIsIdentityEvidence(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	// The default layout: relative paths, like a real install.
	cfg := &Config{}
	cfg.Identity.LedgerPath = filepath.Join("data", "ledger.jsonl")
	cfg.Identity.DBPath = filepath.Join("data", "aii.db")
	cfg.Identity.KeyPath = filepath.Join("data", "identity.sec")
	a := New(cfg)

	backups := filepath.Join(dir, "data", "backups")
	if err := os.MkdirAll(backups, 0o700); err != nil {
		t.Fatal(err)
	}
	// The directory alone proves nothing — maintenance creates it before
	// the first copy is published.
	if choice, err := a.chooseBoot(); err != nil || choice != bootFirstboot {
		t.Fatalf("an empty backups directory was read as identity evidence: %v (err %v)", choice, err)
	}

	const backup = "ledger-20260826T040000Z-seq412.jsonl"
	if err := os.WriteFile(filepath.Join(backups, backup), []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := a.chooseBoot()
	if err == nil {
		t.Fatal("a container holding a full ledger backup firstbooted — a second identity was minted over a recoverable one")
	}
	if !strings.Contains(err.Error(), "refusing FIRSTBOOT") {
		t.Fatalf("refusal does not name itself: %v", err)
	}
	// Recovery is the operator's act, so the refusal has to say WHAT was
	// found and WHERE — otherwise it is a dead end, not a recovery.
	if !strings.Contains(err.Error(), backup) {
		t.Fatalf("the refusal does not name the backup to recover from: %v", err)
	}
}

// The control that keeps the fix honest: a genuinely blank container is
// still born. An evidence rule that refuses everything protects nobody
// and breaks every new user's first run.
func TestABlankContainerStillFirstboots(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	cfg := &Config{}
	cfg.Identity.LedgerPath = filepath.Join("data", "ledger.jsonl")
	cfg.Identity.DBPath = filepath.Join("data", "aii.db")
	cfg.Identity.KeyPath = filepath.Join("data", "identity.sec")

	if choice, err := New(cfg).chooseBoot(); err != nil || choice != bootFirstboot {
		t.Fatalf("a blank container was refused its first boot: %v (err %v)", choice, err)
	}
}
