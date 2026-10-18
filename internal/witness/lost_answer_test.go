package witness

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// losingFront stands in front of a witness and loses the answer to the
// first bookmark: the witness takes the request and commits it, and the
// client is answered 502, as when a proxy fails or a machine sleeps on the
// way back. Every bookmark that reaches the witness is kept, in order.
//
// signs, when set, answers the bookmarks in the witness's place while the
// witness goes on serving its key: the witness under a key rotated since
// the one it serves was fetched.
type losingFront struct {
	mu        sync.Mutex
	witness   http.Handler
	signs     http.Handler
	lose      int
	bookmarks []WitnessRequest
}

// answers puts witness at the address, with signs (or witness itself, when
// nil) answering its bookmarks.
func (f *losingFront) answers(witness, signs http.Handler) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.witness, f.signs = witness, signs
}

func (f *losingFront) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	witness, signs := f.witness, f.signs
	f.mu.Unlock()
	if r.URL.Path != "/witness/bookmark" {
		witness.ServeHTTP(w, r)
		return
	}
	if signs == nil {
		signs = witness
	}
	body, _ := io.ReadAll(r.Body)
	var req WitnessRequest
	_ = json.Unmarshal(body, &req)
	r.Body = io.NopCloser(bytes.NewReader(body))
	rec := httptest.NewRecorder()
	signs.ServeHTTP(rec, r)
	f.mu.Lock()
	f.bookmarks = append(f.bookmarks, req)
	lose := f.lose > 0
	if lose {
		f.lose--
	}
	f.mu.Unlock()
	if lose {
		http.Error(w, "bad gateway", http.StatusBadGateway)
		return
	}
	for k, v := range rec.Header() {
		w.Header()[k] = v
	}
	w.WriteHeader(rec.Code)
	_, _ = w.Write(rec.Body.Bytes())
}

func (f *losingFront) sent() []int64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]int64, len(f.bookmarks))
	for i, b := range f.bookmarks {
		out[i] = b.LedgerOrdinal
	}
	return out
}

