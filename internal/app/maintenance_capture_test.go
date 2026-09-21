package app

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
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
// .
func live(t *testing.T, a *App, turns int) {
	t.Helper()
	for i := 0; i < turns; i++ {
		if err := a.store.AddConversationTurn("operator", "a turn the record never holds"); err != nil {
			t.Fatal(err)
		}
		if err := a.store.IncrementLifetimeTicks(); err != nil {
			t.Fatal(err)
		}
	}
}

func readReceipt(t *testing.T, snap string) snapshotReceipt {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(snap, snapshotReceiptName))
	if err != nil {
		t.Fatal(err)
	}
	var r snapshotReceipt
	if err := json.Unmarshal(raw, &r); err != nil {
		t.Fatal(err)
	}
	return r
}

func operatorWasTold(t *testing.T, a *App, want string) bool {
	t.Helper()
	msgs, err := a.store.UndeliveredFor("operator")
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range msgs {
		if strings.Contains(m.Content, "[maintenance]") && strings.Contains(m.Content, want) {
			return true
		}
	}
	return false
}

func noDebris(t *testing.T, dir string) {
	t.Helper()
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".snapshot-") {
			t.Fatalf("a pass that did not publish left debris: %s", e.Name())
		}
	}
}

// .
// .
// .
// .
func TestARuntimeChangeAtAnUnchangedRecordIsCaptured(t *testing.T) {
	a, _, _ := maintApp(t)
	cfg := a.configSnapshot()
	dir := a.backupsDir(cfg)

	live(t, a, 2)
	a.runMaintenance(t.Context())
	live(t, a, 3)
	nextSecond()
	a.runMaintenance(t.Context())

	snaps := backupFiles(t, dir)
	if len(snaps) != 2 {
		t.Fatalf("%d snapshots, want 2 at one record (%v)", len(snaps), snaps)
	}
	if snapshotSeq(t, snaps[0]) != snapshotSeq(t, snaps[1]) {
		t.Fatalf("fixture: the record grew between the passes (%v)", snaps)
	}
	first, second := readReceipt(t, filepath.Join(dir, snaps[0])), readReceipt(t, filepath.Join(dir, snaps[1]))
	if first.Runtime.LifetimeTicks != 2 || first.Runtime.LastTurnSeq != 2 || first.Runtime.Ephemeral["conversations"] != 2 {
		t.Fatalf("the first receipt is not the first morning: %+v", first.Runtime)
	}
	if second.Runtime.LifetimeTicks != 5 || second.Runtime.LastTurnSeq != 5 || second.Runtime.Ephemeral["conversations"] != 5 {
		t.Fatalf("THE DAY WAS NOT CAPTURED: the second receipt reads %+v", second.Runtime)
	}
	// .
	_, held, err := store.InspectCopy(t.Context(), filepath.Join(dir, snaps[1], snapshotDB))
	if err != nil {
		t.Fatal(err)
	}
	if d := second.Runtime.Differs(held); d != "" {
		t.Fatalf("the receipt and the database copy disagree: %s", d)
	}

	// .
	// .
	if removed := pruneBackups(dir, 1); removed != 1 {
		t.Fatalf("pruned %d, want 1", removed)
	}
	if left := backupFiles(t, dir); len(left) != 1 || left[0] != snaps[1] {
		t.Fatalf("THE NEWER SNAPSHOT WAS PRUNED: %v survived, want %s", left, snaps[1])
	}
}

