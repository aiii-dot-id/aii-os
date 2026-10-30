package witness

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// bookmarksAsked is how many bookmarks the witness has been sent.
func bookmarksAsked(fw *fakeWitness) int {
	fw.mu.Lock()
	defer fw.mu.Unlock()
	n := 0
	for _, c := range fw.sawIdentityIDs {
		n += c
	}
	return n
}

// lostFirstEnrolment is D9: the identity enrolled at fw under an envelope
// minted an hour ago, the witness's first receipt for record 5 answered and
// its mint lost, and then the database and the envelope and receipt beside
// the ledger lost, with two records since. It gives the ledger, the key
// pair, the platform file, the original envelope and its DID.
func lostFirstEnrolment(t *testing.T, fw *fakeWitness, p *platformKeys) (lgPath string, a func(minter EventMinter) (*Anchorer, *recordStore), original []byte, did string) {
	t.Helper()
	lg, kp := testLedger(t, 5)
	key := AsIdentityKey(kp)
	dir := filepath.Dir(lg.Path())
	envPath := p.writeEnv(t)
	original, did = enrolled(t, key, time.Now().Add(-time.Hour).UTC().Truncate(time.Second))
	if err := writeEnvelopeFile(dir, key, original); err != nil {
		t.Fatal(err)
	}
	first := NewAnchorer(New(fw.server.URL, ""), lg, key, &recordStore{lg: lg}, &recordStore{lg: lg}, failingMinter{}, 1, envPath)
	if err := first.CheckAndAnchor(); err == nil {
		t.Fatal("fixture: the first receipt's mint must be lost")
	}
	if err := os.Remove(PendingReceiptPath(dir)); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(EnvelopePath(dir)); err != nil {
		t.Fatal(err)
	}
	appendRecords(t, lg, kp, 2)
	return lg.Path(), func(minter EventMinter) (*Anchorer, *recordStore) {
		store := &recordStore{lg: lg}
		if m, ok := minter.(testMinter); ok && m.lg == nil {
			minter = testMinter{lg, kp}
		}
		return NewAnchorer(New(fw.server.URL, ""), lg, key, store, store, minter, 1, envPath), store
	}, original, did
}

// THE REGISTERED ENROLMENT OF THIS KEY IS ADOPTED ON ITS EVIDENCE (R1). The
// identity enrolled, its first receipt was never minted, and its envelope
// was lost with its database: a fresh envelope is minted, and the witness,
// which holds one enrolment for a key, refuses it with the registered
// identity's envelope and receipt. The envelope holds this key and derives
// the registered id, the receipt verifies, and the record names no witness
// identity: the envelope is kept beside the ledger and as the row, the
// receipt is held to the head rule and minted, late, as the record's first
// head, and the next pass anchors under the adopted identity. The record is
// one the next boot accepts.
func TestTheRegisteredEnrolmentOfThisKeyIsAdoptedOnItsEvidence(t *testing.T) {
	p := synthPlatform(t)
	fw := newFakeWitness(t)
	fw.manifest = buildManifest(t, p, fw.witnessEnv, false)
	fw.oneEnrolmentPerKey = true
	lgPath, anchorer, original, did := lostFirstEnrolment(t, fw, p)
	a, store := anchorer(testMinter{})
	var adopted, stood []string
	a.SetOnEnrolmentAdopted(func(id string) { adopted = append(adopted, id) })
	a.SetOnStandingRefusal(func(_ *ConflictError, why string) { stood = append(stood, why) })
	a.SetOnIntegrityConflict(func(ce *ConflictError) { t.Errorf("an adoption latched a conflict: %v", ce) })

	if err := a.CheckAndAnchor(); err != nil {
		t.Fatalf("the registered enrolment was not adopted: %v", err)
	}
	dir := filepath.Dir(lgPath)
	if file, err := os.ReadFile(EnvelopePath(dir)); err != nil || !bytes.Equal(file, original) || !bytes.Equal(store.row, original) {
		t.Fatalf("the envelope beside the ledger and the row are not the registered one: file %s (%v), row %s", file, err, store.row)
	}
	lg := store.lg
	heads := recordHeads(t, lg)
	if len(heads) != 1 || heads[0].IdentityID != did || heads[0].LedgerOrdinal != 5 || heads[0].PreviousWitnessedLedgerOrdinal != -1 {
		t.Fatalf("the witness's first receipt is not the record's first head: %+v", heads)
	}
	if len(adopted) != 1 || adopted[0] != did || len(stood) != 0 || a.StandingRefusal() != nil {
		t.Fatalf("adopted %v, stood %v, standing %v", adopted, stood, a.StandingRefusal())
	}
	if p, err := readPendingReceipt(dir); err != nil || p != nil {
		t.Fatalf("the minted receipt is still beside the ledger: %+v (%v)", p, err)
	}
	asked := bookmarksAsked(fw)
	if err := a.CheckAndAnchor(); err != nil {
		t.Fatalf("anchoring did not resume under the adopted identity: %v", err)
	}
	heads = recordHeads(t, lg)
	if len(heads) != 2 || heads[1].IdentityID != did || heads[1].PreviousWitnessedLedgerOrdinal != 5 || bookmarksAsked(fw) != asked+1 {
		t.Fatalf("the next pass is not the adopted identity's, answered from its row: %+v", heads)
	}
}

