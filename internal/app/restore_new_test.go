package app

import (
	"bufio"
	"bytes"
	"crypto/tls"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/continuity"
	"github.com/aiii-dot-id/aii-os/internal/continuity/continuitytest"
	"github.com/aiii-dot-id/aii-os/internal/crypto"
)

// restore_new_test.go — restore on a NEW machine (U7 phase C): an old
// machine makes an escrow file and an encrypted snapshot the way a person
// does from the page, those two files are all that leaves it, and a blank
// machine's first boot puts the identity back and hands over to a live boot.

const newMachinePass = "correct horse battery staple"

// leftTheOldMachine is what a person carries away: the escrow file and the
// newest snapshot, as bytes and a name.
type leftTheOldMachine struct {
	escrowFile    []byte
	snapshotName  string
	snapshotBytes []byte // an encrypted snapshot: one file
	through       uint64
	identity      string
}

func oldMachine(t *testing.T) leftTheOldMachine {
	t.Helper()
	return oldMachineLiving(t, nil, nil)
}

// oldMachineLiving is oldMachine with what the old machine did just before
// its snapshot and after it.
func oldMachineLiving(t *testing.T, before, after func(*App)) leftTheOldMachine {
	t.Helper()
	r := newRestoreRig(t)
	a := r.boot(t)
	defer stopApp(t, a)
	for i := 0; i < 3; i++ {
		appendFixtureEvent(t, a, r.keyPath, "exp_old_"+string(rune('a'+i)))
	}
	if before != nil {
		before(a)
	}
	file, _, err := continuityDoor(a).EscrowCreate([]byte(newMachinePass))
	if err != nil {
		t.Fatalf("the old machine's escrow: %v", err)
	}
	if _, err := continuityDoor(a).EscrowCheck(file, []byte(newMachinePass)); err != nil {
		t.Fatalf("the old machine's escrow check: %v", err)
	}
	made, err := askedSnapshot(t, a)
	if err != nil || !made.Encrypted {
		t.Fatalf("the old machine's snapshot must be encrypted: %+v %v", made, err)
	}
	raw, err := os.ReadFile(filepath.Join(a.backupsDir(a.configSnapshot()), made.Name))
	if err != nil {
		t.Fatal(err)
	}
	if after != nil {
		after(a)
	}
	return leftTheOldMachine{escrowFile: file, snapshotName: made.Name, snapshotBytes: raw, through: made.Record, identity: a.keyPair.Fingerprint()}
}

// blankMachine is a first boot with nothing in its directory.
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