// .
// .
// .
// .
// .
// .
func TestWhatIsAdmittedDuringTheCopyIsNotInTheSnapshot(t *testing.T) {
	a, _, keyPath := maintApp(t)
	cfg := a.configSnapshot()
	dir := a.backupsDir(cfg)
	live(t, a, 2)
	captured := a.ledger.LastSeq()

	prev := snapshotStep
	t.Cleanup(func() { snapshotStep = prev })
	snapshotStep = func(step string) {
		if step != "captured" {
			return
		}
		appendFixtureEvent(t, a, keyPath, "exp_during_the_copy")
		live(t, a, 4)
	}
	a.runMaintenance(t.Context())
	snapshotStep = prev

	if a.ledger.LastSeq() != captured+1 {
		t.Fatalf("fixture: the append during the copy did not land (ledger at %d)", a.ledger.LastSeq())
	}
	snaps := backupFiles(t, dir)
	if len(snaps) != 1 {
		t.Fatalf("an identity that went on living cost the day's snapshot: %v", snaps)
	}
	snap := filepath.Join(dir, snaps[0])
	if got := snapshotSeq(t, snaps[0]); got != captured {
		t.Fatalf("the snapshot is named for record %d, captured at %d", got, captured)
	}
	r := readReceipt(t, snap)
	if r.LastSeq != captured || r.Runtime.LifetimeTicks != 2 || r.Runtime.Ephemeral["conversations"] != 2 {
		t.Fatalf("THE RECEIPT READ THE LIVE STORE, NOT THE CAPTURE: %+v", r)
	}
	head, held, err := store.InspectCopy(t.Context(), filepath.Join(snap, snapshotDB))
	if err != nil {
		t.Fatal(err)
	}
	if head.Seq != captured || held.Ephemeral["conversations"] != 2 || held.LifetimeTicks != 2 {
		t.Fatalf("THE DATABASE COPY HOLDS WHAT WAS WRITTEN AFTER THE CAPTURE: head %d, %+v", head.Seq, held)
	}
	var n uint64
	if err := ledger.Stream(filepath.Join(snap, "ledger.jsonl"), func(evt *ledger.Event) error {
		n = evt.Seq
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if n != captured {
		t.Fatalf("THE RECORD COPY RUNS TO %d, captured at %d", n, captured)
	}
}

// .
// .
// .
// .
func TestACancelledPassLeavesNoSnapshotAndNoPin(t *testing.T) {
	a, _, _ := maintApp(t)
	cfg := a.configSnapshot()
	dir := a.backupsDir(cfg)
	live(t, a, 2)

	ctx, cancel := context.WithCancel(t.Context())
	prev := snapshotStep
	t.Cleanup(func() { snapshotStep = prev })
	snapshotStep = func(step string) {
		if step == "captured" {
			cancel()
		}
	}
	a.runMaintenance(ctx)
	snapshotStep = prev

	if snaps := backupFiles(t, dir); len(snaps) != 0 {
		t.Fatalf("a cancelled pass published: %v", snaps)
	}
	noDebris(t, dir)
	// .
	// .
	live(t, a, 1)
	var busy, logged, checkpointed int
	if err := a.store.DB().QueryRow("PRAGMA wal_checkpoint(TRUNCATE)").Scan(&busy, &logged, &checkpointed); err != nil {
		t.Fatal(err)
	}
	if busy != 0 {
		t.Fatal("THE CANCELLED PASS LEFT ITS PIN OPEN: the write-ahead log cannot be reset")
	}
	// .
	a.runMaintenance(t.Context())
	if snaps := backupFiles(t, dir); len(snaps) != 1 {
		t.Fatalf("the pass after a cancelled one: %v", snaps)
	}
}

// .
// .
// .
// .
func TestAMirrorBehindTheRecordIsRefused(t *testing.T) {
	a, _, keyPath := maintApp(t)
	cfg := a.configSnapshot()
	dir := a.backupsDir(cfg)
	kp, err := crypto.LoadKeyPair(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	// .
	if _, err := a.ledger.Append(ledger.EventExperienceCreate, kp.Fingerprint(), 3,
		map[string]interface{}{"id": "exp_past_the_door", "content": "x", "category": "observation", "provenance": "self"}, kp); err != nil {
		t.Fatal(err)
	}
	a.runMaintenance(t.Context())
	if snaps := backupFiles(t, dir); len(snaps) != 0 {
		t.Fatalf("A RECORD AND A DATABASE THAT DISAGREE WERE PUBLISHED AS ONE SNAPSHOT: %v", snaps)
	}
	noDebris(t, dir)
	if !operatorWasTold(t, a, "not at the captured boundary") {
		t.Fatal("the operator was not told why there is no snapshot today")
	}
}

func copyTree(t *testing.T, from, to string) {
	t.Helper()
	if err := filepath.Walk(from, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(from, path)
		if info.IsDir() {
			return os.MkdirAll(filepath.Join(to, rel), 0o700)
		}
		in, err := os.Open(path)
		if err != nil {
			return err
		}
		defer in.Close()
		out, err := os.Create(filepath.Join(to, rel))
		if err != nil {
			return err
		}
		defer out.Close()
		_, err = io.Copy(out, in)
		return err
	}); err != nil {
		t.Fatal(err)
	}
}

func replaceFile(t *testing.T, from, to string) {
	t.Helper()
	raw, err := os.ReadFile(from)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(to, raw, 0o600); err != nil {
		t.Fatal(err)
	}
}

// .
// .
// .
// .
// .
func TestASetThatIsNotOneInstantIsRefused(t *testing.T) {
	a, _, keyPath := maintApp(t)
	cfg := a.configSnapshot()
	dir := a.backupsDir(cfg)

	live(t, a, 2)
	a.runMaintenance(t.Context())
	live(t, a, 1)
	nextSecond()
	a.runMaintenance(t.Context())
	// .
	// .
	forkDir := t.TempDir()
	replaceFile(t, cfg.Identity.LedgerPath, filepath.Join(forkDir, "ledger.jsonl"))
	kp, err := crypto.LoadKeyPair(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	fork, err := ledger.New(filepath.Join(forkDir, "ledger.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fork.Append(ledger.EventExperienceCreate, kp.Fingerprint(), 3,
		map[string]interface{}{"id": "exp_the_other_history", "content": "y", "category": "observation", "provenance": "self"}, kp); err != nil {
		t.Fatal(err)
	}
	fork.Close()
	appendFixtureEvent(t, a, keyPath, "exp_growth")
	nextSecond()
	a.runMaintenance(t.Context())

	snaps := backupFiles(t, dir)
	if len(snaps) != 3 {
		t.Fatalf("fixture: %v", snaps)
	}
	A, B, C := filepath.Join(dir, snaps[0]), filepath.Join(dir, snaps[1]), filepath.Join(dir, snaps[2])

	// .
	otherDir := t.TempDir()
	_, otherLedger, _ := birthFixture(t, otherDir, "Other")

	for name, tc := range map[string]struct {
		base   string
		doctor func(t *testing.T, set string)
		want   string
	}{
		"another identity's record": {C, func(t *testing.T, set string) {
			replaceFile(t, otherLedger, filepath.Join(set, "ledger.jsonl"))
		}, "is identity"},
		"a record copied before the database": {C, func(t *testing.T, set string) {
			replaceFile(t, filepath.Join(A, "ledger.jsonl"), filepath.Join(set, "ledger.jsonl"))
		}, "the record copy holds"},
		"a database copied after the record": {A, func(t *testing.T, set string) {
			replaceFile(t, filepath.Join(C, snapshotDB), filepath.Join(set, snapshotDB))
		}, "the database copy ends at record"},
		"a database from another morning at the same record": {A, func(t *testing.T, set string) {
			replaceFile(t, filepath.Join(B, snapshotDB), filepath.Join(set, snapshotDB))
		}, "is not the captured state"},
		"the same identity and length, another history": {C, func(t *testing.T, set string) {
			replaceFile(t, filepath.Join(forkDir, "ledger.jsonl"), filepath.Join(set, "ledger.jsonl"))
		}, "copy does not restore"},
	} {
		t.Run(name, func(t *testing.T) {
			set := filepath.Join(t.TempDir(), "set")
			copyTree(t, tc.base, set)
			want := readReceipt(t, set)
			if err := a.proveSnapshot(t.Context(), cfg, set, want); err != nil {
				t.Fatalf("fixture: the undoctored set is refused: %v", err)
			}
			tc.doctor(t, set)
			err := a.proveSnapshot(t.Context(), cfg, set, want)
			if err == nil {
				t.Fatal("A SET THAT IS NOT ONE INSTANT WAS ACCEPTED AS A RECOVERY POINT")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("refused, but not by the proof that exists for it: %v (want %q)", err, tc.want)
			}
			if _, serr := os.Stat(filepath.Join(set, ".restore-proof")); !os.IsNotExist(serr) {
				t.Fatal("the restore proof left its scratch in the set")
			}
		})
	}
}

// .
// .
// .
// .
// .
// .
// .
func TestAnEventOfferedDuringTheCaptureWaitsAtTheDoor(t *testing.T) {
	a, _, _ := maintApp(t)
	landed := make(chan error, 1)
	var landedInsideTheCapture bool

	prev := snapshotStep
	t.Cleanup(func() { snapshotStep = prev })
	snapshotStep = func(step string) {
		if step != "tail-copied" {
			return
		}
		go func() {
			_, err := a.door.Append(ledger.EventExperienceCreate, 3, map[string]interface{}{
				"id": "exp_offered_during_the_capture", "content": "x", "category": "observation", "provenance": "self",
			}, "")
			landed <- err
		}()
		// .
		// .
		select {
		case err := <-landed:
			landedInsideTheCapture = true
			landed <- err
		case <-time.After(300 * time.Millisecond):
		}
	}
	before := a.ledger.LastSeq()
	at, pin, err := a.door.capture(t.Context(), filepath.Join(t.TempDir(), "ledger.jsonl"))
	snapshotStep = prev
	if err != nil {
		t.Fatalf("THE CAPTURE WAS REFUSED because an event was admitted inside it: %v", err)
	}
	defer pin.Release()
	if landedInsideTheCapture {
		t.Fatal("an event was admitted between the record's capture and the database's pin")
	}
	if at.LastSeq != before || pin.Head().Hash != at.LastHash {
		t.Fatalf("captured at %d with the mirror at %d, want both at %d", at.LastSeq, pin.Head().Seq, before)
	}
	if err := <-landed; err != nil {
		t.Fatalf("the event that waited at the door was then refused: %v", err)
	}
	if a.ledger.LastSeq() != before+1 {
		t.Fatalf("the event that waited never landed: ledger at %d", a.ledger.LastSeq())
	}
}

// .
// .
// .
// .
// .
func TestAMirrorOfAnotherHistoryIsRefused(t *testing.T) {
	a, _, keyPath := maintApp(t)
	cfg := a.configSnapshot()
	dir := a.backupsDir(cfg)
	kp, err := crypto.LoadKeyPair(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	forkDir := t.TempDir()
	replaceFile(t, cfg.Identity.LedgerPath, filepath.Join(forkDir, "ledger.jsonl"))
	fork, err := ledger.New(filepath.Join(forkDir, "ledger.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	other, err := fork.Append(ledger.EventExperienceCreate, kp.Fingerprint(), 3,
		map[string]interface{}{"id": "exp_the_other_history", "content": "y", "category": "observation", "provenance": "self"}, kp)
	if err != nil {
		t.Fatal(err)
	}
	fork.Close()
	// .
	if _, err := a.ledger.Append(ledger.EventExperienceCreate, kp.Fingerprint(), 3,
		map[string]interface{}{"id": "exp_this_history", "content": "x", "category": "observation", "provenance": "self"}, kp); err != nil {
		t.Fatal(err)
	}
	if err := a.store.Materialize(other); err != nil {
		t.Fatal(err)
	}
	if got, _ := a.store.MaxLedgerSeq(); got != a.ledger.LastSeq() {
		t.Fatalf("fixture: mirror at %d, record at %d — the lengths must agree", got, a.ledger.LastSeq())
	}
	a.runMaintenance(t.Context())
	if snaps := backupFiles(t, dir); len(snaps) != 0 {
		t.Fatalf("A DATABASE OF ANOTHER HISTORY WAS PUBLISHED WITH THIS RECORD: %v", snaps)
	}
	if !operatorWasTold(t, a, "not at the captured boundary") {
		t.Fatal("the operator was not told why there is no snapshot today")
	}
}
