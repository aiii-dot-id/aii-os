package app

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/escrow"
	"github.com/aiii-dot-id/aii-os/internal/genesis"
	"github.com/aiii-dot-id/aii-os/internal/ledger"
	"github.com/aiii-dot-id/aii-os/internal/store"
	"github.com/aiii-dot-id/aii-os/internal/witness"
)

// .
// .
// .

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

// .
// .
func newRestoreRigNamed(t *testing.T, ledgerName, dbName string) *restoreRig {
	t.Helper()
	r := newRestoreRig(t)
	for _, mv := range [][2]string{{r.ledgerPath, filepath.Join(r.dir, ledgerName)}, {r.dbPath, filepath.Join(r.dir, dbName)}} {
		if err := os.Rename(mv[0], mv[1]); err != nil {
			t.Fatal(err)
		}
	}
	for _, side := range databaseSidecars {
		os.Remove(r.dbPath + side)
	}
	r.ledgerPath, r.dbPath = filepath.Join(r.dir, ledgerName), filepath.Join(r.dir, dbName)
	r.cfg = safebootConfig(t, r.dir, "Restorable", r.keyPath, r.ledgerPath, r.dbPath)
	return r
}

// .
func (r *restoreRig) boot(t *testing.T) *App {
	t.Helper()
	a := New(r.cfg)
	if err := startLiveForTest(a); err != nil {
		t.Fatalf("boot: %v", err)
	}
	if reason, safe := a.SafeMode(); safe {
		a.Stop()
		t.Fatalf("the boot is SAFE: %s", reason)
	}
	return a
}

// .
func (r *restoreRig) lived(t *testing.T, n int, tag string, snapshot bool) (last uint64, snap string) {
	t.Helper()
	a := r.boot(t)
	defer a.Stop()
	for i := 0; i < n; i++ {
		appendFixtureEvent(t, a, r.keyPath, fmt.Sprintf("exp_%s_%d", tag, i))
	}
	if snapshot {
		a.runMaintenance(t.Context())
		kept, _ := keptSnapshots(a.backupsDir(a.configSnapshot()))
		if len(kept) == 0 {
			t.Fatal("rig: no snapshot")
		}
		snap = kept[len(kept)-1].Name
	}
	return a.ledger.LastSeq(), snap
}

// .
// .
// .
// .
func TestARestoreIsPerformedByTheNextBootAndCanBeUndone(t *testing.T) {
	r := newRestoreRig(t)
	at, snap := r.lived(t, 2, "before", true)
	later, _ := r.lived(t, 3, "after", false)
	if later != at+3 {
		t.Fatalf("rig: %d then %d", at, later)
	}

	a := r.boot(t)
	plan, err := a.restorePlan(sourceSnapshot, snap)
	if err != nil || plan.RestoredTo != at || plan.LiveThrough != int64(later) || plan.SetAside != 3 || plan.Witnessed != -1 || plan.WitnessedSetAside != 0 {
		a.Stop()
		t.Fatalf("the plan must say the exact cost: %+v %v", plan, err)
	}
	if err := a.requestRestore(sourceSnapshot, snap, "not the name"); err == nil {
		a.Stop()
		t.Fatal("a restore was asked for without the identity's name typed")
	}
	if err := a.requestRestore(sourceSnapshot, "../"+snap, plan.ConfirmText); err == nil {
		a.Stop()
		t.Fatal("a name with a path in it became a request")
	}
	if err := a.requestRestore(sourceSnapshot, snap, plan.ConfirmText); err != nil {
		a.Stop()
		t.Fatal(err)
	}
	if a.ledger.LastSeq() != later {
		t.Fatal("asking for a restore touched the running identity")
	}
	a.Stop()

	a = r.boot(t)
	if a.ledger.LastSeq() != at {
		a.Stop()
		t.Fatalf("THE NEXT BOOT DID NOT RESTORE: the record ends at %d, the snapshot at %d", a.ledger.LastSeq(), at)
	}
	sets := setAsideSets(a.configSnapshot())
	if len(sets) != 1 || sets[0].Record.WasThrough != later || sets[0].Record.RestoredTo != at || sets[0].Record.RestoredFrom != snap {
		a.Stop()
		t.Fatalf("what was live must be set aside and say what it is: %+v", sets)
	}
	if !strings.HasPrefix(sets[0].Name, "Restorable-") || !strings.HasSuffix(sets[0].Name, fmt.Sprintf("-before-restore-to-seq%d", at)) || !filepath.IsAbs(sets[0].Path) {
		t.Errorf("a set kept aside is named for a person who will look for it, and the page is told where it is: %q at %q", sets[0].Name, sets[0].Path)
	}
	for _, name := range []string{"aii.db", "ledger.jsonl", restoreRecordName} {
		if _, err := os.Stat(filepath.Join(sets[0].Path, name)); err != nil {
			t.Errorf("the set kept aside lacks %s", name)
		}
	}
	for _, leftover := range []string{restoreRequestName, restoreJournalName} {
		if _, err := os.Stat(filepath.Join(r.dir, leftover)); !os.IsNotExist(err) {
			t.Errorf("%s was left behind", leftover)
		}
	}
	if left, _ := filepath.Glob(filepath.Join(r.dir, restoreStagePrefix+"*")); len(left) != 0 {
		t.Errorf("staging was left behind: %v", left)
	}
	// .
	appendFixtureEvent(t, a, r.keyPath, "exp_after_restore")
	if a.ledger.LastSeq() != at+1 {
		t.Fatalf("an append after the restore landed at %d", a.ledger.LastSeq())
	}

	// .
	// .
	if err := a.requestRestore(sourceSetAside, sets[0].Name, a.restoreConfirmText()); err != nil {
		a.Stop()
		t.Fatal(err)
	}
	a.Stop()
	a = r.boot(t)
	defer a.Stop()
	if a.ledger.LastSeq() != later {
		t.Fatalf("PUT THIS BACK did not bring the three records back: the record ends at %d, want %d", a.ledger.LastSeq(), later)
	}
	// .
	// .
	// .
	var asideNow, putBack int
	for _, set := range setAsideSets(a.configSnapshot()) {
		switch {
		case set.Record.PutBackAt != "" && set.Record.WasThrough == later:
			putBack++
		case set.Record.PutBackAt == "" && set.Record.WasThrough == at+1:
			asideNow++
		}
	}
	if asideNow != 1 || putBack != 1 {
		t.Fatalf("after the undo: %d set aside in its turn, %d marked put back, want 1 and 1: %+v", asideNow, putBack, setAsideSets(a.configSnapshot()))
	}
}

