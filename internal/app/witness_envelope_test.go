package app

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/continuity"
	"github.com/aiii-dot-id/aii-os/internal/witness"
)

// envelopeOf is the envelope this identity presents, as the anchorer asks
// for it, and fails the test when there is none.
func envelopeOf(t *testing.T, a *App) []byte {
	t.Helper()
	canonical, _, err := witness.EnsureIdentityEnvelope(witness.AsIdentityKey(a.keyPair), a.store, filepath.Dir(a.ledger.Path()))
	if err != nil {
		t.Fatalf("the identity's witness envelope: %v", err)
	}
	return canonical
}

// AN IDENTITY ENROLLED BEFORE THE ENVELOPE WAS KEPT BESIDE ITS RECORD HAS IT
// KEPT THERE AT ITS NEXT START, from the store, as the store holds it, and
// the store keeps its row: a program that does not read the file (0.1.23,
// after a rollback) reads the row, as it always did.
func TestAnIdentityEnrolledBeforeTheFileHasItsEnvelopeKeptBesideItsRecordAtItsNextStart(t *testing.T) {
	f := newSealedFixture(t)
	f.bootAndAnchor(t)
	path := witness.EnvelopePath(filepath.Dir(f.ledgerPath))
	// As 0.1.23 leaves an enrolled identity: the row, and no file.
	if err := os.Remove(path); err != nil {
		t.Fatalf("rig: the anchor pass keeps the envelope beside the record: %v", err)
	}

	a := New(f.cfg)
	if err := startLiveForTest(a); err != nil {
		t.Fatal(err)
	}
	defer stopApp(t, a)
	if reason, safe := a.SafeMode(); safe {
		t.Fatalf("the start is SAFE: %s", reason)
	}
	kept, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("THE ENROLLED IDENTITY'S ENVELOPE WAS NOT KEPT BESIDE ITS RECORD AT ITS START: %v", err)
	}
	row, err := a.store.LoadWitnessEnvelope()
	if err != nil || !bytes.Equal(kept, row) {
		t.Fatalf("the file is not the store's envelope byte for byte, or the store lost its row (%v)", err)
	}
	if err := a.anchorer.CheckAndAnchor(); err != nil {
		t.Fatalf("the identity anchors as itself after its envelope is kept: %v", err)
	}
	if _, identities := f.witness.Holds(); identities != 1 {
		t.Fatalf("the witness holds %d identities", identities)
	}
}

func TestAnEnvelopeBesideTheRecordThatTheRecordContradictsIsRewrittenFromIt(t *testing.T) {
	f := newSealedFixture(t)
	f.bootAndAnchor(t)
	path := witness.EnvelopePath(filepath.Dir(f.ledgerPath))
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// The same key's envelope minted at another second: another identity
	// at the witness.
	other := regexp.MustCompile(`"created_at":"[^"]+"`).ReplaceAll(raw, []byte(`"created_at":"2001-02-03T04:05:06Z"`))
	if bytes.Equal(other, raw) {
		t.Fatal("rig: the envelope was not changed")
	}
	if err := os.WriteFile(path, other, 0o600); err != nil {
		t.Fatal(err)
	}

	a := New(f.cfg)
	if err := startLiveForTest(a); err != nil {
		t.Fatal(err)
	}
	defer stopApp(t, a)
	if reason, safe := a.SafeMode(); safe {
		t.Fatalf("an envelope beside the record put the identity in SAFE: %s", reason)
	}
	appendFixtureEvent(t, a, f.keyPath, "exp_after_the_envelope_was_changed")
	if err := a.anchorer.CheckAndAnchor(); err != nil {
		t.Fatalf("the identity did not anchor as the identity its record names: %v", err)
	}
	if got, _ := os.ReadFile(path); !bytes.Equal(got, raw) {
		t.Fatal("THE ENVELOPE THE RECORD CONTRADICTS WAS NOT REWRITTEN FROM THE RECORD")
	}
	if _, identities := f.witness.Holds(); identities != 1 {
		t.Fatalf("the witness holds %d identities: the contradicted envelope was presented", identities)
	}
}