func TestASnapshotMadeBeforeSigningIsRestoredAtFirstBootsDoor(t *testing.T) {
	var key []byte
	left := oldMachineLiving(t, func(old *App) {
		key, _ = continuitytest.SnapshotKey(t, old.configSnapshot().Identity.KeyPath)
	}, nil)
	carried := filepath.Join(t.TempDir(), left.snapshotName)
	if err := os.WriteFile(carried, left.snapshotBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := continuitytest.Unsign(carried, key); err != nil {
		t.Fatal(err)
	}
	unsigned, err := os.ReadFile(carried)
	if err != nil {
		t.Fatal(err)
	}
	a, _ := blankMachine(t)
	defer stopApp(t, a)
	if _, err := a.continuity.NewMachineKeys(left.escrowFile, []byte(newMachinePass)); err != nil {
		t.Fatal(err)
	}
	if err := a.continuity.NewMachineUpload(left.snapshotName, "", bytes.NewReader(unsigned)); err != nil {
		t.Fatal(err)
	}
	done, err := a.continuity.NewMachineRestore(t.Context(), left.snapshotName, nil)
	if err != nil || done.RestoredTo != left.through || !a.live.Load() {
		t.Fatalf("A SNAPSHOT MADE BEFORE SIGNING WAS REFUSED AT FIRST BOOT'S DOOR: %+v %v", done, err)
	}
	if v := continuityDoor(a).View(); !strings.Contains(v.Restored, "carried no signature of the identity's key") {
		t.Errorf("THE PAGE DOES NOT SAY THE SNAPSHOT WAS UNSIGNED: %q", v.Restored)
	}
}

// THE JOURNEY: two files and a passphrase, and the identity is running on a
// machine that had never seen it — through the record the snapshot held, with
// nothing set aside, because nothing was here.
func TestAnIdentityIsRestoredOnAMachineThatNeverHeldIt(t *testing.T) {
	left := oldMachine(t)
	a, dir := blankMachine(t)
	defer stopApp(t, a)
	first := startFirstbootDashboardForTest(t, a)

	if err := a.continuity.NewMachineUpload(left.snapshotName, "", bytes.NewReader(left.snapshotBytes)); err == nil {
		t.Fatal("a snapshot was accepted before any key was in place")
	}
	if _, err := a.continuity.NewMachineKeys(left.escrowFile, []byte("not the passphrase at all")); err == nil {
		t.Fatal("an escrow opened under the wrong passphrase")
	}
	if _, err := os.Stat(filepath.Join(dir, "identity.sec")); !os.IsNotExist(err) {
		t.Fatal("a failed first act wrote a key")
	}
	keys, err := a.continuity.NewMachineKeys(left.escrowFile, []byte(newMachinePass))
	if err != nil || keys.Identity != left.identity || !keys.HoldsSnapshotKey {
		t.Fatalf("the keys: %+v %v", keys, err)
	}
	// THE ESCROW RECEIPT IS SIGNED AT THIS DOOR. A pass encrypts only where
	// a receipt says an escrow of the host's snapshot key was opened, and
	// this door signed none: an identity that had been encrypting
	// published every snapshot in the clear from its first pass on the new
	// machine. The person has just shown the file and its passphrase,
	// which is all a check proves.
	restoredKey, err := crypto.LoadKeyPair(filepath.Join(dir, "identity.sec"))
	if err != nil {
		t.Fatal(err)
	}
	_, hostRecipient := continuitytest.SnapshotKey(t, filepath.Join(dir, "identity.sec"))
	if err := continuitytest.CheckedReceipt(filepath.Join(dir, "identity.sec"), restoredKey.PublicKeyBytes(), hostRecipient); err != nil {
		t.Fatalf("THE NEW-MACHINE DOOR SIGNED NO ESCROW RECEIPT that this identity's key holds for the snapshot key now on this host: snapshots made here would not be encrypted (%v)", err)
	}
	// From here the directory holds identity evidence; the marker is what
	// keeps the next start at this door.
	if choice, err := New(a.cfg).chooseBoot(); err != nil || choice != bootFirstboot {
		t.Fatalf("AN INTERRUPTED RESTORE WOULD BE REFUSED AT THE NEXT START: %v %v", choice, err)
	}
	// The same act again is the same act: a key is never written over.
	if _, err := a.continuity.NewMachineKeys(left.escrowFile, []byte(newMachinePass)); err != nil {
		t.Errorf("the first act made twice: %v", err)
	}

	for _, bad := range []string{"../" + left.snapshotName, "ledger.tar.age", "ledger-20260920T101500Z-seq7.tar.age.exe", ""} {
		if err := a.continuity.NewMachineUpload(bad, "", bytes.NewReader(left.snapshotBytes)); err == nil {
			t.Errorf("a snapshot was accepted under the name %q", bad)
		}
	}
	if err := a.continuity.NewMachineUpload(left.snapshotName, "", bytes.NewReader(left.snapshotBytes)); err != nil {
		t.Fatalf("the snapshot's upload: %v", err)
	}
	done, err := a.continuity.NewMachineRestore(t.Context(), left.snapshotName, nil)
	if err != nil {
		t.Fatalf("THE RESTORE: %v", err)
	}
	if done.Identity != left.identity || done.RestoredTo != left.through || a.ledger == nil || a.ledger.LastSeq() != left.through {
		t.Fatalf("restored %+v, want identity %s through %d", done, left.identity, left.through)
	}
	if _, safe := a.SafeMode(); safe || !a.live.Load() {
		t.Fatal("the restored identity is not running")
	}
	if continuity.RestoringOnNewMachine(continuitySettingsOf(a.configSnapshot())) {
		t.Error("the marker outlived the restore it guarded")
	}
	// What the door received is the identity's kept snapshot now, and the
	// door's own directory is gone.
	if fileExists(filepath.Join(dir, "restore-new-machine")) || !fileExists(filepath.Join(a.backupsDir(a.configSnapshot()), left.snapshotName)) {
		t.Error("the snapshot the door received is not among the identity's kept snapshots, or the door's directory outlived the restore")
	}
	if sets := continuityDoor(a).View().SetAside; len(sets) != 0 {
		t.Errorf("nothing was here, and the page would say something was set aside: %+v", sets)
	}
	if left, _ := filepath.Glob(filepath.Join(dir, continuity.RestoreStagePrefix+"*")); len(left) != 0 {
		t.Errorf("staging was left behind: %v", left)
	}

	appendFixtureEvent(t, a, filepath.Join(dir, "identity.sec"), "exp_on_the_new_machine")
	if a.ledger.LastSeq() != left.through+1 {
		t.Fatalf("an append after the restore landed at %d", a.ledger.LastSeq())
	}
	if a.dashboard != first {
		t.Fatal("restore replaced the firstboot dashboard instead of wiring it")
	}
	assertLiveDashboardSinks(t, a)
}

func TestARestoreOnANewMachineSendsNothingItBroughtBackAgain(t *testing.T) {
	var deadline int64
	left := oldMachineLiving(t, func(a *App) { deadline = waitingAtTheSnapshot(t, a) }, func(a *App) { sentAfterTheSnapshot(t, a) })
	a, _ := blankMachine(t)
	stopped := false
	defer func() {
		if !stopped {
			stopApp(t, a)
		}
	}()
	if _, err := a.continuity.NewMachineKeys(left.escrowFile, []byte(newMachinePass)); err != nil {
		t.Fatal(err)
	}
	if err := a.continuity.NewMachineUpload(left.snapshotName, "", bytes.NewReader(left.snapshotBytes)); err != nil {
		t.Fatal(err)
	}
	contact(t, a, Contact{Name: "friend", Channel: "primary", Address: "friend@example.test"})
	if done, err := a.continuity.NewMachineRestore(t.Context(), left.snapshotName, nil); err != nil || done.RestoredTo != left.through {
		t.Fatalf("the restore: %+v %v", done, err)
	}
	if sets := continuityDoor(a).View().SetAside; len(sets) != 0 {
		t.Fatalf("rig: something was set aside on a machine that held nothing: %+v", sets)
	}
	settled := settledByTheRestore(t, a, left.snapshotName, deadline)
	if err := a.store.AddOutboxMessage("msg_since", "peer", "friend", "written after the restore", nil); err != nil {
		t.Fatal(err)
	}
	stopApp(t, a)
	stopped = true

	c := New(a.cfg)
	if err := startLiveForTest(c); err != nil {
		t.Fatal(err)
	}
	defer stopApp(t, c)
	settledOnce(t, c, settled)
}

// ANOTHER IDENTITY'S SNAPSHOT IS NOT RESTORED UNDER THESE KEYS. The two
// files are bound to each other by the proof: the snapshot's identity is
// held to the key the escrow put back. The keys stay, the door stays open,
// and the right snapshot then restores.
func TestASnapshotOfAnotherIdentityIsNotRestoredUnderTheseKeys(t *testing.T) {
	mine, other := oldMachine(t), oldMachine(t)
	a, dir := blankMachine(t)
	defer stopApp(t, a)
	if _, err := a.continuity.NewMachineKeys(mine.escrowFile, []byte(newMachinePass)); err != nil {
		t.Fatal(err)
	}
	if err := a.continuity.NewMachineUpload(other.snapshotName, "", bytes.NewReader(other.snapshotBytes)); err != nil {
		t.Fatal(err)
	}
	_, err := a.continuity.NewMachineRestore(t.Context(), other.snapshotName, nil)
	if err == nil || !strings.Contains(err.Error(), "NOT restored") {
		t.Fatalf("ANOTHER IDENTITY'S SNAPSHOT WAS RESTORED: %v", err)
	}
	if fileExists(filepath.Join(dir, "ledger.jsonl")) || a.live.Load() {
		t.Fatal("a refused restore put something in place")
	}
	if !continuity.RestoringOnNewMachine(continuitySettingsOf(a.configSnapshot())) {
		t.Fatal("a refused restore closed the door: the next start would refuse first boot")
	}
	if err := a.continuity.NewMachineUpload(mine.snapshotName, "", bytes.NewReader(mine.snapshotBytes)); err != nil {
		t.Fatal(err)
	}
	if done, err := a.continuity.NewMachineRestore(t.Context(), mine.snapshotName, nil); err != nil || done.RestoredTo != mine.through {
		t.Fatalf("the right snapshot after a wrong one: %+v %v", done, err)
	}
}

// A MACHINE THAT HOLDS AN IDENTITY IS NOT A NEW MACHINE, and a snapshot's
// folder may hold only what a snapshot holds.
func TestTheNewMachineDoorRefusesWhatIsNotItsToDo(t *testing.T) {
	left := oldMachine(t)
	// Another identity's key is here already.
	a, dir := blankMachine(t)
	defer stopApp(t, a)
	other := oldMachine(t)
	otherKey, _, err := continuitytest.EscrowedKey(other.escrowFile, []byte(newMachinePass))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "identity.sec"), otherKey, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := a.continuity.NewMachineKeys(left.escrowFile, []byte(newMachinePass)); err == nil || !strings.Contains(err.Error(), "never written over") {
		t.Fatalf("A KEY WAS WRITTEN OVER, or the refusal does not say why: %v", err)
	}

	b, _ := blankMachine(t)
	defer stopApp(t, b)
	if _, err := b.continuity.NewMachineKeys(left.escrowFile, []byte(newMachinePass)); err != nil {
		t.Fatal(err)
	}
	plain := "ledger-20260920T101500Z-seq7"
	for _, member := range []string{"../identity.sec", "witness-keys/../../x.json", "notes.txt", "aii.db-wal"} {
		if err := b.continuity.NewMachineUpload(plain, member, strings.NewReader("x")); err == nil {
			t.Errorf("a snapshot's folder accepted %q", member)
		}
	}
	if err := b.continuity.NewMachineUpload(plain, "ledger.jsonl", strings.NewReader("x")); err != nil {
		t.Errorf("a snapshot's own member was refused: %v", err)
	}
	if _, err := b.continuity.NewMachineRestore(t.Context(), plain, nil); err == nil || !strings.Contains(err.Error(), "checksum list is missing") {
		t.Errorf("a folder that did not arrive whole: %v", err)
	}
}

