package continuity

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/crypto"
	"github.com/aiii-dot-id/aii-os/internal/genesis"
	"github.com/aiii-dot-id/aii-os/internal/genesis/genesistest"
	"github.com/aiii-dot-id/aii-os/internal/ledger"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

// maintenance_test.go — the sequence a real day runs: verify, copy,
// prune, and the refusals in between. Built on birthFixture, so every
// chain here is really signed and every verification is the real one;
// and here is the fixture the package's tests stand on (testApp).

func maintApp(t *testing.T) (*testApp, string, string) {
	t.Helper()
	return maintAppWithDatabaseIn(t, "")
}

// maintAppWithDatabaseIn is maintApp with the database in a directory of
// its own under the identity's (empty: beside the ledger, as birth leaves
// it). What stands "beside the ledger" is beside the LEDGER, and only a
// fixture that parts the two can tell.
func maintAppWithDatabaseIn(t *testing.T, sub string) (*testApp, string, string) {
	return maintAppDatabase(t, sub, false)
}

func maintAppDatabase(t *testing.T, sub string, compressed bool) (*testApp, string, string) {
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
	a := &testApp{cfg: Settings{Identity: Identity{LedgerPath: ledgerPath, DBPath: dbPath, KeyPath: keyPath}}}
	st, err := store.NewForTest(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	a.store = st
	lg, err := ledger.New(ledgerPath)
	if err != nil {
		st.Close()
		t.Fatal(err)
	}
	a.ledger = lg
	// The admission door, as a boot builds it: a recovery point is
	// captured there, and an event that reaches the ledger any other way
	// leaves the mirror behind the record.
	kp, err := crypto.LoadKeyPair(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	a.door = &testDoor{lg: lg, st: st, kp: kp}
	a.continuity = New(a.wires())
	// What the test closed itself (and set to nil) is the test's.
	t.Cleanup(func() {
		if a.ledger != nil {
			a.ledger.Close()
		}
		if a.store != nil {
			a.store.Close()
		}
	})
	return a, dir, keyPath
}

// testApp is what the domain's tests stand on in place of the App: a real
// identity — born, its chain signed, its projection built the way a clean
// boot builds it — with its ledger and store open, a door that admits and
// captures as the app's ledger door does, and the Runtime wired to them as
// the app wires it (App.Wires). How the pass meets the app's own
// door and boot is tested in internal/app.
type testApp struct {
	continuity *Runtime
	cfg        Settings
	ledger     *ledger.Ledger
	store      *store.Store
	door       *testDoor
	safe       atomic.Bool
}

// testDoor admits as the app's ledger door does (ledgerAdapter.admit):
// prepare, validate, append and materialize under one lock; and captures as
// it does (ledgerAdapter.capture): the tail copied and the database pinned
// at that same record, under the same lock.
type testDoor struct {
	mu sync.Mutex
	lg *ledger.Ledger
	st *store.Store
	kp *crypto.KeyPair
}

func (d *testDoor) Append(eventType ledger.EventType, ring int, payload any, _ string) (*ledger.Event, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	prepared, err := d.lg.PreparePayload(payload)
	if err != nil {
		return nil, err
	}
	if err := d.st.ValidateEvent(eventType, ring, prepared.Bytes()); err != nil {
		return nil, fmt.Errorf("%w before append: %w", store.ErrRefused, err)
	}
	evt, err := d.lg.AppendPrepared(eventType, ring, prepared, d.kp)
	if err != nil {
		return nil, err
	}
	return evt, d.st.Materialize(evt)
}

func (d *testDoor) capture(ctx context.Context, tailDst string) (ledger.Capture, *store.Pin, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	c, err := d.lg.Capture(tailDst)
	if err != nil {
		return ledger.Capture{}, nil, err
	}
	pin, err := d.st.PinAt(ctx, c.LastSeq, c.LastHash)
	if err != nil {
		os.Remove(tailDst)
		return ledger.Capture{}, nil, err
	}
	return c, pin, nil
}

func (a *testApp) configSnapshot() Settings { return a.cfg }

func (a *testApp) backupsDir(cfg Settings) string { return a.continuity.BackupsDir(cfg) }

// enterSafe is the app's: the mode said, the ledger and the store frozen.
func (a *testApp) enterSafe(reason string) {
	a.safe.Store(true)
	if a.ledger != nil {
		a.ledger.SetFrozen(reason)
	}
	if a.store != nil {
		a.store.SetFrozen(true, reason)
	}
}

// wires answers what the domain's tests reach; the rest stay nil, so a
// test that reaches one fails loudly instead of being answered by a stand-in.
func (a *testApp) wires() Wires {
	return Wires{
		Settings: a.configSnapshot,
		// What the app's activeIdentity says of an App that holds this
		// ledger open and nothing else.
		Active: func(id Identity) Identity {
			if a.ledger != nil {
				id.LedgerPath = a.ledger.Path()
			}
			return id
		},
		Safe:  a.safe.Load,
		Store: func() *store.Store { return a.store },
		Capture: func(ctx context.Context, tailDst string) (ledger.Capture, *store.Pin, error) {
			if a.ledger == nil {
				return ledger.Capture{}, nil, errors.New("no ledger is open — there is no record to capture")
			}
			return a.door.capture(ctx, tailDst)
		},
		DoorKey: func() *crypto.KeyPair { return a.door.kp },
		Key:     func() *crypto.KeyPair { return a.door.kp },
		LastSeq: func() (uint64, bool) {
			if a.ledger == nil {
				return 0, false
			}
			return a.ledger.LastSeq(), true
		},
		BootChain: func() string { return "" }, // no boot walked a chain here
	}
}

// birthFixture births a valid identity in dir and returns its paths, as the
// app's own fixture of that name does.
func birthFixture(t *testing.T, dir, name string) (keyPath, ledgerPath, dbPath string) {
	t.Helper()
	keyPath = filepath.Join(dir, "identity.sec")
	ledgerPath = filepath.Join(dir, "ledger.jsonl")
	dbPath = filepath.Join(dir, "aii.db")
	root := genesistest.NewRoot(t)
	result := root.Birth(t, genesis.BirthConfig{
		Name:        name,
		Ring0Bundle: root.Ring0Bundle(t),
		Root:        root.Env,
		KeyPath:     keyPath, LedgerPath: ledgerPath, DBPath: dbPath,
	})
	if _, err := result.Ledger.Append(ledger.EventRelationshipUpsert, 1,
		map[string]any{
			"id": "rel_fixture", "counterpart_name": "Operator", "counterpart_role": "operator",
			"relationship_type": "founding_operator", "charter_text": "Test relationship",
			"operator_approval_excerpt": "Yes — rel_fixture approved.",
			"operator_approval_turn":    1,
			"approval_basis":            "conversation_turn",
		}, result.KeyPair); err != nil {
		t.Fatal(err)
	}
	result.Ledger.Close()
	return keyPath, ledgerPath, dbPath
}

// buildPriorProjection materializes the ledger into the DB exactly the
// way a clean boot would, then closes — the durable footprint a prior
// healthy life leaves behind.
func buildPriorProjection(t *testing.T, ledgerPath, dbPath string) {
	t.Helper()
	st, err := store.NewForTest(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.ReplayFromFile(ledgerPath); err != nil {
		st.Close()
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
}

// tamperChain flips one content_hash nibble in ledger line 1 and asserts
// the corruption landed.
func tamperChain(t *testing.T, keyPath, ledgerPath string) {
	t.Helper()
	raw, err := os.ReadFile(ledgerPath)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(string(raw), "\n")
	l0 := lines[0]
	i := strings.Index(l0, `{"content":"`)
	if i < 0 {
		t.Fatalf("no content hash in ledger line 1")
	}
	j := i + len(`{"content":"`)
	flip := byte('0')
	if l0[j] == '0' {
		flip = '1'
	}
	lines[0] = l0[:j] + string(flip) + l0[j+1:]
	if err := os.WriteFile(ledgerPath, []byte(strings.Join(lines, "\n")), 0600); err != nil {
		t.Fatal(err)
	}
	kp, err := crypto.LoadKeyPair(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ledger.VerifyChain(ledgerPath, kp.PublicKeyBytes(), nil); err == nil {
		t.Fatal("corruption did not land — the flip was a no-op")
	}
}

func appendFixtureEvent(t *testing.T, a *testApp, keyPath, id string) {
	t.Helper()
	// experience.create: the one growth event whose replay is a plain
	// insert — a second relationship.upsert would (correctly) be refused
	// at materialization as an unsuperseded Ring 1 fork. Through the
	// door, so the mirror follows the record as it does in a running
	// identity.
	if _, err := a.door.Append(ledger.EventExperienceCreate, 3,
		map[string]any{
			"id": id, "content": "observed " + id, "category": "observation", "provenance": "self",
		}, ""); err != nil {
		t.Fatal(err)
	}
}

// nextSecond waits out the wall-clock second. A snapshot's name carries
// its UTC second, so two passes at one record need two seconds to have
// two names; a day apart in production, back to back here.
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

// snapshotSeq reads the record length out of a published snapshot name.
func snapshotSeq(t *testing.T, name string) uint64 {
	t.Helper()
	m := backupSeqRe.FindStringSubmatch(name)
	if m == nil {
		t.Fatalf("%q is not a published snapshot name", name)
	}
	n, err := strconv.ParseUint(m[2], 10, 64) // m[1] is the stamp, which is what orders the set
	if err != nil {
		t.Fatal(err)
	}
	return n
}

// THE DAY A REAL DEPLOYMENT RUNS, END TO END. Every pass captures,
// verifies before it publishes, leaves a checksum list the operator can
// check with stock tools, and what it published restores — the record
// and the database together.
func TestMaintenanceCopiesOnlyWhatVerifies(t *testing.T) {
	a, _, keyPath := maintApp(t)
	cfg := a.configSnapshot()
	dir := a.backupsDir(cfg)

	a.continuity.runMaintenance(t.Context())
	files := backupFiles(t, dir)
	if len(files) != 1 {
		t.Fatalf("first pass: %d backups, want 1 (%v)", len(files), files)
	}
	first := filepath.Join(dir, files[0])
	if got := snapshotSeq(t, files[0]); got != a.ledger.LastSeq() {
		t.Fatalf("backup seq %d, ledger at %d", got, a.ledger.LastSeq())
	}

	// The checksum list is real coreutils format, matches the bytes, and
	// names every file the snapshot is made of — the database and the
	// receipt with the record.
	sums, err := os.ReadFile(filepath.Join(first, "SHA256SUMS"))
	if err != nil {
		t.Fatal("no checksum list in the snapshot")
	}
	for _, name := range []string{"ledger.jsonl", SnapshotDB, snapshotReceiptName} {
		blob, err := os.ReadFile(filepath.Join(first, name))
		if err != nil {
			t.Fatalf("the snapshot lacks %s: %v", name, err)
		}
		sum := sha256.Sum256(blob)
		if !strings.Contains(string(sums), hex.EncodeToString(sum[:])+"  "+name+"\n") {
			t.Fatalf("checksum list does not describe %s: %q", name, sums)
		}
	}
	// Nothing but what the list names: no scratch restore, no
	// write-ahead files beside the database copy.
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

	// Growth: a second, newer copy appears; the first survives (keep 8).
	appendFixtureEvent(t, a, keyPath, "exp_growth")
	a.continuity.runMaintenance(t.Context())
	got := backupFiles(t, dir)
	if len(got) != 2 {
		t.Fatalf("growth pass: %d backups, want 2 (%v)", len(got), got)
	}

	// RESTORE IS REAL, AND IT IS THE PAIR: the newest snapshot's database
	// goes back with its record, and a boot's replay accepts them.
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
	raw, err := os.ReadFile(filepath.Join(dir, newest, SnapshotDB))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(restoreDB, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	st, err := store.NewForTest(restoreDB)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.ReplayFromFile(filepath.Join(dir, newest, "ledger.jsonl")); err != nil {
		t.Fatalf("THE BACKUP DOES NOT RESTORE: %v", err)
	}
}

// A LEDGER THAT FAILS ITS WALK PRODUCES NO COPY — and the operator
// hears about it in the outbox, not just a log nobody tails.
func TestACorruptLedgerProducesNoCopyAndAnAlert(t *testing.T) {
	a, _, keyPath := maintApp(t)
	cfg := a.configSnapshot()
	dir := a.backupsDir(cfg)

	tamperChain(t, keyPath, cfg.Identity.LedgerPath)
	a.continuity.runMaintenance(t.Context())

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

// AND AN ALREADY-GOOD COPY IS PROTECTED BY THE REFUSAL. Corruption
// after a healthy backup must not replace it, age it out, or touch it.
func TestAnOlderGoodCopySurvivesLaterCorruption(t *testing.T) {
	a, _, keyPath := maintApp(t)
	cfg := a.configSnapshot()
	dir := a.backupsDir(cfg)

	a.continuity.runMaintenance(t.Context()) // the good copy
	good := backupFiles(t, dir)
	if len(good) != 1 {
		t.Fatalf("fixture: want one good backup, got %v", good)
	}
	goodBytes, err := os.ReadFile(filepath.Join(dir, good[0], "ledger.jsonl"))
	if err != nil {
		t.Fatal(err)
	}

	tamperChain(t, keyPath, cfg.Identity.LedgerPath)
	a.continuity.runMaintenance(t.Context()) // the capture's own gate refuses; the chain fails loudly

	after, err := os.ReadFile(filepath.Join(dir, good[0], "ledger.jsonl"))
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
	logs := logsink.CaptureForTest(t)
	a.enterSafe("test: maintenance posture")
	a.continuity.runMaintenance(t.Context())
	if got := backupFiles(t, a.backupsDir(cfg)); len(got) != 0 {
		t.Fatalf("SAFE wrote a backup: %v", got)
	}
	// Housekeeping is the database's one writing step; its report is the
	// pass's own line.
	if !logs.Contains("write maintenance skipped") || logs.Contains("optimize ok") {
		t.Fatalf("SAFE ran write housekeeping:\n%s", logs.String())
	}
}

func TestMaintenanceBacksUpAndRestoreProvesCompressedStore(t *testing.T) {
	a, _, _ := maintAppDatabase(t, "", true)
	a.continuity.runMaintenance(t.Context())
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

// A TORN TAIL IS DAMAGE NOW, NOT A RACE. The old pass copied under no
// lock, so a partial last line could be an append in flight and the copy
// trimmed it. The capture holds the ledger's lock: nothing is in flight,
// and bytes after the last record are something else's. No snapshot is
// published over them, the operator is told today rather than at the
// next append, and the good copy from before stands.
func TestATornTailIsRefusedNotTrimmed(t *testing.T) {
	a, _, _ := maintApp(t)
	cfg := a.configSnapshot()
	dir := a.backupsDir(cfg)
	a.continuity.runMaintenance(t.Context())
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
	a.continuity.runMaintenance(t.Context())
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

// PRUNE EATS THE OLDEST AND ONLY THE OLDEST, a snapshot is one
// directory, and a published name without its checksum list is debris.
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
	// A published name without its list — not a state the publish order
	// produces, so debris.
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
