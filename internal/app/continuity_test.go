package app

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

// .
// .
// .
// .

// .
// .
// .
func fakeSnapshot(t *testing.T, dir, name string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, name), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name, snapshotSums), nil, 0o600); err != nil {
		t.Fatal(err)
	}
}

func stampAt(at time.Time) string { return at.UTC().Format("20060102T150405Z") }

func refusedFor(t *testing.T, err error, reason string) *ContinuityRefusal {
	t.Helper()
	var r *ContinuityRefusal
	if !errors.As(err, &r) || r.Reason != reason {
		t.Fatalf("want a refusal for %q, got %v", reason, err)
	}
	return r
}

// .
// .
// .
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
		if _, err := a.takeOnDemand(t.Context(), false, time.Now()); err != nil {
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
	left, _ := filepath.Glob(filepath.Join(dir, snapshotWorkPrefix+"*"))
	if len(left) != 0 {
		t.Errorf("replacing the last one left working sets behind: %v", left)
	}
}

// .
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
	// .
	// .
	for _, name := range []string{"ledger-20260920T101500Z-seq7" + onDemandMark, "ledger-20260920T101500Z-seq7" + onDemandMark + escrow.SnapshotSuffix} {
		if !backupSeqRe.MatchString(name) || !backupEvidenceRe.MatchString(name) {
			t.Errorf("%s is not read as a snapshot by the name rule and by boot's evidence check", name)
		}
	}
}

// .
// .
// .
func TestASnapshotAskedForTooSoonIsRefusedAndSaysWhen(t *testing.T) {
	a, _, keyPath := maintApp(t)
	dir := a.backupsDir(a.configSnapshot())
	appendFixtureEvent(t, a, keyPath, "exp_a")
	if _, err := a.takeOnDemand(t.Context(), true, time.Now()); err != nil {
		t.Fatalf("the first take: %v", err)
	}
	_, err := a.takeOnDemand(t.Context(), true, time.Now().Add(30*time.Second))
	r := refusedFor(t, err, "spaced")
	if !strings.Contains(r.Text, "ago") || !strings.Contains(r.Text, "the daily pass runs whatever you do") {
		t.Errorf("the refusal does not say when, or that nothing is lost: %s", r.Text)
	}
	kept, _ := keptSnapshots(dir)
	if len(kept) != 1 {
		t.Fatalf("a refused take made a snapshot: %v", kept)
	}
	// .
	if _, err := a.takeOnDemand(t.Context(), true, time.Now().Add(defaultOnDemandSpacing+time.Second)); err != nil {
		t.Errorf("past the spacing: %v", err)
	}
	if _, err := a.takeOnDemand(t.Context(), false, time.Now()); err != nil {
		t.Errorf("the person's door is not spaced: %v", err)
	}
}

// .
// .
func TestAnAskDuringAPassSaysOneIsRunning(t *testing.T) {
	a, _, keyPath := maintApp(t)
	appendFixtureEvent(t, a, keyPath, "exp_a")
	a.runMaintenance(t.Context())
	appendFixtureEvent(t, a, keyPath, "exp_b")
	a.maintMu.Lock()
	// .
	// .
	asked := make(chan error, 2)
	go func() {
		_, err := a.takeOnDemand(t.Context(), true, time.Now())
		asked <- err
		_, err = a.verifyKept(t.Context(), "", true, time.Now())
		asked <- err
	}()
	for _, what := range []string{"a take", "a proof"} {
		select {
		case err := <-asked:
			refusedFor(t, err, "busy")
		case <-time.After(5 * time.Second):
			a.maintMu.Unlock()
			t.Fatalf("%s WAITED for the pass instead of saying one is running", what)
		}
	}
	done := make(chan struct{})
	go func() { a.runMaintenance(t.Context()); close(done) }()
	select {
	case <-done:
		t.Fatal("the daily pass ran while another held the lock")
	case <-time.After(150 * time.Millisecond):
	}
	a.maintMu.Unlock()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("THE DAILY PASS NEVER RAN: it must wait for the lock, not give the day up")
	}
	if kept, _ := keptSnapshots(a.backupsDir(a.configSnapshot())); len(kept) < 1 || kept[len(kept)-1].OnDemand || kept[len(kept)-1].Record != a.ledger.LastSeq() {
		t.Fatalf("the daily pass that waited did not publish its snapshot: %v", kept)
	}
}

