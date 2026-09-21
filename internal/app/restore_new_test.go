package app

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/escrow"
)

// .
// .
// .
// .

const newMachinePass = "correct horse battery staple"

// .
// .
type leftTheOldMachine struct {
	escrowFile    []byte
	snapshotName  string
	snapshotBytes []byte
	through       uint64
	identity      string
}

func oldMachine(t *testing.T) leftTheOldMachine {
	t.Helper()
	r := newRestoreRig(t)
	a := r.boot(t)
	defer a.Stop()
	for i := 0; i < 3; i++ {
		appendFixtureEvent(t, a, r.keyPath, "exp_old_"+string(rune('a'+i)))
	}
	file, _, err := a.escrowCreateHere([]byte(newMachinePass))
	if err != nil {
		t.Fatalf("the old machine's escrow: %v", err)
	}
	if _, err := a.escrowCheckHere(file, []byte(newMachinePass)); err != nil {
		t.Fatalf("the old machine's escrow check: %v", err)
	}
	made, err := a.takeOnDemand(t.Context(), false, time.Now())
	if err != nil || !made.Encrypted {
		t.Fatalf("the old machine's snapshot must be encrypted: %+v %v", made, err)
	}
	raw, err := os.ReadFile(filepath.Join(a.backupsDir(a.configSnapshot()), made.Name))
	if err != nil {
		t.Fatal(err)
	}
	return leftTheOldMachine{escrowFile: file, snapshotName: made.Name, snapshotBytes: raw, through: made.Record, identity: a.keyPair.Fingerprint()}
}

// .
func blankMachine(t *testing.T) (*App, string) {
	t.Helper()
	dir := t.TempDir()
	cfg := safebootConfig(t, dir, "New", filepath.Join(dir, "identity.sec"), filepath.Join(dir, "ledger.jsonl"), filepath.Join(dir, "aii.db"))
	a := New(cfg)
	if choice, err := a.chooseBoot(); err != nil || choice != bootFirstboot {
		t.Fatalf("rig: a blank machine must be a first boot: %v %v", choice, err)
	}
	return a, dir
}

// .
// .
// .
func TestAnIdentityIsRestoredOnAMachineThatNeverHeldIt(t *testing.T) {
	left := oldMachine(t)
	a, dir := blankMachine(t)
	defer a.Stop()

	if err := a.newMachineUpload(left.snapshotName, "", bytes.NewReader(left.snapshotBytes)); err == nil {
		t.Fatal("a snapshot was accepted before any key was in place")
	}
	if _, err := a.newMachineKeys(left.escrowFile, []byte("not the passphrase at all")); err == nil {
		t.Fatal("an escrow opened under the wrong passphrase")
	}
	if _, err := os.Stat(filepath.Join(dir, "identity.sec")); !os.IsNotExist(err) {
		t.Fatal("a failed first act wrote a key")
	}
	keys, err := a.newMachineKeys(left.escrowFile, []byte(newMachinePass))
	if err != nil || keys.Identity != left.identity || !keys.HoldsSnapshotKey {
		t.Fatalf("the keys: %+v %v", keys, err)
	}
	// .
	// .
	if choice, err := New(a.cfg).chooseBoot(); err != nil || choice != bootFirstboot {
		t.Fatalf("AN INTERRUPTED RESTORE WOULD BE REFUSED AT THE NEXT START: %v %v", choice, err)
	}
	// .
	if _, err := a.newMachineKeys(left.escrowFile, []byte(newMachinePass)); err != nil {
		t.Errorf("the first act made twice: %v", err)
	}

	for _, bad := range []string{"../" + left.snapshotName, "ledger.tar.age", "ledger-20260920T101500Z-seq7.tar.age.exe", ""} {
		if err := a.newMachineUpload(bad, "", bytes.NewReader(left.snapshotBytes)); err == nil {
			t.Errorf("a snapshot was accepted under the name %q", bad)
		}
	}
	if err := a.newMachineUpload(left.snapshotName, "", bytes.NewReader(left.snapshotBytes)); err != nil {
		t.Fatalf("the snapshot's upload: %v", err)
	}
	done, err := a.newMachineRestore(t.Context(), left.snapshotName, nil)
	if err != nil {
		t.Fatalf("THE RESTORE: %v", err)
	}
	if done.Identity != left.identity || done.RestoredTo != left.through || a.ledger == nil || a.ledger.LastSeq() != left.through {
		t.Fatalf("restored %+v, want identity %s through %d", done, left.identity, left.through)
	}
	if _, safe := a.SafeMode(); safe || !a.live {
		t.Fatal("the restored identity is not running")
	}
	if restoringOnNewMachine(a.configSnapshot()) {
		t.Error("the marker outlived the restore it guarded")
	}
	if sets := setAsideSets(a.configSnapshot()); len(sets) != 0 {
		t.Errorf("nothing was here, and the page would say something was set aside: %+v", sets)
	}
	if left, _ := filepath.Glob(filepath.Join(dir, restoreStagePrefix+"*")); len(left) != 0 {
		t.Errorf("staging was left behind: %v", left)
	}
	// .
	// .
	// .
	appendFixtureEvent(t, a, filepath.Join(dir, "identity.sec"), "exp_on_the_new_machine")
	if a.ledger.LastSeq() != left.through+1 {
		t.Fatalf("an append after the restore landed at %d", a.ledger.LastSeq())
	}
}

