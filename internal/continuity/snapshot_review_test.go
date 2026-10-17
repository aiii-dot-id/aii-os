package continuity

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/atomicfile"
	"github.com/aiii-dot-id/aii-os/internal/escrow"
)

func mkPlainSnapshot(t *testing.T, dir, name string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, name), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name, SnapshotSums), []byte("x  y\n"), 0o600); err != nil {
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

// OLDEST IS BY THE STAMP. The set was ordered by the record's length
// first, and the Restore this contract documents rolls the record BACK:
// tonight's snapshot then sorted as the oldest of the set and was the one
// removed — every night, until the record regrew past the old length.
func TestPruneNeverRemovesTheSnapshotThePassJustMade(t *testing.T) {
	dir := t.TempDir()
	for _, day := range []string{"01", "02", "03", "04", "05", "06", "07", "08"} {
		mkPlainSnapshot(t, dir, "ledger-202609"+day+"T040000Z-seq100")
	}
	// The operator restored the seq-50 snapshot; tonight's pass published this.
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

	// keep=1: the newest complete snapshot is never the one removed,
	// whatever the record's length says, and whichever kind each is.
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

	// A name whose stamp is not a stamp is not ours to count or to prune.
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

// A PASS THAT NEVER RETURNED LEFT THE IDENTITY IN THE CLEAR in the one
// directory the contract says leaves the host, and nothing ever looked
// for it. A normal boot removes it; SAFE removes nothing; and at first
// boot it is EVIDENCE that a record survives here, never something to mint
// over.
func TestACrashedPassesWorkingSetIsSweptAtANormalBootAndOnlyThere(t *testing.T) {
	plant := func(dir string) {
		t.Helper()
		work := filepath.Join(dir, SnapshotWorkPrefix+"123456789.tmp")
		if err := os.MkdirAll(filepath.Join(work, "segments"), 0o700); err != nil {
			t.Fatal(err)
		}
		for _, f := range []string{"aii.db", "ledger.jsonl", filepath.Join("segments", "segment-1-9.jsonl.gz")} {
			if err := os.WriteFile(filepath.Join(work, f), []byte("the identity, in the clear"), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		if err := os.WriteFile(filepath.Join(dir, SnapshotWorkPrefix+"987654321"+escrow.SnapshotSuffix+".tmp"), []byte("half a ciphertext"), 0o600); err != nil {
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
	a.continuity.SweepSnapshotDebrisAtBoot(cfg)
	left := strings.Join(dirNames(t, dir), ",")
	if strings.Contains(left, SnapshotWorkPrefix) {
		t.Fatalf("after a normal boot the working set still stands in %s: %s", dir, left)
	}
	if left != "ledger-20260919T040000Z-seq7,operators-own-notes.txt" {
		t.Fatalf("the sweep touched what is not a pass's working set: %s", left)
	}

	// SAFE removes nothing.
	safe, _, _ := maintApp(t)
	safeCfg := safe.configSnapshot()
	safeDir := safe.backupsDir(safeCfg)
	if err := os.MkdirAll(safeDir, 0o700); err != nil {
		t.Fatal(err)
	}
	plant(safeDir)
	safe.enterSafe("test: SAFE removes nothing")
	safe.continuity.SweepSnapshotDebrisAtBoot(safeCfg)
	if got := dirNames(t, safeDir); len(got) != 2 {
		t.Fatalf("SAFE removed a working set that may be the last copy of a record: %v", got)
	}

	// First boot: the working set is evidence, like a published snapshot.
	// The app's FIRSTBOOT probe asks it of each name (aBackupFile; its own
	// half of this is TestANormalBootSweepsACrashedPassesWorkingSet).
	fresh := t.TempDir()
	plant(fresh)
	for _, n := range dirNames(t, fresh) {
		if !IsBackupEvidence(n) {
			t.Fatalf("a crashed pass's working set — a previous resident's whole record — is not evidence at first boot (%q)", n)
		}
	}

	// No backups directory at all is nothing to sweep, and no error.
	if n, err := sweepSnapshotDebris(filepath.Join(t.TempDir(), "absent")); n != 0 || err != nil {
		t.Fatalf("an absent directory: removed %d, err %v", n, err)
	}
}

// THE SET IS DURABLE BEFORE THE NAME THAT PUBLISHES IT. A sync cannot be
// seen from outside; when it is asked for can. The snapshot's own
// directory is synced while it is still the working set — before the
// rename — and the parent after it.
func TestThePlaintextSetIsSyncedBeforeItIsPublished(t *testing.T) {
	a, _, _ := maintApp(t)
	cfg := a.configSnapshot()
	dir := a.backupsDir(cfg)

	var order []string
	a.continuity.SyncDir = func(path string) error {
		switch {
		case strings.Contains(filepath.Base(path), SnapshotWorkPrefix):
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
		return atomicfile.SyncDir(path)
	}
	defer func() { a.continuity.SyncDir = nil }()

	if _, err := a.continuity.takeSnapshot(t.Context(), cfg, dir, false); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(order, ","); got != "set,parent" {
		t.Fatalf("syncs were asked for in the order %q, want the set and then the parent", got)
	}
}
