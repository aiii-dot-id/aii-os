package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/continuity"
	"github.com/aiii-dot-id/aii-os/internal/genesis"
	"github.com/aiii-dot-id/aii-os/internal/genesis/genesistest"
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

// A CONTAINER THAT CANNOT BE INSPECTED IS NOT BLANK. The evidence probes
// read every error as "absent", so a backups directory that could not be
// listed let FIRSTBOOT mint a second identity over a recoverable one: the
// fork the refusal exists to prevent. A probe that cannot answer refuses
// FIRSTBOOT and names what it could not inspect.
func TestAContainerThatCannotBeInspectedIsNotBlank(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	cfg := &Config{}
	cfg.Identity.LedgerPath = filepath.Join("data", "ledger.jsonl")
	cfg.Identity.DBPath = filepath.Join("data", "aii.db")
	cfg.Identity.KeyPath = filepath.Join("data", "identity.sec")
	a := New(cfg)
	// data/backups is there and cannot be listed (unlistable: a file where
	// the directory belongs on unix, a denied listing on Windows).
	if err := os.MkdirAll(filepath.Join(dir, "data"), 0o700); err != nil {
		t.Fatal(err)
	}
	backups := filepath.Join(dir, "data", "backups")
	unlistable(t, backups)
	_, err := a.chooseBoot()
	if err == nil {
		t.Fatal("FIRSTBOOT built on a container it could not inspect")
	}
	if !strings.Contains(err.Error(), "refusing FIRSTBOOT") || !strings.Contains(err.Error(), "backups") {
		t.Fatalf("the refusal does not name what it could not inspect: %v", err)
	}
	// Control: gone, the container is blank, and FIRSTBOOT may build.
	if err := os.Remove(backups); err != nil {
		t.Fatal(err)
	}
	if choice, err := a.chooseBoot(); err != nil || choice != bootFirstboot {
		t.Fatalf("a blank container did not firstboot: %v (err %v)", choice, err)
	}
}

// And the boot does it: a sweep nobody calls protects nothing.
func TestANormalBootSweepsACrashedPassesWorkingSet(t *testing.T) {
	dir := t.TempDir()
	result := genesistest.NewRoot(t).Birth(t, genesis.BirthConfig{
		Name:       "SweepAtBoot",
		KeyPath:    filepath.Join(dir, "identity.sec"),
		LedgerPath: filepath.Join(dir, "ledger.jsonl"),
		DBPath:     filepath.Join(dir, "aii.db"),
	})
	result.Ledger.Close()
	cfgPath := filepath.Join(dir, "config.json")
	cfg := defaultConfig()
	cfg.Identity = IdentityConfig{KeyPath: filepath.Join(dir, "identity.sec"),
		LedgerPath: filepath.Join(dir, "ledger.jsonl"), DBPath: filepath.Join(dir, "aii.db"),
	}
	cfg.LLM = withTestProvider(t, dir, "test", "https://x", "m", "sk-x")
	cfg.Dashboard.Port = 0
	cfg.Tools.CWD = dir
	cfg.SourcePath = cfgPath
	b, _ := json.Marshal(cfg)
	if err := os.WriteFile(cfgPath, b, 0o644); err != nil {
		t.Fatal(err)
	}
	app := New(cfg)
	backups := app.backupsDir(*cfg)
	work := filepath.Join(backups, continuity.SnapshotWorkPrefix+"42.tmp")
	if err := os.MkdirAll(work, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(work, "aii.db"), []byte("the identity, in the clear"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := startLiveForTest(app); err != nil {
		t.Fatalf("startLive: %v", err)
	}
	defer stopApp(t, app)
	if _, err := os.Stat(work); !os.IsNotExist(err) {
		t.Fatalf("A NORMAL BOOT LEFT A CRASHED PASS'S WORKING SET IN %s: the identity in the clear, in the directory that leaves the host", backups)
	}

	// First boot: the working set is evidence, like a published snapshot
	// (continuity's half: TestACrashedPassesWorkingSetIsSweptAtANormalBootAndOnlyThere).
	fresh := t.TempDir()
	if got, err := aBackupFile(fresh); got != "" || err != nil {
		t.Fatalf("an empty directory is evidence of %q (%v)", got, err)
	}
	if err := os.MkdirAll(filepath.Join(fresh, continuity.SnapshotWorkPrefix+"123456789.tmp"), 0o700); err != nil {
		t.Fatal(err)
	}
	if got, _ := aBackupFile(fresh); !strings.Contains(got, continuity.SnapshotWorkPrefix) {
		t.Fatalf("a crashed pass's working set — a previous resident's whole record — is not evidence at first boot (got %q)", got)
	}
}