// THE LAST ARRIVAL UNDER A NAME IS THE ONE THE PERSON MEANS. A snapshot's
// name holds a second and a record number and nothing of whose it is, so
// two identities make the same one. A person who picks the wrong file
// first (refused, as another identity's) and then the right one UNDER THE
// SAME NAME must get the right one: it used to be answered "it arrived
// before", and the wrong bytes stayed for good. What stood under the name
// in the door's directory was only the door's earlier arrival, sent from
// the person's own copy, so it is replaced; and nothing the door receives
// is among an identity's kept snapshots until the restore is done.
func TestTheRightSnapshotArrivesAfterAWrongOneOfTheSameName(t *testing.T) {
	mine, other := oldMachine(t), oldMachine(t)
	a, dir := blankMachine(t)
	defer stopApp(t, a)
	if _, err := a.continuity.NewMachineKeys(mine.escrowFile, []byte(newMachinePass)); err != nil {
		t.Fatal(err)
	}
	name := mine.snapshotName // and the other identity's file is carried under it
	if err := a.continuity.NewMachineUpload(name, "", bytes.NewReader(other.snapshotBytes)); err != nil {
		t.Fatal(err)
	}
	if _, err := a.continuity.NewMachineRestore(t.Context(), name, nil); err == nil {
		t.Fatal("another identity's snapshot was restored")
	}
	if err := a.continuity.NewMachineUpload(name, "", bytes.NewReader(mine.snapshotBytes)); err != nil {
		t.Fatal(err)
	}
	received := filepath.Join(dir, "restore-new-machine")
	if got, err := os.ReadFile(filepath.Join(received, name)); err != nil || !bytes.Equal(got, mine.snapshotBytes) {
		t.Fatalf("THE WRONG BYTES KEPT THE NAME: what is there is not what just arrived (%v)", err)
	}
	if entries, _ := os.ReadDir(received); len(entries) != 1 {
		t.Errorf("the door's directory holds more than the snapshot that arrived last: %v", entries)
	}
	if kept := continuityDoor(a).View().Snapshots; len(kept) != 0 {
		t.Errorf("what the door received reads as an identity's kept snapshot before any restore: %+v", kept)
	}
	done, err := a.continuity.NewMachineRestore(t.Context(), name, nil)
	if err != nil || done.RestoredTo != mine.through || !fileExists(filepath.Join(dir, "ledger.jsonl")) {
		t.Fatalf("the right snapshot under the same name: %+v %v", done, err)
	}
	if got, err := os.ReadFile(filepath.Join(a.backupsDir(a.configSnapshot()), name)); err != nil || !bytes.Equal(got, mine.snapshotBytes) {
		t.Fatalf("the restored snapshot is not the identity's kept snapshot: %v", err)
	}
}