// A BOOKMARK WHOSE ANSWER WAS LOST IS SENT AGAIN FIRST. The witness
// committed record 5 and the reply never arrived. The next pass used to
// bookmark the record's new end: the receipt for that chained from record
// 5, the record carried no head there, the head rule refused it, and
// anchoring and sealing were off for good, again after every restart. Now
// the bookmark for record 5 is kept beside the ledger before it leaves and
// sent again first: the witness answers it with the receipt it already
// made, which becomes the record's next head, late, and the pass after
// that bookmarks the record's end as ever.
func TestABookmarkWhoseAnswerWasLostIsSentAgainFirst(t *testing.T) {
	p := synthPlatform(t)
	fw := newFakeWitness(t)
	fw.manifest = buildManifest(t, p, fw.witnessEnv, false)
	front := &losingFront{witness: fw.server.Config.Handler, lose: 1}
	srv := httptest.NewServer(front)
	t.Cleanup(srv.Close)

	lg, kp := testLedger(t, 5)
	dir := filepath.Dir(lg.Path())
	a := NewAnchorer(New(srv.URL, ""), lg, AsIdentityKey(kp), &memEnvelopeStore{}, &memReceiptStore{}, testMinter{lg, kp}, 1, p.writeEnv(t))
	a.SetSealer(lg)
	var alarms []*ConflictError
	a.SetOnIntegrityConflict(func(ce *ConflictError) { alarms = append(alarms, ce) })

	// The answer is lost. Nothing is anchored, and the bookmark is kept.
	if err := a.CheckAndAnchor(); err == nil {
		t.Fatal("rig: the first bookmark's answer was to be lost")
	}
	kept, err := readPendingRequest(dir)
	if err != nil || kept == nil || kept.LedgerOrdinal != 5 || a.LastAnchoredSeq() != 0 {
		t.Fatalf("THE BOOKMARK THAT LEFT IS NOT KEPT BESIDE THE LEDGER: %+v (%v), anchored at %d", kept, err, a.LastAnchoredSeq())
	}

	// The record moves on, as it does while a laptop sleeps.
	appendRecords(t, lg, kp, 3)

	// The next pass sends the bookmark for record 5 again, and the answer
	// the witness already made becomes the record's next head, late.
	if err := a.CheckAndAnchor(); err != nil {
		t.Fatalf("the pass that sends the kept bookmark again: %v", err)
	}
	if sent := front.sent(); len(sent) != 2 || sent[0] != 5 || sent[1] != 5 {
		t.Fatalf("THE NEXT PASS DID NOT SEND THE SAME BOOKMARK AGAIN: the witness was sent records %v", sent)
	}
	heads := recordHeads(t, lg)
	if len(heads) != 1 || heads[0].LedgerOrdinal != 5 || a.LastAnchoredSeq() != 5 {
		t.Fatalf("the recovered receipt is not the record's head: heads %+v, anchored at %d", heads, a.LastAnchoredSeq())
	}
	if a.IntegrityConflict() != nil || len(alarms) != 0 {
		t.Fatalf("A LOST ANSWER TURNED ANCHORING OFF: %v", a.IntegrityConflict())
	}
	if _, err := os.Stat(PendingRequestPath(dir)); !os.IsNotExist(err) {
		t.Fatalf("the bookmark is still kept after its answer is in the record: %v", err)
	}
	if lg.SealedSeq() != 0 {
		t.Fatalf("a segment closed under a late head: sealed through %d", lg.SealedSeq())
	}
	bootVerifies(t, lg, kp, p.env)

	// And then the record's end, on time, sealing over the late head.
	end := lg.LastSeq()
	if err := a.CheckAndAnchor(); err != nil {
		t.Fatalf("the pass after the recovery: %v", err)
	}
	if sent := front.sent(); len(sent) != 3 || sent[2] != int64(end) {
		t.Fatalf("the pass after the recovery did not bookmark the record's end (%d): %v", end, sent)
	}
	if a.LastAnchoredSeq() != end || lg.SealedSeq() != lg.LastSeq() || a.IntegrityConflict() != nil {
		t.Fatalf("anchoring did not go on: anchored at %d of %d, sealed through %d, conflict %v", a.LastAnchoredSeq(), end, lg.SealedSeq(), a.IntegrityConflict())
	}
	if hv := bootVerifies(t, lg, kp, p.env); hv.Verified() != 2 {
		t.Fatalf("verified %d heads, want 2", hv.Verified())
	}
}

// A BOOKMARK THE WITNESS REFUSED BY ITS CADENCE IS NOT KEPT: the witness
// holds nothing of it, and the next pass bookmarks the record's end.
func TestABookmarkTheWitnessPacedOffIsNotKept(t *testing.T) {
	fw := newFakeWitness(t)
	var bookmarks atomic.Int64
	srv := conflictServer(t, fw, conflictMsgCadence, &bookmarks)
	lg, kp := testLedger(t, 3)
	dir := filepath.Dir(lg.Path())
	a := NewAnchorer(New(srv.URL, ""), lg, AsIdentityKey(kp), &memEnvelopeStore{}, &memReceiptStore{}, testMinter{lg, kp}, 3, "")
	if err := a.CheckAndAnchor(); err == nil || bookmarks.Load() != 1 {
		t.Fatalf("rig: the witness was to pace one bookmark off: %v (%d sent)", err, bookmarks.Load())
	}
	if kept, err := readPendingRequest(dir); err != nil || kept != nil {
		t.Fatalf("a bookmark the witness refused by its cadence is kept to be sent again: %+v (%v)", kept, err)
	}
}

