package app

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/continuity"
	"github.com/aiii-dot-id/aii-os/internal/continuity/continuitytest"
	"github.com/aiii-dot-id/aii-os/internal/crypto"
	"github.com/aiii-dot-id/aii-os/internal/updates"
)

// home_that_lost_half_test.go — a home that lost one half of its identity
// and holds the way to put it back: the record gone with a snapshot kept, or
// the key gone with the record here. Both go in through the boot decision a
// person's start uses, and both used to end there with a line in a terminal.

// THE RECORD IS GONE, THE KEY AND A PROVED SNAPSHOT ARE NOT. The start is
// not a birth and not an exit: it is SAFE at the page, no record is made,
// and a restore asked for there is performed by the start after it.
func TestAHomeThatLostOnlyItsRecordStartsAtItsRestoreDoor(t *testing.T) {
	r := newRestoreRig(t)
	at, snap := r.lived(t, 2, "kept", true)
	r.lived(t, 1, "lost", false)
	if err := os.Remove(r.ledgerPath); err != nil {
		t.Fatal(err)
	}

	safe, err := r.bootThroughChoice(t)
	if err != nil {
		t.Fatalf("A HOME THAT HOLDS ITS KEY AND A PROVED SNAPSHOT WAS REFUSED A START, so there is no page to restore on: %v", err)
	}
	reason, isSafe := safe.SafeMode()
	if !isSafe || !strings.Contains(reason, safePostures[safeRecordMissing].what) || !strings.Contains(reason, "Backups & Keys") {
		stopApp(t, safe)
		t.Fatalf("the start is not SAFE at the restore door: safe=%v %q", isSafe, reason)
	}
	if fileExists(r.ledgerPath) {
		stopApp(t, safe)
		t.Fatal("the start made a record where none was")
	}
	plan, err := continuityDoor(safe).RestorePlan(continuity.SourceSnapshot, snap)
	if err != nil || plan.RestoredTo != at || plan.ConfirmText == "" {
		stopApp(t, safe)
		t.Fatalf("the plan at the restore door: %+v %v", plan, err)
	}
	// THERE IS NO LIVE RECORD, which is not a record that does not read:
	// the plan says none is here and none is set aside.
	if !plan.NoLiveRecord || plan.SetAside != 0 {
		stopApp(t, safe)
		t.Fatalf("THE PLAN AT THE RESTORE DOOR OF A HOME WITH NO RECORD DOES NOT SAY THERE IS NONE: %+v", plan)
	}
	if err := continuityDoor(safe).Restore(continuity.SourceSnapshot, snap, plan.ConfirmText); err != nil {
		stopApp(t, safe)
		t.Fatalf("a restore cannot be asked for at the restore door: %v", err)
	}
	stopApp(t, safe)

	a, err := r.bootThroughChoice(t)
	if err != nil {
		t.Fatalf("the start after the request did not perform it: %v", err)
	}
	defer stopApp(t, a)
	if why, isSafe := a.SafeMode(); isSafe {
		t.Fatalf("the restored identity is SAFE: %s", why)
	}
	if a.ledger.LastSeq() != at {
		t.Fatalf("after the restore the record ends at %d, want the snapshot's %d", a.ledger.LastSeq(), at)
	}
}

// WITH NOTHING TO RESTORE FROM THE REFUSAL STANDS, as it did: a key and a
// database alone open no door, and nothing is born over them.
func TestAHomeWithNoRecordAndNothingToRestoreFromIsStillRefused(t *testing.T) {
	r := newRestoreRig(t)
	r.lived(t, 1, "lost", false)
	if err := os.Remove(r.ledgerPath); err != nil {
		t.Fatal(err)
	}
	a, err := r.bootThroughChoice(t)
	if err == nil {
		stopApp(t, a)
		t.Fatal("a home with no record and no snapshot was started")
	}
	if !strings.Contains(err.Error(), "refusing FIRSTBOOT") || fileExists(r.ledgerPath) {
		t.Fatalf("the refusal does not name itself, or a record was made: %v", err)
	}
}

