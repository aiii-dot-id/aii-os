package witness

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/crypto"
	"github.com/aiii-dot-id/aii-os/internal/ledger"
)

func notMinted(t *testing.T, a *Anchorer, lg *ledger.Ledger, kp *crypto.KeyPair, platform *PublicKeyEnvelope, last uint64, heads int, err error, witnessSide bool, why string) {
	t.Helper()
	dir := filepath.Dir(lg.Path())
	if lg.LastSeq() != last {
		hv, lerr := LoadHeadVerifier(dir, platform)
		if lerr != nil {
			t.Fatal(lerr)
		}
		_, verr := ledger.VerifyChain(lg.Path(), kp.PublicKeyBytes(), hv)
		t.Fatalf("A HEAD WAS MINTED FROM A RECEIPT THAT CANNOT STAND: the record ends at %d, want %d (the pass said: %v); the next boot says: %v", lg.LastSeq(), last, err, verr)
	}
	var ce *ConflictError
	if !errors.As(err, &ce) || !ce.Local || ce.IsCadence() || !strings.Contains(err.Error(), why) {
		t.Fatalf("want a local conflict that says %q, got: %v", why, err)
	}
	if ce.WitnessSide != witnessSide {
		t.Fatalf("the refusal speaks about the wrong side (the witness's row: %v, want %v): %v", ce.WitnessSide, witnessSide, err)
	}
	if a.IntegrityConflict() == nil {
		t.Fatal("the conflict was not latched: the next pass would ask the witness again")
	}
	if n := len(recordHeads(t, lg)); n != heads {
		t.Fatalf("the record carries %d heads, want %d", n, heads)
	}
	if p, perr := readPendingReceipt(dir); perr != nil || p != nil {
		t.Fatalf("a receipt that cannot stand was kept beside the ledger: %+v (%v)", p, perr)
	}
	// Nor its bookmark: sent again first by the next process, it would
	// bring the same receipt back.
	if p, perr := readPendingRequest(dir); perr != nil || p != nil {
		t.Fatalf("the bookmark whose answer cannot stand is kept beside the ledger to be sent again: %+v (%v)", p, perr)
	}
	bootVerifies(t, lg, kp, platform)
}

// The record carries a head and the witness holds no row for the identity
// (its table lost, rolled back or reset, or another witness behind the
// same address): it answers "first", with a receipt that continues from
// nothing.
func TestAFirstAnswerOverARecordThatCarriesAHeadIsNotMinted(t *testing.T) {
	p := synthPlatform(t)
	fw := newFakeWitness(t)
	fw.manifest = buildManifest(t, p, fw.witnessEnv, false)
	lg, kp := testLedger(t, 5)
	envelopes, env := &memEnvelopeStore{}, p.writeEnv(t)
	a := NewAnchorer(New(fw.server.URL, ""), lg, AsIdentityKey(kp), envelopes, &memReceiptStore{}, testMinter{lg, kp}, 1, env)
	a.SetSealer(lg)
	if err := a.CheckAndAnchor(); err != nil {
		t.Fatalf("the first anchor: %v", err)
	}
	if lg.LastSeq() != 6 || lg.SealedSeq() != 6 || len(recordHeads(t, lg)) != 1 {
		t.Fatalf("fixture: want five records and their head, sealed; last %d, sealed %d", lg.LastSeq(), lg.SealedSeq())
	}

	fw.mu.Lock()
	fw.state = map[string]*anchorState{}
	fw.mu.Unlock()
	appendRecords(t, lg, kp, 2)

	err := a.CheckAndAnchor()
	notMinted(t, a, lg, kp, p.env, 8, 1, err, true, "does not continue this record")
	if !strings.Contains(err.Error(), "first=true") {
		t.Fatalf("the refusal does not say the witness answered first: %v", err)
	}
	if lg.SealedSeq() != 6 {
		t.Fatalf("a segment closed under a receipt that cannot stand: sealed through %d", lg.SealedSeq())
	}

	// A restart clears the latch and nothing else. The witness holds the
	// row it made for its "first" answer, which names a record this record
	// holds and carries no head for, so the next pass is answered from
	// that row and refused the same way: the witness's row, not a fork.
	// The record is still one the next boot accepts. What ends this is on
	// the witness's side.
	appendRecords(t, lg, kp, 1)
	again := NewAnchorer(New(fw.server.URL, ""), lg, AsIdentityKey(kp), envelopes, &recordStore{lg: lg}, testMinter{lg, kp}, 1, env)
	again.SetSealer(lg)
	err = again.CheckAndAnchor()
	notMinted(t, again, lg, kp, p.env, 9, 1, err, true, "does not continue this record")
	if lg.SealedSeq() != 6 {
		t.Fatalf("a segment closed after the restart: sealed through %d", lg.SealedSeq())
	}
}

