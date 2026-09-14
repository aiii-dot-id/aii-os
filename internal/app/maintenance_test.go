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

// .
// .
// .

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
	// .
	// .
	// .
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
		if e.IsDir() && backupSeqRe.MatchString(e.Name()) {
			out = append(out, e.Name())
		}
	}
	return out
}

// .
// .
// .
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

	// .
	sums, err := os.ReadFile(filepath.Join(first, "SHA256SUMS"))
	if err != nil {
		t.Fatal("no checksum list in the snapshot")
	}
	blob, err := os.ReadFile(filepath.Join(first, "ledger.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(blob)
	if !strings.Contains(string(sums), hex.EncodeToString(sum[:])+"  ledger.jsonl\n") {
		t.Fatalf("checksum list does not describe the tail copy: %q", sums)
	}

	// .
	a.runMaintenance()
	if got := backupFiles(t, dir); len(got) != 1 {
		t.Fatalf("no-growth pass created a copy: %v", got)
	}

	// .
	appendFixtureEvent(t, a, keyPath, "exp_growth")
	a.runMaintenance()
	if got := backupFiles(t, dir); len(got) != 2 {
		t.Fatalf("growth pass: %d backups, want 2 (%v)", len(got), got)
	}

	// .
	newest := ""
	var newestSeq uint64
	for _, f := range backupFiles(t, dir) {
		if m := backupSeqRe.FindStringSubmatch(f); m != nil {
			// .
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
	if err := st.ReplayFromFile(filepath.Join(dir, newest, "ledger.jsonl")); err != nil {
		t.Fatalf("THE BACKUP DOES NOT RESTORE: %v", err)
	}
	if newestSeq != a.ledger.LastSeq() {
		t.Fatalf("newest backup at seq %d, ledger at %d", newestSeq, a.ledger.LastSeq())
	}
}

// .
// .
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
		if strings.HasPrefix(e.Name(), ".snapshot-") {
			t.Fatalf("a refused copy left debris: %s", e.Name())
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

// .
// .
func TestAnOlderGoodCopySurvivesLaterCorruption(t *testing.T) {
	a, _, keyPath := maintApp(t)
	cfg := a.configSnapshot()
	dir := a.backupsDir(cfg)

	a.runMaintenance()
	good := backupFiles(t, dir)
	if len(good) != 1 {
		t.Fatalf("fixture: want one good backup, got %v", good)
	}
	goodBytes, err := os.ReadFile(filepath.Join(dir, good[0], "ledger.jsonl"))
	if err != nil {
		t.Fatal(err)
	}

	tamperChain(t, keyPath, cfg.Identity.LedgerPath)
	a.runMaintenance()

	after, err := os.ReadFile(filepath.Join(dir, good[0], "ledger.jsonl"))
	if err != nil {
		t.Fatal("the good backup is gone")
	}
	if string(after) != string(goodBytes) {
		t.Fatal("the good backup was modified")
	}
}

// .
// .
func TestSafeModeVerifiesButWritesNothing(t *testing.T) {
	a, _, _ := maintApp(t)
	cfg := a.configSnapshot()
	a.enterSafe("test: maintenance posture")
	a.runMaintenance()
	if got := backupFiles(t, a.backupsDir(cfg)); len(got) != 0 {
		t.Fatalf("SAFE wrote a backup: %v", got)
	}
}

// .
// .
// .
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
	blob, err := os.ReadFile(filepath.Join(a.backupsDir(cfg), files[0], "ledger.jsonl"))
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

// .
// .
func TestPruneKeepsTheNewest(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []string{"1", "2", "3"} {
		snap := filepath.Join(dir, "ledger-20260826T000000Z-seq"+n)
		if err := os.MkdirAll(snap, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(snap, "ledger.jsonl"), []byte("x\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(snap, "SHA256SUMS"), []byte("y  ledger.jsonl\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	// .
	// .
	if err := os.MkdirAll(filepath.Join(dir, "ledger-20260826T000000Z-seq9"), 0o700); err != nil {
		t.Fatal(err)
	}

	if removed := pruneBackups(dir, 1); removed != 2 {
		t.Fatalf("pruned %d, want 2", removed)
	}
	left := backupFiles(t, dir)
	if len(left) != 1 || !strings.Contains(left[0], "seq3") {
		t.Fatalf("THE WRONG COPIES SURVIVED: %v", left)
	}
	if _, err := os.Stat(filepath.Join(dir, left[0], "SHA256SUMS")); err != nil {
		t.Fatal("the survivor lost its checksum list")
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.Contains(e.Name(), "seq9") {
			t.Fatal("the incomplete snapshot survived")
		}
		if strings.Contains(e.Name(), "seq1") || strings.Contains(e.Name(), "seq2") {
			t.Fatalf("pruned copy left debris: %s", e.Name())
		}
	}
}
