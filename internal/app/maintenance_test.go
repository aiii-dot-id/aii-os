package app

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/crypto"
	"github.com/aiii-dot-id/aii-os/internal/ledger"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

// .
// .
// .

func maintApp(t *testing.T) (*App, string, string) {
	t.Helper()
	return maintAppWithDatabaseIn(t, "")
}

// .
// .
// .
// .
func maintAppWithDatabaseIn(t *testing.T, sub string) (*App, string, string) {
	return maintAppDatabase(t, sub, false)
}

func maintAppDatabase(t *testing.T, sub string, compressed bool) (*App, string, string) {
	t.Helper()
	dir := t.TempDir()
	keyPath, ledgerPath, dbPath := birthFixture(t, dir, "Maint")
	if sub != "" {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o700); err != nil {
			t.Fatal(err)
		}
		dbPath = filepath.Join(dir, sub, "aii.db")
	}
	buildPriorProjection(t, ledgerPath, dbPath)
	if compressed {
		converted := dbPath + ".compressed"
		if published, err := store.ConvertDatabase(t.Context(), dbPath, converted, true); err != nil || !published {
			t.Fatalf("prepare compressed fixture: %v %v", published, err)
		}
		dbPath = converted
	}
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
	// .
	// .
	// .
	kp, err := crypto.LoadKeyPair(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	a.door = &ledgerAdapter{Ledger: lg, kp: kp, st: st}
	return a, dir, keyPath
}

func appendFixtureEvent(t *testing.T, a *App, keyPath, id string) {
	t.Helper()
	// .
	// .
	// .
	// .
	// .
	if _, err := a.door.Append(ledger.EventExperienceCreate, 3,
		map[string]interface{}{
			"id": id, "content": "observed " + id, "category": "observation", "provenance": "self",
		}, ""); err != nil {
		t.Fatal(err)
	}
}

