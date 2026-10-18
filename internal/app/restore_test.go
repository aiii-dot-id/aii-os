package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/continuity"
	"github.com/aiii-dot-id/aii-os/internal/continuity/continuitytest"
	"github.com/aiii-dot-id/aii-os/internal/genesis"
	"github.com/aiii-dot-id/aii-os/internal/ledger"
	"github.com/aiii-dot-id/aii-os/internal/store"
	"github.com/aiii-dot-id/aii-os/internal/tools"
	"github.com/aiii-dot-id/aii-os/internal/witness"
)

// restore_test.go — restore on this machine (U7 phase B), through real
// boots: a person's request, the NEXT boot's restore, what is set aside and
// where, the undo, a crash at every step, and a source that does not prove.

type restoreRig struct {
	dir, keyPath, ledgerPath, dbPath string
	cfg                              *Config
}

func newRestoreRig(t *testing.T) *restoreRig {
	t.Helper()
	dir := t.TempDir()
	keyPath, ledgerPath, dbPath := birthFixture(t, dir, "Restorable")
	buildPriorProjection(t, ledgerPath, dbPath)
	return &restoreRig{dir: dir, keyPath: keyPath, ledgerPath: ledgerPath, dbPath: dbPath,
		cfg: safebootConfig(t, dir, "Restorable", keyPath, ledgerPath, dbPath)}
}

// newRestoreRigNamed is the same identity with its pair under names an
// operator chose: nothing about a restore may depend on the default ones.
func newRestoreRigNamed(t *testing.T, ledgerName, dbName string) *restoreRig {
	t.Helper()
	r := newRestoreRig(t)
	for _, mv := range [][2]string{{r.ledgerPath, filepath.Join(r.dir, ledgerName)}, {r.dbPath, filepath.Join(r.dir, dbName)}} {
		if err := os.Rename(mv[0], mv[1]); err != nil {
			t.Fatal(err)
		}
	}
	for _, side := range []string{"-wal", "-shm", "-journal"} { // what SQLite keeps beside a database
		os.Remove(r.dbPath + side)
	}
	r.ledgerPath, r.dbPath = filepath.Join(r.dir, ledgerName), filepath.Join(r.dir, dbName)
	r.cfg = safebootConfig(t, r.dir, "Restorable", r.keyPath, r.ledgerPath, r.dbPath)
	return r
}

// boot starts the identity and fails the test if it is SAFE.
func (r *restoreRig) boot(t *testing.T) *App {
	t.Helper()
	a := New(r.cfg)
	if err := startLiveForTest(a); err != nil {
		t.Fatalf("boot: %v", err)
	}
	if reason, safe := a.SafeMode(); safe {
		stopApp(t, a)
		t.Fatalf("the boot is SAFE: %s", reason)
	}
	return a
}

// lived boots, appends n records, takes a snapshot when asked, and stops.
func (r *restoreRig) lived(t *testing.T, n int, tag string, snapshot bool) (last uint64, snap string) {
	t.Helper()
	a := r.boot(t)
	defer stopApp(t, a)
	for i := 0; i < n; i++ {
		appendFixtureEvent(t, a, r.keyPath, fmt.Sprintf("exp_%s_%d", tag, i))
	}
	if snapshot {
		maintenancePass(t, a)
		kept := continuityDoor(a).View().Snapshots // newest first
		if len(kept) == 0 {
			t.Fatal("rig: no snapshot")
		}
		snap = kept[0].Name
	}
	return a.ledger.LastSeq(), snap
}

// THE JOURNEY ON THE HOST: a snapshot, three more records, a restore asked
// for and confirmed — and the NEXT boot puts the identity back at the
// snapshot, with what was live set aside WHOLE and findable; then the undo,
// which is the same restore, brings the three records back.
func TestARestoreIsPerformedByTheNextBootAndCanBeUndone(t *testing.T) {
	r := newRestoreRig(t)
	at, snap := r.lived(t, 2, "before", true)
	later, _ := r.lived(t, 3, "after", false)
	if later != at+3 {
		t.Fatalf("rig: %d then %d", at, later)
	}

	a := r.boot(t)
	plan, err := continuityDoor(a).RestorePlan(continuity.SourceSnapshot, snap)
	if err != nil || plan.RestoredTo != at || plan.LiveThrough != int64(later) || plan.SetAside != 3 || plan.Witnessed != -1 || plan.WitnessedSetAside != 0 {
		stopApp(t, a)
		t.Fatalf("the plan must say the exact cost: %+v %v", plan, err)
	}
	if err := continuityDoor(a).Restore(continuity.SourceSnapshot, snap, "not the name"); err == nil {
		stopApp(t, a)
		t.Fatal("a restore was asked for without the identity's name typed")
	}
	if err := continuityDoor(a).Restore(continuity.SourceSnapshot, "../"+snap, plan.ConfirmText); err == nil {
		stopApp(t, a)
		t.Fatal("a name with a path in it became a request")
	}
	if err := continuityDoor(a).Restore(continuity.SourceSnapshot, snap, plan.ConfirmText); err != nil {
		stopApp(t, a)
		t.Fatal(err)
	}
	if a.ledger.LastSeq() != later {
		t.Fatal("asking for a restore touched the running identity")
	}
	stopApp(t, a)

	a = r.boot(t)
	if a.ledger.LastSeq() != at {
		stopApp(t, a)
		t.Fatalf("THE NEXT BOOT DID NOT RESTORE: the record ends at %d, the snapshot at %d", a.ledger.LastSeq(), at)
	}
	sets := continuityDoor(a).View().SetAside
	if len(sets) != 1 || sets[0].WasThrough != later || sets[0].RestoredTo != at || sets[0].RestoredFrom != snap {
		stopApp(t, a)
		t.Fatalf("what was live must be set aside and say what it is: %+v", sets)
	}
	if !strings.HasPrefix(sets[0].Name, "Restorable-") || !strings.HasSuffix(sets[0].Name, fmt.Sprintf("-before-restore-to-seq%d", at)) || !filepath.IsAbs(sets[0].Path) {
		t.Errorf("a set kept aside is named for a person who will look for it, and the page is told where it is: %q at %q", sets[0].Name, sets[0].Path)
	}
	for _, name := range []string{"aii.db", "ledger.jsonl", continuity.RestoreRecordName} {
		if _, err := os.Stat(filepath.Join(sets[0].Path, name)); err != nil {
			t.Errorf("the set kept aside lacks %s", name)
		}
	}
	for _, leftover := range []string{continuity.RestoreRequestName, continuity.RestoreJournalName} {
		if _, err := os.Stat(filepath.Join(r.dir, leftover)); !os.IsNotExist(err) {
			t.Errorf("%s was left behind", leftover)
		}
	}
	if left, _ := filepath.Glob(filepath.Join(r.dir, continuity.RestoreStagePrefix+"*")); len(left) != 0 {
		t.Errorf("staging was left behind: %v", left)
	}
	// The restored identity goes on living: a record lands at at+1.
	appendFixtureEvent(t, a, r.keyPath, "exp_after_restore")
	if a.ledger.LastSeq() != at+1 {
		t.Fatalf("an append after the restore landed at %d", a.ledger.LastSeq())
	}

	// THE UNDO: put what was set aside back. Same request, same boot, same
	// proofs — and what is live now is set aside in its turn.
	if err := askRestore(t, a, continuity.SourceSetAside, sets[0].Name); err != nil {
		stopApp(t, a)
		t.Fatal(err)
	}
	stopApp(t, a)
	a = r.boot(t)
	defer stopApp(t, a)
	if a.ledger.LastSeq() != later {
		t.Fatalf("PUT THIS BACK did not bring the three records back: the record ends at %d, want %d", a.ledger.LastSeq(), later)
	}

	var asideNow, putBack int
	for _, set := range continuityDoor(a).View().SetAside {
		switch {
		case set.PutBackAt != "" && set.WasThrough == later:
			putBack++
		case set.PutBackAt == "" && set.WasThrough == at+1:
			asideNow++
		}
	}
	if asideNow != 1 || putBack != 1 {
		t.Fatalf("after the undo: %d set aside in its turn, %d marked put back, want 1 and 1: %+v", asideNow, putBack, continuityDoor(a).View().SetAside)
	}
}

// bootThroughChoice starts the identity the way a production entry point
// does: the shared boot decision first (chooseBoot), then the boot it chose.
// A recovery that can only be reached by a test helper is not reachable.
// before readies the App first (a step marker), as a test seam is set
// before the act it watches.
func (r *restoreRig) bootThroughChoice(t *testing.T, before ...func(*App)) (*App, error) {
	t.Helper()
	a := New(r.cfg)
	for _, ready := range before {
		ready(a)
	}
	err := a.StartEmbedded()
	if err != nil {
		stopApp(t, a)
	}
	return a, err
}

// rows is what replay cannot rebuild: the conversations and the lived clock
// a database holds, with the record it ends at.
func rows(t *testing.T, dbPath string) (uint64, int64, int64) {
	t.Helper()
	head, facts, err := store.InspectCopy(t.Context(), dbPath)
	if err != nil {
		t.Fatalf("%s: %v", dbPath, err)
	}
	return head.Seq, facts.LifetimeTicks, facts.Ephemeral["conversations"]
}

func TestARestoreKilledAtAnyStepPreservesBothSets(t *testing.T) {
	// THE STEPS COME FROM A RESTORE THAT ACTUALLY RAN: every move it makes,
	// not every move its journal lists (an optional file that was never
	// there is not a move), and the two boundaries around cleanup.
	var steps []string
	func() {
		r, _, _ := restoreAsked(t)
		done, err := r.bootThroughChoice(t, func(a *App) {
			a.continuity.RestoreStep = func(step string) { steps = append(steps, step) }
		})
		if err != nil {
			t.Fatalf("the restore this test interrupts does not run: %v", err)
		}
		stopApp(t, done)
	}()
	if len(steps) < 6 {
		t.Fatalf("a restore of %d steps is not this one: %v", len(steps), steps)
	}

	for _, dieAt := range steps {
		t.Run(dieAt, func(t *testing.T) {
			r, at, later := restoreAsked(t)
			wasLive, err := os.ReadFile(r.ledgerPath)
			if err != nil {
				t.Fatal(err)
			}

			killAt(t, r, dieAt)

			resumed, err := r.bootThroughChoice(t)
			if err != nil {
				t.Fatalf("killed at %s: the start a person makes did not come back: %v", dieAt, err)
			}
			stopped := false
			defer func() {
				if !stopped {
					stopApp(t, resumed)
				}
			}()
			if reason, safe := resumed.SafeMode(); safe {
				t.Fatalf("killed at %s: the resumed start is SAFE: %s", dieAt, reason)
			}
			if resumed.ledger.LastSeq() != at {
				t.Fatalf("killed at %s: the record ends at %d, want the snapshot's %d", dieAt, resumed.ledger.LastSeq(), at)
			}
			sets := continuityDoor(resumed).View().SetAside
			// The databases are read as finished images, so the identity is
			// stopped first: an open one keeps part of itself in a
			// write-ahead log, and an inspection that ignored that would be
			// reading less than is there.
			stopApp(t, resumed)
			stopped = true
			// WHAT IS LIVE IS THE SNAPSHOT'S, whole: its record, and the
			// conversations and lived time it captured.
			if seq, ticks, convs := rows(t, r.dbPath); seq != at || ticks != 3 || convs != 3 {
				t.Errorf("killed at %s: the live database holds record %d, %d ticks, %d conversations; the snapshot captured %d, 3, 3", dieAt, seq, ticks, convs, at)
			}
			// AND WHAT WAS SET ASIDE IS WHAT WAS LIVE, whole.
			if len(sets) != 1 {
				t.Fatalf("killed at %s: %d sets aside, want exactly one", dieAt, len(sets))
			}
			if seq, ticks, convs := rows(t, filepath.Join(sets[0].Path, "aii.db")); seq != later || ticks != 7 || convs != 8 {
				t.Errorf("killed at %s: THE PRESERVED ORIGINAL IS NOT WHAT WAS LIVE: it holds record %d, %d ticks, %d conversations; what was live held %d, 7 ticks, 8 occurrences (including the seeded historical maintenance notice)", dieAt, seq, ticks, convs, later)
			}
			if kept, err := os.ReadFile(filepath.Join(sets[0].Path, "ledger.jsonl")); err != nil || !bytes.Equal(kept, wasLive) {
				t.Errorf("killed at %s: the preserved record is not the bytes that were live (%v)", dieAt, err)
			}
			if _, err := os.Stat(filepath.Join(r.dir, continuity.RestoreJournalName)); !os.IsNotExist(err) {
				t.Errorf("killed at %s: the journal outlived the restore it finished", dieAt)
			}
		})
	}
}

