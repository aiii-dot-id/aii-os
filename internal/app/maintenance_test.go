package app

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/crypto"
	"github.com/aiii-dot-id/aii-os/internal/ledger"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

// maintenance_test.go — the sequence a real day runs: verify, copy,
// prune, and the refusals in between. Built on birthFixture, so every
// chain here is really signed and every verification is the real one.

func maintApp(t *testing.T) (*App, string, string) {
	t.Helper()
	dir := t.TempDir()
	keyPath, ledgerPath, dbPath := birthFixture(t, dir, "Maint")
	buildPriorProjection(t, ledgerPath, dbPath)
	st, err := store.New(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	lg, err := ledger.New(ledgerPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { lg.Close() })
	a := New(&Config{
		SourcePath: filepath.Join(dir, "config.json"),
		Identity:   IdentityConfig{LedgerPath: ledgerPath, DBPath: dbPath, KeyPath: keyPath},
	})
	a.store = st
	a.ledger = lg
	return a, dir, keyPath
}

func appendFixtureEvent(t *testing.T, a *App, keyPath, id string) {
	t.Helper()
	kp, err := crypto.LoadKeyPair(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	// experience.create: the one growth event whose replay is a plain
	// insert — a second relationship.upsert would (correctly) be refused
	// at materialization as an unsuperseded Ring 1 fork.
	if _, err := a.ledger.Append(ledger.EventExperienceCreate, kp.Fingerprint(), 3,
		map[string]interface{}{
			"id": id, "content": "observed " + id, "category": "observation", "provenance": "self",
		}, kp); err != nil {
		t.Fatal(err)
	}
}

func backupFiles(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatal(err)
	}
	var out []string
	for _, e := range entries {
		if backupSeqRe.MatchString(e.Name()) {
			out = append(out, e.Name())
		}
	}
	return out
}

// THE DAY A REAL DEPLOYMENT RUNS, END TO END. Copy on growth, verify
// before publish, sidecar the operator can check with stock tools,
// no-op when nothing grew, restore that actually replays.
func TestMaintenanceCopiesOnlyWhatVerifies(t *testing.T) {
	a, _, keyPath := maintApp(t)
	cfg := a.configSnapshot()
	dir := a.backupsDir(cfg)

	a.runMaintenance()
	files := backupFiles(t, dir)
	if len(files) != 1 {
		t.Fatalf("first pass: %d backups, want 1 (%v)", len(files), files)
	}
	first := filepath.Join(dir, files[0])
	if got := newestBackupSeq(dir); got != a.ledger.LastSeq() {
		t.Fatalf("backup seq %d, ledger at %d", got, a.ledger.LastSeq())
	}

	// The sidecar is real coreutils format and matches the bytes.
	sidecar, err := os.ReadFile(first + ".sha256")
	if err != nil {
		t.Fatal("no sidecar beside the backup")
	}
	blob, err := os.ReadFile(first)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(blob)
	if !strings.HasPrefix(string(sidecar), hex.EncodeToString(sum[:])+"  ") {
		t.Fatalf("sidecar does not describe the backup: %q", sidecar)
	}

	// Nothing grew: the second pass verifies but copies nothing.
	a.runMaintenance()
	if got := backupFiles(t, dir); len(got) != 1 {
		t.Fatalf("no-growth pass created a copy: %v", got)
	}

	// Growth: a second, newer copy appears; the first survives (keep 8).
	appendFixtureEvent(t, a, keyPath, "exp_growth")
	a.runMaintenance()
	if got := backupFiles(t, dir); len(got) != 2 {
		t.Fatalf("growth pass: %d backups, want 2 (%v)", len(got), got)
	}

	// RESTORE IS REAL: the newest copy replays into a fresh store.
	newest := ""
	var newestSeq uint64
	for _, f := range backupFiles(t, dir) {
		if m := backupSeqRe.FindStringSubmatch(f); m != nil {
			// backupSeqRe guarantees digits.
			var n uint64
			for _, c := range m[1] {
				n = n*10 + uint64(c-'0')
			}
			if n > newestSeq {
				newestSeq, newest = n, f
			}
		}
	}
	restoreDB := filepath.Join(t.TempDir(), "restore.db")
	st, err := store.New(restoreDB)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.ReplayFromFile(filepath.Join(dir, newest)); err != nil {
		t.Fatalf("THE BACKUP DOES NOT RESTORE: %v", err)
	}
	if newestSeq != a.ledger.LastSeq() {
		t.Fatalf("newest backup at seq %d, ledger at %d", newestSeq, a.ledger.LastSeq())
	}
}

// A LEDGER THAT FAILS ITS WALK PRODUCES NO COPY — and the operator
// hears about it in the outbox, not just a log nobody tails.
func TestACorruptLedgerProducesNoCopyAndAnAlert(t *testing.T) {
	a, _, keyPath := maintApp(t)
	cfg := a.configSnapshot()
	dir := a.backupsDir(cfg)

	tamperChain(t, keyPath, cfg.Identity.LedgerPath)
	a.runMaintenance()

	if got := backupFiles(t, dir); len(got) != 0 {
		t.Fatalf("A CORRUPT LEDGER WAS PUBLISHED AS A BACKUP: %v", got)
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".sha256") {
			t.Fatalf("a sidecar outlived its refused copy: %s", e.Name())
		}
	}
	msgs, err := a.store.UndeliveredFor("operator")
	if err != nil {
		t.Fatal(err)
	}
	var alerted bool
	for _, m := range msgs {
		if strings.Contains(m.Content, "[maintenance]") {
			alerted = true
		}
	}
	if !alerted {
		t.Fatal("the chain failure never reached the outbox — the operator was not told")
	}
}