// NOTHING IS ADOPTED ON EVIDENCE THAT DOES NOT HOLD: no envelope (the id
// alone is an unsigned hint), evidence that does not read, an envelope of
// another key, one that does not derive the id the refusal names, no
// receipt, a receipt for another identity, one in a later version's form,
// one that does not verify under the witness key, and a record that names
// another witness identity. Each says why.
func TestNothingIsAdoptedOnEvidenceThatDoesNotHold(t *testing.T) {
	fw := newFakeWitness(t)
	lg, kp := testLedger(t, 5)
	key := AsIdentityKey(kp)
	original, did := enrolled(t, key, time.Now().Add(-time.Hour).UTC().Truncate(time.Second))
	receipt := fw.signReceipt(t, WitnessRequest{IdentityID: did, LedgerOrdinal: 5, LedgerHash: strings.Repeat("5", 64)}, -1, ZeroLedgerHash)
	_, otherKp := testLedger(t, 0)
	other, otherDID := enrolled(t, AsIdentityKey(otherKp), time.Now().UTC().Truncate(time.Second))
	good := func() *ConflictError {
		r := receipt
		return &ConflictError{Code: codeKeyAlreadyEnrolled, WitnessForm: true, Evidence: &RefusalEvidence{
			Receipt: &r, RegisteredIdentityID: did, RegisteredIdentityPublicKey: append([]byte(nil), original...)}}
	}
	store := &memEnvelopeStore{}
	a := NewAnchorer(New(fw.server.URL, ""), lg, key, store, &memReceiptStore{}, nil, 1, "")
	if _, _, err := a.registeredEvidence(good(), fw.witnessEnv); err != nil {
		t.Fatalf("rig: the good evidence does not hold: %v", err)
	}
	for _, c := range []struct {
		name   string
		mutate func(*ConflictError)
		want   string
	}{
		{"no envelope", func(ce *ConflictError) { ce.Evidence.RegisteredIdentityPublicKey = nil }, "unsigned hint"},
		{"no evidence at all", func(ce *ConflictError) { ce.Evidence = nil }, "unsigned hint"},
		{"evidence that does not read", func(ce *ConflictError) { ce.Evidence.Unreadable = "receipt: not JSON" }, "does not read"},
		{"an envelope of another key", func(ce *ConflictError) {
			ce.Evidence.RegisteredIdentityPublicKey, ce.Evidence.RegisteredIdentityID = other, otherDID
		}, "another signing key"},
		{"an id the envelope does not derive", func(ce *ConflictError) { ce.Evidence.RegisteredIdentityID = otherDID }, "derives"},
		{"no receipt", func(ce *ConflictError) { ce.Evidence.Receipt = nil }, "no receipt"},
		{"a receipt for another identity", func(ce *ConflictError) { ce.Evidence.Receipt.IdentityID = otherDID }, "receipt names"},
		{"a receipt in a later version's form", func(ce *ConflictError) { ce.Evidence.Receipt.ContextSHA256 = "sha256:" + strings.Repeat("c", 64) }, "later version's form"},
		{"a receipt that does not verify", func(ce *ConflictError) { ce.Evidence.Receipt.LedgerOrdinal = 6 }, "does not verify"},
	} {
		ce := good()
		c.mutate(ce)
		if _, _, err := a.registeredEvidence(ce, fw.witnessEnv); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("EVIDENCE WITH %s WAS NOT REFUSED FOR IT (want %q): %v", strings.ToUpper(c.name), c.want, err)
		}
	}
	store.enrolledIDs = []string{otherDID}
	if _, _, err := a.registeredEvidence(good(), fw.witnessEnv); err == nil || !strings.Contains(err.Error(), "the record names") {
		t.Errorf("EVIDENCE OVER A RECORD THAT NAMES ANOTHER WITNESS IDENTITY WAS NOT REFUSED FOR IT: %v", err)
	}
	store.enrolledIDs = []string{did}
	if _, _, err := a.registeredEvidence(good(), fw.witnessEnv); err != nil {
		t.Errorf("evidence over a record that names the registered identity was refused: %v", err)
	}
}