// THE RECORD AND THE KEY ARE BOTH GONE, with a snapshot kept (row 9.13).
// It was refused at the boot decision, and the person who held the escrow
// file and a proved snapshot had a terminal. The start is SAFE at the page
// that puts the key back, held to what a kept snapshot names; the start
// after it is the restore door, and the restore after that brings the
// identity back as the snapshot holds it. Another identity's escrow and a
// wrong passphrase put nothing in place.
func TestAHomeThatLostBothItsRecordAndItsKeyStartsAtThePageThatPutsTheKeyBack(t *testing.T) {
	r := newRestoreRig(t)
	at, snap := r.lived(t, 2, "kept", true)
	a := r.boot(t)
	file, _, err := continuityDoor(a).EscrowCreate([]byte(pagePass))
	stopApp(t, a)
	if err != nil {
		t.Fatalf("rig: the escrow: %v", err)
	}
	held, err := crypto.LoadKeyPair(r.keyPath)
	if err != nil {
		t.Fatal(err)
	}
	other := newRestoreRig(t)
	b := other.boot(t)
	othersFile, _, err := continuityDoor(b).EscrowCreate([]byte(pagePass))
	stopApp(t, b)
	if err != nil {
		t.Fatalf("rig: the other identity's escrow: %v", err)
	}
	for _, gone := range []string{r.ledgerPath, r.keyPath} {
		if err := os.Remove(gone); err != nil {
			t.Fatal(err)
		}
	}

	safe, err := r.bootThroughChoice(t)
	if err != nil {
		t.Fatalf("A HOME THAT LOST ITS RECORD AND ITS KEY, AND KEEPS A SNAPSHOT, WAS REFUSED A START: there is no page to give the escrow file to: %v", err)
	}
	reason, isSafe := safe.SafeMode()
	door := continuityDoor(safe)
	if !isSafe || !strings.HasPrefix(reason, safePostures[safeRecordAndKeyMissing].what) || !door.View().KeyAbsent || !door.View().RecordAbsent {
		stopApp(t, safe)
		t.Fatalf("the start is not SAFE at the key's door for a home with no record: safe=%v view=%+v %q", isSafe, door.View(), reason)
	}
	if fileExists(r.ledgerPath) || fileExists(r.keyPath) {
		stopApp(t, safe)
		t.Fatal("the start made a record or a key")
	}
	if err := door.KeyBack(othersFile, []byte(pagePass)); err == nil || !strings.Contains(err.Error(), "No snapshot or set kept on this machine names the key in that escrow file") || fileExists(r.keyPath) {
		stopApp(t, safe)
		t.Fatalf("ANOTHER IDENTITY'S KEY WAS PUT IN PLACE OVER THIS IDENTITY'S SNAPSHOTS, or its refusal does not say so: %v (key written: %v)", err, fileExists(r.keyPath))
	}
	if err := door.KeyBack(file, []byte("not the passphrase")); err == nil || fileExists(r.keyPath) {
		stopApp(t, safe)
		t.Fatalf("a wrong passphrase put a key in place: %v", err)
	}
	if err := door.KeyBack(file, []byte(pagePass)); err != nil {
		stopApp(t, safe)
		t.Fatalf("the identity's own escrow did not put its key back: %v", err)
	}
	stopApp(t, safe)
	if back, err := crypto.LoadKeyPair(r.keyPath); err != nil || back.Fingerprint() != held.Fingerprint() {
		t.Fatalf("the key in place is not the identity's: %v", err)
	}

	// The start after it is the restore door, and the restore is the page's.
	page, err := r.bootThroughChoice(t)
	if err != nil {
		t.Fatalf("the start with the key back and no record: %v", err)
	}
	if reason, _ := page.SafeMode(); !strings.HasPrefix(reason, safePostures[safeRecordMissing].what) {
		stopApp(t, page)
		t.Fatalf("with the key back the start is not at the restore door: %s", reason)
	}
	plan, err := continuityDoor(page).RestorePlan(continuity.SourceSnapshot, snap)
	if err != nil || plan.RestoredTo != at {
		stopApp(t, page)
		t.Fatalf("the plan at the restore door: %+v %v", plan, err)
	}
	if err := continuityDoor(page).Restore(continuity.SourceSnapshot, snap, plan.ConfirmText); err != nil {
		stopApp(t, page)
		t.Fatalf("the restore cannot be asked for: %v", err)
	}
	stopApp(t, page)
	c, err := r.bootThroughChoice(t)
	if err != nil {
		t.Fatalf("the start after the restore was asked: %v", err)
	}
	defer stopApp(t, c)
	if why, isSafe := c.SafeMode(); isSafe || c.ledger.LastSeq() != at {
		t.Fatalf("the identity is not back as the snapshot holds it: safe=%v %q, the record ends at %d, want %d", isSafe, why, c.ledger.LastSeq(), at)
	}
}

// THE SNAPSHOT IS ENCRYPTED, AND ITS KEY WAS LOST WITH THE REST. The page
// opens the snapshot's receipt with the snapshot key the escrow holds, holds
// the identity key to the identity it names, and puts both keys back.
func TestTheKeyOfAHomeThatLostBothIsHeldToAnEncryptedSnapshot(t *testing.T) {
	r := newRestoreRig(t)
	a := r.boot(t)
	file, _, err := continuityDoor(a).EscrowCreate([]byte(pagePass))
	if err == nil {
		_, err = continuityDoor(a).EscrowCheck(file, []byte(pagePass))
	}
	snapshotKey := a.continuity.SnapshotKeyPathForTest()
	stopApp(t, a)
	if err != nil {
		t.Fatalf("rig: the escrow, made and checked: %v", err)
	}
	if _, snap := r.lived(t, 2, "sealed", true); !strings.HasSuffix(snap, ".tar.age") {
		t.Fatalf("rig: the snapshot was to be encrypted: %s", snap)
	}
	for _, gone := range []string{r.ledgerPath, r.keyPath, snapshotKey} {
		if err := os.Remove(gone); err != nil {
			t.Fatal(err)
		}
	}
	safe, err := r.bootThroughChoice(t)
	if err != nil {
		t.Fatalf("the start of a home that lost both: %v", err)
	}
	defer stopApp(t, safe)
	if err := continuityDoor(safe).KeyBack(file, []byte(pagePass)); err != nil {
		t.Fatalf("THE KEY WAS NOT PUT BACK, HELD TO AN ENCRYPTED SNAPSHOT its own escrow opens: %v", err)
	}
	if !fileExists(r.keyPath) || !fileExists(snapshotKey) {
		t.Fatalf("the identity key and the snapshot key are not both back: key %v, snapshot key %v", fileExists(r.keyPath), fileExists(snapshotKey))
	}
	// The snapshot was opened in the key check's scratch, owner-only, and
	// nothing of it is left there.
	scratch := filepath.Join(filepath.Dir(r.ledgerPath), ".key-check")
	info, err := os.Stat(scratch)
	if err != nil || (runtime.GOOS != "windows" && info.Mode().Perm() != 0o700) {
		t.Fatalf("the key check's scratch is not an owner-only directory beside the record: %v %v", info, err)
	}
	if left, _ := os.ReadDir(scratch); len(left) != 0 {
		t.Fatalf("A DECRYPTED SNAPSHOT WAS LEFT ON THE DISK after the key check returned: %v", left)
	}
}

