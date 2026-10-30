package continuity

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/continuity/continuitytest"
	"github.com/aiii-dot-id/aii-os/internal/escrow"
	"github.com/aiii-dot-id/aii-os/internal/ledger"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

// snapshot_adopt_test.go — the snapshots kept from before snapshots were
// signed, signed by the first start that carries signing where the
// identity's own record proves them.

// asBeforeSigning takes the signature out of a kept snapshot, as one made
// before snapshots were signed has none: the file and its line in the list,
// out of the folder of a plaintext one and out of the archive of an
// encrypted one.
func asBeforeSigning(t *testing.T, a *testApp, dir string, k keptSnapshot) {
	t.Helper()
	var key []byte
	if k.Encrypted {
		key, _ = hostSnapshotKey(t, a)
	}
	if err := continuitytest.Unsign(filepath.Join(dir, k.Name), key); err != nil {
		t.Fatal(err)
	}
}

// statedBeforeSigning records a pass's statement as a program from before
// snapshots were signed made it: no Signed.
func statedBeforeSigning(t *testing.T, a *testApp, k keptSnapshot) {
	t.Helper()
	if err := a.store.SetContinuityStatus(store.ContinuityStatus{Outcome: store.ContinuityOK, Snapshot: k.Name, Record: k.Record, Encrypted: k.Encrypted}); err != nil {
		t.Fatal(err)
	}
}

// keptByName finds a kept snapshot by its record number and form.
func keptAt(t *testing.T, dir string, record uint64, encrypted bool) keptSnapshot {
	t.Helper()
	kept, _ := keptSnapshots(dir)
	for _, k := range kept {
		if k.Record == record && k.Encrypted == encrypted {
			return k
		}
	}
	t.Fatalf("rig: no kept snapshot at record %d (encrypted %v): %v", record, encrypted, kept)
	return keptSnapshot{}
}