// A REFUSAL NOT ADOPTED STANDS FOR THE RUN. A key_already_enrolled that
// carries no registered envelope adopts nothing: the envelope beside the
// ledger is left as it was, nothing is minted or latched, the operator's
// seam is told once why, and no later pass of this run asks the witness
// again, where it would be refused again. A restart asks once more.
func TestARefusalOfASecondEnrolmentNotAdoptedStandsForTheRun(t *testing.T) {
	p := synthPlatform(t)
	fw := newFakeWitness(t)
	fw.manifest = buildManifest(t, p, fw.witnessEnv, false)
	fw.oneEnrolmentPerKey = true
	fw.evidence = func(body map[string]any) { delete(body, "registered_identity_public_key") }
	lgPath, anchorer, original, _ := lostFirstEnrolment(t, fw, p)
	a, store := anchorer(testMinter{})
	var stood []string
	a.SetOnStandingRefusal(func(_ *ConflictError, why string) { stood = append(stood, why) })
	a.SetOnIntegrityConflict(func(ce *ConflictError) { t.Errorf("a refusal of a second enrolment latched a conflict: %v", ce) })
	err := a.CheckAndAnchor()
	var ce *ConflictError
	if !errors.As(err, &ce) || !ce.IsKeyAlreadyEnrolled() {
		t.Fatalf("want the witness's refusal, got: %v", err)
	}
	dir := filepath.Dir(lgPath)
	if file, _ := os.ReadFile(EnvelopePath(dir)); bytes.Equal(file, original) {
		t.Fatal("the registered envelope was adopted on an unsigned hint")
	}
	if len(recordHeads(t, store.lg)) != 0 || len(stood) != 1 || !strings.Contains(stood[0], "unsigned hint") {
		t.Fatalf("heads %d, stood %v", len(recordHeads(t, store.lg)), stood)
	}
	if req, err := readPendingRequest(dir); err != nil || req != nil {
		t.Fatalf("the refused bookmark is kept to be sent again: %+v (%v)", req, err)
	}
	asked := bookmarksAsked(fw)
	if err := a.CheckAndAnchor(); err == nil || bookmarksAsked(fw) != asked || len(stood) != 1 {
		t.Fatalf("a later pass of the run asked the witness again (%d → %d), or told again (%v): %v", asked, bookmarksAsked(fw), stood, err)
	}
	again, _ := anchorer(testMinter{})
	_ = again.CheckAndAnchor()
	if bookmarksAsked(fw) != asked+1 {
		t.Fatalf("a restart did not ask once more: %d → %d", asked, bookmarksAsked(fw))
	}
}