// WHAT A KEY CHECK LEFT THAT NEVER RETURNED IS SWEPT AT EVERY START, SAFE
// INCLUDED. The check opens an encrypted snapshot in its scratch beside the
// record; a crash in the middle of it leaves a decrypted copy of the record
// there. A home the key's door serves starts SAFE until its key is back, so
// a sweep at normal starts alone would leave that copy across every start in
// between.
func TestAKeyChecksDecryptedSnapshotIsSweptAtEveryStart(t *testing.T) {
	plant := func(t *testing.T, dataDir string) string {
		t.Helper()
		left := filepath.Join(dataDir, ".key-check", "open-crashed", "set")
		if err := os.MkdirAll(left, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(left, "ledger.jsonl"), []byte("a decrypted record"), 0o600); err != nil {
			t.Fatal(err)
		}
		return filepath.Join(dataDir, ".key-check")
	}
	t.Run("a SAFE start", func(t *testing.T) {
		r := newRestoreRig(t)
		r.lived(t, 1, "lived", false)
		if err := os.Remove(r.keyPath); err != nil {
			t.Fatal(err)
		}
		scratch := plant(t, filepath.Dir(r.ledgerPath))
		safe, err := r.bootThroughChoice(t)
		if err != nil {
			t.Fatal(err)
		}
		defer stopApp(t, safe)
		if _, isSafe := safe.SafeMode(); !isSafe {
			t.Fatal("rig: the start must be SAFE")
		}
		if fileExists(scratch) {
			t.Fatal("A SAFE START LEFT A DECRYPTED SNAPSHOT A KEY CHECK HAD LEFT")
		}
	})
	t.Run("a normal start", func(t *testing.T) {
		r := newRestoreRig(t)
		r.lived(t, 1, "lived", false)
		scratch := plant(t, filepath.Dir(r.ledgerPath))
		a, err := r.bootThroughChoice(t)
		if err != nil {
			t.Fatal(err)
		}
		defer stopApp(t, a)
		if fileExists(scratch) {
			t.Fatal("A NORMAL START LEFT A DECRYPTED SNAPSHOT A KEY CHECK HAD LEFT")
		}
	})
}

// THE KEY'S DOOR CLEARS THE SECRETS IT HOLDS, on every answer: the key
// file's bytes and the snapshot key the escrow held, and the key read back
// from the disk. What Go cannot clear (the private key inside a parsed key
// pair, a string a request arrived in) is said in openEscrow.
func TestTheKeyDoorClearsTheSecretsItHolds(t *testing.T) {
	r := newRestoreRig(t)
	a := r.boot(t)
	file, _, err := continuityDoor(a).EscrowCreate([]byte(pagePass))
	stopApp(t, a)
	if err != nil {
		t.Fatal(err)
	}
	other := newRestoreRig(t)
	b := other.boot(t)
	othersFile, _, err := continuityDoor(b).EscrowCreate([]byte(pagePass))
	stopApp(t, b)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(r.keyPath); err != nil {
		t.Fatal(err)
	}
	safe, err := r.bootThroughChoice(t)
	if err != nil {
		t.Fatal(err)
	}
	defer stopApp(t, safe)
	var held [][]byte
	safe.continuity.KeyBackHolds = func(secret []byte) { held = append(held, secret) }
	cleared := func(t *testing.T, answer string) {
		t.Helper()
		n := 0
		for _, secret := range held {
			for _, c := range secret {
				if c != 0 {
					t.Fatalf("A SECRET THE KEY'S DOOR HELD WAS NOT CLEARED after %s: %d of its bytes stand", answer, len(secret))
				}
			}
			n += len(secret)
		}
		if n == 0 {
			t.Fatalf("rig: the door held nothing to clear after %s", answer)
		}
		held = nil
	}
	if err := continuityDoor(safe).KeyBack(othersFile, []byte(pagePass)); err == nil {
		t.Fatal("rig: another identity's escrow must be refused")
	}
	cleared(t, "a refusal")
	if err := continuityDoor(safe).KeyBack(file, []byte(pagePass)); err != nil {
		t.Fatal(err)
	}
	if len(held) < 3 {
		t.Fatalf("the door did not hand over the key it read back: %d secret(s)", len(held))
	}
	cleared(t, "the key put back")
}