// A BOOKMARK THE WITNESS REFUSED AS A ROLLBACK, A FORK OR ANOTHER
// IDENTITY'S IS NOT KEPT EITHER: it refused in its own words and holds
// nothing of it, so the process after a restart asks about the record as
// it then is, as it did before this file existed. A 409 this program does
// not recognise need not be the witness's at all: the witness may hold the
// bookmark, and it stays to be sent again.
func TestAKeptBookmarkOutlivesOnlyARefusalThatIsNotTheWitnessesOwn(t *testing.T) {
	for msg, stays := range map[string]bool{
		conflictMsgRollbackFork:              false,
		conflictMsgIdentityMismatch:          false,
		"the gateway is busy (409 Conflict)": true,
	} {
		fw := newFakeWitness(t)
		var bookmarks atomic.Int64
		srv := conflictServer(t, fw, msg, &bookmarks)
		lg, kp := testLedger(t, 3)
		dir := filepath.Dir(lg.Path())
		a := NewAnchorer(New(srv.URL, ""), lg, AsIdentityKey(kp), &memEnvelopeStore{}, &memReceiptStore{}, testMinter{lg, kp}, 3, "")
		if err := a.CheckAndAnchor(); err == nil || bookmarks.Load() != 1 {
			t.Fatalf("%q: rig: one bookmark was to be refused: %v (%d sent)", msg, err, bookmarks.Load())
		}
		kept, err := readPendingRequest(dir)
		if err != nil || (kept != nil) != stays {
			t.Fatalf("%q: the bookmark kept beside the ledger is %+v (%v); want it kept: %v", msg, kept, err, stays)
		}
		if (a.IntegrityConflict() != nil) == stays {
			t.Fatalf("%q: rig: latched %v", msg, a.IntegrityConflict())
		}
	}
}

func TestAnAnswerToABookmarkSentAgainThatCannotStandIsTheConflictItIsForAnyBookmark(t *testing.T) {
	p := synthPlatform(t)
	fw := newFakeWitness(t)
	fw.manifest = buildManifest(t, p, fw.witnessEnv, false)
	front := &losingFront{witness: fw.server.Config.Handler}
	srv := httptest.NewServer(front)
	t.Cleanup(srv.Close)

	lg, kp := testLedger(t, 5)
	dir := filepath.Dir(lg.Path())
	a := NewAnchorer(New(srv.URL, ""), lg, AsIdentityKey(kp), &memEnvelopeStore{}, &memReceiptStore{}, testMinter{lg, kp}, 1, p.writeEnv(t))
	a.SetSealer(lg)
	var alarms []*ConflictError
	a.SetOnIntegrityConflict(func(ce *ConflictError) { alarms = append(alarms, ce) })
	if err := a.CheckAndAnchor(); err != nil {
		t.Fatalf("the first anchor: %v", err)
	}
	if lg.LastSeq() != 6 || len(recordHeads(t, lg)) != 1 {
		t.Fatalf("fixture: want five records and their head; last %d", lg.LastSeq())
	}

	// The bookmark for record 8 reaches the witness and its answer is lost.
	appendRecords(t, lg, kp, 2)
	front.mu.Lock()
	front.lose = 1
	front.mu.Unlock()
	if err := a.CheckAndAnchor(); err == nil {
		t.Fatal("rig: the answer to the bookmark for record 8 was to be lost")
	}
	if kept, err := readPendingRequest(dir); err != nil || kept == nil || kept.LedgerOrdinal != 8 {
		t.Fatalf("rig: the bookmark for record 8 is not kept: %+v (%v)", kept, err)
	}

	// The witness loses its row, and the record moves on.
	fw.mu.Lock()
	fw.state = map[string]*anchorState{}
	fw.mu.Unlock()
	appendRecords(t, lg, kp, 1)

	err := a.CheckAndAnchor()
	if sent := front.sent(); len(sent) != 3 || sent[2] != 8 {
		t.Fatalf("rig: the kept bookmark was to be sent again: the witness was sent records %v", sent)
	}
	notMinted(t, a, lg, kp, p.env, 9, 1, err, true, "does not continue this record")
	if len(alarms) != 1 || !alarms[0].WitnessSide {
		t.Fatalf("the seam was told %d times, want once, of the witness's row: %+v", len(alarms), alarms)
	}
}