// The record carries no head and the witness holds a row for the same
// identity: it answers with a receipt chained from a record this record
// never attested.
func TestAChainedAnswerOverARecordThatCarriesNoHeadIsNotMinted(t *testing.T) {
	p := synthPlatform(t)
	fw := newFakeWitness(t)
	fw.manifest = buildManifest(t, p, fw.witnessEnv, false)
	lg, kp := testLedger(t, 5)
	envelopes := &memEnvelopeStore{}
	env := p.writeEnv(t)
	// The witness comes to hold a row the record does not carry: an
	// anchorer that mints nothing bookmarks record 5.
	bare := NewAnchorer(New(fw.server.URL, ""), lg, AsIdentityKey(kp), envelopes, &memReceiptStore{}, nil, 1, env)
	if err := bare.CheckAndAnchor(); err != nil {
		t.Fatalf("the bookmark that mints nothing: %v", err)
	}
	if lg.LastSeq() != 5 || len(recordHeads(t, lg)) != 0 {
		t.Fatalf("fixture: the record must carry no head; last %d", lg.LastSeq())
	}
	appendRecords(t, lg, kp, 2)

	a := NewAnchorer(New(fw.server.URL, ""), lg, AsIdentityKey(kp), envelopes, &memReceiptStore{}, testMinter{lg, kp}, 1, env)
	a.SetSealer(lg)
	err := a.CheckAndAnchor()
	notMinted(t, a, lg, kp, p.env, 7, 0, err, true, "does not continue this record")
	if lg.SealedSeq() != 0 {
		t.Fatalf("a segment closed under a receipt that cannot stand: sealed through %d", lg.SealedSeq())
	}
}

func TestAWitnessRowThatContradictsTheRecordIsStillAFork(t *testing.T) {
	const notOurs = "sha256:" + "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	t.Run("another hash for the record the last head attests", func(t *testing.T) {
		p := synthPlatform(t)
		fw := newFakeWitness(t)
		fw.manifest = buildManifest(t, p, fw.witnessEnv, false)
		lg, kp := testLedger(t, 5)
		a := NewAnchorer(New(fw.server.URL, ""), lg, AsIdentityKey(kp), &memEnvelopeStore{}, &memReceiptStore{}, testMinter{lg, kp}, 1, p.writeEnv(t))
		a.SetSealer(lg)
		if err := a.CheckAndAnchor(); err != nil {
			t.Fatalf("the first anchor: %v", err)
		}
		fw.mu.Lock()
		fw.state[onlyKey(fw)].hash = notOurs
		fw.mu.Unlock()
		appendRecords(t, lg, kp, 2)
		err := a.CheckAndAnchor()
		notMinted(t, a, lg, kp, p.env, 8, 1, err, false, "witness state fork")
	})
	t.Run("another hash for a record this record holds, and no head here", func(t *testing.T) {
		p := synthPlatform(t)
		fw := newFakeWitness(t)
		fw.manifest = buildManifest(t, p, fw.witnessEnv, false)
		lg, kp := testLedger(t, 5)
		envelopes, env := &memEnvelopeStore{}, p.writeEnv(t)
		bare := NewAnchorer(New(fw.server.URL, ""), lg, AsIdentityKey(kp), envelopes, &memReceiptStore{}, nil, 1, env)
		if err := bare.CheckAndAnchor(); err != nil {
			t.Fatalf("the bookmark that mints nothing: %v", err)
		}
		fw.mu.Lock()
		fw.state[onlyKey(fw)].hash = notOurs
		fw.mu.Unlock()
		appendRecords(t, lg, kp, 2)
		a := NewAnchorer(New(fw.server.URL, ""), lg, AsIdentityKey(kp), envelopes, &memReceiptStore{}, testMinter{lg, kp}, 1, env)
		err := a.CheckAndAnchor()
		notMinted(t, a, lg, kp, p.env, 7, 0, err, false, "witness state fork")
	})
	// The witness's side alone means where the receipt continues from is
	// all that is wrong with it. One that continues from nothing and is
	// wrong in what it attests as well is not that.
	t.Run("a receipt wrong in more than where it continues from", func(t *testing.T) {
		p := synthPlatform(t)
		fw := newFakeWitness(t)
		lg, kp := testLedger(t, 5)
		dir := filepath.Dir(lg.Path())
		a := NewAnchorer(New(fw.server.URL, ""), lg, AsIdentityKey(kp), &memEnvelopeStore{}, &memReceiptStore{}, testMinter{lg, kp}, 1, p.writeEnv(t))
		end := lg.Last()
		r := WitnessReceipt{IdentityID: "did:aiii:identity:sha256:fixture", PreviousWitnessedLedgerOrdinal: -1, PreviousWitnessedLedgerHash: ZeroLedgerHash,
			LedgerOrdinal: int64(end.Seq), LedgerHash: end.Hash}
		if !a.witnessSideOnly(dir, nil, r) {
			t.Fatal("fixture: a receipt that continues from nothing and attests the record's end is the witness's side alone")
		}
		r.LedgerHash = notOurs
		if a.witnessSideOnly(dir, nil, r) {
			t.Fatal("a receipt that attests a hash this record does not have was called the witness's side alone")
		}
	})
}