// A SNAPSHOT KEY THAT DOES NOT OPEN THE SNAPSHOTS IS SAID, AND REPLACED
// ONLY WHEN ASKED. The record is gone, the identity's key is here, and the
// snapshot key on this machine is not the one the snapshots were made under
// (made afresh after a loss). Every restore of them was withdrawn "it does
// not open", and the page did not say why. It says so now, and the escrow's
// snapshot key goes in its place only on the person's act, only if it opens
// them, and the one that stood is moved aside, not deleted; the restore then
// proves. With no snapshot key at all the page says that, and the same act
// puts one in place. A normal start does not take the act.
func TestASnapshotKeyThatDoesNotOpenTheSnapshotsIsSaidAndReplacedOnlyWhenAsked(t *testing.T) {
	r := newRestoreRig(t)
	a := r.boot(t)
	file, _, err := continuityDoor(a).EscrowCreate([]byte(pagePass))
	if err == nil {
		_, err = continuityDoor(a).EscrowCheck(file, []byte(pagePass))
	}
	snapshotKey := a.continuity.SnapshotKeyPathForTest()
	if err == nil {
		if _, refuse := continuityDoor(a).SnapshotKeyBack(file, []byte(pagePass)); refuse == nil || !strings.Contains(refuse.Error(), "from a SAFE start only") {
			err = fmt.Errorf("a running identity's snapshot key was offered to be replaced: %v", refuse)
		}
	}
	stopApp(t, a)
	if err != nil {
		t.Fatal(err)
	}
	other := newRestoreRig(t)
	b := other.boot(t)
	othersFile, _, err := continuityDoor(b).EscrowCreate([]byte(pagePass))
	stopApp(t, b)
	if err != nil {
		t.Fatal(err)
	}
	at, snap := r.lived(t, 2, "sealed", true)
	if !strings.HasSuffix(snap, ".tar.age") {
		t.Fatalf("rig: the snapshot was to be encrypted: %s", snap)
	}
	if err := os.Remove(r.ledgerPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(snapshotKey); err != nil {
		t.Fatal(err)
	}
	stale := continuitytest.FreshSnapshotKey(t)
	if err := os.WriteFile(snapshotKey, stale, 0o600); err != nil {
		t.Fatal(err)
	}

	safe, err := r.bootThroughChoice(t)
	if err != nil {
		t.Fatal(err)
	}
	door := continuityDoor(safe)
	if v := door.View(); v.SnapshotKeyMismatch != snap || v.SnapshotKeyAbsent {
		stopApp(t, safe)
		t.Fatalf("THE PAGE DOES NOT SAY THE SNAPSHOT KEY HERE DOES NOT OPEN THE SNAPSHOTS: %+v", v)
	}
	if _, err := door.SnapshotKeyBack(othersFile, []byte(pagePass)); err == nil || !strings.Contains(err.Error(), "another identity") {
		stopApp(t, safe)
		t.Fatalf("another identity's escrow put a snapshot key in place: %v", err)
	}
	// THIS IDENTITY'S ESCROW, HOLDING A SNAPSHOT KEY THAT DOES NOT OPEN THE
	// SNAPSHOTS EITHER (one made before the key was changed): nothing is
	// put in place, and the key that stands is not moved aside for it.
	keyFile, err := os.ReadFile(r.keyPath)
	if err != nil {
		stopApp(t, safe)
		t.Fatal(err)
	}
	neither := continuitytest.SealedWith(t, keyFile, continuitytest.FreshSnapshotKey(t), []byte(pagePass))
	if _, err := door.SnapshotKeyBack(neither, []byte(pagePass)); err == nil || !strings.Contains(err.Error(), "does not open the snapshots kept here either") {
		stopApp(t, safe)
		t.Fatalf("A SNAPSHOT KEY THAT DOES NOT OPEN THE SNAPSHOTS WAS PUT IN PLACE, or its refusal does not say so: %v", err)
	}
	if raw, _ := os.ReadFile(snapshotKey); !bytes.Equal(raw, stale) {
		stopApp(t, safe)
		t.Fatal("a refused act changed the snapshot key that stood here")
	}
	if aside, _ := filepath.Glob(snapshotKey + ".set-aside-*"); len(aside) != 0 {
		stopApp(t, safe)
		t.Fatalf("a refused act moved the snapshot key that stood here aside: %v", aside)
	}
	said, err := door.SnapshotKeyBack(file, []byte(pagePass))
	if err != nil || !strings.Contains(said, "the one that stood here is kept beside it") {
		stopApp(t, safe)
		t.Fatalf("the escrow's snapshot key was not put in place, or the answer does not say where the old one is: %q %v", said, err)
	}
	aside, _ := filepath.Glob(snapshotKey + ".set-aside-*")
	if len(aside) != 1 {
		stopApp(t, safe)
		t.Fatalf("THE SNAPSHOT KEY THAT STOOD HERE WAS NOT KEPT ASIDE: %v", aside)
	}
	if raw, _ := os.ReadFile(aside[0]); !bytes.Equal(raw, stale) {
		stopApp(t, safe)
		t.Fatal("what was kept aside is not the key that stood here")
	}
	if v := door.View(); v.SnapshotKeyMismatch != "" {
		stopApp(t, safe)
		t.Fatalf("the page still says the snapshot key does not open the snapshots: %+v", v)
	}
	plan, err := door.RestorePlan(continuity.SourceSnapshot, snap)
	if err == nil {
		err = door.Restore(continuity.SourceSnapshot, snap, plan.ConfirmText)
	}
	stopApp(t, safe)
	if err != nil {
		t.Fatal(err)
	}
	c, err := r.bootThroughChoice(t)
	if err != nil {
		t.Fatal(err)
	}
	defer stopApp(t, c)
	if why, isSafe := c.SafeMode(); isSafe || c.ledger.LastSeq() != at {
		t.Fatalf("the restore of the encrypted snapshot did not prove under the escrow's snapshot key: safe=%v %q, record ends at %d, want %d", isSafe, why, c.ledger.LastSeq(), at)
	}
}

// NO SNAPSHOT KEY AT ALL, with encrypted snapshots kept: the page says that,
// and the escrow's snapshot key goes in.
func TestNoSnapshotKeyBesideEncryptedSnapshotsIsSaid(t *testing.T) {
	r := newRestoreRig(t)
	a := r.boot(t)
	file, _, err := continuityDoor(a).EscrowCreate([]byte(pagePass))
	if err == nil {
		_, err = continuityDoor(a).EscrowCheck(file, []byte(pagePass))
	}
	snapshotKey := a.continuity.SnapshotKeyPathForTest()
	stopApp(t, a)
	if err != nil {
		t.Fatal(err)
	}
	_, snap := r.lived(t, 1, "sealed", true)
	for _, gone := range []string{r.ledgerPath, snapshotKey} {
		if err := os.Remove(gone); err != nil {
			t.Fatal(err)
		}
	}
	safe, err := r.bootThroughChoice(t)
	if err != nil {
		t.Fatal(err)
	}
	defer stopApp(t, safe)
	door := continuityDoor(safe)
	if v := door.View(); v.SnapshotKeyMismatch != snap || !v.SnapshotKeyAbsent {
		t.Fatalf("THE PAGE DOES NOT SAY NO SNAPSHOT KEY IS HERE: %+v", v)
	}
	if said, err := door.SnapshotKeyBack(file, []byte(pagePass)); err != nil || strings.Contains(said, "kept beside it") || !fileExists(snapshotKey) {
		t.Fatalf("the escrow's snapshot key was not put in place where none stood: %q %v", said, err)
	}
}

// A HOME THAT KEPT ONLY A SET KEPT ASIDE IS NOT A BLANK CONTAINER. Its
// record, key, database and snapshots are gone, and a set an earlier
// restore kept aside is all that is left: a whole record that lived here.
// The boot decision's evidence did not look for one, so the home went to
// FIRSTBOOT and a new identity would have been born beside the record
// kept aside. It starts at the page that puts the key back, held to what
// the set names; the start after it is the restore door, which offers the
// set.
func TestAHomeThatKeptOnlyASetKeptAsideStartsAtThePageNotFirstBoot(t *testing.T) {
	t.Run("the shape the review found", func(t *testing.T) {
		_, _, kept, _ := recordedBy(t, "Aside", nil)
		dir := t.TempDir()
		set := filepath.Join(dir, "set-aside", "Aside-20261014T120000Z-before-restore-to-seq1")
		if err := os.MkdirAll(set, 0o700); err != nil {
			t.Fatal(err)
		}
		raw, err := os.ReadFile(kept)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(set, "ledger.jsonl"), raw, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(set, continuity.RestoreRecordName), []byte(`{}`), 0o600); err != nil {
			t.Fatal(err)
		}
		a := New(safebootConfig(t, dir, "Aside", filepath.Join(dir, "identity.sec"), filepath.Join(dir, "ledger.jsonl"), filepath.Join(dir, "aii.db")))
		ev, err := a.identityEvidence()
		if err != nil || !strings.Contains(ev, "set kept aside "+set) {
			t.Fatalf("A SET KEPT ASIDE IS NOT IDENTITY EVIDENCE: %q %v", ev, err)
		}
		if choice, err := a.chooseBoot(); err != nil || choice != bootLive {
			t.Fatalf("A HOME THAT KEPT ONLY A SET GOES TO FIRSTBOOT: choice %v (live %v), %v", choice, bootLive, err)
		}
	})

	t.Run("a set a restore kept aside, and the way back", func(t *testing.T) {
		r := newRestoreRig(t)
		_, snap := r.lived(t, 2, "before", true)
		later, _ := r.lived(t, 3, "after", false)
		a := r.boot(t)
		file, _, err := continuityDoor(a).EscrowCreate([]byte(pagePass))
		if err != nil {
			stopApp(t, a)
			t.Fatalf("rig: the escrow: %v", err)
		}
		plan, err := continuityDoor(a).RestorePlan(continuity.SourceSnapshot, snap)
		if err == nil {
			err = continuityDoor(a).Restore(continuity.SourceSnapshot, snap, plan.ConfirmText)
		}
		stopApp(t, a)
		if err != nil {
			t.Fatalf("rig: the restore that sets the record aside: %v", err)
		}
		a = r.boot(t)
		sets := continuityDoor(a).View().SetAside
		stopApp(t, a)
		if len(sets) != 1 || sets[0].WasThrough != later {
			t.Fatalf("rig: one set kept aside, through %d: %+v", later, sets)
		}
		for _, gone := range []string{r.ledgerPath, r.keyPath, r.dbPath, r.dbPath + "-wal", r.dbPath + "-shm", filepath.Join(r.dir, continuity.BackupsDirName)} {
			if err := os.RemoveAll(gone); err != nil {
				t.Fatal(err)
			}
		}

		safe, err := r.bootThroughChoice(t)
		if err != nil {
			t.Fatalf("A HOME THAT KEPT ONLY A SET WAS REFUSED A START: %v", err)
		}
		reason, isSafe := safe.SafeMode()
		if !isSafe || !strings.HasPrefix(reason, safePostures[safeRecordAndKeyMissing].what) || fileExists(r.ledgerPath) || fileExists(r.keyPath) {
			stopApp(t, safe)
			t.Fatalf("A HOME THAT KEPT ONLY A SET IS NOT AT THE PAGE THAT PUTS THE KEY BACK, or a record or a key was made: safe=%v %q", isSafe, reason)
		}
		if err := continuityDoor(safe).KeyBack(file, []byte(pagePass)); err != nil {
			stopApp(t, safe)
			t.Fatalf("the identity's own escrow did not put its key back, held to the set: %v", err)
		}
		stopApp(t, safe)
		page, err := r.bootThroughChoice(t)
		if err != nil {
			t.Fatalf("the start with the key back and only a set: %v", err)
		}
		defer stopApp(t, page)
		if reason, _ := page.SafeMode(); !strings.HasPrefix(reason, safePostures[safeRecordMissing].what) {
			t.Fatalf("with the key back the start is not at the restore door: %s", reason)
		}
		if plan, err := continuityDoor(page).RestorePlan(continuity.SourceSetAside, sets[0].Name); err != nil || plan.RestoredTo != later {
			t.Fatalf("the restore door does not offer the set kept aside: %+v %v", plan, err)
		}
	})
}

