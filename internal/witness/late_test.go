package witness

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/crypto"
	"github.com/aiii-dot-id/aii-os/internal/ledger"
)

// bootVerifies holds the record to what boot holds it to: the chain, under
// the witness keys persisted beside it, verified under the platform root.
func bootVerifies(t *testing.T, lg *ledger.Ledger, kp *crypto.KeyPair, platform *PublicKeyEnvelope) *HeadVerifier {
	t.Helper()
	hv, err := LoadHeadVerifier(filepath.Dir(lg.Path()), platform)
	if err != nil {
		t.Fatal(err)
	}
	if n, err := ledger.VerifyChain(lg.Path(), kp.PublicKeyBytes(), hv); err != nil || uint64(n) != lg.LastSeq() {
		t.Fatalf("THE RECORD DOES NOT VERIFY AS BOOT VERIFIES IT: n=%d of %d, err=%v", n, lg.LastSeq(), err)
	}
	return hv
}

// recordAt is the record at seq as the reader hands it on: from its
// segment, or from the tail.
func recordAt(t *testing.T, path string, seq uint64) ledger.Event {
	t.Helper()
	var found *ledger.Event
	if err := ledger.Stream(path, func(evt *ledger.Event) error {
		if evt.Seq == seq {
			e := *evt
			found = &e
			return ledger.ErrStop
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if found == nil {
		t.Fatalf("no record %d", seq)
	}
	return *found
}

func TestARecordAppendedWhileTheWitnessAnswersMakesALateHeadThatVerifies(t *testing.T) {
	p := synthPlatform(t)
	fw := newFakeWitness(t)
	fw.manifest = buildManifest(t, p, fw.witnessEnv, false)
	lg, kp := testLedger(t, 5)
	dir := filepath.Dir(lg.Path())
	a := NewAnchorer(New(fw.server.URL, ""), lg, AsIdentityKey(kp), &memEnvelopeStore{}, &memReceiptStore{}, testMinter{lg, kp}, 1, p.writeEnv(t))
	a.SetSealer(lg)

	// A slow witness: two records land while it answers the bookmark the
	// anchorer made at record 5.
	landed := make(chan error, 2)
	fw.slowly(func() {
		for i := 0; i < 2; i++ {
			_, err := lg.Append(ledger.EventExperienceCreate, 3, map[string]string{"n": "while the witness answers"}, kp)
			landed <- err
		}
	})
	if err := a.CheckAndAnchor(); err != nil {
		t.Fatalf("anchor: %v", err)
	}
	fw.slowly(nil)
	for i := 0; i < 2; i++ {
		if err := <-landed; err != nil {
			t.Fatal(err)
		}
	}
	heads := recordHeads(t, lg)
	if lg.LastSeq() != 8 || len(heads) != 1 || heads[0].LedgerOrdinal != 5 {
		t.Fatalf("fixture: want one head, record 8, attesting record 5; last %d, heads %+v", lg.LastSeq(), heads)
	}
	hv := bootVerifies(t, lg, kp, p.env)
	if seq, _, ok := hv.Attested(); hv.Verified() != 1 || !ok || seq != 5 {
		t.Fatalf("the late head was not verified under the persisted key: verified %d, attested %d (%v)", hv.Verified(), seq, ok)
	}
	if lg.SealedSeq() != 0 {
		t.Fatalf("A SEGMENT CLOSED UNDER A LATE HEAD: sealed through %d", lg.SealedSeq())
	}
	if a.LastAnchoredSeq() != 5 {
		t.Fatalf("anchored at %d, want the record the receipt attests (5)", a.LastAnchoredSeq())
	}

	// The next head is on time and seals over the late one.
	appendRecords(t, lg, kp, 1)
	if err := a.CheckAndAnchor(); err != nil {
		t.Fatalf("the next anchor: %v", err)
	}
	if lg.LastSeq() != 10 || lg.SealedSeq() != 10 {
		t.Fatalf("the head on time did not seal through itself: last %d, sealed %d", lg.LastSeq(), lg.SealedSeq())
	}
	if _, err := os.Stat(filepath.Join(dir, "segment-1-10.jsonl.gz")); err != nil {
		t.Fatalf("no segment through the head on time: %v", err)
	}
	if hv := bootVerifies(t, lg, kp, p.env); hv.Verified() != 2 {
		t.Fatalf("verified %d heads, want 2", hv.Verified())
	}
	if late := recordAt(t, lg.Path(), 8); !late.Sealed() || late.Closing() || late.Sig != "" {
		t.Fatalf("the late head is not an interior record of the segment: sealed %v, closing %v, proof kept %v", late.Sealed(), late.Closing(), late.Sig != "")
	}
}

// lateWitness is a witness whose key the platform vouched for, and the
// key as boot loads it: one per test, shared by its cases.
type lateWitness struct {
	fw   *fakeWitness
	keys map[string]*WitnessKeyMaterial
}

func newLateWitness(t *testing.T) lateWitness {
	t.Helper()
	f := persistedFixture(t)
	keys, err := LoadWitnessKeys(f.dir, f.p.env)
	if err != nil {
		t.Fatal(err)
	}
	return lateWitness{fw: f.fw, keys: keys}
}

// lateRig is a ledger and that witness, with heads minted by hand where a
// test puts them.
type lateRig struct {
	lg   *ledger.Ledger
	kp   *crypto.KeyPair
	fw   *fakeWitness
	keys map[string]*WitnessKeyMaterial
}

const lateIdentity = "did:aiii:identity:sha256:late"

func (w lateWitness) rig(t *testing.T, records int) lateRig {
	t.Helper()
	lg, kp := testLedger(t, records)
	return lateRig{lg: lg, kp: kp, fw: w.fw, keys: w.keys}
}

// head mints, at the record's end, a head whose receipt the witness signed
// for (ordinal, hash), continuing from (prevOrdinal, prevHash).
func (r lateRig) head(t *testing.T, ordinal int64, hash string, prevOrdinal int64, prevHash string) *ledger.Event {
	t.Helper()
	receipt := r.fw.signReceipt(t, WitnessRequest{IdentityID: lateIdentity, LedgerOrdinal: ordinal, LedgerHash: hash}, prevOrdinal, prevHash)
	evt, err := testMinter{r.lg, r.kp}.MintWitnessed(receipt, r.fw.witnessEnv.KeyID)
	if err != nil {
		t.Fatal(err)
	}
	return evt
}

func (r lateRig) hashOf(t *testing.T, seq uint64) string {
	t.Helper()
	evt := recordAt(t, r.lg.Path(), seq)
	return evt.EntryHash()
}

// returnsAttested keeps what the verifier tells the walk at each head.
type returnsAttested struct {
	*HeadVerifier
	returned []uint64
}

func (r *returnsAttested) VerifyHead(evt *ledger.Event, hashAt func(uint64) (string, bool)) (uint64, error) {
	attested, err := r.HeadVerifier.VerifyHead(evt, hashAt)
	r.returned = append(r.returned, attested)
	return attested, err
}

func (r lateRig) verify() (*HeadVerifier, error) {
	hv := NewHeadVerifier(r.keys)
	_, err := ledger.VerifyChain(r.lg.Path(), r.kp.PublicKeyBytes(), hv)
	return hv, err
}

// A late head may attest a record BEFORE the previous head's own place:
// the previous head was late too, and its receipt attested a record before
// it. What bounds a receipt is the record the previous receipt attested,
// not where the previous head sits. And the head on time after them seals
// over both.
func TestALateHeadMayAttestARecordBeforeThePreviousHead(t *testing.T) {
	r := newLateWitness(t).rig(t, 5)
	h5 := r.lg.LastHash()
	appendRecords(t, r.lg, r.kp, 1) // record 6 lands while the witness answers for 5
	first := r.head(t, 5, h5, -1, ZeroLedgerHash)
	h6 := r.hashOf(t, 6)
	appendRecords(t, r.lg, r.kp, 1) // record 8 lands while it answers for 6
	second := r.head(t, 6, h6, 5, h5)
	if first.Seq != 7 || second.Seq != 9 {
		t.Fatalf("fixture: heads at %d and %d, want 7 and 9", first.Seq, second.Seq)
	}
	hv := &returnsAttested{HeadVerifier: NewHeadVerifier(r.keys)}
	if _, err := ledger.VerifyChain(r.lg.Path(), r.kp.PublicKeyBytes(), hv); err != nil {
		t.Fatalf("A LATE HEAD ATTESTING A RECORD BEFORE THE PREVIOUS HEAD WAS REFUSED: %v", err)
	}
	if seq, hash, ok := hv.Attested(); hv.Verified() != 2 || !ok || seq != 6 || hash != h6 {
		t.Fatalf("verified %d, attested (%d, %s, %v), want 2 heads through record 6", hv.Verified(), seq, hash, ok)
	}
	// What the walk is told to forget through is what each receipt
	// attests: the chain's place, not the head's.
	if want := []uint64{5, 6}; !reflect.DeepEqual(hv.returned, want) {
		t.Fatalf("the verifier told the walk %v, want %v", hv.returned, want)
	}

	onTime := r.head(t, 9, r.lg.LastHash(), 6, h6)
	if err := r.lg.Seal(onTime.Seq); err != nil {
		t.Fatal(err)
	}
	sealed, err := r.verify()
	if err != nil || sealed.Verified() != 3 {
		t.Fatalf("the segment sealed over two late heads: verified %d, err %v", sealed.Verified(), err)
	}
	for _, seq := range []uint64{first.Seq, second.Seq} {
		if evt := recordAt(t, r.lg.Path(), seq); !evt.Sealed() || evt.Closing() || evt.Sig != "" {
			t.Fatalf("late head %d is not an interior record: sealed %v, closing %v, proof kept %v", seq, evt.Sealed(), evt.Closing(), evt.Sig != "")
		}
	}
}

// THE SECURITY PROPERTY. A segment's interior travels without proofs on
// its closing head's receipt; a late head closing one would leave records
// between what its receipt attests and the head itself that no witness saw
// and no proof carries. The anchorer never seals through a late head; a
// segment crafted to close under one is refused. The same head in the
// tail stands: it is closing that it may not do.
func TestASegmentClosedByALateHeadIsRefused(t *testing.T) {
	r := newLateWitness(t).rig(t, 5)
	h5 := r.lg.LastHash()
	appendRecords(t, r.lg, r.kp, 1) // record 6: no witness saw it
	late := r.head(t, 5, h5, -1, ZeroLedgerHash)
	if _, err := r.verify(); err != nil {
		t.Fatalf("the late head in the tail: %v", err)
	}
	if err := r.lg.Seal(late.Seq); err != nil { // what the anchorer never does
		t.Fatal(err)
	}
	if evt := recordAt(t, r.lg.Path(), 6); evt.Sig != "" || !evt.Sealed() {
		t.Fatalf("fixture: record 6 should travel sealed without its proof (proof kept %v)", evt.Sig != "")
	}
	_, err := r.verify()
	var refusal *ledger.VerifyFailure
	if !errors.As(err, &refusal) || refusal.Seq != late.Seq || !strings.Contains(err.Error(), "a late head never closes a segment") {
		t.Fatalf("A SEGMENT CLOSED BY A LATE HEAD WAS NOT REFUSED AS ONE: %v", err)
	}
}

// A late head is held to the record it attests by that record's entry
// hash, and to nothing the walk cannot read back.
func TestALateHeadWhoseRecordIsNotTheOneTheWitnessSawIsRefused(t *testing.T) {
	w := newLateWitness(t)
	t.Run("the hash is not the record's", func(t *testing.T) {
		r := w.rig(t, 5)
		h4, h5 := r.hashOf(t, 4), r.lg.LastHash()
		appendRecords(t, r.lg, r.kp, 1)
		r.head(t, 5, h4, -1, ZeroLedgerHash) // record 5 named by record 4's hash
		_, err := r.verify()
		if err == nil || !strings.Contains(err.Error(), "receipt attests hash "+h4+" for record 5, which is "+h5) {
			t.Fatalf("A LATE RECEIPT FOR A HASH THE RECORD DOES NOT HOLD VERIFIED: %v", err)
		}
	})
	t.Run("it attests the head itself", func(t *testing.T) {
		r := w.rig(t, 5)
		appendRecords(t, r.lg, r.kp, 1)
		r.head(t, 7, r.lg.LastHash(), -1, ZeroLedgerHash) // minted at 7
		if _, err := r.verify(); err == nil || !strings.Contains(err.Error(), "receipt attests ordinal 7, neither the record before the head") {
			t.Fatalf("a receipt attesting its own head verified: %v", err)
		}
	})
}

// The receipt chain only moves on: a late head attesting the record the
// previous head attested, or one before it, is refused, whatever its
// signature says.
func TestALateHeadAtOrBeforeThePreviousAttestedRecordIsRefused(t *testing.T) {
	w := newLateWitness(t)
	for _, c := range []struct {
		name    string
		ordinal uint64
	}{
		{"the record the previous head attested", 5},
		{"a record before it", 4},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := w.rig(t, 5)
			h5 := r.lg.LastHash()
			r.head(t, 5, h5, -1, ZeroLedgerHash) // record 6, on time
			appendRecords(t, r.lg, r.kp, 1)
			r.head(t, int64(c.ordinal), r.hashOf(t, c.ordinal), 5, h5) // record 8
			_, err := r.verify()
			if err == nil || !strings.Contains(err.Error(), "neither the record before the head nor a record after the one the previous head attested (5)") {
				t.Fatalf("A LATE HEAD ATTESTING RECORD %d AFTER A HEAD ATTESTED 5 VERIFIED: %v", c.ordinal, err)
			}
		})
	}
}

func TestAKeptReceiptIsMintedLateAfterTheRecordMovedOn(t *testing.T) {
	// kept leaves a three-record ledger whose bookmark the witness
	// answered and whose mint did not land, and two records the identity
	// appended after that.
	kept := func(t *testing.T) (*fakeWitness, *platformKeys, string, *ledger.Ledger, *crypto.KeyPair) {
		t.Helper()
		p := synthPlatform(t)
		fw := newFakeWitness(t)
		fw.manifest = buildManifest(t, p, fw.witnessEnv, false)
		env := p.writeEnv(t)
		lg, kp := testLedger(t, 3)
		db := &recordStore{lg: lg, born: time.Now().Add(-time.Minute)}
		if err := NewAnchorer(New(fw.server.URL, ""), lg, AsIdentityKey(kp), db, db, failingMinter{}, 1, env).CheckAndAnchor(); err == nil {
			t.Fatal("fixture: the mint landed")
		}
		appendRecords(t, lg, kp, 2)
		return fw, p, env, lg, kp
	}

	t.Run("at the next pass", func(t *testing.T) {
		fw, p, env, lg, kp := kept(t)
		db := &recordStore{lg: lg, born: time.Now().Add(-time.Minute)}
		a := NewAnchorer(New(fw.server.URL, ""), lg, AsIdentityKey(kp), db, db, testMinter{lg, kp}, 1, env)
		a.SetSealer(lg)
		if err := a.CheckAndAnchor(); err != nil {
			t.Fatalf("THE KEPT RECEIPT IS STILL A DEAD END: %v", err)
		}
		// Kept receipt: late, record 6 attesting 3. The pass's own: on
		// time, record 7 attesting 6, continuing from 3 — and sealing.
		heads := recordHeads(t, lg)
		if len(heads) != 2 || heads[0].LedgerOrdinal != 3 || heads[1].LedgerOrdinal != 6 || heads[1].PreviousWitnessedLedgerOrdinal != 3 {
			t.Fatalf("heads %+v, want the kept receipt (3) and the pass's own (6, from 3)", heads)
		}
		if lg.LastSeq() != 7 || lg.SealedSeq() != 7 || pendingOnDisk(t, filepath.Dir(lg.Path())) {
			t.Fatalf("last %d, sealed %d, kept copy present %v", lg.LastSeq(), lg.SealedSeq(), pendingOnDisk(t, filepath.Dir(lg.Path())))
		}
		if hv := bootVerifies(t, lg, kp, p.env); hv.Verified() != 2 {
			t.Fatalf("verified %d heads, want 2", hv.Verified())
		}
		if ids, _ := witnessHolds(fw); ids != 1 {
			t.Fatalf("the witness holds %d identities", ids)
		}
	})

	t.Run("at boot", func(t *testing.T) {
		fw, p, env, lg, kp := kept(t)
		path := lg.Path()
		if err := lg.Close(); err != nil {
			t.Fatal(err)
		}
		lg2, err := ledger.New(path)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { lg2.Close() })
		db := &recordStore{lg: lg2, born: time.Now().Add(-time.Minute)}
		boot := NewAnchorer(New(fw.server.URL, ""), lg2, AsIdentityKey(kp), db, db, testMinter{lg2, kp}, 1, env)
		boot.SetSealer(lg2)
		if err := boot.SettlePending(); err != nil {
			t.Fatalf("BOOT REFUSED THE KEPT RECEIPT: %v", err)
		}
		if heads := recordHeads(t, lg2); lg2.LastSeq() != 6 || len(heads) != 1 || heads[0].LedgerOrdinal != 3 {
			t.Fatalf("boot did not mint the kept receipt late: last %d, heads %+v", lg2.LastSeq(), heads)
		}
		if boot.LastAnchoredSeq() != 3 || pendingOnDisk(t, filepath.Dir(path)) || lg2.SealedSeq() != 0 {
			t.Fatalf("anchored %d, kept copy present %v, sealed %d", boot.LastAnchoredSeq(), pendingOnDisk(t, filepath.Dir(path)), lg2.SealedSeq())
		}
		bootVerifies(t, lg2, kp, p.env)

		appendRecords(t, lg2, kp, 1)
		if err := boot.CheckAndAnchor(); err != nil {
			t.Fatalf("the next anchor: %v", err)
		}
		if lg2.LastSeq() != 8 || lg2.SealedSeq() != 8 {
			t.Fatalf("the head on time did not seal over the late one: last %d, sealed %d", lg2.LastSeq(), lg2.SealedSeq())
		}
		if hv := bootVerifies(t, lg2, kp, p.env); hv.Verified() != 2 {
			t.Fatalf("verified %d heads, want 2", hv.Verified())
		}
	})
}

