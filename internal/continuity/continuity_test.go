package continuity

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/escrow"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

// continuity_test.go — the identity's continuity surface on the host's
// side, hazard by hazard. Built on
// maintApp, so every snapshot here is a real one: captured at the door,
// proved, and published by the pass's own path.

// fakeSnapshot publishes a complete plaintext snapshot's NAME — a
// directory holding a checksum list — for the tests that are about the set,
// not about what is in it.
func fakeSnapshot(t *testing.T, dir, name string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, name), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name, SnapshotSums), nil, 0o600); err != nil {
		t.Fatal(err)
	}
}

func stampAt(at time.Time) string { return at.UTC().Format("20060102T150405Z") }

func refusedFor(t *testing.T, err error, reason string) *Refusal {
	t.Helper()
	var r *Refusal
	if !errors.As(err, &r) || r.Reason != reason {
		t.Fatalf("want a refusal for %q, got %v", reason, err)
	}
	return r
}

// NO NUMBER OF SNAPSHOTS THAT WERE ASKED FOR TOUCHES A DAILY ONE. Prune kept
// the newest N of one set; eight asked for in an afternoon would have left
// eight copies of now and no way back past today.
func TestSnapshotsThatWereAskedForNeverEvictADailyOne(t *testing.T) {
	a, _, keyPath := maintApp(t)
	cfg := a.configSnapshot()
	dir := a.backupsDir(cfg)
	now := time.Now()
	var daily []string
	for d := 8; d >= 1; d-- {
		name := fmt.Sprintf("ledger-%s-seq%d", stampAt(now.Add(-time.Duration(d)*24*time.Hour)), 100-d)
		fakeSnapshot(t, dir, name)
		daily = append(daily, name)
	}
	for i := 0; i < 20; i++ {
		appendFixtureEvent(t, a, keyPath, fmt.Sprintf("exp_take_%d", i))
		if _, err := a.continuity.takeOnDemand(t.Context(), false, time.Now()); err != nil {
			t.Fatalf("take %d: %v", i, err)
		}
	}
	kept, _ := keptSnapshots(dir)
	var asked int
	have := map[string]bool{}
	for _, k := range kept {
		have[k.Name] = true
		if k.OnDemand {
			asked++
		}
	}
	for _, name := range daily {
		if !have[name] {
			t.Errorf("TWENTY SNAPSHOTS THAT WERE ASKED FOR REMOVED A DAILY ONE: %s is gone", name)
		}
	}
	if asked != 1 {
		t.Errorf("%d asked-for snapshots are kept, want the last one only", asked)
	}
	if newest := kept[len(kept)-1]; !newest.OnDemand || newest.Record != a.ledger.LastSeq() {
		t.Errorf("the one that is kept is not the last one taken: %+v (ledger at %d)", newest, a.ledger.LastSeq())
	}
	left, _ := filepath.Glob(filepath.Join(dir, SnapshotWorkPrefix+"*"))
	if len(left) != 0 {
		t.Errorf("replacing the last one left working sets behind: %v", left)
	}
}

// AND THE TWO CLASSES ARE BOUNDED APART, whatever order they were made in.
func TestPruneKeepsTheDailySetAndTheLastOneAskedFor(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	for d := 12; d >= 1; d-- {
		fakeSnapshot(t, dir, fmt.Sprintf("ledger-%s-seq%d", stampAt(now.Add(-time.Duration(d)*24*time.Hour)), 50-d))
	}
	for h := 5; h >= 1; h-- {
		fakeSnapshot(t, dir, fmt.Sprintf("ledger-%s-seq%d%s", stampAt(now.Add(-time.Duration(h)*time.Hour)), 50, onDemandMark))
	}
	if removed := pruneBackups(dir, 8); removed != 4+4 {
		t.Fatalf("pruned %d, want the four oldest daily and the four older asked-for", removed)
	}
	kept, _ := keptSnapshots(dir)
	var daily, asked []keptSnapshot
	for _, k := range kept {
		if k.OnDemand {
			asked = append(asked, k)
		} else {
			daily = append(daily, k)
		}
	}
	if len(daily) != 8 || len(asked) != 1 {
		t.Fatalf("kept %d daily and %d asked-for, want 8 and 1", len(daily), len(asked))
	}
	if daily[0].Stamp != stampAt(now.Add(-8*24*time.Hour)) || asked[0].Stamp != stampAt(now.Add(-time.Hour)) {
		t.Errorf("the NEWEST of each class must stay: oldest daily kept %s, asked-for kept %s", daily[0].Stamp, asked[0].Stamp)
	}
	// Every reader of the name reads both classes: boot's evidence that an
	// identity lives here, and the encrypted form.
	for _, name := range []string{"ledger-20260920T101500Z-seq7" + onDemandMark, "ledger-20260920T101500Z-seq7" + onDemandMark + escrow.SnapshotSuffix} {
		if !backupSeqRe.MatchString(name) || !backupEvidenceRe.MatchString(name) {
			t.Errorf("%s is not read as a snapshot by the name rule and by boot's evidence check", name)
		}
	}
}