// .
// .
// .
// .
func TestASnapshotOfAnotherIdentityIsNotRestoredUnderTheseKeys(t *testing.T) {
	mine, other := oldMachine(t), oldMachine(t)
	a, dir := blankMachine(t)
	defer a.Stop()
	if _, err := a.newMachineKeys(mine.escrowFile, []byte(newMachinePass)); err != nil {
		t.Fatal(err)
	}
	if err := a.newMachineUpload(other.snapshotName, "", bytes.NewReader(other.snapshotBytes)); err != nil {
		t.Fatal(err)
	}
	_, err := a.newMachineRestore(t.Context(), other.snapshotName, nil)
	if err == nil || !strings.Contains(err.Error(), "NOT restored") {
		t.Fatalf("ANOTHER IDENTITY'S SNAPSHOT WAS RESTORED: %v", err)
	}
	if fileExists(filepath.Join(dir, "ledger.jsonl")) || a.live {
		t.Fatal("a refused restore put something in place")
	}
	if !restoringOnNewMachine(a.configSnapshot()) {
		t.Fatal("a refused restore closed the door: the next start would refuse first boot")
	}
	if err := a.newMachineUpload(mine.snapshotName, "", bytes.NewReader(mine.snapshotBytes)); err != nil {
		t.Fatal(err)
	}
	if done, err := a.newMachineRestore(t.Context(), mine.snapshotName, nil); err != nil || done.RestoredTo != mine.through {
		t.Fatalf("the right snapshot after a wrong one: %+v %v", done, err)
	}
}

// .
// .
func TestTheNewMachineDoorRefusesWhatIsNotItsToDo(t *testing.T) {
	left := oldMachine(t)
	// .
	a, dir := blankMachine(t)
	defer a.Stop()
	other := oldMachine(t)
	c, _, err := escrow.Open(other.escrowFile, []byte(newMachinePass))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "identity.sec"), c.IdentityKey, 0o600); err != nil {
		t.Fatal(err)
	}
	c.Wipe()
	if _, err := a.newMachineKeys(left.escrowFile, []byte(newMachinePass)); err == nil || !strings.Contains(err.Error(), "never written over") {
		t.Fatalf("A KEY WAS WRITTEN OVER, or the refusal does not say why: %v", err)
	}

	b, _ := blankMachine(t)
	defer b.Stop()
	if _, err := b.newMachineKeys(left.escrowFile, []byte(newMachinePass)); err != nil {
		t.Fatal(err)
	}
	plain := "ledger-20260920T101500Z-seq7"
	for _, member := range []string{"../identity.sec", "witness-keys/../../x.json", "notes.txt", "aii.db-wal"} {
		if err := b.newMachineUpload(plain, member, strings.NewReader("x")); err == nil {
			t.Errorf("a snapshot's folder accepted %q", member)
		}
	}
	if err := b.newMachineUpload(plain, "ledger.jsonl", strings.NewReader("x")); err != nil {
		t.Errorf("a snapshot's own member was refused: %v", err)
	}
	if _, err := b.newMachineRestore(t.Context(), plain, nil); err == nil || !strings.Contains(err.Error(), "checksum list is missing") {
		t.Errorf("a folder that did not arrive whole: %v", err)
	}
}