// .
// .
// .
func (r *restoreRig) bootThroughChoice(t *testing.T) (*App, error) {
	t.Helper()
	a := New(r.cfg)
	err := a.StartEmbedded()
	if err != nil {
		a.Stop()
	}
	return a, err
}

// .
// .
func rows(t *testing.T, dbPath string) (uint64, int64, int64) {
	t.Helper()
	head, facts, err := store.InspectCopy(t.Context(), dbPath)
	if err != nil {
		t.Fatalf("%s: %v", dbPath, err)
	}
	return head.Seq, facts.LifetimeTicks, facts.Ephemeral["conversations"]
}

// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
func TestARestoreKilledAtAnyStepPreservesBothSets(t *testing.T) {
	// .
	// .
	// .
	var steps []string
	func() {
		r, _, _ := restoreAsked(t)
		prev := restoreStep
		restoreStep = func(step string) { steps = append(steps, step) }
		defer func() { restoreStep = prev }()
		done, err := r.bootThroughChoice(t)
		if err != nil {
			t.Fatalf("the restore this test interrupts does not run: %v", err)
		}
		done.Stop()
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
					resumed.Stop()
				}
			}()
			if reason, safe := resumed.SafeMode(); safe {
				t.Fatalf("killed at %s: the resumed start is SAFE: %s", dieAt, reason)
			}
			if resumed.ledger.LastSeq() != at {
				t.Fatalf("killed at %s: the record ends at %d, want the snapshot's %d", dieAt, resumed.ledger.LastSeq(), at)
			}
			sets := setAsideSets(resumed.configSnapshot())
			// .
			// .
			// .
			// .
			resumed.Stop()
			stopped = true
			// .
			// .
			if seq, ticks, convs := rows(t, r.dbPath); seq != at || ticks != 3 || convs != 3 {
				t.Errorf("killed at %s: the live database holds record %d, %d ticks, %d conversations; the snapshot captured %d, 3, 3", dieAt, seq, ticks, convs, at)
			}
			// .
			if len(sets) != 1 {
				t.Fatalf("killed at %s: %d sets aside, want exactly one", dieAt, len(sets))
			}
			if seq, ticks, convs := rows(t, filepath.Join(sets[0].Path, "aii.db")); seq != later || ticks != 7 || convs != 7 {
				t.Errorf("killed at %s: THE PRESERVED ORIGINAL IS NOT WHAT WAS LIVE: it holds record %d, %d ticks, %d conversations; what was live held %d, 7, 7", dieAt, seq, ticks, convs, later)
			}
			if kept, err := os.ReadFile(filepath.Join(sets[0].Path, "ledger.jsonl")); err != nil || !bytes.Equal(kept, wasLive) {
				t.Errorf("killed at %s: the preserved record is not the bytes that were live (%v)", dieAt, err)
			}
			if _, err := os.Stat(filepath.Join(r.dir, restoreJournalName)); !os.IsNotExist(err) {
				t.Errorf("killed at %s: the journal outlived the restore it finished", dieAt)
			}
		})
	}
}

// .
// .
// .
// .
// .
func restoreAsked(t *testing.T) (r *restoreRig, at, later uint64) {
	t.Helper()
	r = newRestoreRig(t)
	a := r.boot(t)
	live(t, a, 3)
	appendFixtureEvent(t, a, r.keyPath, "exp_before")
	a.runMaintenance(t.Context())
	kept, _ := keptSnapshots(a.backupsDir(a.configSnapshot()))
	if len(kept) == 0 {
		a.Stop()
		t.Fatal("rig: no snapshot")
	}
	snap := kept[len(kept)-1].Name
	at = a.ledger.LastSeq()
	live(t, a, 4)
	appendFixtureEvent(t, a, r.keyPath, "exp_after")
	later = a.ledger.LastSeq()
	if err := a.requestRestore(sourceSnapshot, snap, a.restoreConfirmText()); err != nil {
		a.Stop()
		t.Fatal(err)
	}
	a.Stop()
	return r, at, later
}

// .
// .
func killAt(t *testing.T, r *restoreRig, step string) {
	t.Helper()
	fired := false
	prev := restoreStep
	defer func() { restoreStep = prev }()
	restoreStep = func(s string) {
		if s == step {
			fired = true
			panic(errRestoreKilled)
		}
	}
	func() {
		defer func() {
			if v := recover(); v != nil && v != errRestoreKilled {
				panic(v)
			}
		}()
		dying := New(r.cfg)
		_ = dying.StartEmbedded()
		dying.Stop()
	}()
	if !fired {
		t.Fatalf("the restore never reached %s: the interruption this case is about did not happen", step)
	}
}

var errRestoreKilled = errors.New("killed by the test at a restore step")