// historicalMaintenanceNotice preserves the pre-upgrade notice case without
// relying on successful backups to produce new missing-escrow notices. Seed
// it after the snapshot so only the later live database holds the occurrence.
func historicalMaintenanceNotice(t *testing.T, a *App) {
	t.Helper()
	if err := a.store.AddOutboxNotice("maint_snapshot-unencrypted_20260919", "operator", "",
		"[maintenance] snapshot-unencrypted: historical pre-upgrade fixture notice", nil); err != nil {
		t.Fatal(err)
	}
}

// restoreAsked is the identity this test interrupts, built the same way for
// the run that DISCOVERS the steps and for every run that dies at one — so a
// move's index means the same move in both. Lived time and conversations on
// each side of the snapshot: the snapshot holds three, the identity goes on
// to seven plus the seeded historical notice, and a restore of that snapshot
// has been asked for and confirmed.
func restoreAsked(t *testing.T) (r *restoreRig, at, later uint64) {
	t.Helper()
	r = newRestoreRig(t)
	a := r.boot(t)
	live(t, a, 3)
	appendFixtureEvent(t, a, r.keyPath, "exp_before")
	maintenancePass(t, a)
	historicalMaintenanceNotice(t, a)
	kept := continuityDoor(a).View().Snapshots // newest first
	if len(kept) == 0 {
		stopApp(t, a)
		t.Fatal("rig: no snapshot")
	}
	snap := kept[0].Name
	at = a.ledger.LastSeq()
	live(t, a, 4)
	appendFixtureEvent(t, a, r.keyPath, "exp_after")
	later = a.ledger.LastSeq()
	if err := askRestore(t, a, continuity.SourceSnapshot, snap); err != nil {
		stopApp(t, a)
		t.Fatal(err)
	}
	stopApp(t, a)
	return r, at, later
}

// killAt runs one start that dies at the named step, and fails the test if
// that step never came: a crash test that did not crash proves nothing.
func killAt(t *testing.T, r *restoreRig, step string) {
	t.Helper()
	fired := false
	dying := New(r.cfg)
	dying.continuity.RestoreStep = func(s string) {
		if s == step {
			fired = true
			panic(errRestoreKilled)
		}
	}
	func() {
		defer func() {
			if v := recover(); v != nil {
				if v != errRestoreKilled {
					panic(v) // somebody else's panic is not this test's business
				}
				// What the dying start had opened goes with it, as a
				// process's files do: the restore's last steps are taken
				// with the record and the database open.
				stopApp(t, dying)
			}
		}()
		_ = dying.StartEmbedded()
		stopApp(t, dying)
	}()
	if !fired {
		t.Fatalf("the restore never reached %s: the interruption this case is about did not happen", step)
	}
}

var errRestoreKilled = errors.New("killed by the test at a restore step")