// A STORE ENVELOPE THE RECORD DOES NOT NAME IS NOT KEPT AT THE START THAT
// KEEPS IT, AND THE OPERATOR IS TOLD. Nothing is written beside the
// record; the record decides at the envelope's next use.
func TestAStoreEnvelopeTheRecordDoesNotNameIsNotKeptAndIsTold(t *testing.T) {
	f := newSealedFixture(t)
	f.bootAndAnchor(t)
	path := witness.EnvelopePath(filepath.Dir(f.ledgerPath))
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	a := New(f.cfg)
	if err := startLiveForTest(a); err != nil {
		t.Fatal(err)
	}
	defer stopApp(t, a)
	// As a start meets it: no file, and a row of another identity.
	other := regexp.MustCompile(`"created_at":"[^"]+"`).ReplaceAll(raw, []byte(`"created_at":"2001-02-03T04:05:06Z"`))
	if err := a.store.ReplaceWitnessEnvelope(other); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	a.keepWitnessEnvelope(a.keyPair, a.ledger)
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("A ROW THE RECORD DOES NOT NAME WAS KEPT BESIDE THE RECORD (%v)", err)
	}
	ids, err := a.store.OutboxIDsUnder("witness_envelope_")
	if err != nil || len(ids) != 1 {
		t.Fatalf("THE OPERATOR WAS NOT TOLD THE STORE'S ENVELOPE IS NOT THE RECORD'S: %q %v", ids, err)
	}
	if said, _ := a.store.OutboxContent(ids[0]); !strings.HasPrefix(said, envelopeRowDisagreesSaid) {
		t.Fatalf("the notice is not the one for a row the record does not name: %q", said)
	}
	// The record decides at the next use: its envelope, kept beside it.
	if got := envelopeOf(t, a); !bytes.Equal(got, raw) {
		t.Fatal("the next use did not take the record's envelope")
	}
	if got, _ := os.ReadFile(path); !bytes.Equal(got, raw) {
		t.Fatal("the record's envelope was not kept beside the record at its next use")
	}
}

// A SNAPSHOT CARRIES THE ENVELOPE, AND A RESTORE ON A NEW MACHINE PUTS IT
// BESIDE THE RECORD. The identity on the new machine is the identity the
// witness knows, from its first bookmark.
func TestARestoreOnANewMachineCarriesTheWitnessEnvelope(t *testing.T) {
	var envelope []byte
	left := oldMachineLiving(t, func(old *App) { envelope = envelopeOf(t, old) }, nil)
	a, dir := blankMachine(t)
	defer stopApp(t, a)
	if _, err := a.continuity.NewMachineKeys(left.escrowFile, []byte(newMachinePass)); err != nil {
		t.Fatal(err)
	}
	if err := a.continuity.NewMachineUpload(left.snapshotName, "", bytes.NewReader(left.snapshotBytes)); err != nil {
		t.Fatal(err)
	}
	if done, err := a.continuity.NewMachineRestore(t.Context(), left.snapshotName, nil); err != nil || done.RestoredTo != left.through {
		t.Fatalf("the restore: %+v %v", done, err)
	}
	got, err := os.ReadFile(witness.EnvelopePath(dir))
	if err != nil || !bytes.Equal(got, envelope) {
		t.Fatalf("THE RESTORE ON A NEW MACHINE DID NOT PUT THE WITNESS ENVELOPE BESIDE THE RECORD: %v", err)
	}
	if !bytes.Equal(envelopeOf(t, a), envelope) {
		t.Fatal("the restored identity presents another envelope than the one it was")
	}
}

