package continuity

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/crypto"
	"github.com/aiii-dot-id/aii-os/internal/escrow"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

// continuity_page_test.go — the person's door, on the host: the escrow is
// made in memory and proves nothing until the saved file is picked back; a
// check holds the file to THIS identity and THIS host's snapshot key; and
// the whole of journey 8's first half runs with no command and no path.

const pagePass = "correct horse battery staple"

// THE JOURNEY: no escrow → snapshots in the clear and the page says why →
// make the file (nothing written here, nothing turned on) → pick it back →
// the receipt is signed → the next snapshot is encrypted.
func TestMakingAndCheckingAnEscrowFromThePageTurnsEncryptionOn(t *testing.T) {
	a, home, keyPath := maintApp(t)
	cfg := a.configSnapshot()
	appendFixtureEvent(t, a, keyPath, "exp_a")
	hostSnapshotKey(t, a)

	v := a.continuity.continuityView()
	if v.Encrypting || v.Unencrypted != store.UnencryptedNoEscrow || v.UnencryptedText != "Snapshots are not encrypted: no checked recovery copy of the snapshot key is recorded. To encrypt them, make an escrow file below and check it." {
		t.Fatalf("before any escrow: %+v", v)
	}
	if strings.Contains(v.UnencryptedText, "aii ") || strings.Contains(v.UnencryptedText, home) {
		t.Errorf("the page's words send a person to a terminal or a path: %q", v.UnencryptedText)
	}

	before := dirListing(t, home)
	file, name, err := a.continuity.escrowCreateHere([]byte(pagePass))
	if err != nil || len(file) == 0 || !strings.HasPrefix(name, "escrow-"+a.door.kp.Fingerprint()[:12]+"-") || !strings.HasSuffix(name, ".age") {
		t.Fatalf("create: %q %v", name, err)
	}
	if after := dirListing(t, home); after != before {
		t.Errorf("MAKING THE FILE WROTE ON THE HOST: before %s, after %s", before, after)
	}
	if a.continuity.continuityView().Encrypting {
		t.Fatal("MAKING THE FILE TURNED ENCRYPTION ON: a file nobody has saved protects nothing")
	}

	c, kp, err := escrow.Open(file, []byte(pagePass))
	if err != nil || kp.Fingerprint() != a.door.kp.Fingerprint() || c.SnapshotKey == nil {
		t.Fatalf("the file the person saves: %v", err)
	}
	c.Wipe()

	// A wrong passphrase, a file that is not one, a truncated one: refused in
	// words a person can act on, and nothing is turned on.
	for what, tc := range map[string]struct{ file, pass []byte }{
		"a wrong passphrase":     {file, []byte("not the passphrase at all")},
		"a file that is not one": {[]byte("hello"), []byte(pagePass)},
		"a truncated file":       {file[:len(file)/2], []byte(pagePass)},
		"no file":                {nil, []byte(pagePass)},
	} {
		if _, err := a.continuity.escrowCheckHere(tc.file, tc.pass); err == nil || strings.Contains(err.Error(), home) {
			t.Errorf("%s: %v", what, err)
		}
	}
	if a.continuity.continuityView().Encrypting {
		t.Fatal("a check that failed turned encryption on")
	}

	view, err := a.continuity.escrowCheckHere(file, []byte(pagePass))
	if err != nil || !view.Encrypting || !view.EscrowCovers || view.EscrowCheckedAt == "" {
		t.Fatalf("checking the saved file: %+v %v", view, err)
	}
	if _, err := os.Stat(a.continuity.escrowReceiptPath(cfg)); err != nil {
		t.Fatalf("no receipt was written: %v", err)
	}
	made, err := a.continuity.personTake(t.Context())
	if err != nil || len(made.Snapshots) != 1 || !made.Snapshots[0].Encrypted || !made.Snapshots[0].OnDemand {
		t.Fatalf("Back up now after a checked escrow: %+v %v", made.Snapshots, err)
	}
	if said, err := a.continuity.personVerify(t.Context(), made.Snapshots[0].Name); err != nil || !strings.Contains(said, "proved") {
		t.Fatalf("Prove it restores: %q %v", said, err)
	}
}