// A SOURCE THAT DOES NOT PROVE TOUCHES NOTHING. The request is withdrawn,
// the boot goes on with what it had, and the page is told why.
func TestASourceThatDoesNotProveIsNotRestored(t *testing.T) {
	r := newRestoreRig(t)
	_, snap := r.lived(t, 2, "before", true)
	later, _ := r.lived(t, 2, "after", false)
	a := r.boot(t)
	if err := askRestore(t, a, continuity.SourceSnapshot, snap); err != nil {
		t.Fatal(err)
	}
	backups := a.backupsDir(a.configSnapshot())
	stopApp(t, a)

	// A request made under another identity's key, for a snapshot that is
	// perfectly good: withdrawn, because the request is not this identity's.
	good, _ := os.ReadFile(filepath.Join(r.dir, continuity.RestoreRequestName))
	foreign, _ := json.Marshal(continuity.RestoreRequest{Source: continuity.SourceSnapshot, Name: snap, Identity: strings.Repeat("ab", 32)})
	if err := os.WriteFile(filepath.Join(r.dir, continuity.RestoreRequestName), foreign, 0o600); err != nil {
		t.Fatal(err)
	}
	a = r.boot(t)
	if a.ledger.LastSeq() != later {
		stopApp(t, a)
		t.Fatal("A REQUEST MADE FOR ANOTHER IDENTITY WAS PERFORMED")
	}
	stopApp(t, a)
	if err := os.WriteFile(filepath.Join(r.dir, continuity.RestoreRequestName), good, 0o600); err != nil {
		t.Fatal(err)
	}

	// EDITED AND RE-SUMMED: the database copy cut in half, and the checksum
	// list rewritten to match — every file "matches". What does not hold is
	// the restore, and that is what the proof before a restore is for. (Cut,
	// not one flipped byte: a byte can land where nothing reads it, and a
	// database that still restores to the same record is not one this proof
	// can tell from the original — the checksum list is as trustworthy as
	// the snapshot and no more.)
	db := filepath.Join(backups, snap, continuity.SnapshotDB)
	raw, err := os.ReadFile(db)
	if err != nil {
		t.Fatal(err)
	}
	raw = raw[:len(raw)/2]
	if err := os.WriteFile(db, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	sumsPath := filepath.Join(backups, snap, continuity.SnapshotSums)
	sums, err := os.ReadFile(sumsPath)
	if err != nil {
		t.Fatal(err)
	}
	h := sha256.Sum256(raw)
	var resummed []string
	for _, line := range strings.Split(strings.TrimRight(string(sums), "\n"), "\n") {
		if strings.HasSuffix(line, "  "+continuity.SnapshotDB) {
			line = hex.EncodeToString(h[:]) + "  " + continuity.SnapshotDB
		}
		resummed = append(resummed, line)
	}
	if err := os.WriteFile(sumsPath, []byte(strings.Join(resummed, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := continuitytest.CheckSums(filepath.Join(backups, snap)); err != nil {
		t.Fatalf("rig: the re-summed snapshot must pass its own checksum list: %v", err)
	}

	a = r.boot(t)
	defer stopApp(t, a)
	if a.ledger.LastSeq() != later {
		t.Fatalf("A SNAPSHOT THAT DOES NOT PROVE WAS RESTORED: the record ends at %d, was %d", a.ledger.LastSeq(), later)
	}
	if sets := continuityDoor(a).View().SetAside; len(sets) != 0 {
		t.Errorf("something was set aside for a restore that was not performed: %+v", sets)
	}
	if _, err := os.Stat(filepath.Join(r.dir, continuity.RestoreRequestName)); !os.IsNotExist(err) {
		t.Error("the request was not withdrawn: every boot would try again")
	}
	if f := continuityDoor(a).View().RestoreFailed; !strings.Contains(f, "does not prove") || !strings.Contains(f, "A restore from "+snap+" was asked for") {
		t.Fatalf("the page is owed why: %q", f)
	}
	if v := continuityDoor(a).View(); !strings.Contains(v.RestoreFailed, "NOT performed") || !strings.Contains(v.RestoreFailed, "Nothing was touched") {
		t.Errorf("the view does not say so: %q", v.RestoreFailed)
	}
}

// THE COUNT THAT MATTERS IS THE WITNESS'S BOOKMARK AGAINST THE RESTORED END:
// past it, the witness will call the restore a rollback, and the plan says by
// exactly how many records; at or before it, the witness notices nothing.
func TestTheRestorePlanCountsWhatTheWitnessHasAttested(t *testing.T) {
	r := newRestoreRig(t)
	at, snap := r.lived(t, 2, "before", true)
	later, _ := r.lived(t, 4, "after", false)
	a := r.boot(t)
	defer stopApp(t, a)
	for _, tc := range []struct {
		bookmark          uint64
		wantWitnessedAway int64
	}{{later - 1, int64(later-1) - int64(at)}, {at, 0}, {at - 1, 0}} {
		echoTail(t, r.dir, tc.bookmark, "sha256:"+strings.Repeat("a", 64))
		plan, err := continuityDoor(a).RestorePlan(continuity.SourceSnapshot, snap)
		if err != nil || plan.Witnessed != int64(tc.bookmark) || plan.WitnessedSetAside != tc.wantWitnessedAway || plan.SetAside != int64(later-at) {
			t.Errorf("bookmark %d, restored end %d, live %d: %+v %v", tc.bookmark, at, later, plan, err)
		}
	}
	os.Remove(filepath.Join(r.dir, "witness-tail.json"))
}

// THE IDENTITY CAN READ THAT IT WAS RESTORED — one line, while it is recent —
// and has no way to ask for one.
func TestTheIdentityReadsThatItWasRestored(t *testing.T) {
	r := newRestoreRig(t)
	at, snap := r.lived(t, 2, "before", true)
	later, _ := r.lived(t, 2, "after", false)
	a := r.boot(t)
	if out, _ := a.continuity.ReadForTest("", time.Now()); strings.Contains(out, "restored:") {
		t.Fatalf("no restore has happened, and the read says one did:\n%s", out)
	}
	if err := askRestore(t, a, continuity.SourceSnapshot, snap); err != nil {
		t.Fatal(err)
	}
	stopApp(t, a)
	a = r.boot(t)
	defer stopApp(t, a)
	out, err := a.continuity.ReadForTest("", time.Now())
	if err != nil || !strings.Contains(out, fmt.Sprintf("put back to %d", at)) || !strings.Contains(out, fmt.Sprintf("through record %d", later)) {
		t.Fatalf("the read after a restore: %v\n%s", err, out)
	}
	if strings.Contains(out, r.dir) {
		t.Errorf("the identity's read carries a path:\n%s", out)
	}
	if old, _ := a.continuity.ReadForTest("", time.Now().Add(continuity.RestoreSaidFor+time.Hour)); strings.Contains(old, "restored:") {
		t.Error("the line outlives its thirty days")
	}
}

// FROM SAFE, WHICH IS WHERE A PERSON WITH A DAMAGED RECORD IS. The record
// does not verify, the boot is SAFE, and the page there can still ask for a
// restore: the next boot puts a snapshot in place, sets the damaged pair
// aside — evidence, not debris — and comes up healthy.
func TestARestoreAskedForFromSafeBringsTheIdentityBack(t *testing.T) {
	r := newRestoreRig(t)
	at, snap := r.lived(t, 2, "before", true)
	r.lived(t, 2, "after", false)
	// Damage: a line that is not a record, appended to the tail.
	f, err := os.OpenFile(r.ledgerPath, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString("{\"not\":\"a record\"}\n")
	f.Close()

	safe := New(r.cfg)
	if err := startLiveForTest(safe); err != nil {
		t.Fatalf("a damaged record must boot SAFE, not die: %v", err)
	}
	if _, isSafe := safe.SafeMode(); !isSafe {
		stopApp(t, safe)
		t.Fatal("rig: the damaged record did not boot SAFE")
	}
	plan, err := continuityDoor(safe).RestorePlan(continuity.SourceSnapshot, snap)
	if err != nil || plan.RestoredTo != at || plan.ConfirmText == "" {
		stopApp(t, safe)
		t.Fatalf("the plan in SAFE: %+v %v", plan, err)
	}
	if err := continuityDoor(safe).Restore(continuity.SourceSnapshot, snap, plan.ConfirmText); err != nil {
		stopApp(t, safe)
		t.Fatalf("A RESTORE CANNOT BE ASKED FOR FROM SAFE: %v", err)
	}
	stopApp(t, safe)

	a := r.boot(t)
	defer stopApp(t, a)
	if a.ledger.LastSeq() != at {
		t.Fatalf("after the restore the record ends at %d, want the snapshot's %d", a.ledger.LastSeq(), at)
	}
	sets := continuityDoor(a).View().SetAside
	if len(sets) != 1 {
		t.Fatalf("the damaged pair must be set aside: %+v", sets)
	}
	kept, err := os.ReadFile(filepath.Join(sets[0].Path, "ledger.jsonl"))
	if err != nil || !strings.Contains(string(kept), `{"not":"a record"}`) {
		t.Errorf("the damaged record was not kept as it was: %v", err)
	}
}

// WHAT ONLY THE CHECKSUM LIST CAN KNOW: a snapshot holding a file its list
// does not name is not the snapshot that was published, and is not restored.
func TestASnapshotHoldingWhatItsListDoesNotNameIsNotRestored(t *testing.T) {
	r := newRestoreRig(t)
	_, snap := r.lived(t, 2, "before", true)
	later, _ := r.lived(t, 1, "after", false)
	a := r.boot(t)
	if err := askRestore(t, a, continuity.SourceSnapshot, snap); err != nil {
		t.Fatal(err)
	}
	stray := filepath.Join(a.backupsDir(a.configSnapshot()), snap, "stray.txt")
	stopApp(t, a)
	if err := os.WriteFile(stray, []byte("not part of what was published"), 0o600); err != nil {
		t.Fatal(err)
	}
	a = r.boot(t)
	defer stopApp(t, a)
	if a.ledger.LastSeq() != later {
		t.Fatalf("a snapshot holding a file its checksum list does not name was restored: the record ends at %d, was %d", a.ledger.LastSeq(), later)
	}
	if f := continuityDoor(a).View().RestoreFailed; !strings.Contains(f, "stray.txt") {
		t.Errorf("the page is owed why: %q", f)
	}
}

// WHICH TAIL GOES IN PLACE: THE SOURCE'S OWN. The live tail names a record
// the restored ledger no longer holds; kept in place it would make the boot
// after every restore behind a witnessed record SAFE, saying TRUNCATION — a
// restore nobody could use. It is set aside with the rest, as evidence.
func TestARestorePutsTheSourcesOwnWitnessTailInPlace(t *testing.T) {
	r := newRestoreRig(t)
	tailFor := func(seq uint64) string {
		t.Helper()
		events, err := ledger.ReadAll(r.ledgerPath)
		if err != nil {
			t.Fatal(err)
		}
		for _, evt := range events {
			if evt.Seq == seq {
				return evt.EntryHash()
			}
		}
		t.Fatalf("rig: no record %d", seq)
		return ""
	}
	// The witness attested record `at`, and the snapshot carries that tail.
	a := r.boot(t)
	appendFixtureEvent(t, a, r.keyPath, "exp_before")
	at := a.ledger.LastSeq()
	stopApp(t, a)
	echoTail(t, r.dir, at, tailFor(at))
	a = r.boot(t)
	maintenancePass(t, a)
	kept := continuityDoor(a).View().Snapshots // newest first
	snap := kept[0].Name
	// Life goes on, and the witness attests a LATER record: the live tail.
	appendFixtureEvent(t, a, r.keyPath, "exp_after_1")
	appendFixtureEvent(t, a, r.keyPath, "exp_after_2")
	later := a.ledger.LastSeq()
	stopApp(t, a)
	echoTail(t, r.dir, later, tailFor(later))

	a = r.boot(t)
	plan, err := continuityDoor(a).RestorePlan(continuity.SourceSnapshot, snap)
	if err != nil || plan.WitnessedSetAside != int64(later-at) {
		stopApp(t, a)
		t.Fatalf("the plan must count the attested records being set aside: %+v %v", plan, err)
	}
	if err := continuityDoor(a).Restore(continuity.SourceSnapshot, snap, plan.ConfirmText); err != nil {
		stopApp(t, a)
		t.Fatal(err)
	}
	stopApp(t, a)

	a = r.boot(t) // fails the test if this boot is SAFE
	defer stopApp(t, a)
	if a.ledger.LastSeq() != at {
		t.Fatalf("the record ends at %d, want %d", a.ledger.LastSeq(), at)
	}
	inPlace, err := witness.ReadLocalTail(r.dir)
	if err != nil || inPlace == nil || uint64(inPlace.LedgerOrdinal) != at {
		t.Fatalf("the tail in place must be the source's own (record %d): %+v %v", at, inPlace, err)
	}
	sets := continuityDoor(a).View().SetAside
	if len(sets) != 1 {
		t.Fatalf("the set kept aside must say how far the witness had attested: %+v", sets)
	}
	var held continuity.RestoreRecord // the set's own RESTORE.json: the page's list does not carry it whole
	if raw, err := os.ReadFile(filepath.Join(sets[0].Path, continuity.RestoreRecordName)); err == nil {
		_ = json.Unmarshal(raw, &held)
	}
	if held.Witnessed != int64(later) {
		t.Fatalf("the set kept aside must say how far the witness had attested: %+v %+v", sets, held)
	}
	aside, err := witness.ReadLocalTail(sets[0].Path)
	if err != nil || aside == nil || uint64(aside.LedgerOrdinal) != later {
		t.Fatalf("the live tail must be kept, as evidence, with what was set aside: %+v %v", aside, err)
	}
}

func TestASetKeptAsideCarriesItsSealedHistoryAndKeys(t *testing.T) {
	f := newSealedFixture(t)
	sealedThrough := f.bootAndAnchor(t) // segment 1..N, under a persisted key

	// A snapshot of the sealed identity, then more life and a SECOND seal:
	// the two sets share the first segment and only the live one has the
	// second.
	a := New(f.cfg)
	if err := startLiveForTest(a); err != nil {
		t.Fatal(err)
	}
	live(t, a, 2)
	maintenancePass(t, a)
	kept := continuityDoor(a).View().Snapshots // newest first
	snap := kept[0].Name
	at := a.ledger.LastSeq()
	appendFixtureEvent(t, a, f.keyPath, "exp_after_the_snapshot")
	if err := a.anchorer.CheckAndAnchor(); err != nil {
		t.Fatalf("the second anchor: %v", err)
	}
	later, sealedAgain := a.ledger.LastSeq(), a.ledger.SealedSeq()
	if sealedAgain <= sealedThrough {
		t.Fatalf("rig: the second seal did not happen (%d then %d)", sealedThrough, sealedAgain)
	}
	segments, _ := filepath.Glob(filepath.Join(f.dir, "segment-*.jsonl.gz"))
	if len(segments) < 2 {
		t.Fatalf("rig: %d segments, want the shared one and the later one", len(segments))
	}
	if err := askRestore(t, a, continuity.SourceSnapshot, snap); err != nil {
		t.Fatal(err)
	}
	stopApp(t, a)

	restored := New(f.cfg)
	if err := restored.StartEmbedded(); err != nil {
		t.Fatalf("the restore: %v", err)
	}
	if reason, safe := restored.SafeMode(); safe {
		stopApp(t, restored)
		t.Fatalf("the restored identity is SAFE: %s", reason)
	}
	if restored.ledger.LastSeq() != at {
		stopApp(t, restored)
		t.Fatalf("the record ends at %d, want the snapshot's %d", restored.ledger.LastSeq(), at)
	}
	sets := continuityDoor(restored).View().SetAside
	if len(sets) != 1 {
		stopApp(t, restored)
		t.Fatalf("%d sets aside, want one", len(sets))
	}
	aside := sets[0].Path

	// IT PROVES ITSELF, where it lies, with nothing from the directory it
	// came out of: its own chain under its own keys, held to its own tail.
	beside, err := besideWhereItLies(restored.configSnapshot(), aside)
	if err != nil {
		stopApp(t, restored)
		t.Fatalf("the set kept aside carries no witness keys: %v", err)
	}
	n, fp, err := genesis.VerifyHeld(filepath.Join(aside, "ledger.jsonl"), beside, nil)
	if err != nil || uint64(n) != later || fp != restored.keyPair.Fingerprint() {
		stopApp(t, restored)
		t.Fatalf("THE SET KEPT ASIDE DOES NOT VERIFY ON ITS OWN: %d records, %v", n, err)
	}
	for _, seg := range segments {
		if _, err := os.Stat(filepath.Join(aside, filepath.Base(seg))); err != nil {
			t.Errorf("the set lacks %s, which its own record lives in: %v", filepath.Base(seg), err)
		}
	}
	// And the restored identity kept the keys and segments IT needs.
	if _, err := verifiesWhereItLies(restored.configSnapshot(), restored.ledger.Path()); err != nil {
		t.Errorf("the restored identity does not verify: %v", err)
	}

	// AND IT GOES BACK: the same restore, the other way.
	if err := askRestore(t, restored, continuity.SourceSetAside, sets[0].Name); err != nil {
		stopApp(t, restored)
		t.Fatal(err)
	}
	stopApp(t, restored)
	back := New(f.cfg)
	if err := back.StartEmbedded(); err != nil {
		t.Fatalf("PUT THIS BACK: %v", err)
	}
	defer stopApp(t, back)
	if reason, safe := back.SafeMode(); safe {
		t.Fatalf("the identity put back is SAFE: %s", reason)
	}
	if back.ledger.LastSeq() != later || back.ledger.SealedSeq() != sealedAgain {
		t.Fatalf("put back, the record ends at %d sealed through %d; it was %d and %d", back.ledger.LastSeq(), back.ledger.SealedSeq(), later, sealedAgain)
	}
	if _, err := verifiesWhereItLies(back.configSnapshot(), back.ledger.Path()); err != nil {
		t.Errorf("the identity put back does not verify: %v", err)
	}
}

func TestARestoreBehindTheWitnessEndsInSafeNotInUnwitnessedRunning(t *testing.T) {
	f := newSealedFixture(t)
	a := New(f.cfg)
	if err := startLiveForTest(a); err != nil {
		t.Fatal(err)
	}
	// AN IDENTITY THAT HAS BEEN ANCHORING, which is the identity this case is
	// about: it anchors, and only then is the snapshot taken. The witness
	// bookmarks an identity by a key ENVELOPE the store holds, so a snapshot
	// older than the first anchor carries no envelope and the restored
	// identity mints a new one — the witness then sees a newcomer, not a
	// rollback, and accepts it. That is a real gap in the witness protocol,
	// reported to the operator for the witness work he has taken; it is not
	// what this test is about and it is not blessed here.
	if err := a.anchorer.CheckAndAnchor(); err != nil {
		stopApp(t, a)
		t.Fatalf("the first anchor: %v", err)
	}
	maintenancePass(t, a)
	kept := continuityDoor(a).View().Snapshots // newest first
	if len(kept) == 0 {
		stopApp(t, a)
		t.Fatal("rig: no snapshot")
	}
	snap := kept[0].Name
	at := a.ledger.LastSeq()
	for i := 0; i < 3; i++ {
		appendFixtureEvent(t, a, f.keyPath, fmt.Sprintf("exp_after_the_snapshot_%d", i))
	}
	if err := a.anchorer.CheckAndAnchor(); err != nil {
		stopApp(t, a)
		t.Fatalf("the anchor: %v", err)
	}
	// WHAT THE WITNESS HOLDS IS ASKED OF THE WITNESS. An anchor pass that
	// returns no error may not have anchored at all, and the anchorer's own
	// background pass may have anchored instead: a number worked out from
	// this side's ledger is a guess, and the gate caught it as one.
	witnessed, identities := f.witness.Holds()
	if identities != 1 {
		stopApp(t, a)
		t.Fatalf("rig: the witness has bookmarked %d identities, so there is no one rollback to refuse", identities)
	}
	// The identity will go one record past the snapshot and still be BEHIND
	// the witness: that is the case this test is about, and it is stated
	// rather than arrived at.
	if int64(at)+1 > witnessed {
		stopApp(t, a)
		t.Fatalf("rig: restored to %d and one record on is %d, which the witness at %d would not refuse", at, at+1, witnessed)
	}
	plan, err := continuityDoor(a).RestorePlan(continuity.SourceSnapshot, snap)
	if err != nil {
		t.Fatal(err)
	}
	if plan.WitnessedSetAside == 0 {
		t.Fatalf("rig: the plan says the witness will notice nothing: %+v", plan)
	}
	if err := continuityDoor(a).Restore(continuity.SourceSnapshot, snap, plan.ConfirmText); err != nil {
		t.Fatal(err)
	}
	stopApp(t, a)

	restored := New(f.cfg)
	if err := restored.StartEmbedded(); err != nil {
		t.Fatal(err)
	}
	defer stopApp(t, restored)
	if reason, safe := restored.SafeMode(); safe {
		t.Fatalf("the restore itself must not be SAFE: %s", reason)
	}
	// Life goes on until the anchorer speaks to the witness.
	appendFixtureEvent(t, restored, f.keyPath, "exp_after_the_restore")
	if int64(restored.ledger.LastSeq()) > witnessed {
		t.Fatalf("rig: the restored record is at %d, past the witness at %d", restored.ledger.LastSeq(), witnessed)
	}

	_ = restored.anchorer.CheckAndAnchor()
	// THE SAME IDENTITY, to the witness: one row, not a newcomer's second.
	if _, identities := f.witness.Holds(); identities != 1 {
		t.Fatalf("the restored identity reached the witness as %d identities: a rollback it cannot see is not a rollback it refuses", identities)
	}
	var reason string
	var safe bool
	for wait := 0; wait < 200; wait++ {
		if reason, safe = restored.SafeMode(); safe {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	if !safe {
		t.Fatalf("A RESTORE BEHIND THE WITNESSED RECORD LEFT THE IDENTITY RUNNING: the record is at %d, the witness holds %d, and the identity is not SAFE", restored.ledger.LastSeq(), witnessed)
	}
	if !strings.Contains(reason, "rollback") || !strings.HasPrefix(reason, safePostures[safeWitnessSays].what+": ") {
		t.Errorf("the SAFE reason does not name what happened: %q", reason)
	}

	// The page's words are held to that outcome, and to nothing it cannot do.
	said := restorePlanSaysAboutTheWitness(t)
	for _, want := range []string{"SAFE", "frozen"} {
		if !strings.Contains(said, want) {
			t.Errorf("the confirm screen does not say %q happens: %q", want, said)
		}
	}
	for _, wrong := range []string{"runs normally", "simply not witnessed"} {
		if strings.Contains(said, wrong) {
			t.Errorf("the confirm screen still promises %q: %q", wrong, said)
		}
	}
}

// besideWhereItLies is the witness's side of verifying the record in dir —
// the heads under the keys there, held to the tail there — under the root
// cfg names, as a snapshot's proof and SAFE's daily walk load it.
func besideWhereItLies(cfg Config, dir string) (*witness.Beside, error) {
	platform, err := witnessPlatform(cfg)
	if err != nil {
		return nil, err
	}
	return witness.LoadBeside(dir, platform)
}

// verifiesWhereItLies walks the record at ledgerPath held to what lies
// beside it, as SAFE's daily walk of the live record does.
func verifiesWhereItLies(cfg Config, ledgerPath string) (int, error) {
	beside, err := besideWhereItLies(cfg, filepath.Dir(ledgerPath))
	if err != nil {
		return 0, err
	}
	n, _, err := genesis.VerifyHeld(ledgerPath, beside, nil)
	if err != nil {
		return n, fmt.Errorf("%w; witness: %s", err, beside.Summary())
	}
	return n, nil
}

// restorePlanSaysAboutTheWitness reads the shipped page's own sentence about
// a witness conflict, so the promise and the proof above cannot drift apart.
func restorePlanSaysAboutTheWitness(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "dashboard", "static", "views", "backups.js"))
	if err != nil {
		t.Fatal(err)
	}
	const mark = "data-plan-anchoring"
	i := strings.Index(string(raw), mark)
	if i < 0 {
		t.Fatal("the page no longer marks what it says about the witness")
	}
	rest := string(raw)[i:]
	return rest[:strings.Index(rest, "</div>")]
}

// A RESTORE THAT CANNOT FINISH DOES NOT BECOME AN ORDINARY BOOT. The pair is
// half in place: the record is not opened over it, the person is told, and
// the journal stays so the next start goes on from where this one stopped.
func TestARestoreThatCannotFinishBootsSafeAndKeepsItsJournal(t *testing.T) {
	r, _, later := restoreAsked(t)

	// The staged record disappears between the preservation moves and the
	// installation that needs it: what a half-written disk looks like.
	a := New(r.cfg)
	a.continuity.RestoreStep = func(step string) {
		if step == "moved:0" {
			staging, _ := filepath.Glob(filepath.Join(r.dir, continuity.RestoreStagePrefix+"*"))
			for _, s := range staging {
				if err := os.Remove(filepath.Join(s, "set", "ledger.jsonl")); err != nil {
					t.Errorf("rig: %v", err)
				}
			}
		}
	}
	err := a.StartEmbedded()
	a.continuity.RestoreStep = nil
	if err != nil {
		t.Fatalf("the start must come back SAFE, not die: %v", err)
	}
	defer stopApp(t, a)
	reason, safe := a.SafeMode()
	if !safe {
		t.Fatalf("A HALF-INSTALLED PAIR WAS OPENED BY AN ORDINARY BOOT: %q", reason)
	}
	for _, want := range []string{"did not finish", "ledger.jsonl"} {
		if !strings.Contains(reason, want) {
			t.Errorf("the reason does not say %q: %q", want, reason)
		}
	}
	if _, err := os.Stat(filepath.Join(r.dir, continuity.RestoreJournalName)); err != nil {
		t.Errorf("the journal must stay for the next start: %v", err)
	}
	// And what was live is still whole where the restore put it.
	sets := continuityDoor(a).View().SetAside
	if len(sets) != 1 {
		t.Fatalf("%d sets aside, want one", len(sets))
	}
	if seq, _, convs := rows(t, filepath.Join(sets[0].Path, "aii.db")); seq != later || convs != 8 {
		t.Errorf("the preserved original: record %d, %d conversations; want %d and 8 occurrences (including the seeded historical maintenance notice)", seq, convs, later)
	}
}

// journalOf is the journal a real restore wrote, as it reads.
func journalOf(t *testing.T, r *restoreRig) continuity.RestoreJournal {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(r.dir, continuity.RestoreJournalName))
	if err != nil {
		t.Fatal(err)
	}
	var j continuity.RestoreJournal
	if err := json.Unmarshal(raw, &j); err != nil {
		t.Fatal(err)
	}
	return j
}

// loseStaging removes the staged files a journal was written for, as a
// person or a cleaner tidying hidden folders would.
func loseStaging(t *testing.T, r *restoreRig) {
	t.Helper()
	staging, _ := filepath.Glob(filepath.Join(r.dir, continuity.RestoreStagePrefix+"*"))
	if len(staging) == 0 {
		t.Fatal("rig: no staging directory to lose")
	}
	for _, s := range staging {
		if err := os.RemoveAll(s); err != nil {
			t.Fatal(err)
		}
	}
}

// firstInstall is the index of the journal's first install: killed there,
// every original is aside and exactly one staged file is in place.
func firstInstall(t *testing.T, j continuity.RestoreJournal) int {
	t.Helper()
	for i, m := range j.Moves {
		if m.Kind == continuity.MoveInstall {
			if i+1 == len(j.Moves) {
				t.Fatal("rig: the first install is the last move; nothing would be left to lose")
			}
			return i
		}
	}
	t.Fatal("rig: the journal installs nothing")
	return 0
}

// withdrawnWhole is what a withdrawn restore leaves, whether or not it had
// begun: the identity starts on what was live — its record, byte for byte,
// verified and opened, and the conversations and lived time replay cannot
// rebuild — the page says why, nothing is listed as a set aside, and no
// journal, request or staging remains to be done again.
func withdrawnWhole(t *testing.T, r *restoreRig, wasLive []byte, later uint64, says ...string) {
	t.Helper()
	a, err := r.bootThroughChoice(t)
	if err != nil {
		t.Fatalf("the start a person makes did not come back: %v", err)
	}
	if reason, safe := a.SafeMode(); safe {
		stopApp(t, a)
		t.Fatalf("A RESTORE WHOSE STAGED FILES ARE GONE LEFT THE IDENTITY SAFE: %s", reason)
	}
	seq := a.ledger.LastSeq()
	view := continuityDoor(a).View()
	stopApp(t, a)
	if seq != later {
		t.Fatalf("the record ends at %d, want what was live, %d", seq, later)
	}
	for _, want := range append([]string{"NOT performed", "gone from"}, says...) {
		if !strings.Contains(view.RestoreFailed, want) {
			t.Errorf("the page does not say %q: %q", want, view.RestoreFailed)
		}
	}
	if len(view.SetAside) != 0 {
		t.Errorf("a withdrawn restore is listed as a set kept aside: %+v", view.SetAside)
	}
	for _, leftover := range []string{continuity.RestoreJournalName, continuity.RestoreRequestName} {
		if fileExists(filepath.Join(r.dir, leftover)) {
			t.Errorf("%s outlived the withdrawal: every start would try again", leftover)
		}
	}
	if left, _ := filepath.Glob(filepath.Join(r.dir, continuity.RestoreStagePrefix+"*")); len(left) != 0 {
		t.Errorf("staging was left behind: %v", left)
	}
	if now, err := os.ReadFile(r.ledgerPath); err != nil || !bytes.Equal(now, wasLive) {
		t.Errorf("the record in place is not the bytes that were live (%v)", err)
	}
	if got, ticks, convs := rows(t, r.dbPath); got != later || ticks != 7 || convs != 8 {
		t.Errorf("the database in place holds record %d, %d ticks, %d conversations; what was live held %d, 7 ticks, 8 occurrences (including the seeded historical maintenance notice)", got, ticks, convs, later)
	}
}

func TestARestoreWhoseStagingIsGoneBeforeItsFirstMoveIsWithdrawn(t *testing.T) {
	r, _, later := restoreAsked(t)
	wasLive, err := os.ReadFile(r.ledgerPath)
	if err != nil {
		t.Fatal(err)
	}
	killAt(t, r, "journalled") // a real journal, written by a real restore
	aside := journalOf(t, r).Aside
	loseStaging(t, r)

	withdrawnWhole(t, r, wasLive, later, "Nothing was touched")
	if _, err := os.Stat(aside); !os.IsNotExist(err) {
		t.Errorf("a restore that moved nothing left a set-aside folder: %v", err)
	}
}

// AND ONE THAT HAD BEGUN IS PUT BACK, NOT DELETED. A start died after every
// original went aside and one staged file went into place; then the staging
// was lost. The next start puts what it had installed into not-completed/
// inside the set-aside folder, puts every original back in its place, and
// withdraws the restore — the page saying what was rolled back and where
// the file it had not completed is kept.
func TestARestoreInterruptedAndThenLosingItsStagingIsRolledBack(t *testing.T) {
	r, at, later := restoreAsked(t)
	wasLive, err := os.ReadFile(r.ledgerPath)
	if err != nil {
		t.Fatal(err)
	}
	killAt(t, r, "journalled")
	j := journalOf(t, r)
	fi := firstInstall(t, j)
	killAt(t, r, fmt.Sprintf("moved:%d", fi)) // the start that resumes it dies after its first install
	if fileExists(r.ledgerPath) && j.Moves[fi].To != r.ledgerPath {
		t.Fatal("rig: the live record is still in place; the originals did not go aside")
	}
	loseStaging(t, r)

	notCompleted := filepath.Join(j.Aside, "not-completed")
	withdrawnWhole(t, r, wasLive, later, "rolled back", notCompleted)
	// NOTHING IS DELETED: what it had put in place is kept, and is what it
	// put there — the snapshot's.
	rel, err := filepath.Rel(r.dir, j.Moves[fi].To)
	if err != nil {
		t.Fatal(err)
	}
	keptAt := filepath.Join(notCompleted, rel)
	if !fileExists(keptAt) {
		t.Fatalf("THE FILE THE RESTORE HAD PUT IN PLACE IS GONE: it is not at %s", keptAt)
	}
	if j.Moves[fi].To == r.dbPath {
		if seq, ticks, convs := rows(t, keptAt); seq != at || ticks != 3 || convs != 3 {
			t.Errorf("what is kept as not completed is not the snapshot's database: record %d, %d ticks, %d conversations", seq, ticks, convs)
		}
	}
	// The set-aside folder is not what was live before a restore: it holds
	// what was not completed and the record that says so, and no RESTORE.json.
	held, _ := os.ReadDir(j.Aside)
	var names []string
	for _, e := range held {
		names = append(names, e.Name())
	}
	if strings.Join(names, ",") != "not-completed,"+continuity.RestoreFailedName {
		t.Errorf("after the way back the set-aside folder holds %v, want only not-completed/ and its record", names)
	}
}

func TestARolledBackRestoreLeavesNoRecordThatItFinished(t *testing.T) {
	f := newSealedFixture(t)
	f.bootAndAnchor(t)
	r := &restoreRig{dir: f.dir, keyPath: f.keyPath, ledgerPath: f.ledgerPath, dbPath: f.dbPath, cfg: f.cfg}
	a := New(f.cfg)
	if err := startLiveForTest(a); err != nil {
		t.Fatal(err)
	}
	maintenancePass(t, a)
	kept := continuityDoor(a).View().Snapshots // newest first
	if len(kept) == 0 {
		stopApp(t, a)
		t.Fatal("rig: no snapshot")
	}
	appendFixtureEvent(t, a, r.keyPath, "exp_after")
	// What waits in the LIVE database, written after the snapshot: a
	// restore that is rolled back leaves it waiting.
	a.timeFac.Stop()
	contact(t, a, Contact{Name: "friend", Channel: "primary", Address: "friend@example.test"})
	if err := a.store.AddOutboxMessage("msg_live", "peer", "friend", "written after the snapshot", nil); err != nil {
		t.Fatal(err)
	}
	later := a.ledger.LastSeq()
	if err := askRestore(t, a, continuity.SourceSnapshot, kept[0].Name); err != nil {
		stopApp(t, a)
		t.Fatal(err)
	}
	stopApp(t, a)
	wasLive, err := os.ReadFile(r.ledgerPath)
	if err != nil {
		t.Fatal(err)
	}

	killAt(t, r, "journalled")
	j := journalOf(t, r)
	killAt(t, r, "cleanup") // every move made and both records written; the journal not yet removed
	settle := filepath.Join(r.dir, continuity.RestoreSettleName)
	record := filepath.Join(j.Aside, continuity.RestoreRecordName)
	if !fileExists(settle) || !fileExists(record) || !fileExists(filepath.Join(r.dir, continuity.RestoreJournalName)) {
		t.Fatal("rig: the restore did not die in its last step: the settlement, the set-aside's record and the journal are not all there")
	}
	loseStaging(t, r)

	b, err := r.bootThroughChoice(t)
	if err != nil {
		t.Fatalf("the start a person makes did not come back: %v", err)
	}
	reason, safe := b.SafeMode()
	seq := b.ledger.LastSeq()
	var row store.OutboxMessage
	var restored store.Restore
	var recorded bool
	var rerr error
	if !safe {
		row = deliveryRow(t, b, "msg_live")
		restored, recorded, rerr = b.store.LastRestore()
	}
	view := continuityDoor(b).View()
	stopApp(t, b)
	if safe {
		t.Fatalf("the start after the way back is SAFE: %s", reason)
	}
	if now, err := os.ReadFile(r.ledgerPath); seq != later || err != nil || !bytes.Equal(now, wasLive) {
		t.Fatalf("rig: the restore was not rolled back: the record ends at %d, what was live ended at %d (%v)", seq, later, err)
	}
	if !strings.Contains(view.RestoreFailed, "rolled back") {
		t.Errorf("the page does not say the restore was rolled back: %q", view.RestoreFailed)
	}

	if row.Parked || strings.Contains(row.LastError, "restored from") {
		t.Errorf("A MESSAGE WAITING IN THE DATABASE THAT WAS PUT BACK IS PARKED AS POSSIBLY SENT, though no restore brought it: %+v", row)
	}
	if rerr != nil || recorded {
		t.Errorf("a restore that was rolled back is recorded as performed: %+v (%v)", restored, rerr)
	}
	if view.Restored != "" {
		t.Errorf("the page says a restore was performed: %q", view.Restored)
	}
	if fileExists(settle) {
		t.Errorf("%s outlived the restore that was rolled back", continuity.RestoreSettleName)
	}
	if fileExists(record) {
		t.Errorf("the set-aside folder holds only what was not completed and still carries %s", continuity.RestoreRecordName)
	}
	if len(view.SetAside) != 0 {
		t.Errorf("a restore that was rolled back is listed as a set that can be restored: %+v", view.SetAside)
	}
}

func TestARolledBackUndoTakesBackItsMarkOnTheSetItCameFrom(t *testing.T) {
	f := newSealedFixture(t)
	f.bootAndAnchor(t)
	r := &restoreRig{dir: f.dir, keyPath: f.keyPath, ledgerPath: f.ledgerPath, dbPath: f.dbPath, cfg: f.cfg}
	a := New(f.cfg)
	if err := startLiveForTest(a); err != nil {
		t.Fatal(err)
	}
	maintenancePass(t, a)
	kept := continuityDoor(a).View().Snapshots // newest first
	if len(kept) == 0 {
		stopApp(t, a)
		t.Fatal("rig: no snapshot")
	}
	appendFixtureEvent(t, a, r.keyPath, "exp_after")
	if err := askRestore(t, a, continuity.SourceSnapshot, kept[0].Name); err != nil {
		stopApp(t, a)
		t.Fatal(err)
	}
	stopApp(t, a)

	// The restore itself, whole; then the undo is asked for.
	a = New(f.cfg)
	if err := startLiveForTest(a); err != nil {
		t.Fatal(err)
	}
	sets := continuityDoor(a).View().SetAside
	if len(sets) != 1 || sets[0].PutBackAt != "" {
		stopApp(t, a)
		t.Fatalf("rig: the restore did not leave one set aside: %+v", sets)
	}
	from := sets[0]
	wasLive := a.ledger.LastSeq()
	if err := askRestore(t, a, continuity.SourceSetAside, from.Name); err != nil {
		stopApp(t, a)
		t.Fatal(err)
	}
	stopApp(t, a)

	killAt(t, r, "journalled")
	killAt(t, r, "cleanup") // the undo's every move made, the set marked put back; the journal not yet removed
	marked := func() string {
		t.Helper()
		var rec continuity.RestoreRecord
		raw, err := os.ReadFile(filepath.Join(from.Path, continuity.RestoreRecordName))
		if err != nil || json.Unmarshal(raw, &rec) != nil {
			t.Fatalf("the record of the set the undo came from does not read: %v", err)
		}
		return rec.PutBackAt
	}
	if marked() == "" {
		t.Fatal("rig: the undo did not die in its last step: the set it came from is not marked put back")
	}
	loseStaging(t, r)

	b, err := r.bootThroughChoice(t)
	if err != nil {
		t.Fatalf("the start a person makes did not come back: %v", err)
	}
	reason, safe := b.SafeMode()
	seq := b.ledger.LastSeq()
	view := continuityDoor(b).View()
	stopApp(t, b)
	if safe {
		t.Fatalf("the start after the way back is SAFE: %s", reason)
	}
	if seq != wasLive || !strings.Contains(view.RestoreFailed, "rolled back") {
		t.Fatalf("rig: the undo was not rolled back: the record ends at %d, what was live ended at %d; the page says %q", seq, wasLive, view.RestoreFailed)
	}
	if at := marked(); at != "" {
		t.Errorf("THE SET WAS NOT PUT BACK AND STILL SAYS IT WAS (at %s)", at)
	}
	if len(view.SetAside) != 1 || view.SetAside[0].Name != from.Name || view.SetAside[0].PutBackAt != "" {
		t.Errorf("the page lists %+v, want the one set, kept aside and not put back", view.SetAside)
	}
}

// AN EARLIER RESTORE'S SETTLEMENT IS NOT THIS ONE'S TO TAKE BACK. A restore
// finished and no normal start has settled it yet; then another restore is
// asked for and withdrawn. The database in place is still the one the
// earlier restore brought, and what waits in it is still owed its
// settlement: the start that follows makes it, as that earlier restore's.
func TestAWithdrawnRestoreLeavesAnEarlierRestoresSettlementOwed(t *testing.T) {
	for name, earlier := range map[string]func(j continuity.RestoreJournal) continuity.RestoreSettlement{
		// Stamped after this restore was planned, as a clock set back would
		// leave it: only its source says it is not this restore's.
		"from another source": func(continuity.RestoreJournal) continuity.RestoreSettlement {
			return continuity.RestoreSettlement{At: time.Now().UTC().Add(time.Minute).Truncate(time.Second), Source: continuity.SourceSetAside, Name: "Restorable-20260101T000000Z-before-restore-to-seq1"}
		},
		"from the same source, before this restore was planned": func(j continuity.RestoreJournal) continuity.RestoreSettlement {
			planned, err := time.Parse(time.RFC3339, j.Restored.SetAsideAt)
			if err != nil {
				t.Fatalf("rig: the journal says when it was planned as %q: %v", j.Restored.SetAsideAt, err)
			}
			return continuity.RestoreSettlement{At: planned.Add(-time.Hour), Source: j.Request.Source, Name: j.Request.Name}
		},
	} {
		t.Run(name, func(t *testing.T) {
			r, _, later := restoreAsked(t)
			killAt(t, r, "journalled")
			owed := earlier(journalOf(t, r))
			raw, err := json.Marshal(owed)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(r.dir, continuity.RestoreSettleName), raw, 0o600); err != nil {
				t.Fatal(err)
			}
			loseStaging(t, r)

			b, err := r.bootThroughChoice(t)
			if err != nil {
				t.Fatalf("the start a person makes did not come back: %v", err)
			}
			reason, safe := b.SafeMode()
			seq := b.ledger.LastSeq()
			var settled store.Restore
			var recorded bool
			var rerr error
			if !safe {
				settled, recorded, rerr = b.store.LastRestore()
			}
			view := continuityDoor(b).View()
			stopApp(t, b)
			if safe || seq != later || !strings.Contains(view.RestoreFailed, "NOT performed") {
				t.Fatalf("rig: the restore was not withdrawn: safe=%v (%s), the record ends at %d, want %d; the page says %q", safe, reason, seq, later, view.RestoreFailed)
			}
			if rerr != nil || !recorded || !settled.At.Equal(owed.At) || settled.Source != owed.Source {
				t.Errorf("THE EARLIER RESTORE'S SETTLEMENT WAS NOT MADE: recorded %v as %+v (%v), owed %+v", recorded, settled, rerr, owed)
			}
		})
	}
}

// AND A SETTLEMENT THIS RESTORE WROTE OVER COMES BACK WITH THE DATABASE IT
// WAS OWED TO. An earlier restore finished and only SAFE starts followed,
// so its settlement was still to be made when the next restore's last step
// wrote its own over it. That restore replaced the database the earlier
// settlement was owed to, so nothing is lost while it stands. Rolled back,
// that database is in place again: what waits in it may have been sent
// before the EARLIER restore, and the start that follows parks it, as that
// earlier restore's.
func TestARolledBackRestoreGivesBackTheSettlementItWroteOver(t *testing.T) {
	f := newSealedFixture(t)
	f.bootAndAnchor(t)
	r := &restoreRig{dir: f.dir, keyPath: f.keyPath, ledgerPath: f.ledgerPath, dbPath: f.dbPath, cfg: f.cfg}
	a := New(f.cfg)
	if err := startLiveForTest(a); err != nil {
		t.Fatal(err)
	}
	maintenancePass(t, a)
	kept := continuityDoor(a).View().Snapshots // newest first
	if len(kept) == 0 {
		stopApp(t, a)
		t.Fatal("rig: no snapshot")
	}
	appendFixtureEvent(t, a, r.keyPath, "exp_after")
	a.timeFac.Stop()
	contact(t, a, Contact{Name: "friend", Channel: "primary", Address: "friend@example.test"})
	if err := a.store.AddOutboxMessage("msg_live", "peer", "friend", "waiting since the earlier restore", nil); err != nil {
		t.Fatal(err)
	}
	later := a.ledger.LastSeq()
	if err := askRestore(t, a, continuity.SourceSnapshot, kept[0].Name); err != nil {
		stopApp(t, a)
		t.Fatal(err)
	}
	stopApp(t, a)

	// The earlier restore's settlement, never made: as its last step wrote it.
	settle := filepath.Join(r.dir, continuity.RestoreSettleName)
	owed := continuity.RestoreSettlement{At: time.Now().UTC().Add(-time.Hour).Truncate(time.Second), Source: continuity.SourceSetAside, Name: "Sealed-20260101T000000Z-before-restore-to-seq1"}
	earlier, err := json.MarshalIndent(owed, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	earlier = append(earlier, '\n')
	if err := os.WriteFile(settle, earlier, 0o600); err != nil {
		t.Fatal(err)
	}

	read := func() continuity.RestoreSettlement {
		t.Helper()
		var s continuity.RestoreSettlement
		raw, err := os.ReadFile(settle)
		if err != nil || json.Unmarshal(raw, &s) != nil {
			t.Fatalf("the settlement does not read: %v", err)
		}
		return s
	}
	killAt(t, r, "journalled")
	j := journalOf(t, r)
	killAt(t, r, "cleanup") // the last step has written this restore's settlement over the earlier one
	if s := read(); s.Source != j.Request.Source || s.Name != j.Request.Name || s.Earlier != string(earlier) {
		t.Fatalf("THE EARLIER SETTLEMENT WAS WRITTEN OVER AND NOT CARRIED: the file holds %+v", s)
	}
	// A pass that was interrupted and runs again carries the same one, not
	// its own inside itself.
	killAt(t, r, "cleanup")
	if s := read(); s.Earlier != string(earlier) {
		t.Fatalf("a second pass of the last step carries %q, want the earlier settlement as it was", s.Earlier)
	}
	loseStaging(t, r)

	b, err := r.bootThroughChoice(t)
	if err != nil {
		t.Fatalf("the start a person makes did not come back: %v", err)
	}
	reason, safe := b.SafeMode()
	seq := b.ledger.LastSeq()
	var row store.OutboxMessage
	var settled store.Restore
	var recorded bool
	var rerr error
	if !safe {
		row = deliveryRow(t, b, "msg_live")
		settled, recorded, rerr = b.store.LastRestore()
	}
	view := continuityDoor(b).View()
	stopApp(t, b)
	if safe || seq != later || !strings.Contains(view.RestoreFailed, "rolled back") {
		t.Fatalf("rig: the restore was not rolled back: safe=%v (%s), the record ends at %d, want %d; the page says %q", safe, reason, seq, later, view.RestoreFailed)
	}
	if rerr != nil || !recorded || !settled.At.Equal(owed.At) || settled.Source != owed.Source {
		t.Errorf("THE EARLIER RESTORE'S SETTLEMENT DID NOT COME BACK WITH ITS DATABASE: recorded %v as %+v (%v), owed %+v", recorded, settled, rerr, owed)
	}
	if !row.Parked || !strings.Contains(row.LastError, "may already have been sent") {
		t.Errorf("what waited in the database the earlier restore brought was not parked as possibly sent: %+v", row)
	}
}

// A WAY BACK THAT CANNOT BE COMPLETED IS NOT GUESSED AT. Interrupted, then
// the staging lost, and then an original that had been set aside is gone
// from there too: it is in neither place, and that is where a person must
// look. The start is SAFE, says which file, and moves nothing more.
func TestARollbackThatCannotCompleteBootsSafeAndMovesNothing(t *testing.T) {
	r, _, _ := restoreAsked(t)
	killAt(t, r, "journalled")
	j := journalOf(t, r)
	fi := firstInstall(t, j)
	killAt(t, r, fmt.Sprintf("moved:%d", fi))
	loseStaging(t, r)
	preservedRecord := filepath.Join(j.Aside, "ledger.jsonl")
	if err := os.Rename(preservedRecord, filepath.Join(t.TempDir(), "taken.jsonl")); err != nil {
		t.Fatalf("rig: the original record was not aside: %v", err)
	}
	// Where every file the journal names is. (Not the whole directory: the
	// SAFE start mounts what is in place read-only, and SQLite keeps its
	// own files beside it.)
	tree := func() []string {
		var files []string
		for _, m := range j.Moves {
			for _, path := range []string{m.From, m.To} {
				if fileExists(path) {
					files = append(files, path)
				}
			}
		}
		return files
	}
	before := tree()

	a, err := r.bootThroughChoice(t)
	if err != nil {
		t.Fatalf("the start must come back SAFE, not die: %v", err)
	}
	reason, safe := a.SafeMode()
	stopApp(t, a)
	if !safe {
		t.Fatalf("A RESTORE THAT CAN NEITHER FINISH NOR BE PUT BACK WAS TAKEN FOR AN ORDINARY START: %q", reason)
	}
	for _, want := range []string{"cannot be put back", "ledger.jsonl", "neither where it was", "nothing more was moved"} {
		if !strings.Contains(reason, want) {
			t.Errorf("the reason does not say %q: %q", want, reason)
		}
	}
	if after := tree(); strings.Join(after, "\n") != strings.Join(before, "\n") {
		t.Errorf("a way back that could not be completed moved something:\nbefore %v\nafter  %v", before, after)
	}
	if !fileExists(j.Moves[fi].To) || fileExists(filepath.Join(j.Aside, "not-completed")) {
		t.Error("what the restore had put in place was moved on a way back that could not be completed")
	}
	if !fileExists(filepath.Join(r.dir, continuity.RestoreJournalName)) {
		t.Error("the journal must stay for the start after a person has looked")
	}
}

// A JOURNAL THAT DOES NOT READ IS NOT AN ABSENT JOURNAL: what it describes
// may be half in place. With the record still here the boot is SAFE rather
// than ordinary; with the record already moved aside it is SAFE too, on the
// same reason, and the journal has opened nothing.
func TestAJournalThatDoesNotReadIsNotAnAbsentJournal(t *testing.T) {
	// Not parsing and not being READABLE are different failures and neither
	// is absence: a directory by that name, a file the process cannot open,
	// bytes that are not a journal.
	for _, tc := range []struct {
		what string
		put  func(t *testing.T, path string)
	}{
		{"bytes that are not a journal", func(t *testing.T, path string) {
			if err := os.WriteFile(path, []byte("{not a journal"), 0o600); err != nil {
				t.Fatal(err)
			}
		}},
		{"something that cannot be read at all", func(t *testing.T, path string) {
			if err := os.Mkdir(path, 0o700); err != nil {
				t.Fatal(err)
			}
		}},
		// Its set-aside directory is a real one, so the emptiness is the
		// only thing wrong with it: a journal is a list of work, and one
		// that lists none was written by something that is not this restore.
		{"a journal that lists nothing to do", func(t *testing.T, path string) {
			aside := filepath.Join(filepath.Dir(path), "set-aside", "Restorable-20260921T000000Z-before-restore-to-seq1")
			j, err := json.Marshal(continuity.RestoreJournal{Aside: aside})
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, j, 0o600); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(tc.what, func(t *testing.T) { aJournalThatDoesNotRead(t, tc.put) })
	}
}

func aJournalThatDoesNotRead(t *testing.T, put func(*testing.T, string)) {
	r, _, _ := restoreAsked(t)
	put(t, filepath.Join(r.dir, continuity.RestoreJournalName))
	a := New(r.cfg)
	if err := a.StartEmbedded(); err != nil {
		t.Fatalf("the start must come back SAFE, not die: %v", err)
	}
	reason, safe := a.SafeMode()
	stopApp(t, a)
	if !safe || !strings.Contains(reason, "a restore journal is here") {
		t.Fatalf("a journal that does not read, with the record still here: safe=%v %q", safe, reason)
	}

	// The same journal, and the record moved aside. The file is still no
	// authority: what starts this home is its key and its kept snapshot (the
	// restore door, boot_choice.go), and the start stops, SAFE, on the journal
	// that does not read, with no record opened and none made.
	if err := os.Rename(r.ledgerPath, filepath.Join(r.dir, "ledger.jsonl.moved")); err != nil {
		t.Fatal(err)
	}
	b := New(r.cfg)
	if err := b.StartEmbedded(); err != nil {
		t.Fatalf("the start must come back SAFE, not die: %v", err)
	}
	reason, safe = b.SafeMode()
	stopApp(t, b)
	if !safe || !strings.Contains(reason, "a restore journal is here") || fileExists(r.ledgerPath) {
		t.Fatalf("a journal that does not read, with the record moved aside: safe=%v %q (record made: %v)", safe, reason, fileExists(r.ledgerPath))
	}
}

// A FILE THAT WENT MISSING BETWEEN THE JOURNAL AND THE REPLAY IS NOT
// SHRUGGED OFF. Only what is here is planned, so a planned move with nothing
// on either side means the file was lost — and a lost original is the one
// thing this whole design exists to prevent. It refuses and says which file.
func TestARestoreRefusesWhenSomethingItWasToKeepWentMissing(t *testing.T) {
	r, _, _ := restoreAsked(t)
	dying := New(r.cfg)
	dying.continuity.RestoreStep = func(step string) {
		if step == "journalled" {
			if err := os.Remove(r.dbPath); err != nil {
				t.Errorf("rig: %v", err)
			}
			panic(errRestoreKilled)
		}
	}
	func() {
		defer func() {
			if v := recover(); v != nil {
				if v != errRestoreKilled {
					panic(v)
				}
				// What the dying start held goes with it, as a process's
				// files do — its claim on the identity among them (killAt).
				stopApp(t, dying)
			}
		}()
		_ = dying.StartEmbedded()
		stopApp(t, dying)
	}()

	a := New(r.cfg)
	if err := a.StartEmbedded(); err != nil {
		t.Fatalf("the start must come back SAFE, not die: %v", err)
	}
	defer stopApp(t, a)
	reason, safe := a.SafeMode()
	if !safe {
		t.Fatalf("A FILE THE RESTORE WAS TO KEEP WENT MISSING AND THE BOOT WENT ON: %q", reason)
	}
	if !strings.Contains(reason, "aii.db") || !strings.Contains(reason, "neither where it was") {
		t.Errorf("the reason does not name the file that is gone: %q", reason)
	}
}

// AN INSTALL DESTINATION IS COMPARED, NOT ASSUMED. The witness keys stay
// live while a copy goes aside — they are only ever added — so the restore
// meets a file already standing where it would put one. The same bytes are
// the same key and it moves on; anything else is a disagreement it refuses
// rather than resolves.
func TestARestoreRefusesAKeyInPlaceThatIsNotTheOneItWouldPut(t *testing.T) {
	// Two ways the file in place is not the file: other bytes, and bytes
	// that cannot be read at all. They are different facts and the reason a
	// person reads says which.
	for _, tc := range []struct {
		what        string
		putInTheWay func(t *testing.T, path string)
		says        string
	}{
		{"another key's bytes", func(t *testing.T, path string) {
			if err := os.WriteFile(path, []byte(`{"public_key_manifest":{},"public_key":{}}`), 0o600); err != nil {
				t.Errorf("rig: %v", err)
			}
		}, "is in place already and is not what this restore would put there"},
		{"something that does not read", func(t *testing.T, path string) {
			if err := os.Remove(path); err != nil {
				t.Errorf("rig: %v", err)
			}
			if err := os.Mkdir(path, 0o700); err != nil {
				t.Errorf("rig: %v", err)
			}
		}, "is a directory"},
	} {
		t.Run(tc.what, func(t *testing.T) { restoreMeetsAKeyInTheWay(t, tc.putInTheWay, tc.says) })
	}
}

func restoreMeetsAKeyInTheWay(t *testing.T, putInTheWay func(*testing.T, string), says string) {
	f := newSealedFixture(t)
	f.bootAndAnchor(t)
	a := New(f.cfg)
	if err := startLiveForTest(a); err != nil {
		t.Fatal(err)
	}
	maintenancePass(t, a)
	kept := continuityDoor(a).View().Snapshots // newest first
	snap := kept[0].Name
	appendFixtureEvent(t, a, f.keyPath, "exp_after")
	if err := askRestore(t, a, continuity.SourceSnapshot, snap); err != nil {
		t.Fatal(err)
	}
	stopApp(t, a)

	keys, err := filepath.Glob(filepath.Join(witness.WitnessKeysDir(f.dir), "*.json"))
	if err != nil || len(keys) == 0 {
		t.Fatalf("rig: no witness key beside the ledger: %v", err)
	}
	b := New(f.cfg)
	b.continuity.RestoreStep = func(step string) {
		if step == "journalled" {
			putInTheWay(t, keys[0])
		}
	}
	err = b.StartEmbedded()
	b.continuity.RestoreStep = nil
	if err != nil {
		t.Fatalf("the start must come back SAFE, not die: %v", err)
	}
	defer stopApp(t, b)
	reason, safe := b.SafeMode()
	if !safe {
		t.Fatalf("A DIFFERENT KEY STOOD WHERE THE RESTORE WOULD PUT ONE AND IT WENT ON: %q", reason)
	}
	if !strings.Contains(reason, says) {
		t.Errorf("the reason does not say what it met (%q): %q", says, reason)
	}
}

// A SET THAT WAS LIVE WHEN ITS PROCESS DIED GOES BACK TOO. A process that is
// killed leaves part of its database in a write-ahead log, and that is the
// very machine a person restores on: power went, the start was SAFE, they
// restored. The set kept aside then holds aii.db AND aii.db-wal, and putting
// it back has to bring home what the log holds — the conversations nobody
// can replay — not refuse the set for having one.
func TestASetThatDiedWithAWriteAheadLogIsPutBackWhole(t *testing.T) {
	r := newRestoreRig(t)
	a := r.boot(t)
	live(t, a, 3)
	appendFixtureEvent(t, a, r.keyPath, "exp_before")
	maintenancePass(t, a)
	historicalMaintenanceNotice(t, a)
	kept := continuityDoor(a).View().Snapshots // newest first
	snap := kept[0].Name
	at := a.ledger.LastSeq()
	live(t, a, 4)
	appendFixtureEvent(t, a, r.keyPath, "exp_after")
	later := a.ledger.LastSeq()

	// WHAT A KILL LEAVES ON DISK: the files as they stand while the identity
	// is running, log and all — taken again if the record moved meanwhile, so
	// the image is of one moment.
	died := t.TempDir()
	for try := 0; ; try++ {
		if try == 20 {
			stopApp(t, a)
			t.Fatal("rig: the record never held still long enough to image")
		}
		seq := a.ledger.LastSeq()
		for _, name := range []string{"aii.db", "aii.db-wal", "ledger.jsonl"} {
			raw, err := os.ReadFile(filepath.Join(r.dir, name))
			if err != nil {
				stopApp(t, a)
				t.Fatalf("rig: %v", err)
			}
			if err := os.WriteFile(filepath.Join(died, name), raw, 0o600); err != nil {
				t.Fatal(err)
			}
		}
		if a.ledger.LastSeq() == seq {
			break
		}
	}
	if st, err := os.Stat(filepath.Join(died, "aii.db-wal")); err != nil || st.Size() == 0 {
		stopApp(t, a)
		t.Fatalf("rig: the running identity kept nothing in a write-ahead log, so this is not the case: %v", err)
	}
	if err := askRestore(t, a, continuity.SourceSnapshot, snap); err != nil {
		stopApp(t, a)
		t.Fatal(err)
	}
	stopApp(t, a)
	// The clean stop folded the log in; put back what the kill would have left.
	os.Remove(filepath.Join(r.dir, "aii.db-shm"))
	for _, name := range []string{"aii.db", "aii.db-wal", "ledger.jsonl"} {
		raw, err := os.ReadFile(filepath.Join(died, name))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(r.dir, name), raw, 0o600); err != nil {
			t.Fatal(err)
		}
	}

	restored, err := r.bootThroughChoice(t)
	if err != nil {
		t.Fatalf("the restore: %v", err)
	}
	if reason, safe := restored.SafeMode(); safe || restored.ledger.LastSeq() != at {
		stopApp(t, restored)
		t.Fatalf("the restore: safe=%v %q, record at %d want %d", safe, reason, restored.ledger.LastSeq(), at)
	}
	sets := continuityDoor(restored).View().SetAside
	if len(sets) != 1 {
		stopApp(t, restored)
		t.Fatalf("%d sets aside, want one", len(sets))
	}
	if st, err := os.Stat(filepath.Join(sets[0].Path, "aii.db-wal")); err != nil || st.Size() == 0 {
		stopApp(t, restored)
		t.Fatalf("the set kept aside must hold the log its database died with: %v", err)
	}
	asideWAL, _ := os.ReadFile(filepath.Join(sets[0].Path, "aii.db-wal"))

	if err := askRestore(t, restored, continuity.SourceSetAside, sets[0].Name); err != nil {
		stopApp(t, restored)
		t.Fatal(err)
	}
	stopApp(t, restored)
	back, err := r.bootThroughChoice(t)
	if err != nil {
		t.Fatalf("PUT THIS BACK: %v", err)
	}
	if f := continuityDoor(back).View().RestoreFailed; f != "" {
		stopApp(t, back)
		t.Fatalf("PUT THIS BACK WAS REFUSED for a set that died with a write-ahead log: %s", f)
	}
	reason, safe := back.SafeMode()
	seq := back.ledger.LastSeq()
	stopApp(t, back)
	if safe || seq != later {
		t.Fatalf("put back: safe=%v %q, record at %d want %d", safe, reason, seq, later)
	}
	// What the log held came home: all seven conversation turns and the seeded
	// historical maintenance notice, not the ones the main file happened to contain.
	if got, ticks, convs := rows(t, r.dbPath); got != later || ticks != 7 || convs != 8 {
		t.Errorf("put back, the database holds record %d, %d ticks, %d conversations; what died held %d, 7 ticks, 8 occurrences (including the seeded historical maintenance notice)", got, ticks, convs, later)
	}
	// And proving it changed nothing in the set it was proved from.
	if now, err := os.ReadFile(filepath.Join(sets[0].Path, "aii.db-wal")); err != nil || !bytes.Equal(now, asideWAL) {
		t.Errorf("the set kept aside was written to while it was being proved (%v)", err)
	}
}

func TestARestoreAndItsUndoWorkUnderConfiguredNames(t *testing.T) {
	r := newRestoreRigNamed(t, "record.jsonl", "mind.db")
	a := r.boot(t)
	live(t, a, 2)
	appendFixtureEvent(t, a, r.keyPath, "exp_before")
	maintenancePass(t, a)
	historicalMaintenanceNotice(t, a)
	kept := continuityDoor(a).View().Snapshots // newest first
	snap := kept[0].Name
	at := a.ledger.LastSeq()
	live(t, a, 3)
	appendFixtureEvent(t, a, r.keyPath, "exp_after")
	later := a.ledger.LastSeq()
	if err := askRestore(t, a, continuity.SourceSnapshot, snap); err != nil {
		t.Fatal(err)
	}
	stopApp(t, a)

	restored, err := r.bootThroughChoice(t)
	if err != nil {
		t.Fatalf("the restore: %v", err)
	}
	if f := continuityDoor(restored).View().RestoreFailed; f != "" {
		stopApp(t, restored)
		t.Fatalf("the restore was refused: %s", f)
	}
	if restored.ledger.LastSeq() != at {
		stopApp(t, restored)
		t.Fatalf("the record ends at %d, want %d", restored.ledger.LastSeq(), at)
	}
	sets := continuityDoor(restored).View().SetAside
	if len(sets) != 1 {
		stopApp(t, restored)
		t.Fatalf("%d sets aside, want one", len(sets))
	}
	for _, name := range []string{"ledger.jsonl", continuity.SnapshotDB} {
		if !fileExists(filepath.Join(sets[0].Path, name)) {
			t.Errorf("the set kept aside does not hold %s: it is not a source a restore can read", name)
		}
	}
	if err := askRestore(t, restored, continuity.SourceSetAside, sets[0].Name); err != nil {
		stopApp(t, restored)
		t.Fatal(err)
	}
	stopApp(t, restored)
	back, err := r.bootThroughChoice(t)
	if err != nil {
		t.Fatalf("PUT THIS BACK: %v", err)
	}
	if f := continuityDoor(back).View().RestoreFailed; f != "" {
		stopApp(t, back)
		t.Fatalf("PUT THIS BACK WAS REFUSED under configured names: %s", f)
	}
	seq := back.ledger.LastSeq()
	stopApp(t, back)
	if seq != later {
		t.Fatalf("put back, the record ends at %d, want %d", seq, later)
	}
	if got, ticks, convs := rows(t, r.dbPath); got != later || ticks != 5 || convs != 6 {
		t.Errorf("put back, %s holds record %d, %d ticks, %d conversations; want %d, 5 ticks, 6 occurrences (including the seeded historical maintenance notice)", filepath.Base(r.dbPath), got, ticks, convs, later)
	}
	if fileExists(filepath.Join(r.dir, continuity.SnapshotDB)) || fileExists(filepath.Join(r.dir, "ledger.jsonl")) {
		t.Error("a restore put the pair under the default names beside the configured ones")
	}
}

// A JOURNAL IS INSTRUCTIONS TO RENAME FILES, READ FROM DISK, and it is held to
// this restore's own places and this restore's own files before anything
// acts on it. Each of these is a journal that READS — and is refused whole:
// the boot is SAFE, and nothing it named has moved.
func TestAJournalIsHeldToItsOwnRestore(t *testing.T) {
	for _, tc := range []struct {
		what   string
		tamper func(r *restoreRig, j *continuity.RestoreJournal, outside string)
		still  func(r *restoreRig, outside string) string // a file that must be where it was
	}{
		{"it takes the signing key aside", func(r *restoreRig, j *continuity.RestoreJournal, _ string) {
			j.Moves = append([]continuity.RestoreMove{{Kind: continuity.MovePreserve, From: r.keyPath, To: filepath.Join(j.Aside, filepath.Base(r.keyPath))}}, j.Moves...)
		}, func(r *restoreRig, _ string) string { return r.keyPath }},
		{"it moves the record out of this identity's directory", func(r *restoreRig, j *continuity.RestoreJournal, outside string) {
			j.Moves[0].To = filepath.Join(outside, "taken.db")
		}, func(r *restoreRig, _ string) string { return r.dbPath }},
		{"it installs a file from outside its staging", func(r *restoreRig, j *continuity.RestoreJournal, outside string) {
			j.Moves = append(j.Moves, continuity.RestoreMove{Kind: continuity.MoveInstall, From: filepath.Join(outside, "planted"), To: filepath.Join(r.dir, witness.TailFileName)})
		}, func(r *restoreRig, outside string) string { return filepath.Join(outside, "planted") }},
		{"its set-aside directory is somewhere else", func(r *restoreRig, j *continuity.RestoreJournal, outside string) {
			for i := range j.Moves {
				j.Moves[i].To = strings.Replace(j.Moves[i].To, j.Aside, outside, 1)
			}
			j.Aside = outside
		}, func(r *restoreRig, _ string) string { return r.dbPath }},
		{"it climbs out with dots", func(r *restoreRig, j *continuity.RestoreJournal, _ string) {
			j.Moves[0].To = filepath.Join(j.Aside, "..", "..", "climbed.db")
		}, func(r *restoreRig, _ string) string { return r.dbPath }},
	} {
		t.Run(tc.what, func(t *testing.T) {
			r, _, _ := restoreAsked(t)
			killAt(t, r, "journalled") // a real journal, written by a real restore
			outside := t.TempDir()
			if err := os.WriteFile(filepath.Join(outside, "planted"), []byte("{}"), 0o600); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(r.dir, continuity.RestoreJournalName)
			var j continuity.RestoreJournal
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(raw, &j); err != nil {
				t.Fatal(err)
			}
			tc.tamper(r, &j, outside)
			out, _ := json.Marshal(j)
			if err := os.WriteFile(path, out, 0o600); err != nil {
				t.Fatal(err)
			}

			a := New(r.cfg)
			if err := a.StartEmbedded(); err != nil {
				t.Fatalf("the start must come back SAFE, not die: %v", err)
			}
			reason, safe := a.SafeMode()
			stopApp(t, a)
			if !safe || !strings.Contains(reason, "restore journal") {
				t.Fatalf("A JOURNAL THAT REACHES OUTSIDE ITS RESTORE WAS ACTED ON: safe=%v %q", safe, reason)
			}
			if must := tc.still(r, outside); !fileExists(must) {
				t.Errorf("%s moved on a refused journal's word", must)
			}
			if got, _ := filepath.Glob(filepath.Join(outside, "*")); len(got) != 1 {
				t.Errorf("a refused journal put something outside: %v", got)
			}
		})
	}
	// And the journal a restore really writes passes its own bound: the
	// crash tests above resume through this same reader at every step.
}

// WHAT STANDS BESIDE A DATABASE MOVES WITH IT, BOTH WAYS — the rollback
// journal as much as the write-ahead log. The proof opens a set's database
// with its journal (store.ProveRestore); the plan then dropped it, so what
// was installed was not what was proved, and an old live journal stayed
// beside a database it did not belong to.
func TestARollbackJournalMovesWithItsDatabase(t *testing.T) {
	r, _, _ := restoreAsked(t)
	stale := []byte("a journal that belongs to the database being set aside")
	if err := os.WriteFile(r.dbPath+"-journal", stale, 0o600); err != nil {
		t.Fatal(err)
	}
	a, err := r.bootThroughChoice(t)
	if err != nil {
		t.Fatalf("the restore: %v", err)
	}
	sets := continuityDoor(a).View().SetAside
	stopApp(t, a)
	if len(sets) != 1 {
		t.Fatalf("%d sets aside, want one", len(sets))
	}
	if got, err := os.ReadFile(filepath.Join(sets[0].Path, continuity.SnapshotDB+"-journal")); err != nil || !bytes.Equal(got, stale) {
		t.Errorf("the live database's journal did not go aside with it (%v)", err)
	}
	if fileExists(r.dbPath + "-journal") {
		t.Error("AN OLD JOURNAL STAYED BESIDE THE RESTORED DATABASE, where SQLite would apply it to the wrong image")
	}

	// The other way: a staged set that holds one gets it installed.
	set := filepath.Join(t.TempDir(), "staging", "set")
	if err := os.MkdirAll(set, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{continuity.SnapshotDB, continuity.SnapshotDB + "-journal", "ledger.jsonl"} {
		if err := os.WriteFile(filepath.Join(set, name), []byte(name), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	j, err := continuity.PlanRestoreMovesForTest(continuitySettingsOf(*r.cfg), continuity.RestoreRequest{Source: continuity.SourceSetAside, Name: "a-set"}, set, filepath.Dir(set), 1, "fp", "Label")
	if err != nil {
		t.Fatal(err)
	}
	installed := false
	for _, m := range j.Moves {
		if m.Kind == continuity.MoveInstall && m.From == filepath.Join(set, continuity.SnapshotDB+"-journal") && m.To == r.dbPath+"-journal" {
			installed = true
		}
	}
	if !installed {
		t.Errorf("a staged rollback journal is not installed beside its database: %+v", j.Moves)
	}
}

func TestAJournalsRequestIsHeldToTheSameNamesTheDoorIs(t *testing.T) {
	for _, tc := range []struct{ what, source, name string }{
		{"a set-aside name that climbs out", continuity.SourceSetAside, "../../../elsewhere"},
		{"a set-aside name that is not one", continuity.SourceSetAside, "Vega"},
		{"a snapshot name that climbs out", continuity.SourceSnapshot, "../../../elsewhere"},
		{"a source that is neither", "anywhere", "ledger-20260921T000000Z-seq1"},
	} {
		t.Run(tc.what, func(t *testing.T) {
			r, _, _ := restoreAsked(t)
			killAt(t, r, "journalled")
			path := filepath.Join(r.dir, continuity.RestoreJournalName)
			var j continuity.RestoreJournal
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(raw, &j); err != nil {
				t.Fatal(err)
			}
			j.Request.Source, j.Request.Name = tc.source, tc.name
			out, _ := json.Marshal(j)
			if err := os.WriteFile(path, out, 0o600); err != nil {
				t.Fatal(err)
			}
			// Something outside this identity's tree, in the shape the
			// marking step reads and rewrites.
			outside := t.TempDir()
			elsewhere := filepath.Join(outside, continuity.RestoreRecordName)
			was := []byte(`{"set_aside_at":"somebody else's","identity":"not this one"}`)
			if err := os.WriteFile(elsewhere, was, 0o600); err != nil {
				t.Fatal(err)
			}

			a := New(r.cfg)
			if err := a.StartEmbedded(); err != nil {
				t.Fatalf("the start must come back SAFE, not die: %v", err)
			}
			reason, safe := a.SafeMode()
			stopApp(t, a)
			if !safe || !strings.Contains(reason, "restore journal") {
				t.Fatalf("A JOURNAL NAMING %q WAS ACTED ON: safe=%v %q", tc.name, safe, reason)
			}
			if now, _ := os.ReadFile(elsewhere); !bytes.Equal(now, was) {
				t.Errorf("a refused journal rewrote a record outside this identity's tree")
			}
		})
	}
}

// waitingAtTheSnapshot is what the restore cases hold when the snapshot is
// taken: a message to a person waiting to be sent, one being sent at that
// moment, a notice for the operator, and an alarm already due. TIME is
// stopped first, so the alarm waits as a due one does in a system that was
// down when it came due.
func waitingAtTheSnapshot(t *testing.T, a *App) (alarmDeadline int64) {
	t.Helper()
	a.timeFac.Stop()
	contact(t, a, Contact{Name: "friend", Channel: "primary", Address: "friend@example.test"})
	for _, m := range []struct{ id, role, to string }{{"msg_waiting", "peer", "friend"}, {"msg_flying", "peer", "friend"}, {"note_waiting", "operator", ""}} {
		if err := a.store.AddOutboxMessage(m.id, m.role, m.to, "restore case "+m.id, nil); err != nil {
			t.Fatal(err)
		}
	}
	claimRow(t, a, "msg_flying")
	alarmDeadline = time.Now().Add(-time.Minute).UnixMilli()
	if err := a.store.SetAlarm("t_due", "timers", "wall", alarmDeadline, nil, `{"message":"call the plumber"}`); err != nil {
		t.Fatal(err)
	}
	return alarmDeadline
}

// sentAfterTheSnapshot is the live system sending what waited in the
// snapshot after it was taken: an effect in the world, and nothing written
// to the record a restore could read it from.
func sentAfterTheSnapshot(t *testing.T, a *App) {
	t.Helper()
	at := a.ledger.LastSeq()
	var sent atomic.Int32
	installScriptedAdapter(t, a, "org.example.primary", "primary", func(context.Context) (tools.Result, error) {
		sent.Add(1)
		return tools.Result{Output: `{"receipt":"live"}`}, nil
	})
	if n, err := a.deliverOutbox(t.Context()); err != nil || n != 1 || sent.Load() != 1 {
		t.Fatalf("rig: the live system did not send what waited: %d delivered, %d asked, %v", n, sent.Load(), err)
	}
	if row := deliveryRow(t, a, "msg_waiting"); row.Delivered != 1 {
		t.Fatalf("rig: the live row is not delivered: %+v", row)
	}
	if a.ledger.LastSeq() != at {
		t.Fatalf("rig: the send wrote to the record (%d after %d); the case is an effect with no later record", a.ledger.LastSeq(), at)
	}
}

// settledByTheRestore is what the start after a restore shows on either
// door: what waited in the snapshot for a person is parked as possibly
// sent, is not sent again, and is said to the identity and on the page;
// the operator's notice stays where it was; the restore is recorded; and
// the alarm that was due fires, saying it may be a repeat.
func settledByTheRestore(t *testing.T, b *App, snapshot string, alarmDeadline int64) store.Restore {
	t.Helper()
	m := snapshotNameRe.FindStringSubmatch(snapshot)
	if m == nil {
		t.Fatalf("rig: %q is not a snapshot's name", snapshot)
	}
	stamp, err := time.Parse("20060102T150405Z", m[1])
	if err != nil {
		t.Fatal(err)
	}
	from := "restored from a snapshot of " + stamp.Format("2006-01-02 15:04") + " UTC"
	for _, id := range []string{"msg_waiting", "msg_flying"} {
		row := deliveryRow(t, b, id)
		if row.Delivered != 0 || !row.Parked || row.Effect != "unknown" || row.Attempts != 1 || row.DeliveredVia != "" ||
			!strings.Contains(row.LastError, from) || !strings.Contains(row.LastError, "may already have been sent") {
			t.Errorf("%s: WHAT THE SNAPSHOT HELD WAITING IS NOT PARKED AS POSSIBLY SENT: %+v", id, row)
		}
	}
	if note := deliveryRow(t, b, "note_waiting"); note.Delivered != 0 || note.Parked || note.Effect != "" || note.Attempts != 0 {
		t.Errorf("the operator's notice was settled or walked: %+v", note)
	}
	events, err := b.store.PendingTurnEvents(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	told := map[string]string{}
	for _, e := range events {
		if e.Outcome != nil && e.Outcome.Attempts == 1 {
			told[e.Outcome.ID] = deliveryOutcomeLines([]store.OutboxMessage{*e.Outcome})
		}
	}
	for _, id := range []string{"msg_waiting", "msg_flying"} {
		if line := told[id]; !strings.Contains(line, from) || !strings.Contains(line, "not resent") {
			t.Errorf("%s: the identity's next turn is not told what became of it: %q", id, line)
		}
	}
	var again atomic.Int32
	installScriptedAdapter(t, b, "org.example.primary", "primary", func(context.Context) (tools.Result, error) {
		again.Add(1)
		return tools.Result{Output: `{"receipt":"again"}`}, nil
	})
	if _, err := b.deliverOutbox(t.Context()); err != nil {
		t.Fatal(err)
	}
	if again.Load() != 0 {
		t.Errorf("A MESSAGE THE SNAPSHOT HELD WAS SENT AGAIN after the restore (%d)", again.Load())
	}
	r, ok, err := b.store.LastRestore()
	if err != nil || !ok || r.Source != continuity.SourceSnapshot || !r.SourceAt.Equal(stamp) || time.Since(r.At) > time.Hour {
		t.Errorf("the restore is not recorded as it was: %+v %v %v", r, ok, err)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(b.configSnapshot().Identity.LedgerPath), continuity.RestoreSettleName)); !os.IsNotExist(err) {
		t.Errorf("the settlement's file outlived the settlement: %v", err)
	}
	if v := continuityDoor(b).View(); !strings.Contains(v.Restored, from) || !strings.Contains(v.Restored, "may already have been sent") {
		t.Errorf("the page does not say what the restore did with what waited: %q", v.Restored)
	}
	if floor := timerFloor(t, b, "t_due", alarmDeadline); !strings.Contains(floor, "call the plumber") || !strings.Contains(floor, "may already have been delivered") {
		t.Errorf("the alarm due before the restore did not fire saying it may be a repeat: %q", floor)
	}
	return r
}

// timerFloor waits for an alarm's durable floor, and for the alarm to be
// accepted — a one-shot's row is gone then — and returns what it says.
func timerFloor(t *testing.T, a *App, alarmID string, deadline int64) string {
	t.Helper()
	id := fmt.Sprintf("timer_%s_%d", alarmID, deadline)
	for wait := time.Now().Add(20 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		fired, err := a.store.TimerFiringsSince(0)
		if err != nil {
			t.Fatal(err)
		}
		pending, err := a.store.DueAlarms("wall", 1<<62, 1000)
		if err != nil {
			t.Fatal(err)
		}
		accepted := true
		for _, p := range pending {
			accepted = accepted && p.AlarmID != alarmID
		}
		for _, f := range fired {
			if f.ID == id && accepted {
				return f.Content
			}
		}
		if time.Now().After(wait) {
			t.Fatalf("the alarm %s did not fire and settle after the restore (%d firings, %d alarms waiting)", alarmID, len(fired), len(pending))
		}
	}
}

// firingsOf counts an alarm's durable floors.
func firingsOf(t *testing.T, a *App, alarmID string) int {
	t.Helper()
	fired, err := a.store.TimerFiringsSince(0)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, f := range fired {
		if strings.HasPrefix(f.ID, "timer_"+alarmID+"_") {
			n++
		}
	}
	return n
}

// settledOnce is the start after that: a second one changes nothing — the
// rows stay as the settlement left them, a message written since the
// restore is not touched, the restore is the same one, and the alarm does
// not fire again.
func settledOnce(t *testing.T, c *App, settled store.Restore) {
	t.Helper()
	for id, attempts := range map[string]int{"msg_waiting": 1, "msg_flying": 1, "msg_since": 0} {
		row := deliveryRow(t, c, id)
		if row.Attempts != attempts || row.Parked != (attempts == 1) {
			t.Errorf("%s: THE SETTLEMENT RAN AGAIN on the next start: %+v", id, row)
		}
	}
	if since := deliveryRow(t, c, "msg_since"); since.Effect != "" {
		t.Errorf("a message written after the restore was settled as the restore's: %+v", since)
	}
	if r, ok, err := c.store.LastRestore(); err != nil || !ok || !r.At.Equal(settled.At) {
		t.Errorf("the recorded restore changed on the next start: %+v %v %v, was %+v", r, ok, err, settled)
	}
	if n := firingsOf(t, c, "t_due"); n != 1 {
		t.Errorf("the alarm fired %d times across the restore's starts, want once", n)
	}
}

func TestARestoreOnThisMachineSendsNothingItBroughtBackAgain(t *testing.T) {
	r := newRestoreRig(t)
	a := r.boot(t)
	deadline := waitingAtTheSnapshot(t, a)
	made, err := askedSnapshot(t, a)
	if err != nil {
		stopApp(t, a)
		t.Fatal(err)
	}
	sentAfterTheSnapshot(t, a)
	if err := askRestore(t, a, continuity.SourceSnapshot, made.Name); err != nil {
		stopApp(t, a)
		t.Fatal(err)
	}
	stopApp(t, a)
	for _, side := range []string{"", "-wal", "-shm", "-journal"} { // the database and what SQLite keeps beside it
		if err := os.Remove(r.dbPath + side); err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
	}

	// THE SETTLEMENT COMES BEFORE ANYTHING THAT CAN SEND. Were the tool
	// registry — and with it an adapter and the outbox — already up when
	// it begins, a pass of the outbox there would carry the message.
	b := New(r.cfg)
	var early atomic.Int32
	b.continuity.RestoreStep = func(step string) {
		if step == "settle" && b.toolReg != nil {
			installScriptedAdapter(t, b, "org.example.primary", "primary", func(context.Context) (tools.Result, error) {
				early.Add(1)
				return tools.Result{Output: `{"receipt":"early"}`}, nil
			})
			_, _ = b.deliverOutbox(t.Context())
		}
	}
	err = startLiveForTest(b)
	b.continuity.RestoreStep = nil
	if err != nil {
		t.Fatal(err)
	}
	if reason, safe := b.SafeMode(); safe {
		stopApp(t, b)
		t.Fatalf("the restored start is SAFE: %s", reason)
	}
	if early.Load() != 0 {
		stopApp(t, b)
		t.Fatalf("THE OUTBOX COULD SEND BEFORE THE SETTLEMENT: what the snapshot held was sent again (%d)", early.Load())
	}
	if b.ledger.LastSeq() != made.Record {
		stopApp(t, b)
		t.Fatalf("rig: the restore did not happen: the record ends at %d, the snapshot at %d", b.ledger.LastSeq(), made.Record)
	}
	settled := settledByTheRestore(t, b, made.Name, deadline)
	// Keep only the new message unclaimed until this boot has stopped.
	// The ordinary delivery worker is already running; its legitimate send
	// must not be mistaken for a second restore settlement. The restored rows
	// remain unprotected so the assertions still catch any repeated send.
	if _, err := b.store.DB().Exec(`CREATE TRIGGER hold_post_restore_fixture
		BEFORE UPDATE OF dispatched_ms ON outbox WHEN OLD.id='msg_since'
		BEGIN SELECT RAISE(ABORT, 'fixture holds the post-restore send'); END`); err != nil {
		t.Fatal(err)
	}
	if err := b.store.AddOutboxMessage("msg_since", "peer", "friend", "written after the restore", nil); err != nil {
		t.Fatal(err)
	}
	stopApp(t, b)

	c := r.boot(t)
	defer stopApp(t, c)
	settledOnce(t, c, settled)

	var sent atomic.Int32
	installScriptedAdapter(t, c, "org.example.primary", "primary", func(context.Context) (tools.Result, error) {
		sent.Add(1)
		return tools.Result{Output: `{"receipt":"post-restore"}`}, nil
	})
	// Opening the store can already have removed this fixture-only trigger.
	if _, err := c.store.DB().Exec(`DROP TRIGGER IF EXISTS hold_post_restore_fixture`); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, func() bool {
		if _, err := c.deliverOutbox(t.Context()); err != nil {
			t.Fatal(err)
		}
		return deliveryRow(t, c, "msg_since").Delivered == 1
	}, "the new message leaves normally after restore inspection")
	if row := deliveryRow(t, c, "msg_since"); sent.Load() != 1 || row.Delivered != 1 || row.Attempts != 1 || row.Parked || row.Effect != "performed" {
		t.Errorf("the post-restore message was not delivered normally once: sent=%d row=%+v", sent.Load(), row)
	}
}

// A SETTLEMENT THAT DOES NOT READ IS NOT "NOTHING OWED". The restore
// finished and its start died before the settlement; what it left is then
// damaged. The next start does not guess: it is SAFE, where nothing leaves,
// and says why.
func TestARestoreSettlementThatDoesNotReadStartsSafe(t *testing.T) {
	r, _, _ := restoreAsked(t)
	killAt(t, r, "settle")
	if err := os.WriteFile(filepath.Join(r.dir, continuity.RestoreSettleName), []byte("{not a settlement"), 0o600); err != nil {
		t.Fatal(err)
	}
	a := New(r.cfg)
	if err := a.StartEmbedded(); err != nil {
		t.Fatalf("the start must come back SAFE, not die: %v", err)
	}
	reason, safe := a.SafeMode()
	stopApp(t, a)
	if !safe || !strings.Contains(reason, "settlement") {
		t.Fatalf("A SETTLEMENT THAT DOES NOT READ WAS TAKEN FOR NONE: safe=%v %q", safe, reason)
	}
}