// A FOLDER OF SETS KEPT ASIDE THAT CANNOT BE LISTED REFUSES FIRST BOOT:
// whether a record is kept in it cannot be told, so nothing is born over
// what may be one, and the refusal names the folder.
func TestASetAsideFolderThatCannotBeListedRefusesFirstBoot(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("a folder this user may not list is an access list on Windows, not a mode")
	}
	if os.Geteuid() == 0 {
		t.Skip("the superuser lists every folder")
	}
	dir := t.TempDir()
	root := filepath.Join(dir, "set-aside")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(root, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(root, 0o700) })
	a := New(safebootConfig(t, dir, "Unlisted", filepath.Join(dir, "identity.sec"), filepath.Join(dir, "ledger.jsonl"), filepath.Join(dir, "aii.db")))
	choice, err := a.chooseBoot()
	if err == nil || !strings.Contains(err.Error(), "refusing FIRSTBOOT") || !strings.Contains(err.Error(), "could not list the sets kept aside at "+root) {
		t.Fatalf("A FOLDER OF SETS THAT CANNOT BE LISTED WAS TAKEN FOR AN EMPTY HOME: choice %v (first boot %v), %v", choice, bootFirstboot, err)
	}
}

// WITH NOTHING TO RESTORE FROM, A HOME THAT LOST BOTH IS REFUSED, as it
// was: a database alone opens no door, and nothing is born over it.
func TestAHomeThatLostBothWithNothingToRestoreFromIsStillRefused(t *testing.T) {
	r := newRestoreRig(t)
	r.lived(t, 1, "lost", false)
	for _, gone := range []string{r.ledgerPath, r.keyPath} {
		if err := os.Remove(gone); err != nil {
			t.Fatal(err)
		}
	}
	a, err := r.bootThroughChoice(t)
	if err == nil {
		stopApp(t, a)
		t.Fatal("a home with no record, no key and nothing to restore from was started")
	}
	if !strings.Contains(err.Error(), "refusing FIRSTBOOT") || fileExists(r.ledgerPath) || fileExists(r.keyPath) {
		t.Fatalf("the refusal does not name itself, or a record or a key was made: %v", err)
	}
}