// tornTip is the ledger with one record landing at the anchorer's read of
// its end: before the hash is read when the read is two (LastSeq, then
// LastHash: the gap two locks leave), right after it when it is one.
type tornTip struct {
	*ledger.Ledger
	kp    *crypto.KeyPair
	armed bool
	err   error
}

func (l *tornTip) land() {
	if l.armed {
		l.armed = false
		_, l.err = l.Ledger.Append(ledger.EventExperienceCreate, 3, map[string]string{"n": "at the read"}, l.kp)
	}
}

func (l *tornTip) LastHash() string { l.land(); return l.Ledger.LastHash() }

func (l *tornTip) Last() ledger.Boundary {
	end := l.Ledger.Last()
	l.land()
	return end
}

// THE BOOKMARK NAMES ONE RECORD. The anchorer read the record's end as two
// values under two locks, and an append between them paired record N's
// ordinal with record N+1's hash: the witness signed what it was asked,
// the head was minted late, and its receipt, held to record N's hash, was
// refused — the next boot SAFE. The settle read the same way and refused a
// kept receipt that stood. Read together, the end names a record of the
// chain, and a record landing after the read makes a late head that
// verifies.
func TestTheBookmarkNamesOneRecordWhileRecordsLand(t *testing.T) {
	t.Run("a fresh anchor", func(t *testing.T) {
		p := synthPlatform(t)
		fw := newFakeWitness(t)
		fw.manifest = buildManifest(t, p, fw.witnessEnv, false)
		lg, kp := testLedger(t, 5)
		tip := &tornTip{Ledger: lg, kp: kp, armed: true}
		a := NewAnchorer(New(fw.server.URL, ""), tip, AsIdentityKey(kp), &memEnvelopeStore{}, &memReceiptStore{}, testMinter{lg, kp}, 1, p.writeEnv(t))
		a.SetSealer(lg)
		if err := a.CheckAndAnchor(); err != nil {
			t.Fatalf("anchor: %v", err)
		}
		if tip.armed || tip.err != nil {
			t.Fatalf("fixture: no record landed at the read (%v)", tip.err)
		}
		if heads := recordHeads(t, lg); len(heads) != 1 || heads[0].LedgerOrdinal != 5 || lg.LastSeq() != 7 {
			t.Fatalf("fixture: want one head at 7 for record 5; last %d, heads %+v", lg.LastSeq(), heads)
		}
		bootVerifies(t, lg, kp, p.env)
	})
	t.Run("a kept receipt", func(t *testing.T) {
		fw := newFakeWitness(t)
		lg, kp := testLedger(t, 3)
		db := &recordStore{lg: lg, born: time.Now().Add(-time.Minute)}
		if err := NewAnchorer(New(fw.server.URL, ""), lg, AsIdentityKey(kp), db, db, failingMinter{}, 1, "").CheckAndAnchor(); err == nil {
			t.Fatal("fixture: the mint landed")
		}
		tip := &tornTip{Ledger: lg, kp: kp, armed: true}
		boot := NewAnchorer(New(fw.server.URL, ""), tip, AsIdentityKey(kp), db, db, testMinter{lg, kp}, 1, "")
		if err := boot.SettlePending(); err != nil {
			t.Fatalf("A RECORD LANDING AT THE SETTLE'S READ REFUSED A KEPT RECEIPT THAT STANDS: %v", err)
		}
		if tip.armed || tip.err != nil {
			t.Fatalf("fixture: no record landed at the read (%v)", tip.err)
		}
		if heads := recordHeads(t, lg); len(heads) != 1 || heads[0].LedgerOrdinal != 3 || lg.LastSeq() != 5 {
			t.Fatalf("want the kept receipt minted at 5; last %d, heads %+v", lg.LastSeq(), heads)
		}
		verifiesAsBootDoes(t, lg, kp)
	})
}