// THE IDENTITY'S DOOR IS SPACED, and the spacing is read from the last
// asked-for snapshot's own name: it holds across a restart and keeps no
// state. The person's door is not spaced.
func TestASnapshotAskedForTooSoonIsRefusedAndSaysWhen(t *testing.T) {
	a, _, keyPath := maintApp(t)
	dir := a.backupsDir(a.configSnapshot())
	appendFixtureEvent(t, a, keyPath, "exp_a")
	if _, err := a.continuity.takeOnDemand(t.Context(), true, time.Now()); err != nil {
		t.Fatalf("the first take: %v", err)
	}
	_, err := a.continuity.takeOnDemand(t.Context(), true, time.Now().Add(30*time.Second))
	r := refusedFor(t, err, "spaced")
	if !strings.Contains(r.Text, "ago") || !strings.Contains(r.Text, "the daily pass runs whatever you do") {
		t.Errorf("the refusal does not say when, or that nothing is lost: %s", r.Text)
	}
	kept, _ := keptSnapshots(dir)
	if len(kept) != 1 {
		t.Fatalf("a refused take made a snapshot: %v", kept)
	}
	// Past the spacing, and through the unspaced door at once: both take.
	if _, err := a.continuity.takeOnDemand(t.Context(), true, time.Now().Add(DefaultOnDemandSpacing+time.Second)); err != nil {
		t.Errorf("past the spacing: %v", err)
	}
	if _, err := a.continuity.takeOnDemand(t.Context(), false, time.Now()); err != nil {
		t.Errorf("the person's door is not spaced: %v", err)
	}
}

// ONE PASS AT A TIME: an ask does not wait and says one is running; the
// daily pass waits, and runs.
func TestAnAskDuringAPassSaysOneIsRunning(t *testing.T) {
	a, _, keyPath := maintApp(t)
	appendFixtureEvent(t, a, keyPath, "exp_a")
	a.continuity.runMaintenance(t.Context())   // one kept snapshot, so a proof has something to prove
	appendFixtureEvent(t, a, keyPath, "exp_b") // the next pass is at another record: another name, whatever the second
	a.continuity.maint.mu.Lock()
	// An ask that WAITED would hang here for as long as the pass ran; it is
	// asked from its own goroutine so that waiting is a failure, not a hang.
	asked := make(chan error, 2)
	go func() {
		_, err := a.continuity.takeOnDemand(t.Context(), true, time.Now())
		asked <- err
		_, err = a.continuity.verifyKept(t.Context(), "", true, time.Now())
		asked <- err
	}()
	for _, what := range []string{"a take", "a proof"} {
		select {
		case err := <-asked:
			refusedFor(t, err, "busy")
		case <-time.After(5 * time.Second):
			a.continuity.maint.mu.Unlock()
			t.Fatalf("%s WAITED for the pass instead of saying one is running", what)
		}
	}
	done := make(chan struct{})
	go func() { a.continuity.runMaintenance(t.Context()); close(done) }()
	select {
	case <-done:
		t.Fatal("the daily pass ran while another held the lock")
	case <-time.After(150 * time.Millisecond):
	}
	a.continuity.maint.mu.Unlock()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("THE DAILY PASS NEVER RAN: it must wait for the lock, not give the day up")
	}
	if kept, _ := keptSnapshots(a.backupsDir(a.configSnapshot())); len(kept) < 1 || kept[len(kept)-1].OnDemand || kept[len(kept)-1].Record != a.ledger.LastSeq() {
		t.Fatalf("the daily pass that waited did not publish its snapshot: %v", kept)
	}
}