// SO DOES A PLAINTEXT SNAPSHOT'S FOLDER sent again under its name: a
// folder takes its name at the third act, and one that took it before is
// replaced by the folder that arrived since, not kept in its way.
func TestAFolderSentAgainReplacesTheOneThatTookItsName(t *testing.T) {
	left := oldMachine(t)
	a, dir := blankMachine(t)
	defer stopApp(t, a)
	if _, err := a.continuity.NewMachineKeys(left.escrowFile, []byte(newMachinePass)); err != nil {
		t.Fatal(err)
	}
	const plain = "ledger-20260920T101500Z-seq7"
	published := filepath.Join(dir, "restore-new-machine", plain, "ledger.jsonl")
	for _, sent := range []string{"the first", "the second"} {
		for member, body := range map[string]string{"ledger.jsonl": sent, "SHA256SUMS": "x"} {
			if err := a.continuity.NewMachineUpload(plain, member, strings.NewReader(body)); err != nil {
				t.Fatal(err)
			}
		}
		_, err := a.continuity.NewMachineRestore(t.Context(), plain, nil)
		if err == nil || strings.Contains(err.Error(), "could not be kept") {
			t.Fatalf("%s folder: it must take its name and fail only its proof: %v", sent, err)
		}
		if got, _ := os.ReadFile(published); string(got) != sent {
			t.Fatalf("THE FOLDER UNDER THE NAME IS NOT THE ONE SENT LAST: %q, want %q", got, sent)
		}
	}
}

