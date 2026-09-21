package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/escrow"
	"github.com/aiii-dot-id/aii-os/internal/genesis"
	"github.com/aiii-dot-id/aii-os/internal/genesis/genesistest"
)

func mkPlainSnapshot(t *testing.T, dir, name string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, name), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name, snapshotSums), []byte("x  y\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func dirNames(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range entries {
		out = append(out, e.Name())
	}
	return out
}

// .
// .
// .
// .
func TestPruneNeverRemovesTheSnapshotThePassJustMade(t *testing.T) {
	dir := t.TempDir()
	for _, day := range []string{"01", "02", "03", "04", "05", "06", "07", "08"} {
		mkPlainSnapshot(t, dir, "ledger-202609"+day+"T040000Z-seq100")
	}
	// .
	tonight := "ledger-20260920T040000Z-seq50"
	mkPlainSnapshot(t, dir, tonight)

	if removed := pruneBackups(dir, 8); removed != 1 {
		t.Fatalf("pruned %d, want 1", removed)
	}
	left := dirNames(t, dir)
	if !holdsName(left, tonight) {
		t.Fatalf("THE SNAPSHOT THE PASS JUST PUBLISHED WAS PRUNED: %v", left)
	}
	if holdsName(left, "ledger-20260901T040000Z-seq100") {
		t.Fatalf("the oldest snapshot by its stamp was kept: %v", left)
	}

	// .
	// .
	one := t.TempDir()
	mkPlainSnapshot(t, one, "ledger-20260901T040000Z-seq100")
	newest := "ledger-20260920T040000Z-seq50" + escrow.SnapshotSuffix
	if err := os.WriteFile(filepath.Join(one, newest), []byte("c"), 0o600); err != nil {
		t.Fatal(err)
	}
	if removed := pruneBackups(one, 1); removed != 1 {
		t.Fatalf("pruned %d, want 1", removed)
	}
	if left := dirNames(t, one); len(left) != 1 || left[0] != newest {
		t.Fatalf("THE NEWEST COMPLETE SNAPSHOT WAS PRUNED: %v survived, want %s", left, newest)
	}

	// .
	odd := t.TempDir()
	mkPlainSnapshot(t, odd, "ledger-ZZZZ-seq1")
	mkPlainSnapshot(t, odd, "ledger-20260920T040000Z-seq2")
	if removed := pruneBackups(odd, 1); removed != 0 {
		t.Fatalf("pruned %d with one snapshot of ours in the set, want 0: %v", removed, dirNames(t, odd))
	}
}

func holdsName(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

// .
// .
// .
// .
// .
func TestACrashedPassesWorkingSetIsSweptAtANormalBootAndOnlyThere(t *testing.T) {
	plant := func(dir string) {
		t.Helper()
		work := filepath.Join(dir, snapshotWorkPrefix+"123456789.tmp")
		if err := os.MkdirAll(filepath.Join(work, "segments"), 0o700); err != nil {
			t.Fatal(err)
		}
		for _, f := range []string{"aii.db", "ledger.jsonl", filepath.Join("segments", "segment-1-9.jsonl.gz")} {
			if err := os.WriteFile(filepath.Join(work, f), []byte("the identity, in the clear"), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		if err := os.WriteFile(filepath.Join(dir, snapshotWorkPrefix+"987654321"+escrow.SnapshotSuffix+".tmp"), []byte("half a ciphertext"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	a, _, _ := maintApp(t)
	cfg := a.configSnapshot()
	dir := a.backupsDir(cfg)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	mkPlainSnapshot(t, dir, "ledger-20260919T040000Z-seq7")
	if err := os.WriteFile(filepath.Join(dir, "operators-own-notes.txt"), []byte("mine"), 0o600); err != nil {
		t.Fatal(err)
	}
	plant(dir)
	a.sweepSnapshotDebrisAtBoot(cfg)
	left := strings.Join(dirNames(t, dir), ",")
	if strings.Contains(left, snapshotWorkPrefix) {
		t.Fatalf("after a normal boot the working set still stands in %s: %s", dir, left)
	}
	if left != "ledger-20260919T040000Z-seq7,operators-own-notes.txt" {
		t.Fatalf("the sweep touched what is not a pass's working set: %s", left)
	}

	// .
	safe, _, _ := maintApp(t)
	safeCfg := safe.configSnapshot()
	safeDir := safe.backupsDir(safeCfg)
	if err := os.MkdirAll(safeDir, 0o700); err != nil {
		t.Fatal(err)
	}
	plant(safeDir)
	safe.enterSafe("test: SAFE removes nothing")
	safe.sweepSnapshotDebrisAtBoot(safeCfg)
	if got := dirNames(t, safeDir); len(got) != 2 {
		t.Fatalf("SAFE removed a working set that may be the last copy of a record: %v", got)
	}

	// .
	fresh := t.TempDir()
	if got := aBackupFile(fresh); got != "" {
		t.Fatalf("an empty directory is evidence of %q", got)
	}
	plant(fresh)
	if got := aBackupFile(fresh); !strings.Contains(got, snapshotWorkPrefix) {
		t.Fatalf("a crashed pass's working set — a previous resident's whole record — is not evidence at first boot (got %q)", got)
	}

	// .
	if n, err := sweepSnapshotDebris(filepath.Join(t.TempDir(), "absent")); n != 0 || err != nil {
		t.Fatalf("an absent directory: removed %d, err %v", n, err)
	}
}

// .
// .
// .
// .
func TestThePlaintextSetIsSyncedBeforeItIsPublished(t *testing.T) {
	a, _, _ := maintApp(t)
	cfg := a.configSnapshot()
	dir := a.backupsDir(cfg)

	var order []string
	prev := syncDir
	syncDir = func(path string) error {
		switch {
		case strings.Contains(filepath.Base(path), snapshotWorkPrefix):
			published := 0
			for _, n := range dirNames(t, dir) {
				if backupSeqRe.MatchString(n) {
					published++
				}
			}
			if published != 0 {
				t.Errorf("the working set was synced AFTER a snapshot was already published")
			}
			order = append(order, "set")
		case path == dir:
			order = append(order, "parent")
		}
		return prev(path)
	}
	defer func() { syncDir = prev }()

	if _, _, err := a.maintenanceBackup(t.Context(), cfg, dir); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(order, ","); got != "set,parent" {
		t.Fatalf("syncs were asked for in the order %q, want the set and then the parent", got)
	}
}

// .
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
	work := filepath.Join(backups, snapshotWorkPrefix+"42.tmp")
	if err := os.MkdirAll(work, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(work, "aii.db"), []byte("the identity, in the clear"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := startLiveForTest(app); err != nil {
		t.Fatalf("startLive: %v", err)
	}
	defer app.Stop()
	if _, err := os.Stat(work); !os.IsNotExist(err) {
		t.Fatalf("A NORMAL BOOT LEFT A CRASHED PASS'S WORKING SET IN %s: the identity in the clear, in the directory that leaves the host", backups)
	}
}