// SAFE MAKES NO FILES, AND AN OPERATOR WHO SWITCHED THE PASS OFF SWITCHED
// THIS OFF WITH IT. The read answers in both.
func TestSafeAndASwitchedOffPassRefuseTheActsAndAnswerTheRead(t *testing.T) {
	a, _, keyPath := maintApp(t)
	appendFixtureEvent(t, a, keyPath, "exp_a")
	off := false
	a.cfg.Maintenance.Enabled = &off
	_, err := a.continuity.takeOnDemand(t.Context(), true, time.Now())
	refusedFor(t, err, "disabled")
	_, err = a.continuity.verifyKept(t.Context(), "", true, time.Now())
	refusedFor(t, err, "disabled")
	if out, err := a.continuity.continuityRead("", time.Now()); err != nil || !strings.Contains(out, "switched the pass off") {
		t.Errorf("the read under a switched-off pass: %v\n%s", err, out)
	}
	a.cfg.Maintenance.Enabled = nil

	a.enterSafe("test: the record is frozen")
	_, err = a.continuity.takeOnDemand(t.Context(), true, time.Now())
	refusedFor(t, err, "safe")
	_, err = a.continuity.verifyKept(t.Context(), "", true, time.Now())
	refusedFor(t, err, "safe")
	out, err := a.continuity.continuityRead("", time.Now())
	if err != nil || !strings.Contains(out, "this boot is SAFE") {
		t.Errorf("the read in SAFE: %v\n%s", err, out)
	}
	if kept, _ := keptSnapshots(a.backupsDir(a.configSnapshot())); len(kept) != 0 {
		t.Errorf("a refused act made files: %v", kept)
	}
}