// A rule that does not load is found before the witness is asked: nothing
// leaves for it, so no receipt exists that would have to be kept, and
// nothing is latched, since nothing has been said about any receipt.
func TestAHeadRuleThatDoesNotLoadRefusesThePassBeforeTheWitnessIsAsked(t *testing.T) {
	p := synthPlatform(t)
	fw := newFakeWitness(t)
	fw.manifest = buildManifest(t, p, fw.witnessEnv, false)
	lg, kp := testLedger(t, 5)
	dir := filepath.Dir(lg.Path())
	// A file among the keys beside the ledger that is not a key: boot
	// refuses the directory, and so does the rule a pass loads.
	if err := os.MkdirAll(WitnessKeysDir(dir), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(WitnessKeysDir(dir), "not-a-key.json"), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadHeadVerifier(dir, p.env); err == nil {
		t.Fatal("fixture: the keys beside the ledger load, so the rule does")
	}
	a := NewAnchorer(New(fw.server.URL, ""), lg, AsIdentityKey(kp), &memEnvelopeStore{}, &memReceiptStore{}, testMinter{lg, kp}, 1, p.writeEnv(t))
	err := a.CheckAndAnchor()
	if err == nil || !strings.Contains(err.Error(), "head rule beside the ledger") {
		t.Fatalf("want the pass refused for the rule, got: %v", err)
	}
	fw.mu.Lock()
	asked := len(fw.sawIdentityIDs)
	fw.mu.Unlock()
	if asked != 0 {
		t.Fatal("the witness was sent a bookmark by a pass that could not have held its answer to the rule")
	}
	if a.IntegrityConflict() != nil {
		t.Fatalf("a rule that did not load was latched as a conflict: %v", a.IntegrityConflict())
	}
	if lg.LastSeq() != 5 {
		t.Fatalf("the record moved: last %d", lg.LastSeq())
	}
}

// The record a late receipt attests does not read back when the witness
// answers: the receipt may yet stand, and the witness holds it now. It is
// kept beside the ledger and not minted, nothing is latched, and the next
// pass settles it under the same rule.
func TestALateReceiptWhoseRecordDoesNotReadBackIsKeptAndSettledLater(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fixture moves the ledger's file aside while it is open")
	}
	p := synthPlatform(t)
	fw := newFakeWitness(t)
	fw.manifest = buildManifest(t, p, fw.witnessEnv, false)
	lg, kp := testLedger(t, 5)
	dir := filepath.Dir(lg.Path())
	a := NewAnchorer(New(fw.server.URL, ""), lg, AsIdentityKey(kp), &memEnvelopeStore{}, &memReceiptStore{}, testMinter{lg, kp}, 1, p.writeEnv(t))

	// A record lands while the witness answers, so the head would be late
	// and the record it attests has to be read back; and the ledger's file
	// is not where the reader looks.
	away := lg.Path() + ".away"
	during := make(chan error, 2)
	fw.slowly(func() {
		_, err := lg.Append(ledger.EventExperienceCreate, 3, map[string]string{"n": "while the witness answers"}, kp)
		during <- err
		during <- os.Rename(lg.Path(), away)
	})
	err := a.CheckAndAnchor()
	fw.slowly(nil)
	for i := 0; i < 2; i++ {
		if derr := <-during; derr != nil {
			t.Fatalf("fixture: %v", derr)
		}
	}
	if rerr := os.Rename(away, lg.Path()); rerr != nil {
		t.Fatal(rerr)
	}
	if err == nil || !strings.Contains(err.Error(), "does not read back") || !strings.Contains(err.Error(), "kept beside the ledger") {
		t.Fatalf("want the receipt kept and not minted, got: %v", err)
	}
	var ce *ConflictError
	if errors.As(err, &ce) || a.IntegrityConflict() != nil {
		t.Fatalf("a record that did not read back was called a conflict: %v", err)
	}
	if lg.LastSeq() != 6 || len(recordHeads(t, lg)) != 0 {
		t.Fatalf("a head was minted without the rule: last %d", lg.LastSeq())
	}
	kept, perr := readPendingReceipt(dir)
	if perr != nil || kept == nil || kept.Receipt.LedgerOrdinal != 5 {
		t.Fatalf("the receipt for record 5 is not beside the ledger: %+v (%v)", kept, perr)
	}

	if err := a.SettlePending(); err != nil {
		t.Fatalf("the next pass's settle: %v", err)
	}
	heads := recordHeads(t, lg)
	if lg.LastSeq() != 7 || len(heads) != 1 || heads[0].LedgerOrdinal != 5 {
		t.Fatalf("want one late head at record 7 attesting record 5; last %d, heads %+v", lg.LastSeq(), heads)
	}
	if kept, perr := readPendingReceipt(dir); perr != nil || kept != nil {
		t.Fatalf("the settled receipt is still beside the ledger: %+v (%v)", kept, perr)
	}
	if hv := bootVerifies(t, lg, kp, p.env); hv.Verified() != 1 {
		t.Fatalf("the late head was not verified under the persisted key: verified %d", hv.Verified())
	}
}