func TestARestoreTakesTheEnvelopeWithTheRecord(t *testing.T) {
	r := newRestoreRig(t)
	at, snap := r.lived(t, 2, "before", true)
	a := r.boot(t)
	envelope := envelopeOf(t, a)
	// A bookmark kept beside the live record is about that record too.
	bookmark := []byte(`{"kept":"beside the live record"}`)
	if err := os.WriteFile(witness.PendingRequestPath(filepath.Dir(r.ledgerPath)), bookmark, 0o600); err != nil {
		stopApp(t, a)
		t.Fatal(err)
	}
	plan, err := continuityDoor(a).RestorePlan(continuity.SourceSnapshot, snap)
	if err != nil {
		stopApp(t, a)
		t.Fatal(err)
	}
	if err := continuityDoor(a).Restore(continuity.SourceSnapshot, snap, plan.ConfirmText); err != nil {
		stopApp(t, a)
		t.Fatal(err)
	}
	stopApp(t, a)

	a = r.boot(t)
	defer stopApp(t, a)
	if a.ledger.LastSeq() != at {
		t.Fatalf("rig: the restore ended at %d, the snapshot at %d", a.ledger.LastSeq(), at)
	}
	if _, err := os.Stat(witness.EnvelopePath(filepath.Dir(r.ledgerPath))); !os.IsNotExist(err) {
		t.Fatalf("AN ENVELOPE STANDS BESIDE A RECORD RESTORED TO BEFORE IT EXISTED (%v)", err)
	}
	sets := continuityDoor(a).View().SetAside
	if len(sets) != 1 {
		t.Fatalf("rig: %d sets kept aside", len(sets))
	}
	if kept, err := os.ReadFile(filepath.Join(sets[0].Path, witness.EnvelopeFileName)); err != nil || !bytes.Equal(kept, envelope) {
		t.Fatalf("THE LIVE ENVELOPE DID NOT GO ASIDE WITH THE RECORD IT STOOD BESIDE: %v", err)
	}
	if kept, err := os.ReadFile(filepath.Join(sets[0].Path, witness.PendingRequestFileName)); err != nil || !bytes.Equal(kept, bookmark) {
		t.Fatalf("THE BOOKMARK KEPT BESIDE THE LIVE RECORD DID NOT GO ASIDE WITH IT: %v", err)
	}
	if _, err := os.Stat(witness.PendingRequestPath(filepath.Dir(r.ledgerPath))); !os.IsNotExist(err) {
		t.Fatalf("a bookmark about the record set aside stands beside the restored one (%v)", err)
	}
}