// THE READ IS SIX LINES, and under every reason a snapshot can be
// unencrypted for it carries no path, no key and no command: those reasons
// were written for an operator at a terminal.
func TestTheReadIsSixLinesAndCarriesNoPathAndNoSecret(t *testing.T) {
	a, home, keyPath := maintApp(t)
	cfg := a.configSnapshot()
	appendFixtureEvent(t, a, keyPath, "exp_a")
	a.continuity.runMaintenance(t.Context())

	check := func(state, wantReason string) {
		t.Helper()
		out, err := a.continuity.continuityRead("", time.Now())
		if err != nil {
			t.Fatalf("%s: %v", state, err)
		}
		lines := strings.Split(out, "\n")
		if len(lines) != 6 {
			t.Errorf("%s: %d lines, want six:\n%s", state, len(lines), out)
		}
		for i, prefix := range []string{"snapshots: ", "last pass: ", "encryption: ", "escrow of your keys: ", "chain: ", "next pass: "} {
			if i < len(lines) && !strings.HasPrefix(lines[i], prefix) {
				t.Errorf("%s: line %d is %q, want it to begin %q", state, i+1, lines[i], prefix)
			}
		}
		for _, leak := range []string{home, string(os.PathSeparator) + "data", "identity.sec", escrow.SnapshotKeyFileName, "AGE-SECRET-KEY", "age1", "aii escrow", "-ledger", a.door.kp.Fingerprint()} {
			if strings.Contains(out, leak) {
				t.Errorf("%s: THE READ CARRIES %q:\n%s", state, leak, out)
			}
		}
		if wantReason != "" && !strings.Contains(out, store.UnencryptedForIdentity(wantReason)) {
			t.Errorf("%s: the read does not word the reason %s for the identity:\n%s", state, wantReason, out)
		}
	}
	check("no snapshot key", store.UnencryptedNoKey)
	hostSnapshotKey(t, a)
	check("a key and no escrow", store.UnencryptedNoEscrow)
	if err := os.WriteFile(a.continuity.escrowReceiptPath(cfg), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	check("a receipt that does not read", store.UnencryptedReceiptUnread)
	checkedEscrow(t, a, a.door.kp, "age1pq1notthisone")
	check("a receipt for another key", store.UnencryptedReceiptMismatch)
	if err := os.WriteFile(a.continuity.snapshotKeyPath(cfg), []byte("not a key"), 0o600); err != nil {
		t.Fatal(err)
	}
	check("a key that is not one", store.UnencryptedKeyUnusable)
	os.Remove(a.continuity.snapshotKeyPath(cfg))
	_, recipient := hostSnapshotKey(t, a)
	checkedEscrow(t, a, a.door.kp, recipient)
	check("a checked escrow", "")
	if out, _ := a.continuity.continuityRead("", time.Now()); !strings.Contains(out, "encryption: on") || !strings.Contains(out, "escrow of your keys: checked") {
		t.Errorf("a checked escrow reads as:\n%s", out)
	}
}

// A QUERY WORD OPENS ONE PART AND NOTHING OPENS MORE: the list is never
// longer than what is kept, and a word that names nothing is refused.
func TestTheReadsPartsAreBounded(t *testing.T) {
	a, _, keyPath := maintApp(t)
	dir := a.backupsDir(a.configSnapshot())
	now := time.Now()
	for d := 8; d >= 1; d-- {
		fakeSnapshot(t, dir, fmt.Sprintf("ledger-%s-seq%d", stampAt(now.Add(-time.Duration(d)*24*time.Hour)), 100-d))
	}
	appendFixtureEvent(t, a, keyPath, "exp_a")
	made, err := a.continuity.takeOnDemand(t.Context(), false, now)
	if err != nil {
		t.Fatal(err)
	}
	list, err := a.continuity.continuityRead("snapshots", now)
	if err != nil {
		t.Fatal(err)
	}
	if n := len(strings.Split(list, "\n")); n != 9 {
		t.Errorf("the list has %d lines, want the 8 daily and the 1 asked for:\n%s", n, list)
	}
	if !strings.HasPrefix(list, made.Name+" — ") || !strings.Contains(strings.Split(list, "\n")[0], "asked for") {
		t.Errorf("the list is not newest first, or does not say which was asked for:\n%s", list)
	}
	one, err := a.continuity.continuityRead(made.Name, now)
	if err != nil || !strings.Contains(one, "lived ticks") || strings.Contains(one, "\n") {
		t.Errorf("one snapshot by name: %v\n%s", err, one)
	}
	if _, err := a.continuity.continuityRead("everything", now); err == nil {
		t.Error("a query that names no part was answered")
	}
}

// THE PASS SAYS WHAT HAPPENED ON EVERY WAY OUT, where it can be read. The
// log line was its only record.
func TestThePassRecordsItsResultOnEveryWayOut(t *testing.T) {
	a, home, keyPath := maintApp(t)
	appendFixtureEvent(t, a, keyPath, "exp_a")
	status := func(want string) store.ContinuityStatus {
		t.Helper()
		st, ok, err := a.store.ContinuityStatus()
		if err != nil || !ok || st.Outcome != want || st.At == "" {
			t.Fatalf("want a recorded %q, got ok=%v %+v (%v)", want, ok, st, err)
		}
		return st
	}
	if _, ok, _ := a.store.ContinuityStatus(); ok {
		t.Fatal("rig: a status before any pass")
	}
	a.continuity.runMaintenance(t.Context())
	if st := status(store.ContinuityOK); st.Snapshot == "" || st.Record != a.ledger.LastSeq() || st.Encrypted || st.Unencrypted != store.UnencryptedNoKey || st.FailingSince != "" {
		t.Errorf("a pass that published: %+v", st)
	}

	// A pass that cannot publish: the backups directory is a file.
	dir := a.backupsDir(a.configSnapshot())
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dir, []byte("in the way"), 0o600); err != nil {
		t.Fatal(err)
	}
	a.continuity.runMaintenance(t.Context())
	first := status(store.ContinuityFailed)
	if first.FailingSince == "" || first.Detail == "" {
		t.Errorf("a failed pass: %+v", first)
	}
	time.Sleep(1100 * time.Millisecond)
	a.continuity.runMaintenance(t.Context())
	if again := status(store.ContinuityFailed); again.FailingSince != first.FailingSince || again.At == first.At {
		t.Errorf("a second failure must carry when the failing began: first %+v, then %+v", first, again)
	}
	// What failed names a file; the identity's read does not show it.
	if out, err := a.continuity.continuityRead("", time.Now()); err != nil || !strings.Contains(out, "FAILED") || strings.Contains(out, home) {
		t.Errorf("the read of a failed pass: %v\n%s", err, out)
	}
	os.Remove(dir)

	off := false
	a.cfg.Maintenance.Enabled = &off
	a.continuity.runMaintenance(t.Context())
	status(store.ContinuityDisabled)
	a.cfg.Maintenance.Enabled = nil

	a.enterSafe("test: the record is frozen")
	a.continuity.runMaintenance(t.Context())
	if st := status(store.ContinuitySafe); st.FailingSince != "" {
		t.Errorf("SAFE's walk verified, and the failing run is over: %+v", st)
	}
}