// .
// .
func TestSafeAndASwitchedOffPassRefuseTheActsAndAnswerTheRead(t *testing.T) {
	a, _, keyPath := maintApp(t)
	appendFixtureEvent(t, a, keyPath, "exp_a")
	off := false
	a.cfg.Maintenance.Enabled = &off
	_, err := a.takeOnDemand(t.Context(), true, time.Now())
	refusedFor(t, err, "disabled")
	_, err = a.verifyKept(t.Context(), "", true, time.Now())
	refusedFor(t, err, "disabled")
	if out, err := a.continuityRead("", time.Now()); err != nil || !strings.Contains(out, "switched the pass off") {
		t.Errorf("the read under a switched-off pass: %v\n%s", err, out)
	}
	a.cfg.Maintenance.Enabled = nil

	a.enterSafe("test: the record is frozen")
	_, err = a.takeOnDemand(t.Context(), true, time.Now())
	refusedFor(t, err, "safe")
	_, err = a.verifyKept(t.Context(), "", true, time.Now())
	refusedFor(t, err, "safe")
	out, err := a.continuityRead("", time.Now())
	if err != nil || !strings.Contains(out, "this boot is SAFE") {
		t.Errorf("the read in SAFE: %v\n%s", err, out)
	}
	if kept, _ := keptSnapshots(a.backupsDir(a.configSnapshot())); len(kept) != 0 {
		t.Errorf("a refused act made files: %v", kept)
	}
}

// .
// .
// .
func TestTheReadIsSixLinesAndCarriesNoPathAndNoSecret(t *testing.T) {
	a, home, keyPath := maintApp(t)
	cfg := a.configSnapshot()
	appendFixtureEvent(t, a, keyPath, "exp_a")
	a.runMaintenance(t.Context())

	check := func(state, wantReason string) {
		t.Helper()
		out, err := a.continuityRead("", time.Now())
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
	if err := os.WriteFile(a.escrowReceiptPath(cfg), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	check("a receipt that does not read", store.UnencryptedReceiptUnread)
	checkedEscrow(t, a, a.door.kp, "age1pq1notthisone")
	check("a receipt for another key", store.UnencryptedReceiptMismatch)
	if err := os.WriteFile(a.snapshotKeyPath(cfg), []byte("not a key"), 0o600); err != nil {
		t.Fatal(err)
	}
	check("a key that is not one", store.UnencryptedKeyUnusable)
	os.Remove(a.snapshotKeyPath(cfg))
	_, recipient := hostSnapshotKey(t, a)
	checkedEscrow(t, a, a.door.kp, recipient)
	check("a checked escrow", "")
	if out, _ := a.continuityRead("", time.Now()); !strings.Contains(out, "encryption: on") || !strings.Contains(out, "escrow of your keys: checked") {
		t.Errorf("a checked escrow reads as:\n%s", out)
	}
}

// .
// .
func TestTheReadsPartsAreBounded(t *testing.T) {
	a, _, keyPath := maintApp(t)
	dir := a.backupsDir(a.configSnapshot())
	now := time.Now()
	for d := 8; d >= 1; d-- {
		fakeSnapshot(t, dir, fmt.Sprintf("ledger-%s-seq%d", stampAt(now.Add(-time.Duration(d)*24*time.Hour)), 100-d))
	}
	appendFixtureEvent(t, a, keyPath, "exp_a")
	made, err := a.takeOnDemand(t.Context(), false, now)
	if err != nil {
		t.Fatal(err)
	}
	list, err := a.continuityRead("snapshots", now)
	if err != nil {
		t.Fatal(err)
	}
	if n := len(strings.Split(list, "\n")); n != 9 {
		t.Errorf("the list has %d lines, want the 8 daily and the 1 asked for:\n%s", n, list)
	}
	if !strings.HasPrefix(list, made.Name+" — ") || !strings.Contains(strings.Split(list, "\n")[0], "asked for") {
		t.Errorf("the list is not newest first, or does not say which was asked for:\n%s", list)
	}
	one, err := a.continuityRead(made.Name, now)
	if err != nil || !strings.Contains(one, "lived ticks") || strings.Contains(one, "\n") {
		t.Errorf("one snapshot by name: %v\n%s", err, one)
	}
	if _, err := a.continuityRead("everything", now); err == nil {
		t.Error("a query that names no part was answered")
	}
}

// .
// .
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
	a.runMaintenance(t.Context())
	if st := status(store.ContinuityOK); st.Snapshot == "" || st.Record != a.ledger.LastSeq() || st.Encrypted || st.Unencrypted != store.UnencryptedNoKey || st.FailingSince != "" {
		t.Errorf("a pass that published: %+v", st)
	}

	// .
	dir := a.backupsDir(a.configSnapshot())
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dir, []byte("in the way"), 0o600); err != nil {
		t.Fatal(err)
	}
	a.runMaintenance(t.Context())
	first := status(store.ContinuityFailed)
	if first.FailingSince == "" || first.Detail == "" {
		t.Errorf("a failed pass: %+v", first)
	}
	time.Sleep(1100 * time.Millisecond)
	a.runMaintenance(t.Context())
	if again := status(store.ContinuityFailed); again.FailingSince != first.FailingSince || again.At == first.At {
		t.Errorf("a second failure must carry when the failing began: first %+v, then %+v", first, again)
	}
	// .
	if out, err := a.continuityRead("", time.Now()); err != nil || !strings.Contains(out, "FAILED") || strings.Contains(out, home) {
		t.Errorf("the read of a failed pass: %v\n%s", err, out)
	}
	os.Remove(dir)

	off := false
	a.cfg.Maintenance.Enabled = &off
	a.runMaintenance(t.Context())
	status(store.ContinuityDisabled)
	a.cfg.Maintenance.Enabled = nil

	a.enterSafe("test: the record is frozen")
	a.runMaintenance(t.Context())
	if st := status(store.ContinuitySafe); st.FailingSince != "" {
		t.Errorf("SAFE's walk verified, and the failing run is over: %+v", st)
	}
}