// rotated is the witness fw under a new key the platform vouches for: its
// rows, and the receipts it stored in them under the key it had, are fw's.
func rotated(t *testing.T, p *platformKeys, fw *fakeWitness) *fakeWitness {
	t.Helper()
	next := newFakeWitness(t)
	next.manifest = buildManifest(t, p, next.witnessEnv, false)
	fw.mu.Lock()
	next.state = fw.state
	fw.mu.Unlock()
	return next
}

// A BOOKMARK SENT ONCE WHOSE ANSWER DOES NOT VERIFY IS SENT AGAIN, UNDER
// THE KEY FETCHED THEN. The witness's key rotated between this pass's
// fetch and its signature: the receipt is signed under a key this pass
// did not fetch. The witness holds the bookmark. Kept, it is sent again by
// the next pass, which fetches the key the witness now serves, and the
// receipt the witness stored verifies under it. The record has not moved,
// so the recovered head is on time and seals.
func TestABookmarkWhoseAnswerDidNotVerifyUnderAKeyJustRotatedIsSentAgain(t *testing.T) {
	p := synthPlatform(t)
	fw := newFakeWitness(t)
	fw.manifest = buildManifest(t, p, fw.witnessEnv, false)
	next := rotated(t, p, fw)
	front := &losingFront{witness: fw.server.Config.Handler, signs: next.server.Config.Handler}
	srv := httptest.NewServer(front)
	t.Cleanup(srv.Close)

	lg, kp := testLedger(t, 5)
	dir := filepath.Dir(lg.Path())
	a := NewAnchorer(New(srv.URL, ""), lg, AsIdentityKey(kp), &memEnvelopeStore{}, &memReceiptStore{}, testMinter{lg, kp}, 1, p.writeEnv(t))
	a.SetSealer(lg)

	err := a.CheckAndAnchor()
	if err == nil || !strings.Contains(err.Error(), "receipt verification") {
		t.Fatalf("rig: the receipt was to be signed under a key this pass did not fetch: %v", err)
	}
	if kept, perr := readPendingRequest(dir); perr != nil || kept == nil || kept.LedgerOrdinal != 5 {
		t.Fatalf("the bookmark the witness answered under its new key is not kept: %+v (%v)", kept, perr)
	}

	// The witness serves the key it signs under.
	front.answers(next.server.Config.Handler, nil)
	if err := a.CheckAndAnchor(); err != nil {
		t.Fatalf("the pass that sends the kept bookmark again: %v", err)
	}
	if sent := front.sent(); len(sent) != 2 || sent[1] != 5 {
		t.Fatalf("the kept bookmark was not sent again: the witness was sent records %v", sent)
	}
	if heads := recordHeads(t, lg); len(heads) != 1 || heads[0].LedgerOrdinal != 5 || a.LastAnchoredSeq() != 5 || a.IntegrityConflict() != nil {
		t.Fatalf("the recovered receipt is not the record's head: heads %+v, anchored at %d, conflict %v", heads, a.LastAnchoredSeq(), a.IntegrityConflict())
	}
	if lg.SealedSeq() != lg.LastSeq() {
		t.Fatalf("a recovered head on time did not seal: the record ends at %d, sealed through %d", lg.LastSeq(), lg.SealedSeq())
	}
	if kept, perr := readPendingRequest(dir); perr != nil || kept != nil {
		t.Fatalf("the bookmark is still kept after its answer is in the record: %+v (%v)", kept, perr)
	}
	bootVerifies(t, lg, kp, p.env)
}