// A KEPT SNAPSHOT IS PROVED AGAIN FROM WHAT IS ON THE DISK NOW — and one
// that has rotted does not prove, is named to the identity without what
// failed, and costs the others nothing.
func TestAKeptSnapshotIsProvedAndOneThatRottedIsNot(t *testing.T) {
	a, home, keyPath := maintApp(t)
	dir := a.backupsDir(a.configSnapshot())
	live(t, a, 2)
	appendFixtureEvent(t, a, keyPath, "exp_a")
	a.continuity.runMaintenance(t.Context())
	kept, _ := keptSnapshots(dir)
	if len(kept) != 1 {
		t.Fatalf("rig: %v", kept)
	}
	if _, err := a.continuity.verifyKept(t.Context(), "ledger-20200101T000000Z-seq1", false, time.Now()); err == nil {
		t.Error("a name that is not one of the snapshots was proved")
	}
	if _, err := a.continuity.verifyKept(t.Context(), "../"+kept[0].Name, false, time.Now()); err == nil {
		t.Error("a name with a path in it reached the disk")
	}
	out, err := a.continuity.verifyKept(t.Context(), "", false, time.Now())
	if err != nil || !strings.Contains(out, "proved: "+kept[0].Name) {
		t.Fatalf("a good snapshot: %v\n%s", err, out)
	}
	before, _ := filesUnder(filepath.Join(dir, kept[0].Name))
	if left, _ := filepath.Glob(filepath.Join(dir, SnapshotWorkPrefix+"*")); len(left) != 0 {
		t.Errorf("the proof left its working set: %v", left)
	}

	// Something in it that its checksum list does not name: only the list
	// can know. Removed again, it proves.
	stray := filepath.Join(dir, kept[0].Name, "stray.txt")
	if err := os.WriteFile(stray, []byte("not part of what was published"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := a.continuity.verifyKept(t.Context(), kept[0].Name, false, time.Now()); err == nil {
		t.Fatal("a snapshot holding a file its checksum list does not name proved")
	}
	os.Remove(stray)
	if _, err := a.continuity.verifyKept(t.Context(), kept[0].Name, false, time.Now()); err != nil {
		t.Fatalf("rig: without the stray file it proves again: %v", err)
	}

	// Rot: one byte of the database copy.
	db := filepath.Join(dir, kept[0].Name, SnapshotDB)
	raw, err := os.ReadFile(db)
	if err != nil {
		t.Fatal(err)
	}
	raw[len(raw)/2] ^= 0xff
	if err := os.WriteFile(db, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	_, err = a.continuity.verifyKept(t.Context(), kept[0].Name, false, time.Now())
	if err == nil || !strings.Contains(err.Error(), "does NOT prove") {
		t.Fatalf("A ROTTED SNAPSHOT PROVED: %v", err)
	}
	if strings.Contains(err.Error(), home) || strings.Contains(err.Error(), SnapshotDB) {
		t.Errorf("what failed names files, and the identity was shown it: %v", err)
	}
	after, _ := filesUnder(filepath.Join(dir, kept[0].Name))
	if len(after) != len(before) {
		t.Errorf("the proof wrote into the published snapshot: %v, then %v", before, after)
	}
	// EDITED AND RE-SUMMED: whoever changes a snapshot can rewrite its
	// checksum list to match, and then every file "matches". What still
	// does not hold is the restore — the database no longer ends where the
	// record does — and that is what the proof is for.
	// Cut in half before it is re-summed: one flipped byte can land where
	// nothing reads it, and then there is nothing for a proof to refuse.
	raw = raw[:len(raw)/2]
	if err := os.WriteFile(db, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	sumsPath := filepath.Join(dir, kept[0].Name, SnapshotSums)
	sums, err := os.ReadFile(sumsPath)
	if err != nil {
		t.Fatal(err)
	}
	h := sha256.Sum256(raw)
	var resummed []string
	for _, line := range strings.Split(strings.TrimRight(string(sums), "\n"), "\n") {
		if strings.HasSuffix(line, "  "+SnapshotDB) {
			line = hex.EncodeToString(h[:]) + "  " + SnapshotDB
		}
		resummed = append(resummed, line)
	}
	if err := os.WriteFile(sumsPath, []byte(strings.Join(resummed, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := escrow.CheckSums(filepath.Join(dir, kept[0].Name), before); err != nil {
		t.Fatalf("rig: the re-summed snapshot must pass its own checksum list: %v", err)
	}
	if _, err := a.continuity.verifyKept(t.Context(), kept[0].Name, false, time.Now()); err == nil {
		t.Fatal("A SNAPSHOT EDITED AND RE-SUMMED PROVED: the checksum list is the editor's to rewrite, the restore is not")
	}
	// The identity's door is spaced on its own stamp.
	if _, err := a.continuity.verifyKept(t.Context(), "", true, time.Now()); err == nil {
		t.Fatal("rig: the rotted snapshot proved")
	}
	_, err = a.continuity.verifyKept(t.Context(), "", true, time.Now().Add(time.Minute))
	refusedFor(t, err, "spaced")
}

// AN ENCRYPTED SNAPSHOT IS PROVED IN THE CLEAR AND NOTHING OF IT STAYS — and
// a proof that never returned leaves what the next normal boot sweeps.
func TestProvingAnEncryptedSnapshotLeavesNothingInTheClear(t *testing.T) {
	a, _, keyPath := maintApp(t)
	dir := a.backupsDir(a.configSnapshot())
	live(t, a, 2)
	appendFixtureEvent(t, a, keyPath, "exp_a")
	_, recipient := hostSnapshotKey(t, a)
	checkedEscrow(t, a, a.door.kp, recipient)
	made, err := a.continuity.takeOnDemand(t.Context(), true, time.Now())
	if err != nil || !made.Encrypted || !strings.HasSuffix(made.Name, onDemandMark+escrow.SnapshotSuffix) {
		t.Fatalf("an asked-for snapshot under a checked escrow: %+v %v", made, err)
	}
	out, err := a.continuity.verifyKept(t.Context(), made.Name, true, time.Now())
	if err != nil || !strings.Contains(out, "proved: "+made.Name) {
		t.Fatalf("an encrypted snapshot: %v\n%s", err, out)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 || entries[0].Name() != made.Name {
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf("THE IDENTITY WAS LEFT IN THE CLEAR beside its encrypted snapshot: %v", names)
	}
	// A proof that was killed: its working set carries the pass's prefix,
	// so the boot's sweep takes it.
	stranded := filepath.Join(dir, SnapshotWorkPrefix+"verify-killed.tmp")
	if err := os.MkdirAll(filepath.Join(stranded, "set"), 0o700); err != nil {
		t.Fatal(err)
	}
	a.continuity.SweepSnapshotDebrisAtBoot(a.configSnapshot())
	if _, err := os.Stat(stranded); !os.IsNotExist(err) {
		t.Errorf("a killed proof's working set survives the boot's sweep: %v", err)
	}
}