// A WITNESS THAT REQUIRES A CONTEXT PARKS THE PASS FOR THE RUN. A witness
// in its strict mode enrols a new identity only from a request that carries
// a context, which this version does not send: asked again at every pass,
// it would be refused at every pass. The first refusal is not SAFE and
// latches nothing; it retires its bookmark (the witness holds nothing of
// it), tells the operator's seam once, and no later pass of the run asks.
// A restart asks once more.
func TestAWitnessThatRequiresAContextParksThePassForTheRun(t *testing.T) {
	fw := newFakeWitness(t)
	fw.contextRequired = true
	lg, kp := testLedger(t, 5)
	envelopes := &memEnvelopeStore{}
	a := NewAnchorer(New(fw.server.URL, ""), lg, AsIdentityKey(kp), envelopes, &memReceiptStore{}, testMinter{lg, kp}, 1, "")
	var stood []string
	a.SetOnStandingRefusal(func(ce *ConflictError, why string) {
		if !ce.IsContextRequired() {
			t.Errorf("the refusal that stands is not context_required: %v", ce)
		}
		stood = append(stood, why)
	})
	a.SetOnIntegrityConflict(func(ce *ConflictError) { t.Errorf("context_required latched a conflict: %v", ce) })
	a.SetOnUnrecognisedConflict(func(ce *ConflictError) { t.Errorf("context_required was told as a reply not recognised: %v", ce) })
	err := a.CheckAndAnchor()
	var ce *ConflictError
	if !errors.As(err, &ce) || !ce.IsContextRequired() || ce.IsIntegrity() {
		t.Fatalf("want the witness's context_required, never integrity: %v", err)
	}
	if req, rerr := readPendingRequest(filepath.Dir(lg.Path())); rerr != nil || req != nil {
		t.Fatalf("the refused bookmark is kept to be sent again: %+v (%v)", req, rerr)
	}
	if len(stood) != 1 || !strings.Contains(stood[0], "context") || a.IntegrityConflict() != nil {
		t.Fatalf("stood %v, latched %v", stood, a.IntegrityConflict())
	}
	asked := bookmarksAsked(fw)
	for i := 0; i < 2; i++ {
		if err := a.CheckAndAnchor(); err == nil || bookmarksAsked(fw) != asked {
			t.Fatalf("pass %d of the run asked the witness again (%d → %d): %v", i+2, asked, bookmarksAsked(fw), err)
		}
	}
	if len(stood) != 1 {
		t.Fatalf("told %d times, want once: %v", len(stood), stood)
	}
	again := NewAnchorer(New(fw.server.URL, ""), lg, AsIdentityKey(kp), envelopes, &memReceiptStore{}, testMinter{lg, kp}, 1, "")
	_ = again.CheckAndAnchor()
	if bookmarksAsked(fw) != asked+1 {
		t.Fatalf("a restart did not ask once more: %d → %d", asked, bookmarksAsked(fw))
	}
	if len(recordHeads(t, lg)) != 0 {
		t.Fatal("a head was minted")
	}
}

func TestAnAdoptedReceiptThatCannotStandIsTheConflictItIs(t *testing.T) {
	p := synthPlatform(t)
	fw := newFakeWitness(t)
	fw.manifest = buildManifest(t, p, fw.witnessEnv, false)
	fw.oneEnrolmentPerKey = true
	lgPath, anchorer, _, did := lostFirstEnrolment(t, fw, p)
	fw.mu.Lock()
	row := fw.state[did]
	row.ordinal, row.hash = 9, strings.Repeat("9", 64)
	row.receipt = fw.signReceipt(t, WitnessRequest{IdentityID: did, LedgerOrdinal: 9, LedgerHash: row.hash}, -1, ZeroLedgerHash)
	fw.mu.Unlock()
	a, store := anchorer(testMinter{})
	var latched []*ConflictError
	a.SetOnIntegrityConflict(func(ce *ConflictError) { latched = append(latched, ce) })
	err := a.CheckAndAnchor()
	var ce *ConflictError
	if !errors.As(err, &ce) || !ce.Local || ce.WitnessSide || len(latched) != 1 || a.IntegrityConflict() == nil {
		t.Fatalf("a receipt past the record's end stood or was not the fork it is: %v (latched %v)", err, latched)
	}
	dir := filepath.Dir(lgPath)
	if len(recordHeads(t, store.lg)) != 0 {
		t.Fatal("a receipt that cannot stand was minted")
	}
	if r, err := readPendingReceipt(dir); err != nil || r != nil {
		t.Fatalf("a receipt that cannot stand was kept beside the ledger: %+v (%v)", r, err)
	}
}