func TestABookmarkSentAgainWhoseAnswerDoesNotVerifyIsRetiredAndTheNextPassAsksAboutTheRecordsEnd(t *testing.T) {
	p := synthPlatform(t)
	fw := newFakeWitness(t)
	fw.manifest = buildManifest(t, p, fw.witnessEnv, false)
	front := &losingFront{witness: fw.server.Config.Handler}
	srv := httptest.NewServer(front)
	t.Cleanup(srv.Close)

	lg, kp := testLedger(t, 5)
	dir := filepath.Dir(lg.Path())
	a := NewAnchorer(New(srv.URL, ""), lg, AsIdentityKey(kp), &memEnvelopeStore{}, &memReceiptStore{}, testMinter{lg, kp}, 1, p.writeEnv(t))
	a.SetSealer(lg)
	var alarms []*ConflictError
	a.SetOnIntegrityConflict(func(ce *ConflictError) { alarms = append(alarms, ce) })
	if err := a.CheckAndAnchor(); err != nil {
		t.Fatalf("the first anchor: %v", err)
	}

	// The bookmark for record 8 reaches the witness and its answer is lost.
	appendRecords(t, lg, kp, 2)
	front.mu.Lock()
	front.lose = 1
	front.mu.Unlock()
	if err := a.CheckAndAnchor(); err == nil {
		t.Fatal("rig: the answer to the bookmark for record 8 was to be lost")
	}
	if kept, err := readPendingRequest(dir); err != nil || kept == nil || kept.LedgerOrdinal != 8 {
		t.Fatalf("rig: the bookmark for record 8 is not kept: %+v (%v)", kept, err)
	}

	// The witness's key is rotated, and the record moves on.
	front.answers(rotated(t, p, fw).server.Config.Handler, nil)
	appendRecords(t, lg, kp, 1)

	// The bookmark sent again: the stored receipt does not verify.
	err := a.CheckAndAnchor()
	var ce *ConflictError
	if err == nil || !strings.Contains(err.Error(), "receipt verification") || errors.As(err, &ce) {
		t.Fatalf("rig: the receipt stored under the key the witness had was not to verify: %v", err)
	}
	if sent := front.sent(); len(sent) != 3 || sent[2] != 8 {
		t.Fatalf("rig: the kept bookmark was to be sent again: the witness was sent records %v", sent)
	}
	if a.IntegrityConflict() != nil || len(alarms) != 0 {
		t.Fatalf("a receipt that did not verify was latched as a conflict: %v", a.IntegrityConflict())
	}
	if kept, perr := readPendingRequest(dir); perr != nil || kept != nil {
		t.Fatalf("A BOOKMARK WHOSE ANSWER WILL NEVER VERIFY IS KEPT TO BE SENT AGAIN AT EVERY PASS: %+v (%v)", kept, perr)
	}
	if kept, perr := readPendingReceipt(dir); perr != nil || kept != nil || lg.LastSeq() != 9 {
		t.Fatalf("a receipt that did not verify was kept or minted: %+v (%v), the record ends at %d", kept, perr, lg.LastSeq())
	}

	// The next pass sends no kept bookmark: it asks about the record's end,
	// and the answer is the witness's row, latched and told.
	err = a.CheckAndAnchor()
	if sent := front.sent(); len(sent) != 4 || sent[3] != 9 {
		t.Fatalf("THE NEXT PASS DID NOT ASK ABOUT THE RECORD'S END (9): the witness was sent records %v", sent)
	}
	notMinted(t, a, lg, kp, p.env, 9, 1, err, true, "does not continue this record")
	if len(alarms) != 1 || !alarms[0].WitnessSide {
		t.Fatalf("the seam was told %d times, want once, of the witness's row: %+v", len(alarms), alarms)
	}
	// And latched: nothing more is sent.
	if err := a.CheckAndAnchor(); err == nil || len(front.sent()) != 4 || len(alarms) != 1 {
		t.Fatalf("the latch did not hold: %v, the witness was sent records %v, the seam told %d times", err, front.sent(), len(alarms))
	}
}