// .
// .
// .
// .
// .
// .
func TestTheRightSnapshotArrivesAfterAWrongOneOfTheSameName(t *testing.T) {
	mine, other := oldMachine(t), oldMachine(t)
	a, dir := blankMachine(t)
	defer a.Stop()
	if _, err := a.newMachineKeys(mine.escrowFile, []byte(newMachinePass)); err != nil {
		t.Fatal(err)
	}
	name := mine.snapshotName
	if err := a.newMachineUpload(name, "", bytes.NewReader(other.snapshotBytes)); err != nil {
		t.Fatal(err)
	}
	if _, err := a.newMachineRestore(t.Context(), name, nil); err == nil {
		t.Fatal("another identity's snapshot was restored")
	}
	if err := a.newMachineUpload(name, "", bytes.NewReader(mine.snapshotBytes)); err != nil {
		t.Fatal(err)
	}
	backups := a.backupsDir(a.configSnapshot())
	if got, err := os.ReadFile(filepath.Join(backups, name)); err != nil || !bytes.Equal(got, mine.snapshotBytes) {
		t.Fatalf("THE WRONG BYTES KEPT THE NAME: what is published is not what just arrived (%v)", err)
	}
	// .
	replaced, _ := filepath.Glob(filepath.Join(backups, name+".replaced-*"))
	if len(replaced) != 1 {
		t.Fatalf("the snapshot that was in the way must be kept aside, never deleted: %v", replaced)
	}
	if got, _ := os.ReadFile(replaced[0]); !bytes.Equal(got, other.snapshotBytes) {
		t.Error("what was kept aside is not what was in the way")
	}
	if kept, _ := keptSnapshots(backups); len(kept) != 1 {
		t.Errorf("what was kept aside must not read as a snapshot: %+v", kept)
	}
	done, err := a.newMachineRestore(t.Context(), name, nil)
	if err != nil || done.RestoredTo != mine.through || !fileExists(filepath.Join(dir, "ledger.jsonl")) {
		t.Fatalf("the right snapshot under the same name: %+v %v", done, err)
	}
	// .
	if err := a.newMachineUpload(name, "", bytes.NewReader(mine.snapshotBytes)); err == nil {
		if again, _ := filepath.Glob(filepath.Join(backups, name+".replaced-*")); len(again) != 1 {
			t.Errorf("the same snapshot arriving again set something aside: %v", again)
		}
	}
}

// .
// .
// .
func TestKeysAreNotReportedInPlaceOverAFilesystemFailure(t *testing.T) {
	// .
	// .
	// .
	for _, tc := range []struct {
		what   string
		breaks func(t *testing.T, a *App)
		says   string
	}{
		{"a directory where the identity's key goes", func(t *testing.T, a *App) {
			if err := os.MkdirAll(a.configSnapshot().Identity.KeyPath, 0o700); err != nil {
				t.Fatal(err)
			}
		}, "does not read as a key"},
		{"a directory where the snapshot key goes", func(t *testing.T, a *App) {
			if err := os.MkdirAll(a.snapshotKeyPath(a.configSnapshot()), 0o700); err != nil {
				t.Fatal(err)
			}
		}, "snapshot key goes"},
		{"the key's directory is a file", func(t *testing.T, a *App) {
			a.cfgMu.Lock()
			a.cfg.Identity.KeyPath = filepath.Join(a.cfg.Identity.KeyPath+".d", "identity.sec")
			keyDir := filepath.Dir(a.cfg.Identity.KeyPath)
			a.cfgMu.Unlock()
			if err := os.WriteFile(keyDir, []byte("not a directory"), 0o600); err != nil {
				t.Fatal(err)
			}
		}, "cannot be looked at"},
	} {
		t.Run(tc.what, func(t *testing.T) {
			mine := oldMachine(t)
			a, _ := blankMachine(t)
			defer a.Stop()
			tc.breaks(t, a)
			got, err := a.newMachineKeys(mine.escrowFile, []byte(newMachinePass))
			if err == nil {
				t.Fatalf("THE KEYS WERE REPORTED IN PLACE (%+v) over %s", got, tc.what)
			}
			if !strings.Contains(err.Error(), tc.says) {
				t.Errorf("the refusal does not say what was met (%q): %v", tc.says, err)
			}
			if got.Identity != "" || got.HoldsSnapshotKey {
				t.Errorf("a refusal reported keys: %+v", got)
			}
		})
	}
}

// .
// .
// .
// .
func TestAKeyThatDoesNotReadBackIsNotInPlace(t *testing.T) {
	mine := oldMachine(t)
	a, _ := blankMachine(t)
	defer a.Stop()
	prev := afterKeyPublished
	afterKeyPublished = func(path string) {
		if err := os.WriteFile(path, []byte("what the disk gave back"), 0o600); err != nil {
			t.Errorf("rig: %v", err)
		}
	}
	got, err := a.newMachineKeys(mine.escrowFile, []byte(newMachinePass))
	afterKeyPublished = prev
	if err == nil {
		t.Fatalf("THE KEYS WERE REPORTED IN PLACE over a key that does not read back: %+v", got)
	}
	if !strings.Contains(err.Error(), "does not read back") {
		t.Errorf("the refusal does not say what happened: %v", err)
	}
	if got.Identity != "" || got.HoldsSnapshotKey {
		t.Errorf("a refusal reported keys: %+v", got)
	}
	if !restoringOnNewMachine(a.configSnapshot()) {
		t.Error("the door closed on a refusal: the person cannot try again")
	}
}