// THE RECORD IS HERE AND THE KEY IS NOT. The start is SAFE at the page, the
// page says the key is what is missing, and the escrow file puts it back:
// only this record's key, only under its passphrase, and the start after it
// verifies the whole record under the key and goes on.
func TestAHomeThatLostOnlyItsKeyStartsAtTheDoorThatPutsItBack(t *testing.T) {
	r := newRestoreRig(t)
	a := r.boot(t)
	appendFixtureEvent(t, a, r.keyPath, "exp_before_the_key_was_lost")
	last := a.ledger.LastSeq()
	file, _, err := continuityDoor(a).EscrowCreate([]byte(pagePass))
	stopApp(t, a)
	if err != nil {
		t.Fatalf("rig: the escrow: %v", err)
	}
	held, err := crypto.LoadKeyPair(r.keyPath)
	if err != nil {
		t.Fatal(err)
	}
	// Another identity's escrow, under the same passphrase.
	other := newRestoreRig(t)
	b := other.boot(t)
	othersFile, _, err := continuityDoor(b).EscrowCreate([]byte(pagePass))
	stopApp(t, b)
	if err != nil {
		t.Fatalf("rig: the other identity's escrow: %v", err)
	}
	if err := os.Remove(r.keyPath); err != nil {
		t.Fatal(err)
	}

	safe, err := r.bootThroughChoice(t)
	if err != nil {
		t.Fatalf("A HOME THAT HOLDS ITS RECORD AND LOST ITS KEY EXITED, so there is no page to give the escrow file to: %v", err)
	}
	reason, isSafe := safe.SafeMode()
	door := continuityDoor(safe)
	if !isSafe || !strings.Contains(reason, safePostures[safeKeyMissing].what) || !door.View().KeyAbsent {
		stopApp(t, safe)
		t.Fatalf("the start is not SAFE at the key's door: safe=%v key_absent=%v %q", isSafe, door.View().KeyAbsent, reason)
	}
	if err := door.KeyBack(othersFile, []byte(pagePass)); err == nil || !strings.Contains(err.Error(), "another identity") || fileExists(r.keyPath) {
		stopApp(t, safe)
		t.Fatalf("ANOTHER IDENTITY'S KEY WAS PUT UNDER THIS RECORD, or its refusal does not say so: %v (key written: %v)", err, fileExists(r.keyPath))
	}
	if err := door.KeyBack(file, []byte("not the passphrase")); err == nil || fileExists(r.keyPath) {
		stopApp(t, safe)
		t.Fatalf("a wrong passphrase put a key in place: %v", err)
	}
	if err := door.KeyBack(file, []byte(pagePass)); err != nil {
		stopApp(t, safe)
		t.Fatalf("the identity's own escrow did not put its key back: %v", err)
	}
	stopApp(t, safe)
	if back, err := crypto.LoadKeyPair(r.keyPath); err != nil || back.Fingerprint() != held.Fingerprint() {
		t.Fatalf("the key in place is not the identity's: %v", err)
	}

	c, err := r.bootThroughChoice(t)
	if err != nil {
		t.Fatalf("the start under the key that was put back: %v", err)
	}
	defer stopApp(t, c)
	if why, isSafe := c.SafeMode(); isSafe {
		t.Fatalf("the identity is SAFE under its own key: %s", why)
	}
	if c.ledger.LastSeq() != last {
		t.Fatalf("the record ends at %d, want %d: the key came back and the record is what it was", c.ledger.LastSeq(), last)
	}
	if continuityDoor(c).View().KeyAbsent {
		t.Fatal("a normal start says its key is absent")
	}
}