// A FILE THAT OPENS IS NOT YET A FILE THAT COVERS. Another identity's escrow,
// and an escrow made before this host's snapshot key was made again, both
// open under their passphrase — and neither may sign a receipt here.
func TestAnEscrowThatDoesNotCoverThisHostSignsNoReceipt(t *testing.T) {
	a, _, keyPath := maintApp(t)
	cfg := a.configSnapshot()
	appendFixtureEvent(t, a, keyPath, "exp_a")
	hostSnapshotKey(t, a)
	old, _, err := a.continuity.escrowCreateHere([]byte(pagePass))
	if err != nil {
		t.Fatal(err)
	}
	// The snapshot key is made again: the old escrow holds the old one.
	os.Remove(a.continuity.snapshotKeyPath(cfg))
	hostSnapshotKey(t, a)
	if _, err := a.continuity.escrowCheckHere(old, []byte(pagePass)); err == nil || !strings.Contains(err.Error(), "OLDER snapshot key") {
		t.Errorf("an escrow holding an older snapshot key: %v", err)
	}

	// Another identity's escrow.
	otherDir := t.TempDir()
	otherKP, err := crypto.GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	otherPath := filepath.Join(otherDir, "identity.sec")
	if _, err := crypto.SaveKeyPair(otherKP, otherPath); err != nil {
		t.Fatal(err)
	}
	otherKey, _ := os.ReadFile(otherPath)
	snap, _ := escrow.NewSnapshotKey()
	foreign, err := escrow.Seal(escrow.Contents{IdentityKey: otherKey, SnapshotKey: snap}, []byte(pagePass))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.continuity.escrowCheckHere(foreign, []byte(pagePass)); err == nil || !strings.Contains(err.Error(), "another identity") {
		t.Errorf("another identity's escrow: %v", err)
	}
	if _, err := os.Stat(a.continuity.escrowReceiptPath(cfg)); !os.IsNotExist(err) {
		t.Fatalf("A RECEIPT WAS SIGNED FOR AN ESCROW THAT DOES NOT COVER THIS HOST: %v", err)
	}
	if a.continuity.continuityView().Encrypting {
		t.Fatal("encryption is on with no escrow that covers this host")
	}
}

// THE PAGE'S FLOOR AND SAFE: a passphrase that needs no guessing is refused
// on the host too, and SAFE makes and checks nothing.
func TestThePagesEscrowRefusals(t *testing.T) {
	a, _, keyPath := maintApp(t)
	appendFixtureEvent(t, a, keyPath, "exp_a")
	if _, _, err := a.continuity.escrowCreateHere([]byte(pagePass)); err == nil || !strings.Contains(err.Error(), "no snapshot key yet") {
		t.Errorf("before the snapshot key exists: %v", err)
	}
	hostSnapshotKey(t, a)
	if _, _, err := a.continuity.escrowCreateHere([]byte("short")); err == nil || !strings.Contains(err.Error(), "shorter than") {
		t.Errorf("a short passphrase: %v", err)
	}
	file, _, err := a.continuity.escrowCreateHere([]byte(pagePass))
	if err != nil {
		t.Fatal(err)
	}
	a.enterSafe("test: the record is frozen")
	if _, _, err := a.continuity.escrowCreateHere([]byte(pagePass)); err == nil || !strings.Contains(err.Error(), "SAFE") {
		t.Errorf("create in SAFE: %v", err)
	}
	if _, err := a.continuity.escrowCheckHere(file, []byte(pagePass)); err == nil || !strings.Contains(err.Error(), "SAFE") {
		t.Errorf("check in SAFE: %v", err)
	}
	if _, err := a.continuity.personTake(t.Context()); err == nil || !strings.Contains(err.Error(), "Restart normally") || strings.Contains(err.Error(), "recall source") || strings.Contains(err.Error(), "your operator") {
		t.Errorf("Back up now in SAFE must be refused in the PERSON's words: %v", err)
	}
	if v := a.continuity.continuityView(); !v.Safe || v.NextPass != "" {
		t.Errorf("the view in SAFE: %+v", v)
	}
}