// .
// .
func TestASourceThatDoesNotProveIsNotRestored(t *testing.T) {
	r := newRestoreRig(t)
	_, snap := r.lived(t, 2, "before", true)
	later, _ := r.lived(t, 2, "after", false)
	a := r.boot(t)
	if err := a.requestRestore(sourceSnapshot, snap, a.restoreConfirmText()); err != nil {
		t.Fatal(err)
	}
	backups := a.backupsDir(a.configSnapshot())
	a.Stop()

	// .
	// .
	good, _ := os.ReadFile(filepath.Join(r.dir, restoreRequestName))
	foreign, _ := json.Marshal(restoreRequest{Source: sourceSnapshot, Name: snap, Identity: strings.Repeat("ab", 32)})
	if err := os.WriteFile(filepath.Join(r.dir, restoreRequestName), foreign, 0o600); err != nil {
		t.Fatal(err)
	}
	a = r.boot(t)
	if a.ledger.LastSeq() != later {
		a.Stop()
		t.Fatal("A REQUEST MADE FOR ANOTHER IDENTITY WAS PERFORMED")
	}
	a.Stop()
	if err := os.WriteFile(filepath.Join(r.dir, restoreRequestName), good, 0o600); err != nil {
		t.Fatal(err)
	}

	// .
	// .
	// .
	// .
	// .
	// .
	// .
	db := filepath.Join(backups, snap, snapshotDB)
	raw, err := os.ReadFile(db)
	if err != nil {
		t.Fatal(err)
	}
	raw = raw[:len(raw)/2]
	if err := os.WriteFile(db, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	sumsPath := filepath.Join(backups, snap, snapshotSums)
	sums, err := os.ReadFile(sumsPath)
	if err != nil {
		t.Fatal(err)
	}
	h := sha256.Sum256(raw)
	var resummed []string
	for _, line := range strings.Split(strings.TrimRight(string(sums), "\n"), "\n") {
		if strings.HasSuffix(line, "  "+snapshotDB) {
			line = hex.EncodeToString(h[:]) + "  " + snapshotDB
		}
		resummed = append(resummed, line)
	}
	if err := os.WriteFile(sumsPath, []byte(strings.Join(resummed, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	present, _ := filesUnder(filepath.Join(backups, snap))
	if err := escrow.CheckSums(filepath.Join(backups, snap), present); err != nil {
		t.Fatalf("rig: the re-summed snapshot must pass its own checksum list: %v", err)
	}

	a = r.boot(t)
	defer a.Stop()
	if a.ledger.LastSeq() != later {
		t.Fatalf("A SNAPSHOT THAT DOES NOT PROVE WAS RESTORED: the record ends at %d, was %d", a.ledger.LastSeq(), later)
	}
	if sets := setAsideSets(a.configSnapshot()); len(sets) != 0 {
		t.Errorf("something was set aside for a restore that was not performed: %+v", sets)
	}
	if _, err := os.Stat(filepath.Join(r.dir, restoreRequestName)); !os.IsNotExist(err) {
		t.Error("the request was not withdrawn: every boot would try again")
	}
	f := lastRestoreFailure(a.configSnapshot())
	if f == nil || !strings.Contains(f.Why, "does not prove") || f.Request.Name != snap {
		t.Fatalf("the page is owed why: %+v", f)
	}
	if v := a.continuityView(); !strings.Contains(v.RestoreFailed, "NOT performed") || !strings.Contains(v.RestoreFailed, "Nothing was touched") {
		t.Errorf("the view does not say so: %q", v.RestoreFailed)
	}
}

// .
// .
// .
func TestTheRestorePlanCountsWhatTheWitnessHasAttested(t *testing.T) {
	r := newRestoreRig(t)
	at, snap := r.lived(t, 2, "before", true)
	later, _ := r.lived(t, 4, "after", false)
	a := r.boot(t)
	defer a.Stop()
	for _, tc := range []struct {
		bookmark          uint64
		wantWitnessedAway int64
	}{{later - 1, int64(later-1) - int64(at)}, {at, 0}, {at - 1, 0}} {
		echoTail(t, r.dir, tc.bookmark, "sha256:"+strings.Repeat("a", 64))
		plan, err := a.restorePlan(sourceSnapshot, snap)
		if err != nil || plan.Witnessed != int64(tc.bookmark) || plan.WitnessedSetAside != tc.wantWitnessedAway || plan.SetAside != int64(later-at) {
			t.Errorf("bookmark %d, restored end %d, live %d: %+v %v", tc.bookmark, at, later, plan, err)
		}
	}
	os.Remove(filepath.Join(r.dir, "witness-tail.json"))
}

// .
// .
func TestTheIdentityReadsThatItWasRestored(t *testing.T) {
	r := newRestoreRig(t)
	at, snap := r.lived(t, 2, "before", true)
	later, _ := r.lived(t, 2, "after", false)
	a := r.boot(t)
	if out, _ := a.continuityRead("", time.Now()); strings.Contains(out, "restored:") {
		t.Fatalf("no restore has happened, and the read says one did:\n%s", out)
	}
	if err := a.requestRestore(sourceSnapshot, snap, a.restoreConfirmText()); err != nil {
		t.Fatal(err)
	}
	a.Stop()
	a = r.boot(t)
	defer a.Stop()
	out, err := a.continuityRead("", time.Now())
	if err != nil || !strings.Contains(out, fmt.Sprintf("put back to %d", at)) || !strings.Contains(out, fmt.Sprintf("through record %d", later)) {
		t.Fatalf("the read after a restore: %v\n%s", err, out)
	}
	if strings.Contains(out, r.dir) {
		t.Errorf("the identity's read carries a path:\n%s", out)
	}
	if old, _ := a.continuityRead("", time.Now().Add(restoreSaidFor+time.Hour)); strings.Contains(old, "restored:") {
		t.Error("the line outlives its thirty days")
	}
}

// .
// .
// .
// .
func TestARestoreAskedForFromSafeBringsTheIdentityBack(t *testing.T) {
	r := newRestoreRig(t)
	at, snap := r.lived(t, 2, "before", true)
	r.lived(t, 2, "after", false)
	// .
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
		safe.Stop()
		t.Fatal("rig: the damaged record did not boot SAFE")
	}
	plan, err := safe.restorePlan(sourceSnapshot, snap)
	if err != nil || plan.RestoredTo != at || plan.ConfirmText == "" {
		safe.Stop()
		t.Fatalf("the plan in SAFE: %+v %v", plan, err)
	}
	if err := safe.requestRestore(sourceSnapshot, snap, plan.ConfirmText); err != nil {
		safe.Stop()
		t.Fatalf("A RESTORE CANNOT BE ASKED FOR FROM SAFE: %v", err)
	}
	safe.Stop()

	a := r.boot(t)
	defer a.Stop()
	if a.ledger.LastSeq() != at {
		t.Fatalf("after the restore the record ends at %d, want the snapshot's %d", a.ledger.LastSeq(), at)
	}
	sets := setAsideSets(a.configSnapshot())
	if len(sets) != 1 {
		t.Fatalf("the damaged pair must be set aside: %+v", sets)
	}
	kept, err := os.ReadFile(filepath.Join(sets[0].Path, "ledger.jsonl"))
	if err != nil || !strings.Contains(string(kept), `{"not":"a record"}`) {
		t.Errorf("the damaged record was not kept as it was: %v", err)
	}
}

// .
// .
func TestASnapshotHoldingWhatItsListDoesNotNameIsNotRestored(t *testing.T) {
	r := newRestoreRig(t)
	_, snap := r.lived(t, 2, "before", true)
	later, _ := r.lived(t, 1, "after", false)
	a := r.boot(t)
	if err := a.requestRestore(sourceSnapshot, snap, a.restoreConfirmText()); err != nil {
		t.Fatal(err)
	}
	stray := filepath.Join(a.backupsDir(a.configSnapshot()), snap, "stray.txt")
	a.Stop()
	if err := os.WriteFile(stray, []byte("not part of what was published"), 0o600); err != nil {
		t.Fatal(err)
	}
	a = r.boot(t)
	defer a.Stop()
	if a.ledger.LastSeq() != later {
		t.Fatalf("a snapshot holding a file its checksum list does not name was restored: the record ends at %d, was %d", a.ledger.LastSeq(), later)
	}
	if f := lastRestoreFailure(a.configSnapshot()); f == nil || !strings.Contains(f.Why, "stray.txt") {
		t.Errorf("the page is owed why: %+v", f)
	}
}

// .
// .
// .
// .
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
	// .
	a := r.boot(t)
	appendFixtureEvent(t, a, r.keyPath, "exp_before")
	at := a.ledger.LastSeq()
	a.Stop()
	echoTail(t, r.dir, at, tailFor(at))
	a = r.boot(t)
	a.runMaintenance(t.Context())
	kept, _ := keptSnapshots(a.backupsDir(a.configSnapshot()))
	snap := kept[len(kept)-1].Name
	// .
	appendFixtureEvent(t, a, r.keyPath, "exp_after_1")
	appendFixtureEvent(t, a, r.keyPath, "exp_after_2")
	later := a.ledger.LastSeq()
	a.Stop()
	echoTail(t, r.dir, later, tailFor(later))

	a = r.boot(t)
	plan, err := a.restorePlan(sourceSnapshot, snap)
	if err != nil || plan.WitnessedSetAside != int64(later-at) {
		a.Stop()
		t.Fatalf("the plan must count the attested records being set aside: %+v %v", plan, err)
	}
	if err := a.requestRestore(sourceSnapshot, snap, plan.ConfirmText); err != nil {
		a.Stop()
		t.Fatal(err)
	}
	a.Stop()

	a = r.boot(t)
	defer a.Stop()
	if a.ledger.LastSeq() != at {
		t.Fatalf("the record ends at %d, want %d", a.ledger.LastSeq(), at)
	}
	inPlace, err := witness.ReadLocalTail(r.dir)
	if err != nil || inPlace == nil || uint64(inPlace.LedgerOrdinal) != at {
		t.Fatalf("the tail in place must be the source's own (record %d): %+v %v", at, inPlace, err)
	}
	sets := setAsideSets(a.configSnapshot())
	if len(sets) != 1 || sets[0].Record.Witnessed != int64(later) {
		t.Fatalf("the set kept aside must say how far the witness had attested: %+v", sets)
	}
	aside, err := witness.ReadLocalTail(sets[0].Path)
	if err != nil || aside == nil || uint64(aside.LedgerOrdinal) != later {
		t.Fatalf("the live tail must be kept, as evidence, with what was set aside: %+v %v", aside, err)
	}
}

// .
// .
// .
// .
// .
// .
// .
// .
// .
func TestASetKeptAsideCarriesItsSealedHistoryAndKeys(t *testing.T) {
	f := newSealedFixture(t)
	sealedThrough := f.bootAndAnchor(t)

	// .
	// .
	// .
	a := New(f.cfg)
	if err := startLiveForTest(a); err != nil {
		t.Fatal(err)
	}
	live(t, a, 2)
	a.runMaintenance(t.Context())
	kept, _ := keptSnapshots(a.backupsDir(a.configSnapshot()))
	snap := kept[len(kept)-1].Name
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
	if err := a.requestRestore(sourceSnapshot, snap, a.restoreConfirmText()); err != nil {
		t.Fatal(err)
	}
	a.Stop()

	restored := New(f.cfg)
	if err := restored.StartEmbedded(); err != nil {
		t.Fatalf("the restore: %v", err)
	}
	if reason, safe := restored.SafeMode(); safe {
		restored.Stop()
		t.Fatalf("the restored identity is SAFE: %s", reason)
	}
	if restored.ledger.LastSeq() != at {
		restored.Stop()
		t.Fatalf("the record ends at %d, want the snapshot's %d", restored.ledger.LastSeq(), at)
	}
	sets := setAsideSets(restored.configSnapshot())
	if len(sets) != 1 {
		restored.Stop()
		t.Fatalf("%d sets aside, want one", len(sets))
	}
	aside := sets[0].Path

	// .
	// .
	beside, err := restored.besideFor(restored.configSnapshot(), aside)
	if err != nil {
		restored.Stop()
		t.Fatalf("the set kept aside carries no witness keys: %v", err)
	}
	n, fp, err := genesis.VerifyHeld(filepath.Join(aside, "ledger.jsonl"), beside, nil)
	if err != nil || uint64(n) != later || fp != restored.keyPair.Fingerprint() {
		restored.Stop()
		t.Fatalf("THE SET KEPT ASIDE DOES NOT VERIFY ON ITS OWN: %d records, %v", n, err)
	}
	for _, seg := range segments {
		if _, err := os.Stat(filepath.Join(aside, filepath.Base(seg))); err != nil {
			t.Errorf("the set lacks %s, which its own record lives in: %v", filepath.Base(seg), err)
		}
	}
	// .
	if _, err := restored.verifyLiveLedger(restored.configSnapshot()); err != nil {
		t.Errorf("the restored identity does not verify: %v", err)
	}

	// .
	if err := restored.requestRestore(sourceSetAside, sets[0].Name, restored.restoreConfirmText()); err != nil {
		restored.Stop()
		t.Fatal(err)
	}
	restored.Stop()
	back := New(f.cfg)
	if err := back.StartEmbedded(); err != nil {
		t.Fatalf("PUT THIS BACK: %v", err)
	}
	defer back.Stop()
	if reason, safe := back.SafeMode(); safe {
		t.Fatalf("the identity put back is SAFE: %s", reason)
	}
	if back.ledger.LastSeq() != later || back.ledger.SealedSeq() != sealedAgain {
		t.Fatalf("put back, the record ends at %d sealed through %d; it was %d and %d", back.ledger.LastSeq(), back.ledger.SealedSeq(), later, sealedAgain)
	}
	if _, err := back.verifyLiveLedger(back.configSnapshot()); err != nil {
		t.Errorf("the identity put back does not verify: %v", err)
	}
}

// .
// .
// .
// .
// .
// .
// .
func TestARestoreBehindTheWitnessEndsInSafeNotInUnwitnessedRunning(t *testing.T) {
	f := newSealedFixture(t)
	a := New(f.cfg)
	if err := startLiveForTest(a); err != nil {
		t.Fatal(err)
	}
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	if err := a.anchorer.CheckAndAnchor(); err != nil {
		a.Stop()
		t.Fatalf("the first anchor: %v", err)
	}
	a.runMaintenance(t.Context())
	kept, _ := keptSnapshots(a.backupsDir(a.configSnapshot()))
	if len(kept) == 0 {
		a.Stop()
		t.Fatal("rig: no snapshot")
	}
	snap := kept[len(kept)-1].Name
	at := a.ledger.LastSeq()
	for i := 0; i < 3; i++ {
		appendFixtureEvent(t, a, f.keyPath, fmt.Sprintf("exp_after_the_snapshot_%d", i))
	}
	if err := a.anchorer.CheckAndAnchor(); err != nil {
		a.Stop()
		t.Fatalf("the anchor: %v", err)
	}
	// .
	// .
	// .
	// .
	witnessed, identities := f.witness.Holds()
	if identities != 1 {
		a.Stop()
		t.Fatalf("rig: the witness has bookmarked %d identities, so there is no one rollback to refuse", identities)
	}
	// .
	// .
	// .
	if int64(at)+1 > witnessed {
		a.Stop()
		t.Fatalf("rig: restored to %d and one record on is %d, which the witness at %d would not refuse", at, at+1, witnessed)
	}
	plan, err := a.restorePlan(sourceSnapshot, snap)
	if err != nil {
		t.Fatal(err)
	}
	if plan.WitnessedSetAside == 0 {
		t.Fatalf("rig: the plan says the witness will notice nothing: %+v", plan)
	}
	if err := a.requestRestore(sourceSnapshot, snap, plan.ConfirmText); err != nil {
		t.Fatal(err)
	}
	a.Stop()

	restored := New(f.cfg)
	if err := restored.StartEmbedded(); err != nil {
		t.Fatal(err)
	}
	defer restored.Stop()
	if reason, safe := restored.SafeMode(); safe {
		t.Fatalf("the restore itself must not be SAFE: %s", reason)
	}
	// .
	appendFixtureEvent(t, restored, f.keyPath, "exp_after_the_restore")
	if int64(restored.ledger.LastSeq()) > witnessed {
		t.Fatalf("rig: the restored record is at %d, past the witness at %d", restored.ledger.LastSeq(), witnessed)
	}
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	_ = restored.anchorer.CheckAndAnchor()
	// .
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
	if !strings.Contains(reason, "rollback") {
		t.Errorf("the SAFE reason does not name what happened: %q", reason)
	}

	// .
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

// .
// .
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

// .
// .
// .
func TestARestoreThatCannotFinishBootsSafeAndKeepsItsJournal(t *testing.T) {
	r, _, later := restoreAsked(t)

	// .
	// .
	prev := restoreStep
	restoreStep = func(step string) {
		if step == "moved:0" {
			staging, _ := filepath.Glob(filepath.Join(r.dir, restoreStagePrefix+"*"))
			for _, s := range staging {
				if err := os.Remove(filepath.Join(s, "set", "ledger.jsonl")); err != nil {
					t.Errorf("rig: %v", err)
				}
			}
		}
	}
	a := New(r.cfg)
	err := a.StartEmbedded()
	restoreStep = prev
	if err != nil {
		t.Fatalf("the start must come back SAFE, not die: %v", err)
	}
	defer a.Stop()
	reason, safe := a.SafeMode()
	if !safe {
		t.Fatalf("A HALF-INSTALLED PAIR WAS OPENED BY AN ORDINARY BOOT: %q", reason)
	}
	for _, want := range []string{"did not finish", "ledger.jsonl"} {
		if !strings.Contains(reason, want) {
			t.Errorf("the reason does not say %q: %q", want, reason)
		}
	}
	if _, err := os.Stat(filepath.Join(r.dir, restoreJournalName)); err != nil {
		t.Errorf("the journal must stay for the next start: %v", err)
	}
	// .
	sets := setAsideSets(a.configSnapshot())
	if len(sets) != 1 {
		t.Fatalf("%d sets aside, want one", len(sets))
	}
	if seq, _, convs := rows(t, filepath.Join(sets[0].Path, "aii.db")); seq != later || convs != 7 {
		t.Errorf("the preserved original: record %d, %d conversations; want %d and 7", seq, convs, later)
	}
}

// .
// .
// .
// .
func TestAJournalThatDoesNotReadIsNotAnAbsentJournal(t *testing.T) {
	// .
	// .
	// .
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
		// .
		// .
		// .
		{"a journal that lists nothing to do", func(t *testing.T, path string) {
			aside := filepath.Join(filepath.Dir(path), "set-aside", "Restorable-20260921T000000Z-before-restore-to-seq1")
			j, err := json.Marshal(restoreJournal{Aside: aside})
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
	put(t, filepath.Join(r.dir, restoreJournalName))
	a := New(r.cfg)
	if err := a.StartEmbedded(); err != nil {
		t.Fatalf("the start must come back SAFE, not die: %v", err)
	}
	reason, safe := a.SafeMode()
	a.Stop()
	if !safe || !strings.Contains(reason, "a restore journal is here") {
		t.Fatalf("a journal that does not read, with the record still here: safe=%v %q", safe, reason)
	}

	// .
	// .
	if err := os.Rename(r.ledgerPath, filepath.Join(r.dir, "ledger.jsonl.moved")); err != nil {
		t.Fatal(err)
	}
	b := New(r.cfg)
	err := b.StartEmbedded()
	b.Stop()
	if err == nil || !strings.Contains(err.Error(), "recovery required") {
		t.Fatalf("an unreadable journal must not open the boot decision's door: %v", err)
	}
}

// .
// .
// .
// .
func TestARestoreRefusesWhenSomethingItWasToKeepWentMissing(t *testing.T) {
	r, _, _ := restoreAsked(t)
	prev := restoreStep
	restoreStep = func(step string) {
		if step == "journalled" {
			if err := os.Remove(r.dbPath); err != nil {
				t.Errorf("rig: %v", err)
			}
			panic(errRestoreKilled)
		}
	}
	func() {
		defer func() {
			if v := recover(); v != nil && v != errRestoreKilled {
				panic(v)
			}
		}()
		dying := New(r.cfg)
		_ = dying.StartEmbedded()
		dying.Stop()
	}()
	restoreStep = prev

	a := New(r.cfg)
	if err := a.StartEmbedded(); err != nil {
		t.Fatalf("the start must come back SAFE, not die: %v", err)
	}
	defer a.Stop()
	reason, safe := a.SafeMode()
	if !safe {
		t.Fatalf("A FILE THE RESTORE WAS TO KEEP WENT MISSING AND THE BOOT WENT ON: %q", reason)
	}
	if !strings.Contains(reason, "aii.db") || !strings.Contains(reason, "neither where it was") {
		t.Errorf("the reason does not name the file that is gone: %q", reason)
	}
}

// .
// .
// .
// .
// .
func TestARestoreRefusesAKeyInPlaceThatIsNotTheOneItWouldPut(t *testing.T) {
	// .
	// .
	// .
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
	a.runMaintenance(t.Context())
	kept, _ := keptSnapshots(a.backupsDir(a.configSnapshot()))
	snap := kept[len(kept)-1].Name
	appendFixtureEvent(t, a, f.keyPath, "exp_after")
	if err := a.requestRestore(sourceSnapshot, snap, a.restoreConfirmText()); err != nil {
		t.Fatal(err)
	}
	a.Stop()

	keys, err := filepath.Glob(filepath.Join(witness.WitnessKeysDir(f.dir), "*.json"))
	if err != nil || len(keys) == 0 {
		t.Fatalf("rig: no witness key beside the ledger: %v", err)
	}
	prev := restoreStep
	restoreStep = func(step string) {
		if step == "journalled" {
			putInTheWay(t, keys[0])
		}
	}
	b := New(f.cfg)
	err = b.StartEmbedded()
	restoreStep = prev
	if err != nil {
		t.Fatalf("the start must come back SAFE, not die: %v", err)
	}
	defer b.Stop()
	reason, safe := b.SafeMode()
	if !safe {
		t.Fatalf("A DIFFERENT KEY STOOD WHERE THE RESTORE WOULD PUT ONE AND IT WENT ON: %q", reason)
	}
	if !strings.Contains(reason, says) {
		t.Errorf("the reason does not say what it met (%q): %q", says, reason)
	}
}

// .
// .
// .
// .
// .
// .
func TestASetThatDiedWithAWriteAheadLogIsPutBackWhole(t *testing.T) {
	r := newRestoreRig(t)
	a := r.boot(t)
	live(t, a, 3)
	appendFixtureEvent(t, a, r.keyPath, "exp_before")
	a.runMaintenance(t.Context())
	kept, _ := keptSnapshots(a.backupsDir(a.configSnapshot()))
	snap := kept[len(kept)-1].Name
	at := a.ledger.LastSeq()
	live(t, a, 4)
	appendFixtureEvent(t, a, r.keyPath, "exp_after")
	later := a.ledger.LastSeq()

	// .
	// .
	// .
	died := t.TempDir()
	for try := 0; ; try++ {
		if try == 20 {
			a.Stop()
			t.Fatal("rig: the record never held still long enough to image")
		}
		seq := a.ledger.LastSeq()
		for _, name := range []string{"aii.db", "aii.db-wal", "ledger.jsonl"} {
			raw, err := os.ReadFile(filepath.Join(r.dir, name))
			if err != nil {
				a.Stop()
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
		a.Stop()
		t.Fatalf("rig: the running identity kept nothing in a write-ahead log, so this is not the case: %v", err)
	}
	if err := a.requestRestore(sourceSnapshot, snap, a.restoreConfirmText()); err != nil {
		a.Stop()
		t.Fatal(err)
	}
	a.Stop()
	// .
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
		restored.Stop()
		t.Fatalf("the restore: safe=%v %q, record at %d want %d", safe, reason, restored.ledger.LastSeq(), at)
	}
	sets := setAsideSets(restored.configSnapshot())
	if len(sets) != 1 {
		restored.Stop()
		t.Fatalf("%d sets aside, want one", len(sets))
	}
	if st, err := os.Stat(filepath.Join(sets[0].Path, "aii.db-wal")); err != nil || st.Size() == 0 {
		restored.Stop()
		t.Fatalf("the set kept aside must hold the log its database died with: %v", err)
	}
	asideWAL, _ := os.ReadFile(filepath.Join(sets[0].Path, "aii.db-wal"))

	if err := restored.requestRestore(sourceSetAside, sets[0].Name, restored.restoreConfirmText()); err != nil {
		restored.Stop()
		t.Fatal(err)
	}
	restored.Stop()
	back, err := r.bootThroughChoice(t)
	if err != nil {
		t.Fatalf("PUT THIS BACK: %v", err)
	}
	if f := lastRestoreFailure(back.configSnapshot()); f != nil {
		back.Stop()
		t.Fatalf("PUT THIS BACK WAS REFUSED for a set that died with a write-ahead log: %s", f.Why)
	}
	reason, safe := back.SafeMode()
	seq := back.ledger.LastSeq()
	back.Stop()
	if safe || seq != later {
		t.Fatalf("put back: safe=%v %q, record at %d want %d", safe, reason, seq, later)
	}
	// .
	// .
	if got, ticks, convs := rows(t, r.dbPath); got != later || ticks != 7 || convs != 7 {
		t.Errorf("put back, the database holds record %d, %d ticks, %d conversations; what died held %d, 7, 7", got, ticks, convs, later)
	}
	// .
	if now, err := os.ReadFile(filepath.Join(sets[0].Path, "aii.db-wal")); err != nil || !bytes.Equal(now, asideWAL) {
		t.Errorf("the set kept aside was written to while it was being proved (%v)", err)
	}
}

// .
// .
// .
// .
// .
// .
// .
func TestARestoreAndItsUndoWorkUnderConfiguredNames(t *testing.T) {
	r := newRestoreRigNamed(t, "record.jsonl", "mind.db")
	a := r.boot(t)
	live(t, a, 2)
	appendFixtureEvent(t, a, r.keyPath, "exp_before")
	a.runMaintenance(t.Context())
	kept, _ := keptSnapshots(a.backupsDir(a.configSnapshot()))
	snap := kept[len(kept)-1].Name
	at := a.ledger.LastSeq()
	live(t, a, 3)
	appendFixtureEvent(t, a, r.keyPath, "exp_after")
	later := a.ledger.LastSeq()
	if err := a.requestRestore(sourceSnapshot, snap, a.restoreConfirmText()); err != nil {
		t.Fatal(err)
	}
	a.Stop()

	restored, err := r.bootThroughChoice(t)
	if err != nil {
		t.Fatalf("the restore: %v", err)
	}
	if f := lastRestoreFailure(restored.configSnapshot()); f != nil {
		restored.Stop()
		t.Fatalf("the restore was refused: %s", f.Why)
	}
	if restored.ledger.LastSeq() != at {
		restored.Stop()
		t.Fatalf("the record ends at %d, want %d", restored.ledger.LastSeq(), at)
	}
	sets := setAsideSets(restored.configSnapshot())
	if len(sets) != 1 {
		restored.Stop()
		t.Fatalf("%d sets aside, want one", len(sets))
	}
	for _, name := range []string{"ledger.jsonl", snapshotDB} {
		if !fileExists(filepath.Join(sets[0].Path, name)) {
			t.Errorf("the set kept aside does not hold %s: it is not a source a restore can read", name)
		}
	}
	if err := restored.requestRestore(sourceSetAside, sets[0].Name, restored.restoreConfirmText()); err != nil {
		restored.Stop()
		t.Fatal(err)
	}
	restored.Stop()
	back, err := r.bootThroughChoice(t)
	if err != nil {
		t.Fatalf("PUT THIS BACK: %v", err)
	}
	if f := lastRestoreFailure(back.configSnapshot()); f != nil {
		back.Stop()
		t.Fatalf("PUT THIS BACK WAS REFUSED under configured names: %s", f.Why)
	}
	seq := back.ledger.LastSeq()
	back.Stop()
	if seq != later {
		t.Fatalf("put back, the record ends at %d, want %d", seq, later)
	}
	if got, ticks, convs := rows(t, r.dbPath); got != later || ticks != 5 || convs != 5 {
		t.Errorf("put back, %s holds record %d, %d ticks, %d conversations; want %d, 5, 5", filepath.Base(r.dbPath), got, ticks, convs, later)
	}
	if fileExists(filepath.Join(r.dir, snapshotDB)) || fileExists(filepath.Join(r.dir, "ledger.jsonl")) {
		t.Error("a restore put the pair under the default names beside the configured ones")
	}
}

// .
// .
// .
// .
func TestAJournalIsHeldToItsOwnRestore(t *testing.T) {
	for _, tc := range []struct {
		what   string
		tamper func(r *restoreRig, j *restoreJournal, outside string)
		still  func(r *restoreRig, outside string) string
	}{
		{"it takes the signing key aside", func(r *restoreRig, j *restoreJournal, _ string) {
			j.Moves = append([]restoreMove{{Kind: movePreserve, From: r.keyPath, To: filepath.Join(j.Aside, filepath.Base(r.keyPath))}}, j.Moves...)
		}, func(r *restoreRig, _ string) string { return r.keyPath }},
		{"it moves the record out of this identity's directory", func(r *restoreRig, j *restoreJournal, outside string) {
			j.Moves[0].To = filepath.Join(outside, "taken.db")
		}, func(r *restoreRig, _ string) string { return r.dbPath }},
		{"it installs a file from outside its staging", func(r *restoreRig, j *restoreJournal, outside string) {
			j.Moves = append(j.Moves, restoreMove{Kind: moveInstall, From: filepath.Join(outside, "planted"), To: filepath.Join(r.dir, witness.TailFileName)})
		}, func(r *restoreRig, outside string) string { return filepath.Join(outside, "planted") }},
		{"its set-aside directory is somewhere else", func(r *restoreRig, j *restoreJournal, outside string) {
			for i := range j.Moves {
				j.Moves[i].To = strings.Replace(j.Moves[i].To, j.Aside, outside, 1)
			}
			j.Aside = outside
		}, func(r *restoreRig, _ string) string { return r.dbPath }},
		{"it climbs out with dots", func(r *restoreRig, j *restoreJournal, _ string) {
			j.Moves[0].To = filepath.Join(j.Aside, "..", "..", "climbed.db")
		}, func(r *restoreRig, _ string) string { return r.dbPath }},
	} {
		t.Run(tc.what, func(t *testing.T) {
			r, _, _ := restoreAsked(t)
			killAt(t, r, "journalled")
			outside := t.TempDir()
			if err := os.WriteFile(filepath.Join(outside, "planted"), []byte("{}"), 0o600); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(r.dir, restoreJournalName)
			var j restoreJournal
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
			a.Stop()
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
	// .
	// .
}

// .
// .
// .
// .
func TestFilesABootDecidesByArePublishedByRename(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, restoreJournalName)
	if err := os.WriteFile(path, []byte("what was here before"), 0o600); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeFileDurably(path, []byte(`{"whole":true}`+"\n")); err != nil {
		t.Fatal(err)
	}
	after, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if os.SameFile(before, after) {
		t.Fatal("THE REAL NAME WAS WRITTEN INTO: a crash mid-write leaves half a file under it")
	}
	if got, _ := os.ReadFile(path); string(got) != `{"whole":true}`+"\n" {
		t.Fatalf("published %q", got)
	}
	if after.Mode().Perm() != 0o600 {
		t.Errorf("published with mode %v, want 0600", after.Mode().Perm())
	}
	if left, _ := filepath.Glob(filepath.Join(dir, ".*.tmp")); len(left) != 0 {
		t.Errorf("a publication left its working file behind: %v", left)
	}
	// .
	if err := writeFileDurably(filepath.Join(dir, "no-such-dir", "x.json"), []byte("x")); err == nil {
		t.Error("a write into a directory that does not exist reported success")
	}
}

// .
// .
// .
// .
// .
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
	sets := setAsideSets(a.configSnapshot())
	a.Stop()
	if len(sets) != 1 {
		t.Fatalf("%d sets aside, want one", len(sets))
	}
	if got, err := os.ReadFile(filepath.Join(sets[0].Path, snapshotDB+"-journal")); err != nil || !bytes.Equal(got, stale) {
		t.Errorf("the live database's journal did not go aside with it (%v)", err)
	}
	if fileExists(r.dbPath + "-journal") {
		t.Error("AN OLD JOURNAL STAYED BESIDE THE RESTORED DATABASE, where SQLite would apply it to the wrong image")
	}

	// .
	set := filepath.Join(t.TempDir(), "staging", "set")
	if err := os.MkdirAll(set, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{snapshotDB, snapshotDB + "-journal", "ledger.jsonl"} {
		if err := os.WriteFile(filepath.Join(set, name), []byte(name), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	j, err := planRestoreMoves(*r.cfg, restoreRequest{Source: sourceSetAside, Name: "a-set"}, set, filepath.Dir(set), 1, "fp", "Label")
	if err != nil {
		t.Fatal(err)
	}
	installed := false
	for _, m := range j.Moves {
		if m.Kind == moveInstall && m.From == filepath.Join(set, snapshotDB+"-journal") && m.To == r.dbPath+"-journal" {
			installed = true
		}
	}
	if !installed {
		t.Errorf("a staged rollback journal is not installed beside its database: %+v", j.Moves)
	}
}

// .
// .
// .
// .
// .
// .
func TestAJournalsRequestIsHeldToTheSameNamesTheDoorIs(t *testing.T) {
	for _, tc := range []struct{ what, source, name string }{
		{"a set-aside name that climbs out", sourceSetAside, "../../../elsewhere"},
		{"a set-aside name that is not one", sourceSetAside, "Ivy"},
		{"a snapshot name that climbs out", sourceSnapshot, "../../../elsewhere"},
		{"a source that is neither", "anywhere", "ledger-20260921T000000Z-seq1"},
	} {
		t.Run(tc.what, func(t *testing.T) {
			r, _, _ := restoreAsked(t)
			killAt(t, r, "journalled")
			path := filepath.Join(r.dir, restoreJournalName)
			var j restoreJournal
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
			// .
			// .
			outside := t.TempDir()
			elsewhere := filepath.Join(outside, restoreRecordName)
			was := []byte(`{"set_aside_at":"somebody else's","identity":"not this one"}`)
			if err := os.WriteFile(elsewhere, was, 0o600); err != nil {
				t.Fatal(err)
			}

			a := New(r.cfg)
			if err := a.StartEmbedded(); err != nil {
				t.Fatalf("the start must come back SAFE, not die: %v", err)
			}
			reason, safe := a.SafeMode()
			a.Stop()
			if !safe || !strings.Contains(reason, "restore journal") {
				t.Fatalf("A JOURNAL NAMING %q WAS ACTED ON: safe=%v %q", tc.name, safe, reason)
			}
			if now, _ := os.ReadFile(elsewhere); !bytes.Equal(now, was) {
				t.Errorf("a refused journal rewrote a record outside this identity's tree")
			}
		})
	}
}