// .
// .
// .
func TestAKeptSnapshotIsProvedAndOneThatRottedIsNot(t *testing.T) {
	a, home, keyPath := maintApp(t)
	dir := a.backupsDir(a.configSnapshot())
	live(t, a, 2)
	appendFixtureEvent(t, a, keyPath, "exp_a")
	a.runMaintenance(t.Context())
	kept, _ := keptSnapshots(dir)
	if len(kept) != 1 {
		t.Fatalf("rig: %v", kept)
	}
	if _, err := a.verifyKept(t.Context(), "ledger-20200101T000000Z-seq1", false, time.Now()); err == nil {
		t.Error("a name that is not one of the snapshots was proved")
	}
	if _, err := a.verifyKept(t.Context(), "../"+kept[0].Name, false, time.Now()); err == nil {
		t.Error("a name with a path in it reached the disk")
	}
	out, err := a.verifyKept(t.Context(), "", false, time.Now())
	if err != nil || !strings.Contains(out, "proved: "+kept[0].Name) {
		t.Fatalf("a good snapshot: %v\n%s", err, out)
	}
	before, _ := filesUnder(filepath.Join(dir, kept[0].Name))
	if left, _ := filepath.Glob(filepath.Join(dir, snapshotWorkPrefix+"*")); len(left) != 0 {
		t.Errorf("the proof left its working set: %v", left)
	}

	// .
	// .
	stray := filepath.Join(dir, kept[0].Name, "stray.txt")
	if err := os.WriteFile(stray, []byte("not part of what was published"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := a.verifyKept(t.Context(), kept[0].Name, false, time.Now()); err == nil {
		t.Fatal("a snapshot holding a file its checksum list does not name proved")
	}
	os.Remove(stray)
	if _, err := a.verifyKept(t.Context(), kept[0].Name, false, time.Now()); err != nil {
		t.Fatalf("rig: without the stray file it proves again: %v", err)
	}

	// .
	db := filepath.Join(dir, kept[0].Name, snapshotDB)
	raw, err := os.ReadFile(db)
	if err != nil {
		t.Fatal(err)
	}
	raw[len(raw)/2] ^= 0xff
	if err := os.WriteFile(db, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	_, err = a.verifyKept(t.Context(), kept[0].Name, false, time.Now())
	if err == nil || !strings.Contains(err.Error(), "does NOT prove") {
		t.Fatalf("A ROTTED SNAPSHOT PROVED: %v", err)
	}
	if strings.Contains(err.Error(), home) || strings.Contains(err.Error(), snapshotDB) {
		t.Errorf("what failed names files, and the identity was shown it: %v", err)
	}
	after, _ := filesUnder(filepath.Join(dir, kept[0].Name))
	if len(after) != len(before) {
		t.Errorf("the proof wrote into the published snapshot: %v, then %v", before, after)
	}
	// .
	// .
	// .
	// .
	// .
	// .
	raw = raw[:len(raw)/2]
	if err := os.WriteFile(db, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	sumsPath := filepath.Join(dir, kept[0].Name, snapshotSums)
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
	if err := escrow.CheckSums(filepath.Join(dir, kept[0].Name), before); err != nil {
		t.Fatalf("rig: the re-summed snapshot must pass its own checksum list: %v", err)
	}
	if _, err := a.verifyKept(t.Context(), kept[0].Name, false, time.Now()); err == nil {
		t.Fatal("A SNAPSHOT EDITED AND RE-SUMMED PROVED: the checksum list is the editor's to rewrite, the restore is not")
	}
	// .
	if _, err := a.verifyKept(t.Context(), "", true, time.Now()); err == nil {
		t.Fatal("rig: the rotted snapshot proved")
	}
	_, err = a.verifyKept(t.Context(), "", true, time.Now().Add(time.Minute))
	refusedFor(t, err, "spaced")
}

// .
// .
func TestProvingAnEncryptedSnapshotLeavesNothingInTheClear(t *testing.T) {
	a, _, keyPath := maintApp(t)
	dir := a.backupsDir(a.configSnapshot())
	live(t, a, 2)
	appendFixtureEvent(t, a, keyPath, "exp_a")
	_, recipient := hostSnapshotKey(t, a)
	checkedEscrow(t, a, a.door.kp, recipient)
	made, err := a.takeOnDemand(t.Context(), true, time.Now())
	if err != nil || !made.Encrypted || !strings.HasSuffix(made.Name, onDemandMark+escrow.SnapshotSuffix) {
		t.Fatalf("an asked-for snapshot under a checked escrow: %+v %v", made, err)
	}
	out, err := a.verifyKept(t.Context(), made.Name, true, time.Now())
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
	// .
	// .
	stranded := filepath.Join(dir, snapshotWorkPrefix+"verify-killed.tmp")
	if err := os.MkdirAll(filepath.Join(stranded, "set"), 0o700); err != nil {
		t.Fatal(err)
	}
	a.sweepSnapshotDebrisAtBoot(a.configSnapshot())
	if _, err := os.Stat(stranded); !os.IsNotExist(err) {
		t.Errorf("a killed proof's working set survives the boot's sweep: %v", err)
	}
}

// .
// .
func TestTheTurnHearsOfContinuityOnlyWhenItIsNotWell(t *testing.T) {
	a, _, keyPath := maintApp(t)
	appendFixtureEvent(t, a, keyPath, "exp_a")
	_, recipient := hostSnapshotKey(t, a)
	checkedEscrow(t, a, a.door.kp, recipient)
	a.runMaintenance(t.Context())
	state, err := a.buildTurnFacts(false)
	if err != nil {
		t.Fatal(err)
	}
	for _, word := range []string{"snapshot", "backup.", "continuity"} {
		if strings.Contains(state, word) {
			t.Fatalf("A HEALTHY PASS REACHED THE TURN (%q):\n%s", word, state)
		}
	}
	if err := a.store.SetContinuityStatus(store.ContinuityStatus{Outcome: store.ContinuityFailed, Detail: "copy: no space left on device"}); err != nil {
		t.Fatal(err)
	}
	state, err = a.buildTurnFacts(false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(state, "### Attention") || !strings.Contains(state, "work action=backup.take") {
		t.Errorf("a failing pass did not reach the turn's attention slot:\n%s", state)
	}
	if strings.Contains(state, "no space left") {
		t.Errorf("what failed is the operator's, and the turn was shown it:\n%s", state)
	}
}