// THE WITNESS FILES ARE CAPTURED WITH THE RECORD, AND A PUBLICATION OF ONE
// OFFERED DURING THE CAPTURE WAITS AT THE DOOR (P1 of the witness design,
// revision 5). The capture copies the witness files beside the ledger under
// the door's lock, at the record's instant; every publication of them takes
// the same lock (ledgerAdapter.WitnessFiles), so one offered inside the
// capture lands after it, and the capture holds the files as they stood
// with its record.
func TestAWitnessFilePublicationOfferedDuringTheCaptureWaitsAtTheDoor(t *testing.T) {
	a, _, _ := maintApp(t)
	dir := filepath.Dir(a.ledger.Path())
	receipt := []byte(`{"kept":"before the capture"}`)
	if err := os.WriteFile(witness.PendingReceiptPath(dir), receipt, 0o600); err != nil {
		t.Fatal(err)
	}
	landed := make(chan error, 1)
	var landedInsideTheCapture bool
	prev := snapshotStep
	t.Cleanup(func() { snapshotStep = prev })
	snapshotStep = func(step string) {
		if step != "tail-copied" {
			return
		}
		go func() {
			landed <- a.door.WitnessFiles(func() error {
				return os.WriteFile(witness.PendingRequestPath(dir), []byte(`{"kept":"during the capture"}`), 0o600)
			})
		}()
		// Long enough for an unguarded publication to land many times
		// over; under the door's lock it cannot land however long this
		// waits.
		select {
		case err := <-landed:
			landedInsideTheCapture = true
			landed <- err
		case <-time.After(300 * time.Millisecond):
		}
	}
	dst := t.TempDir()
	_, pin, err := a.door.capture(t.Context(), filepath.Join(dst, "ledger.jsonl"))
	snapshotStep = prev
	if err != nil {
		t.Fatal(err)
	}
	pin.Release()
	if landedInsideTheCapture {
		t.Fatal("A WITNESS FILE WAS PUBLISHED INSIDE THE CAPTURE")
	}
	if got, err := os.ReadFile(filepath.Join(dst, witness.PendingReceiptFileName)); err != nil || !bytes.Equal(got, receipt) {
		t.Fatalf("THE CAPTURE DID NOT TAKE THE WITNESS FILES THAT STOOD WITH ITS RECORD: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dst, witness.PendingRequestFileName)); !os.IsNotExist(err) {
		t.Fatalf("the capture holds a file published after its record (%v)", err)
	}
	if err := <-landed; err != nil {
		t.Fatalf("the publication that waited at the door was then refused: %v", err)
	}
	if _, err := os.Stat(witness.PendingRequestPath(dir)); err != nil {
		t.Fatalf("the publication that waited never landed: %v", err)
	}
}

// A WITNESS KEY PROOF THAT MAY NOT REPLACE THE KEPT ONE LEAVES THE IDENTITY
// RUNNING AND TELLS THE OPERATOR (P3 of the witness design, revision 5).
// The witness serves a manifest for its key whose window begins later than
// the one kept beside the record: the kept file stands, the pass is
// refused, the identity is not SAFE, and the operator has a notice.
func TestAWitnessKeyProofThatMayNotReplaceTheKeptOneTellsTheOperator(t *testing.T) {
	f := newSealedFixture(t)
	a := New(f.cfg)
	if err := startLiveForTest(a); err != nil {
		t.Fatal(err)
	}
	defer stopApp(t, a)
	if err := a.anchorer.CheckAndAnchor(); err != nil {
		t.Fatalf("the first anchor: %v", err)
	}
	keyFile := filepath.Join(witness.WitnessKeysDir(filepath.Dir(f.ledgerPath)), f.witness.KeyID+".json")
	kept, err := os.ReadFile(keyFile)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	f.witness.ServesManifest(f.platform.SignManifestWindow(t, f.witness.Env, now.Add(-30*time.Minute), now.Add(24*time.Hour)))
	appendFixtureEvent(t, a, f.keyPath, "exp_after_the_refreshed_manifest")
	if err := a.anchorer.CheckAndAnchor(); !errors.Is(err, witness.ErrWitnessKeyKept) {
		t.Fatalf("a manifest that may not replace the kept one was not refused: %v", err)
	}
	if reason, safe := a.SafeMode(); safe {
		t.Fatalf("A KEY PROOF THE WITNESS SERVES PUT THE IDENTITY IN SAFE: %s", reason)
	}
	if got, _ := os.ReadFile(keyFile); !bytes.Equal(got, kept) {
		t.Fatal("THE KEPT KEY FILE WAS CHANGED")
	}
	ids, err := a.store.OutboxIDsUnder("witness_key_kept_")
	if err != nil || len(ids) != 1 {
		t.Fatalf("THE OPERATOR WAS NOT TOLD THE WITNESS'S KEY PROOF WAS NOT TAKEN: %q %v", ids, err)
	}
	if said, _ := a.store.OutboxContent(ids[0]); !strings.HasPrefix(said, fmt.Sprintf(witnessKeyKeptSaid, f.cfg.Witness.URL)) {
		t.Fatalf("the notice is not the one for a key proof that was not taken: %q", said)
	}
}

// THE RUNNING ANCHORER PUBLISHES ITS FILES UNDER THE DOOR (P1). Boot gives
// the anchorer the ledger door as its file guard; one that omitted it would
// write beside the ledger while a capture holds the door. Here the door is
// held while an anchor pass runs: the pass writes nothing beside the ledger
// until the door is let go, and then anchors.
func TestTheRunningAnchorerPublishesItsFilesUnderTheDoor(t *testing.T) {
	f := newSealedFixture(t)
	a := New(f.cfg)
	if err := startLiveForTest(a); err != nil {
		t.Fatal(err)
	}
	defer stopApp(t, a)
	keys := witness.WitnessKeysDir(filepath.Dir(f.ledgerPath))
	if _, err := os.Stat(keys); !os.IsNotExist(err) {
		t.Fatalf("rig: the fresh identity already keeps witness keys (%v)", err)
	}
	a.door.mu.Lock()
	done := make(chan error, 1)
	go func() { done <- a.anchorer.CheckAndAnchor() }()
	select {
	case err := <-done:
		a.door.mu.Unlock()
		t.Fatalf("the anchor pass ran to its end while the door was held: %v", err)
	case <-time.After(300 * time.Millisecond):
	}
	_, keyErr := os.Stat(keys)
	_, envErr := os.Stat(witness.EnvelopePath(filepath.Dir(f.ledgerPath)))
	a.door.mu.Unlock()
	if keyErr == nil || envErr == nil {
		t.Fatalf("THE ANCHOR PASS WROTE BESIDE THE LEDGER WHILE THE DOOR WAS HELD (keys: %v, envelope: %v)", keyErr, envErr)
	}
	if err := <-done; err != nil {
		t.Fatalf("the anchor pass, the door let go: %v", err)
	}
}