// EVERY REASON THE PAGE CAN END SAYS WHAT TO DO THERE. The line under "not
// encrypted" is read by someone who opened Backups, and since the
// missing escrow stopped being an alert and an item of attention (R115 as
// amended 2026-10-10) it is the one place that lack is said. A line that
// names a lack and not the act that ends it leaves a person where they
// were. The three reasons the page's own controls end each name the act,
// "below"; none sends a person to a terminal or a path.
func TestEveryReasonThePageCanEndSaysWhatToDoThere(t *testing.T) {
	for _, reason := range []string{store.UnencryptedNoEscrow, store.UnencryptedReceiptUnread, store.UnencryptedReceiptMismatch} {
		text := unencryptedForPerson(reason)
		if !strings.Contains(text, " below") || !strings.Contains(text, "escrow file") || !strings.Contains(text, "heck") {
			t.Errorf("%s: the page says what is wrong and not what to do about it: %q", reason, text)
		}
		if strings.Contains(text, "aii ") || strings.Contains(text, "/") {
			t.Errorf("%s: the page's words send a person to a terminal or a path: %q", reason, text)
		}
	}
}

// dirListing names everything under a directory, for "nothing was written".
func dirListing(t *testing.T, root string) string {
	t.Helper()
	var names []string
	filepath.WalkDir(root, func(p string, _ os.DirEntry, err error) error {
		if err == nil {
			names = append(names, strings.TrimPrefix(p, root))
		}
		return nil
	})
	return strings.Join(names, "|")
}

func TestOnlyAnEncryptedKeptSnapshotIsHandedOverForDownload(t *testing.T) {
	a, _, keyPath := maintApp(t)
	dir := a.backupsDir(a.configSnapshot())
	live(t, a, 1)
	appendFixtureEvent(t, a, keyPath, "exp_download")
	a.continuity.runMaintenance(t.Context())
	kept, _ := keptSnapshots(dir)
	if len(kept) != 1 || kept[0].Encrypted {
		t.Fatalf("rig: one plaintext snapshot: %v", kept)
	}
	if rc, _, err := a.continuity.downloadSnapshot(kept[0].Name); err == nil {
		rc.Close()
		t.Fatal("A SNAPSHOT IN THE CLEAR WAS HANDED OVER FOR DOWNLOAD")
	} else if !strings.Contains(err.Error(), "not encrypted") || !strings.Contains(err.Error(), "escrow file") {
		t.Errorf("the refusal does not say why or what to do: %v", err)
	}

	_, recipient := hostSnapshotKey(t, a)
	checkedEscrow(t, a, a.door.kp, recipient)
	made, err := a.continuity.takeOnDemand(t.Context(), false, time.Now())
	if err != nil || !made.Encrypted {
		t.Fatalf("rig: an encrypted snapshot: %+v %v", made, err)
	}
	for _, name := range []string{"", "../" + made.Name, made.Name + "x", filepath.Join(dir, made.Name)} {
		if rc, _, err := a.continuity.downloadSnapshot(name); err == nil {
			rc.Close()
			t.Errorf("%q, which is not a kept snapshot's name, was handed over", name)
		}
	}
	rc, size, err := a.continuity.downloadSnapshot(made.Name)
	if err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(rc)
	rc.Close()
	want, _ := os.ReadFile(filepath.Join(dir, made.Name))
	if err != nil || int64(len(got)) != size || !bytes.Equal(got, want) {
		t.Fatalf("the download is not the kept file: %d of %d bytes, %v", len(got), size, err)
	}
	if left, _ := filepath.Glob(filepath.Join(dir, SnapshotWorkPrefix+"*")); len(left) != 0 {
		t.Errorf("a download made files beside the snapshots: %v", left)
	}
}
