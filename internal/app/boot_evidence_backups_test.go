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
// .
func TestALedgerBackupIsIdentityEvidence(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	// .
	cfg := &Config{}
	cfg.Identity.LedgerPath = filepath.Join("data", "ledger.jsonl")
	cfg.Identity.DBPath = filepath.Join("data", "aii.db")
	cfg.Identity.KeyPath = filepath.Join("data", "identity.sec")
	a := New(cfg)

	backups := filepath.Join(dir, "data", "backups")
	if err := os.MkdirAll(backups, 0o700); err != nil {
		t.Fatal(err)
	}
	// .
	// .
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
	// .
	// .
	if !strings.Contains(err.Error(), backup) {
		t.Fatalf("the refusal does not name the backup to recover from: %v", err)
	}

	// .
	// .
	if err := os.Remove(filepath.Join(backups, backup)); err != nil {
		t.Fatal(err)
	}
	const snapshot = "ledger-20260903T040000Z-seq413"
	if err := os.MkdirAll(filepath.Join(backups, snapshot), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(backups, snapshot, "ledger.jsonl"), []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err = a.chooseBoot()
	if err == nil {
		t.Fatal("a container holding a snapshot firstbooted — a second identity was minted over a recoverable one")
	}
	if !strings.Contains(err.Error(), snapshot) {
		t.Fatalf("the refusal does not name the snapshot to recover from: %v", err)
	}
}

// .
// .
// .
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