// THE FIRST START SIGNS WHAT THE IDENTITY'S OWN RECORD PROVES, AND NOTHING
// ELSE. Three snapshots from before signing: a plaintext one and an
// encrypted one that this identity's record holds, and one from a history
// it no longer holds — taken after a record that was later put back, as a
// restore does, and replaced by another at the same number. That one is
// whole and proves on its own (its chain, its database, its restore in
// scratch); only the identity's record can tell, and it is not signed and
// is named with why. The two are signed, the encrypted one inside its
// archive, and prove. The last pass's statement named the third, so a
// signed snapshot is taken, and the start after it has nothing to do.
func TestTheFirstStartSignsWhatItsOwnRecordProvesAndNothingElse(t *testing.T) {
	a, _, keyPath := maintApp(t)
	cfg := a.configSnapshot()
	dir := a.backupsDir(cfg)
	live(t, a, 1)
	appendFixtureEvent(t, a, keyPath, "adopt_a")
	a.continuity.runMaintenance(t.Context())
	plain := keptAt(t, dir, a.ledger.LastSeq(), false)

	// The record and the database as they stand now: what the history is
	// put back to below.
	forkSeq := a.ledger.LastSeq()
	pin, err := a.store.PinAt(t.Context(), forkSeq, a.ledger.LastHash())
	if err != nil {
		t.Fatal(err)
	}
	olderDB := filepath.Join(t.TempDir(), "older.db")
	if err := pin.BackupTo(t.Context(), olderDB); err != nil {
		t.Fatal(err)
	}
	pin.Release()
	prefix, err := os.ReadFile(cfg.Identity.LedgerPath)
	if err != nil {
		t.Fatal(err)
	}
	nextSecond()
	appendFixtureEvent(t, a, keyPath, "adopt_lost")
	a.continuity.runMaintenance(t.Context())
	forked := keptAt(t, dir, forkSeq+1, false)

	// Put back to forkSeq, and live on: record forkSeq+1 is another one now.
	if err := a.ledger.Close(); err != nil {
		t.Fatal(err)
	}
	if err := a.store.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfg.Identity.LedgerPath, prefix, 0o600); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(olderDB)
	if err != nil {
		t.Fatal(err)
	}
	for _, side := range []string{"-wal", "-shm"} {
		os.Remove(cfg.Identity.DBPath + side)
	}
	if err := os.WriteFile(cfg.Identity.DBPath, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if a.store, err = store.NewForTest(cfg.Identity.DBPath); err != nil {
		t.Fatal(err)
	}
	if a.ledger, err = ledger.New(cfg.Identity.LedgerPath); err != nil {
		t.Fatal(err)
	}
	a.door = &testDoor{lg: a.ledger, st: a.store, kp: a.door.kp}
	appendFixtureEvent(t, a, keyPath, "adopt_kept")
	if a.ledger.LastSeq() != forkSeq+1 {
		t.Fatalf("rig: the record is at %d, want %d", a.ledger.LastSeq(), forkSeq+1)
	}

	_, recipient := hostSnapshotKey(t, a)
	checkedEscrow(t, a, a.door.kp, recipient)
	nextSecond()
	appendFixtureEvent(t, a, keyPath, "adopt_c")
	a.continuity.runMaintenance(t.Context())
	sealed := keptAt(t, dir, a.ledger.LastSeq(), true)

	for _, k := range []keptSnapshot{plain, forked, sealed} {
		asBeforeSigning(t, a, dir, k)
	}
	// A signing that stopped between its two writes left a signature its
	// list does not name: it goes, and the snapshot is signed again.
	if err := os.WriteFile(filepath.Join(dir, plain.Name, escrow.SumsSigName), []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	forkedSet := filepath.Join(dir, forked.Name)
	if err := a.continuity.proveSnapshotIn(t.Context(), cfg, forkedSet, readReceipt(t, forkedSet), filepath.Join(t.TempDir(), "proof")); err != nil {
		t.Fatalf("rig: the snapshot of the history put back must prove on its own: %v", err)
	}
	statedBeforeSigning(t, a, forked)
	if !a.continuity.signingOwed(cfg) {
		t.Fatal("a start after a pass that signed nothing owes no signing")
	}

	signed, notSigned := a.continuity.signKeptAtStart(t.Context(), cfg)
	if !contains(signed, plain.Name) || !contains(signed, sealed.Name) {
		t.Fatalf("snapshots the record proves were not signed: signed %v, not %v", signed, notSigned)
	}
	if contains(signed, forked.Name) {
		t.Fatal("A SNAPSHOT OF A HISTORY THIS IDENTITY'S RECORD NO LONGER HOLDS WAS SIGNED")
	}
	if why := notSigned[forked.Name]; !strings.Contains(why, "this identity's record holds") {
		t.Errorf("why the third was not signed is not said: %q", why)
	}
	for _, k := range []keptSnapshot{plain, sealed} {
		if out, err := a.continuity.verifyKept(t.Context(), k.Name, false, time.Now()); err != nil {
			t.Errorf("%s, signed at the start, does not prove: %v %s", k.Name, err, out)
		}
	}
	// What the start signed, an earlier program still takes: the list names
	// every file, the signature included.
	present, err := filesUnder(filepath.Join(dir, plain.Name))
	if err != nil {
		t.Fatal(err)
	}
	everyFileNamed(t, filepath.Join(dir, plain.Name), present)
	key, _ := hostSnapshotKey(t, a)
	opened := filepath.Join(t.TempDir(), "opened")
	names, err := escrow.OpenSnapshot(filepath.Join(dir, sealed.Name), key, opened)
	if err != nil {
		t.Fatal(err)
	}
	everyFileNamed(t, opened, names)
	if _, err := a.continuity.verifyKept(t.Context(), forked.Name, false, time.Now()); err == nil {
		t.Error("the snapshot that was not signed proves")
	}
	st, ok, err := a.store.ContinuityStatus()
	if err != nil || !ok || !st.Signed || !st.OnDemand {
		t.Fatalf("the last pass's snapshot could not be signed and no signed one was taken: %+v %v %v", st, ok, err)
	}
	if a.continuity.signingOwed(cfg) {
		t.Error("the start after it signs again")
	}
	if left, _ := filepath.Glob(filepath.Join(dir, SnapshotWorkPrefix+"*")); len(left) != 0 {
		t.Errorf("the signing left its working sets: %v", left)
	}
}

// WHERE THE LAST PASS'S SNAPSHOT IS SIGNED AT THE START, ITS STATEMENT SAYS
// SO, and nothing more is taken. A start that is SAFE or whose pass is
// switched off signs nothing.
func TestTheStatementIsMarkedSignedWhenItsSnapshotIsSigned(t *testing.T) {
	a, _, keyPath := maintApp(t)
	cfg := a.configSnapshot()
	dir := a.backupsDir(cfg)
	appendFixtureEvent(t, a, keyPath, "adopt_one")
	a.continuity.runMaintenance(t.Context())
	k := keptAt(t, dir, a.ledger.LastSeq(), false)
	asBeforeSigning(t, a, dir, k)
	statedBeforeSigning(t, a, k)

	off := false
	disabled := cfg
	disabled.Maintenance.Enabled = &off
	if a.continuity.signingOwed(disabled) {
		t.Error("a pass switched off signs at the start")
	}
	a.safe.Store(true)
	if a.continuity.signingOwed(cfg) {
		t.Error("a SAFE start signs")
	}
	a.safe.Store(false)

	signed, notSigned := a.continuity.signKeptAtStart(t.Context(), cfg)
	if len(signed) != 1 || signed[0] != k.Name || len(notSigned) != 0 {
		t.Fatalf("signed %v, not %v", signed, notSigned)
	}
	if st, ok, _ := a.store.ContinuityStatus(); !ok || !st.Signed || st.Snapshot != k.Name {
		t.Fatalf("the statement does not say its snapshot is signed: %+v", st)
	}
	if kept, _ := keptSnapshots(dir); len(kept) != 1 {
		t.Errorf("a snapshot was taken though the last one is signed: %v", kept)
	}
}