// AND AN ALREADY-GOOD COPY IS PROTECTED BY THE REFUSAL. Corruption
// after a healthy backup must not replace it, age it out, or touch it.
func TestAnOlderGoodCopySurvivesLaterCorruption(t *testing.T) {
	a, _, keyPath := maintApp(t)
	cfg := a.configSnapshot()
	dir := a.backupsDir(cfg)

	a.runMaintenance() // the good copy
	good := backupFiles(t, dir)
	if len(good) != 1 {
		t.Fatalf("fixture: want one good backup, got %v", good)
	}
	goodBytes, err := os.ReadFile(filepath.Join(dir, good[0]))
	if err != nil {
		t.Fatal(err)
	}

	tamperChain(t, keyPath, cfg.Identity.LedgerPath)
	a.runMaintenance() // verify-only path now; chain fails loudly

	after, err := os.ReadFile(filepath.Join(dir, good[0]))
	if err != nil {
		t.Fatal("the good backup is gone")
	}
	if string(after) != string(goodBytes) {
		t.Fatal("the good backup was modified")
	}
}

// SAFE VERIFIES AND WRITES NOTHING — integrity evidence is what SAFE
// wants; new files are not.
func TestSafeModeVerifiesButWritesNothing(t *testing.T) {
	a, _, _ := maintApp(t)
	cfg := a.configSnapshot()
	a.enterSafe("test: maintenance posture")
	a.runMaintenance()
	if got := backupFiles(t, a.backupsDir(cfg)); len(got) != 0 {
		t.Fatalf("SAFE wrote a backup: %v", got)
	}
}

// A TORN TAIL IS TRIMMED, NEVER SHIPPED. An append racing the copy —
// or a crash mid-append — leaves a partial last line; the copy stops
// at the last complete event and still verifies.
func TestATornTailIsTrimmedNeverCopied(t *testing.T) {
	a, _, _ := maintApp(t)
	cfg := a.configSnapshot()
	f, err := os.OpenFile(cfg.Identity.LedgerPath, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(`{"seq":999,"torn`); err != nil {
		t.Fatal(err)
	}
	f.Close()

	a.runMaintenance()
	files := backupFiles(t, a.backupsDir(cfg))
	if len(files) != 1 {
		t.Fatalf("torn tail blocked the whole copy: %v", files)
	}
	blob, err := os.ReadFile(filepath.Join(a.backupsDir(cfg), files[0]))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(blob), "torn") {
		t.Fatal("THE TORN TAIL WAS COPIED")
	}
	if blob[len(blob)-1] != '\n' {
		t.Fatal("backup does not end on a complete line")
	}
}

// PRUNE EATS THE OLDEST AND ONLY THE OLDEST, sidecars follow their
// files, and an orphaned sidecar counts as debris.
func TestPruneKeepsTheNewest(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []string{"1", "2", "3"} {
		name := "ledger-20260826T000000Z-seq" + n + ".jsonl"
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name+".sha256"), []byte("y\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	// An orphaned sidecar — its file lost to a crash between removes.
	if err := os.WriteFile(filepath.Join(dir, "ledger-20260826T000000Z-seq9.jsonl.sha256"), []byte("z\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if removed := pruneBackups(dir, 1); removed != 2 {
		t.Fatalf("pruned %d, want 2", removed)
	}
	left := backupFiles(t, dir)
	if len(left) != 1 || !strings.Contains(left[0], "seq3") {
		t.Fatalf("THE WRONG COPIES SURVIVED: %v", left)
	}
	if _, err := os.Stat(filepath.Join(dir, left[0]+".sha256")); err != nil {
		t.Fatal("the survivor lost its sidecar")
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.Contains(e.Name(), "seq9") {
			t.Fatal("the orphaned sidecar survived")
		}
		if strings.Contains(e.Name(), "seq1") || strings.Contains(e.Name(), "seq2") {
			t.Fatalf("pruned copy left debris: %s", e.Name())
		}
	}
}