// .
// .
// .
func nextSecond() {
	time.Sleep(time.Until(time.Now().Truncate(time.Second).Add(time.Second)))
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
func snapshotSeq(t *testing.T, name string) uint64 {
	t.Helper()
	m := backupSeqRe.FindStringSubmatch(name)
	if m == nil {
		t.Fatalf("%q is not a published snapshot name", name)
	}
	n, err := strconv.ParseUint(m[2], 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

// .
// .
// .
// .
func TestMaintenanceCopiesOnlyWhatVerifies(t *testing.T) {
	a, _, keyPath := maintApp(t)
	cfg := a.configSnapshot()
	dir := a.backupsDir(cfg)

	a.runMaintenance(t.Context())
	files := backupFiles(t, dir)
	if len(files) != 1 {
		t.Fatalf("first pass: %d backups, want 1 (%v)", len(files), files)
	}
	first := filepath.Join(dir, files[0])
	if got := snapshotSeq(t, files[0]); got != a.ledger.LastSeq() {
		t.Fatalf("backup seq %d, ledger at %d", got, a.ledger.LastSeq())
	}

	// .
	// .
	// .
	sums, err := os.ReadFile(filepath.Join(first, "SHA256SUMS"))
	if err != nil {
		t.Fatal("no checksum list in the snapshot")
	}
	for _, name := range []string{"ledger.jsonl", snapshotDB, snapshotReceiptName} {
		blob, err := os.ReadFile(filepath.Join(first, name))
		if err != nil {
			t.Fatalf("the snapshot lacks %s: %v", name, err)
		}
		sum := sha256.Sum256(blob)
		if !strings.Contains(string(sums), hex.EncodeToString(sum[:])+"  "+name+"\n") {
			t.Fatalf("checksum list does not describe %s: %q", name, sums)
		}
	}
	// .
	// .
	entries, err := os.ReadDir(first)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if n := e.Name(); n != "SHA256SUMS" && !strings.Contains(string(sums), "  "+n+"\n") && !e.IsDir() {
			t.Fatalf("the snapshot holds %s, which its checksum list does not name", n)
		}
		if e.IsDir() && e.Name() != "witness-keys" {
			t.Fatalf("the snapshot holds a directory it should not: %s", e.Name())
		}
	}

	// .
	appendFixtureEvent(t, a, keyPath, "exp_growth")
	a.runMaintenance(t.Context())
	got := backupFiles(t, dir)
	if len(got) != 2 {
		t.Fatalf("growth pass: %d backups, want 2 (%v)", len(got), got)
	}

	// .
	// .
	newest := got[0]
	for _, f := range got {
		if snapshotSeq(t, f) > snapshotSeq(t, newest) {
			newest = f
		}
	}
	if snapshotSeq(t, newest) != a.ledger.LastSeq() {
		t.Fatalf("newest backup at seq %d, ledger at %d", snapshotSeq(t, newest), a.ledger.LastSeq())
	}
	restoreDB := filepath.Join(t.TempDir(), "aii.db")
	raw, err := os.ReadFile(filepath.Join(dir, newest, snapshotDB))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(restoreDB, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	st, err := store.New(restoreDB)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.ReplayFromFile(filepath.Join(dir, newest, "ledger.jsonl")); err != nil {
		t.Fatalf("THE BACKUP DOES NOT RESTORE: %v", err)
	}
}

// .
// .
func TestACorruptLedgerProducesNoCopyAndAnAlert(t *testing.T) {
	a, _, keyPath := maintApp(t)
	cfg := a.configSnapshot()
	dir := a.backupsDir(cfg)

	tamperChain(t, keyPath, cfg.Identity.LedgerPath)
	a.runMaintenance(t.Context())

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

	a.runMaintenance(t.Context())
	good := backupFiles(t, dir)
	if len(good) != 1 {
		t.Fatalf("fixture: want one good backup, got %v", good)
	}
	goodBytes, err := os.ReadFile(filepath.Join(dir, good[0], "ledger.jsonl"))
	if err != nil {
		t.Fatal(err)
	}

	tamperChain(t, keyPath, cfg.Identity.LedgerPath)
	a.runMaintenance(t.Context())

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
	if _, err := a.store.DB().Exec(`INSERT INTO tool_events(execution_id,turn_id,ordinal,actor,tool,args_record,state,started_ms,finished_ms)
		VALUES('safe-maintenance-fixture','fixture',0,'test','test','[]','done',1,2)`); err != nil {
		t.Fatal(err)
	}
	a.enterSafe("test: maintenance posture")
	a.runMaintenance(t.Context())
	if got := backupFiles(t, a.backupsDir(cfg)); len(got) != 0 {
		t.Fatalf("SAFE wrote a backup: %v", got)
	}
	var kept int
	if err := a.store.DB().QueryRow("SELECT count(*) FROM tool_events WHERE execution_id='safe-maintenance-fixture'").Scan(&kept); err != nil || kept != 1 {
		t.Fatalf("SAFE ran write housekeeping: count=%d err=%v", kept, err)
	}
}

func TestMaintenanceBacksUpAndRestoreProvesCompressedStore(t *testing.T) {
	a, _, _ := maintAppDatabase(t, "", true)
	a.runMaintenance(t.Context())
	dir := a.backupsDir(a.configSnapshot())
	backups := backupFiles(t, dir)
	if len(backups) != 1 {
		t.Fatalf("compressed source produced %d proved snapshots", len(backups))
	}
	image := filepath.Join(dir, backups[0], "aii.db")
	b, err := os.ReadFile(image)
	if err != nil || !bytes.HasPrefix(b, []byte("SQLite format 3\x00")) {
		t.Fatalf("snapshot is not ordinary SQLite: %v", err)
	}
	if _, _, err := store.InspectCopy(t.Context(), image); err != nil {
		t.Fatalf("snapshot inspection: %v", err)
	}
}

// .
// .
// .
// .
// .
// .
func TestATornTailIsRefusedNotTrimmed(t *testing.T) {
	a, _, _ := maintApp(t)
	cfg := a.configSnapshot()
	dir := a.backupsDir(cfg)
	a.runMaintenance(t.Context())
	good := backupFiles(t, dir)
	if len(good) != 1 {
		t.Fatalf("fixture: want one good backup, got %v", good)
	}

	f, err := os.OpenFile(cfg.Identity.LedgerPath, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(`{"seq":999,"torn`); err != nil {
		t.Fatal(err)
	}
	f.Close()

	nextSecond()
	a.runMaintenance(t.Context())
	if after := backupFiles(t, dir); len(after) != 1 || after[0] != good[0] {
		t.Fatalf("A DAMAGED TAIL WAS PUBLISHED, or the good copy is gone: %v", after)
	}
	msgs, err := a.store.UndeliveredFor("operator")
	if err != nil {
		t.Fatal(err)
	}
	var told bool
	for _, m := range msgs {
		if strings.Contains(m.Content, "[maintenance] backup") && strings.Contains(m.Content, "capture refused") {
			told = true
		}
	}
	if !told {
		t.Fatalf("the operator was not told the tail is damaged: %+v", msgs)
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