// WHILE AN UPDATE'S ROLLBACK IS ARMED THE START ENDS, AS IT DID. A page does
// not restart, and the rollback is made by the next start: at the first boot
// of an update a key that does not load may be the new program's fault, and
// under a service the restart restores the previous program unattended. The
// state is the one a swap leaves (as in
// TestASchemaSafeBootLeavesAnUpdateRollbackArmed).
func TestAStartThatCannotLoadItsKeyEndsWhileAnUpdatesRollbackIsArmed(t *testing.T) {
	whereAnEndedStartIsStartedAgain(t)
	dir := t.TempDir()
	keyPath, ledgerPath, dbPath := birthFixture(t, dir, "Update")
	buildPriorProjection(t, ledgerPath, dbPath)
	backup := []byte("the previous binary")
	sum := sha256.Sum256(backup)
	if err := os.WriteFile(filepath.Join(dir, "aii.previous"), backup, 0o755); err != nil {
		t.Fatal(err)
	}
	record := `{"attempts":0,"backup_sha256":"` + hex.EncodeToString(sum[:]) + `","new_sha256":"` + strings.Repeat("0", 64) + `"}`
	if err := os.WriteFile(filepath.Join(dir, ".update_pending"), []byte(record), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(keyPath); err != nil {
		t.Fatal(err)
	}

	a := New(safebootConfig(t, dir, "Update", keyPath, ledgerPath, dbPath))
	err := startLiveForTest(a)
	_, safe := a.SafeMode()
	stopApp(t, a)
	if err == nil || safe {
		t.Fatalf("A START THAT COULD NOT LOAD ITS KEY HELD A PAGE WHILE AN UPDATE'S ROLLBACK WAS ARMED: nothing restarts it, so nothing restores the previous program (err %v, safe %v)", err, safe)
	}
	if !strings.Contains(err.Error(), "load identity key") || !updates.RollbackArmed(dir) {
		t.Fatalf("the start must end on the key, with the rollback still armed: armed=%v err=%v", updates.RollbackArmed(dir), err)
	}
}

// A HOME WITH NO RECORD ENDS WHILE AN UPDATE'S ROLLBACK IS ARMED, as a home
// whose key does not load does (above): a program that looks for the record
// in the wrong place may be the update that failed, and the next start
// restores the previous one. The start ends on a line that says so, makes
// no record, and leaves the rollback armed.
func TestAHomeThatLostOnlyItsRecordEndsWhileAnUpdatesRollbackIsArmed(t *testing.T) {
	whereAnEndedStartIsStartedAgain(t)
	r := newRestoreRig(t)
	r.lived(t, 2, "kept", true)
	if err := os.Remove(r.ledgerPath); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Dir(r.ledgerPath)
	backup := []byte("the previous binary")
	sum := sha256.Sum256(backup)
	if err := os.WriteFile(filepath.Join(dir, "aii.previous"), backup, 0o755); err != nil {
		t.Fatal(err)
	}
	record := `{"attempts":0,"backup_sha256":"` + hex.EncodeToString(sum[:]) + `","new_sha256":"` + strings.Repeat("0", 64) + `"}`
	if err := os.WriteFile(filepath.Join(dir, ".update_pending"), []byte(record), 0o600); err != nil {
		t.Fatal(err)
	}
	// The swap retires the boot marker the lives above wrote.
	if err := os.Rename(filepath.Join(dir, ".boot_completed"), filepath.Join(dir, ".boot_completed.retired")); err != nil {
		t.Fatal(err)
	}

	a, err := r.bootThroughChoice(t)
	if err == nil {
		_, safe := a.SafeMode()
		stopApp(t, a)
		t.Fatalf("A HOME WITH NO RECORD HELD A PAGE WHILE AN UPDATE'S ROLLBACK WAS ARMED: nothing restarts it, so nothing restores the previous program (safe %v)", safe)
	}
	if !strings.Contains(err.Error(), "no record at "+r.ledgerPath+", and an update installed this version just before this start: the next start restores the previous version") {
		t.Fatalf("the start did not end on the line for a home with no record under an armed rollback: %v", err)
	}
	if !updates.RollbackArmed(dir) {
		t.Fatal("the start that ended left no rollback armed")
	}
	if fileExists(r.ledgerPath) {
		t.Fatal("the start made a record where none was")
	}
}

// A HOME THAT LOST BOTH ITS RECORD AND ITS KEY IS NOT AT THE KEY'S PAGE
// WHILE AN UPDATE'S ROLLBACK IS ARMED (row 9.13). A program that cannot
// find the record or load the key may be the update that failed: where a
// service manager starts it again the start ends, and the next start
// restores the previous program; where nothing does (Windows) the start
// holds the page of a start that could not complete and is tried again by
// itself, and that start restores the previous program. Neither asks a
// person for the escrow file over a fault the rollback mends.
func TestAHomeThatLostBothIsNotAtTheKeysPageWhileAnUpdatesRollbackIsArmed(t *testing.T) {
	lostBoth := func(t *testing.T) *restoreRig {
		t.Helper()
		r := newRestoreRig(t)
		r.lived(t, 2, "kept", true)
		for _, gone := range []string{r.ledgerPath, r.keyPath} {
			if err := os.Remove(gone); err != nil {
				t.Fatal(err)
			}
		}
		dir := filepath.Dir(r.ledgerPath)
		armedUpdate(t, dir)
		// The swap retires the boot marker the lives above wrote.
		if err := os.Rename(filepath.Join(dir, ".boot_completed"), filepath.Join(dir, ".boot_completed.retired")); err != nil {
			t.Fatal(err)
		}
		return r
	}

	t.Run("where a service manager starts it again", func(t *testing.T) {
		whereAnEndedStartIsStartedAgain(t)
		r := lostBoth(t)
		a, err := r.bootThroughChoice(t)
		if err == nil {
			reason, _ := a.SafeMode()
			stopApp(t, a)
			t.Fatalf("A HOME THAT LOST BOTH HELD A PAGE WHILE AN UPDATE'S ROLLBACK WAS ARMED: nothing restarts it, so nothing restores the previous program (%s)", reason)
		}
		if !strings.Contains(err.Error(), "load identity key") || !updates.RollbackArmed(filepath.Dir(r.ledgerPath)) {
			t.Fatalf("the start must end on the key, with the rollback still armed: %v", err)
		}
		if fileExists(r.ledgerPath) || fileExists(r.keyPath) {
			t.Fatal("the start that ended made a record or a key")
		}
	})
	t.Run("where nothing starts it again", func(t *testing.T) {
		prev := endedStartsRestart
		t.Cleanup(func() { endedStartsRestart = prev })
		endedStartsRestart = false
		caught := catchRetries(t)
		r := lostBoth(t)
		a, err := r.bootThroughChoice(t)
		if err != nil {
			t.Fatalf("the start with nothing to start it again ended: %v", err)
		}
		defer stopApp(t, a)
		reason, _ := a.SafeMode()
		if strings.HasPrefix(reason, safePostures[safeRecordAndKeyMissing].what) || !strings.Contains(reason, "that start restores the previous program") || len(caught.delays) != 1 {
			t.Fatalf("A HOME THAT LOST BOTH WAS AT THE KEY'S PAGE WHILE AN UPDATE'S ROLLBACK WAS ARMED, or the retry that restores the previous program was not armed: %v %s", caught.delays, reason)
		}
	})
}

// WHAT A HOME THAT LOST HALF IS TOLD SAYS WHAT WAS NOT WRITTEN. Such a start
// writes its claim's file, its log and the boot marker, so "nothing was
// written" was not true of it; what it does not write is a record, or
// anything to the record. Held word for word: the identity reads it in
// Ring 0 of that start.
func TestAHomeThatLostHalfIsToldWhatWasNotWritten(t *testing.T) {
	for trigger, want := range map[safeTrigger]string{
		safeRecordMissing: "Your signed record is not where it is kept on this machine. Your key is, and so is at least one snapshot of your record, or a set an earlier restore kept aside. No one was born in your place, and no record was made where yours was kept. " +
			"Your operator will restore from one of them on the page; the next boot performs the restore and checks it. What you lived after that snapshot was taken is not in it.",
		safeKeyMissing: "Your record is here, and the key that signs it is not on this machine, or does not read, so your record can be neither checked nor added to. Nothing was written to your record. " +
			"Your operator will put your key back from the escrow file they keep; the next boot checks your whole record under it.",
	} {
		if got := safePostures[trigger].why; got != want {
			t.Errorf("trigger %d tells the identity:\n got %s\nwant %s", trigger, got, want)
		}
	}
}