// A KEPT BOOKMARK FOR A RECORD THIS LEDGER NO LONGER HOLDS IS NOT SENT
// AGAIN. After a restore to an earlier point the file beside the ledger
// names a record the ledger does not have, or has with another hash.
// Sending it again would bring back a receipt that can never be settled
// and keep every later pass from asking the witness about the record as
// it is, whose answer is what decides (a rollback is SAFE's).
func TestAKeptBookmarkForARecordTheLedgerNoLongerHoldsIsNotSentAgain(t *testing.T) {
	fw := newFakeWitness(t)
	front := &losingFront{witness: fw.server.Config.Handler}
	srv := httptest.NewServer(front)
	t.Cleanup(srv.Close)
	lg, kp := testLedger(t, 5)
	dir := filepath.Dir(lg.Path())
	a := NewAnchorer(New(srv.URL, ""), lg, AsIdentityKey(kp), &memEnvelopeStore{}, &memReceiptStore{}, testMinter{lg, kp}, 1, "")

	for name, kept := range map[string]WitnessRequest{
		"past the record's end":           {IdentityID: "did:kept", LedgerOrdinal: 40, LedgerHash: "sha256:" + strings.Repeat("a", 64)},
		"a record held with another hash": {IdentityID: "did:kept", LedgerOrdinal: 3, LedgerHash: "sha256:" + strings.Repeat("b", 64)},
	} {
		if err := writePendingRequest(dir, kept, time.Now()); err != nil {
			t.Fatal(err)
		}
		if again := a.requestToSendAgain(dir); again != nil {
			t.Fatalf("%s: A BOOKMARK THIS RECORD DOES NOT HOLD WOULD BE SENT AGAIN: %+v", name, again)
		}
		if _, err := os.Stat(PendingRequestPath(dir)); !os.IsNotExist(err) {
			t.Fatalf("%s: the bookmark is still kept: %v", name, err)
		}
	}
	// And the pass bookmarks the record as it is.
	if err := a.CheckAndAnchor(); err != nil {
		t.Fatalf("the pass over the record as it is: %v", err)
	}
	if sent := front.sent(); len(sent) != 1 || sent[0] != 5 {
		t.Fatalf("the witness was sent %v, want the record's end (5) alone", sent)
	}
}

// A KEPT BOOKMARK SIGNED FOR ANOTHER IDENTITY IS NOT SENT AS THIS ONE'S. The
// file beside the ledger names an identity the envelope at hand does not
// (a home whose files were copied over from another, a hand edit): it is
// not sent again, it is retired, and the pass says why.
func TestAKeptBookmarkNamingAnotherIdentityIsNotSent(t *testing.T) {
	fw := newFakeWitness(t)
	front := &losingFront{witness: fw.server.Config.Handler, lose: 1}
	srv := httptest.NewServer(front)
	t.Cleanup(srv.Close)
	lg, kp := testLedger(t, 5)
	dir := filepath.Dir(lg.Path())
	a := NewAnchorer(New(srv.URL, ""), lg, AsIdentityKey(kp), &memEnvelopeStore{}, &memReceiptStore{}, testMinter{lg, kp}, 1, "")
	if err := a.CheckAndAnchor(); err == nil {
		t.Fatal("rig: the first bookmark's answer was to be lost")
	}
	kept, err := readPendingRequest(dir)
	if err != nil || kept == nil {
		t.Fatalf("rig: the bookmark is kept: %+v %v", kept, err)
	}
	another := kept.Request
	another.IdentityID = "did:another-identity"
	if err := writePendingRequest(dir, another, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := a.CheckAndAnchor(); !errors.Is(err, ErrNotTheRecordedIdentity) {
		t.Fatalf("A BOOKMARK KEPT FOR ANOTHER IDENTITY WAS NOT REFUSED AS SUCH: %v", err)
	}
	if sent := front.sent(); len(sent) != 1 {
		t.Fatalf("A BOOKMARK KEPT FOR ANOTHER IDENTITY WAS SENT AS THIS ONE'S: the witness was sent %v", sent)
	}
	if _, err := os.Stat(PendingRequestPath(dir)); !os.IsNotExist(err) {
		t.Fatalf("the bookmark kept for another identity is still kept: %v", err)
	}
}