// "THE KEYS ARE IN PLACE" IS SAID ONLY OF KEYS THAT WERE READ BACK. Every way
// of looking at the place a key goes ends in one of three answers, and a
// failure to look is not "nothing is there".
func TestKeysAreNotReportedInPlaceOverAFilesystemFailure(t *testing.T) {
	// The failures are built from shapes no user can read through — a
	// directory where a file goes, a file where a directory goes — because a
	// permission bit is not a failure to the root these gates run as.
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
			if err := os.MkdirAll(a.continuity.SnapshotKeyPathForTest(), 0o700); err != nil {
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
		}, lookingThroughAFileSays()},
	} {
		t.Run(tc.what, func(t *testing.T) {
			mine := oldMachine(t)
			a, _ := blankMachine(t)
			defer stopApp(t, a)
			tc.breaks(t, a)
			got, err := a.continuity.NewMachineKeys(mine.escrowFile, []byte(newMachinePass))
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

// lookingThroughAFileSays is the refusal a key path under a FILE meets.
// Unix cannot look through a file (ENOTDIR), and the look refuses. Go
// reports that on Windows as ERROR_PATH_NOT_FOUND — nothing is there, a
// true answer — so the restore goes on and refuses one step later, when
// the key's directory cannot be made. Either way no key is reported in
// place.
func lookingThroughAFileSays() string {
	if runtime.GOOS == "windows" {
		return "could not be made"
	}
	return "cannot be looked at"
}

// A KEY IS IN PLACE WHEN IT HAS BEEN READ BACK FROM THE DISK A LATER START
// WILL READ IT FROM — not when a write returned no error. A disk that takes
// the bytes and gives back others is the case nothing else here can catch,
// and "the keys are in place" is the sentence a person acts on.
func TestAKeyThatDoesNotReadBackIsNotInPlace(t *testing.T) {
	mine := oldMachine(t)
	a, _ := blankMachine(t)
	defer stopApp(t, a)
	a.continuity.AfterKeyPublished = func(path string) {
		if err := os.WriteFile(path, []byte("what the disk gave back"), 0o600); err != nil {
			t.Errorf("rig: %v", err)
		}
	}
	got, err := a.continuity.NewMachineKeys(mine.escrowFile, []byte(newMachinePass))
	a.continuity.AfterKeyPublished = nil
	if err == nil {
		t.Fatalf("THE KEYS WERE REPORTED IN PLACE over a key that does not read back: %+v", got)
	}
	if !strings.Contains(err.Error(), "does not read back") {
		t.Errorf("the refusal does not say what happened: %v", err)
	}
	if got.Identity != "" || got.HoldsSnapshotKey {
		t.Errorf("a refusal reported keys: %+v", got)
	}
	if !continuity.RestoringOnNewMachine(continuitySettingsOf(a.configSnapshot())) {
		t.Error("the door closed on a refusal: the person cannot try again")
	}
}

// A RESTORE WHOSE START STOPS AT A PAGE DOES NOT SAY "THROUGH RECORD 0". A
// start that stops at a step it could not complete (here the provider the
// configuration names is not there, and the restore gave none) comes up at
// the SAFE page with the record closed, so there is no record to count. The
// door said "Restored through record 0" and the log "through record 0"; it
// says the identity is restored and its start stopped in SAFE, and the page
// is the identity's own and says why.
func TestARestoreOnANewMachineWhoseStartStopsAtAPageSaysSo(t *testing.T) {
	left := oldMachine(t)
	dir := t.TempDir()
	cfg := safebootConfig(t, dir, "New", filepath.Join(dir, "identity.sec"), filepath.Join(dir, "ledger.jsonl"), filepath.Join(dir, "aii.db"))
	cfg.LLM = LLMConfig{Provider: "an-entry-that-is-not-there", Model: "m"}
	a := New(cfg)
	defer stopApp(t, a)
	if choice, err := a.chooseBoot(); err != nil || choice != bootFirstboot {
		t.Fatalf("rig: a blank machine must be a first boot: %v %v", choice, err)
	}
	startFirstbootDashboardForTest(t, a)
	if _, err := a.continuity.NewMachineKeys(left.escrowFile, []byte(newMachinePass)); err != nil {
		t.Fatal(err)
	}
	if err := a.continuity.NewMachineUpload(left.snapshotName, "", bytes.NewReader(left.snapshotBytes)); err != nil {
		t.Fatal(err)
	}
	done, err := a.continuity.NewMachineRestore(t.Context(), left.snapshotName, nil)
	const said = "The identity is restored, and its start stopped in SAFE. Reload this page: it is the identity's own now, and it says what stopped the start and what to put right."
	if err == nil || err.Error() != said {
		t.Fatalf("A RESTORE WHOSE START STOPPED AT A PAGE WAS NOT SAID AS SUCH: %+v, %v", done, err)
	}
	reason, safe := a.SafeMode()
	if !safe || !strings.Contains(reason, safePostures[safeCouldNotStart].what) || !strings.Contains(reason, "LLM substrate: ") {
		t.Fatalf("the restored identity is not at the page of a start that could not complete: safe=%v %s", safe, reason)
	}
	if !fileExists(filepath.Join(dir, "ledger.jsonl")) {
		t.Fatal("the record the restore put in place is not there")
	}
}

// AN UPLOAD THAT STOPS ARRIVING IS ENDED, AND WHAT ARRIVED IS REMOVED.
// Through first boot's own route: a peer sends the head of an upload and a
// byte, keeps the connection, and sends nothing more. Nothing ended it: the
// upload, its half-written file and its connection stayed for as long as
// the peer liked. The body now has a read deadline that each byte moves
// forward, maintenance.restore_upload_idle_seconds after the last one: an
// upload still arriving, however slowly, is never cut, and one that has
// stopped is ended and answered, and what had arrived of it removed.
func TestAnUploadThatStopsArrivingIsEnded(t *testing.T) {
	left := oldMachine(t)
	a, dir := blankMachine(t)
	defer stopApp(t, a)
	if _, err := a.continuity.NewMachineKeys(left.escrowFile, []byte(newMachinePass)); err != nil {
		t.Fatal(err)
	}
	a.cfgMu.Lock()
	a.cfg.Maintenance.RestoreUploadIdleSeconds = 1
	a.cfgMu.Unlock()
	d := startFirstbootDashboardForTest(t, a)
	scheme, addr, _ := strings.Cut(d.Origin(), "://")
	var conn net.Conn
	var err error
	if scheme == "https" {
		conn, err = tls.Dial("tcp", addr, &tls.Config{InsecureSkipVerify: true})
	} else {
		conn, err = net.Dial("tcp", addr)
	}
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	head := fmt.Sprintf("POST /restore/upload?snapshot=%s HTTP/1.1\r\nHost: %s\r\nOrigin: %s\r\nSec-Fetch-Site: same-origin\r\nContent-Length: %d\r\n\r\n",
		left.snapshotName, addr, d.Origin(), len(left.snapshotBytes))
	if _, err := io.WriteString(conn, head); err != nil {
		t.Fatal(err)
	}
	type answer struct {
		status int
		body   string
		err    error
	}
	answered := make(chan answer, 1)
	go func() {
		resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
		if err != nil {
			answered <- answer{err: err}
			return
		}
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		answered <- answer{status: resp.StatusCode, body: string(body), err: err}
	}()
	// arriving lists what an upload has written so far, wherever under the
	// home: the snapshot's name under the pass's work prefix.
	arriving := func() []string {
		var work []string
		filepath.WalkDir(dir, func(path string, e fs.DirEntry, err error) error {
			if err == nil && strings.HasPrefix(e.Name(), continuity.SnapshotWorkPrefix) {
				work = append(work, path)
			}
			return nil
		})
		return work
	}
	// Seven bytes, three tenths of a second apart: two seconds of an upload
	// still arriving under a wait of one.
	for i := 0; i < 7; i++ {
		if _, err := conn.Write(left.snapshotBytes[i : i+1]); err != nil {
			t.Fatalf("byte %d of an upload still arriving could not be sent: %v", i, err)
		}
		time.Sleep(300 * time.Millisecond)
	}
	select {
	case got := <-answered:
		t.Fatalf("AN UPLOAD WHOSE BYTES WERE STILL ARRIVING WAS ENDED: %d %q %v", got.status, got.body, got.err)
	default:
	}
	if len(arriving()) == 0 {
		t.Fatal("rig: nothing of the upload is on this machine while it arrives")
	}

	// Nothing more is sent, and the connection is kept.
	var got answer
	select {
	case got = <-answered:
	case <-time.After(15 * time.Second):
		t.Fatal("AN UPLOAD THAT STOPPED ARRIVING WAS NEVER ENDED: it lasts for as long as the peer keeps the connection")
	}
	if got.err != nil || got.status != http.StatusBadRequest || !strings.Contains(got.body, "No part of the snapshot arrived for a second, so this machine stopped receiving it and removed what had arrived.") ||
		!strings.Contains(got.body, "maintenance.restore_upload_idle_seconds") {
		t.Fatalf("the page is not told why its upload ended and what to do: %d %q %v", got.status, got.body, got.err)
	}
	if work := arriving(); len(work) != 0 {
		t.Fatalf("WHAT ARRIVED OF AN UPLOAD THAT WAS ENDED IS STILL HERE: %v", work)
	}
	// The same snapshot sent again, whole, arrives.
	if err := a.continuity.NewMachineUpload(left.snapshotName, "", bytes.NewReader(left.snapshotBytes)); err != nil {
		t.Fatalf("the snapshot sent again: %v", err)
	}
}
