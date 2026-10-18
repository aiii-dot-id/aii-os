package witness

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/aiii-dot-id/aii-os/internal/ledger"
	"github.com/aiii-dot-id/aii-os/internal/tlsfault"
)

// conflictServer answers every bookmark with a http.StatusConflict carrying the REAL
// wire body ({"error": msg} — witnessd server.go:377-378 writeJSONError)
// while serving the key/status endpoints a full anchor pass needs.
func conflictServer(t *testing.T, fw *fakeWitness, msg string, bookmarks *atomic.Int64) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/witness/pubkey/hash", func(w http.ResponseWriter, r *http.Request) {
		writeTestJSON(w, map[string]string{
			"witness_public_key_hash": sha256Prefixed(mustCanonical(t, fw.witnessEnv)),
			"key_id":                  fw.witnessEnv.KeyID,
		})
	})
	mux.HandleFunc("/witness/pubkey", func(w http.ResponseWriter, r *http.Request) {
		writeTestJSON(w, fw.witnessEnv)
	})
	mux.HandleFunc("/status", func(w http.ResponseWriter, r *http.Request) {
		writeTestJSON(w, map[string]any{"max_range_entries": 4096})
	})
	mux.HandleFunc("/witness/bookmark", func(w http.ResponseWriter, r *http.Request) {
		bookmarks.Add(1)
		w.WriteHeader(http.StatusConflict)
		writeTestJSON(w, map[string]string{"error": msg})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestBookmarkConflictIsTyped(t *testing.T) {
	fw := newFakeWitness(t)
	var n atomic.Int64
	cases := []struct {
		msg          string
		cadence      bool
		unrecognised bool
	}{
		{conflictMsgRollbackFork, false, false},
		{conflictMsgIdentityMismatch, false, false},
		{conflictMsgCadence, true, false},
		// store.go:154, a message of the witness's own that this client
		// does not list: it is not one of the two integrity answers, so it
		// refuses the pass and latches nothing (ConflictError.Unrecognised).
		{"witness state advanced after reconstruction", false, true},
	}
	for _, tc := range cases {
		srv := conflictServer(t, fw, tc.msg, &n)
		_, err := New(srv.URL, "").Bookmark(WitnessRequest{IdentityID: "did:test", LedgerOrdinal: 1})
		var ce *ConflictError
		if !errors.As(err, &ce) {
			t.Fatalf("%q: Bookmark http.StatusConflict must be a *ConflictError, got %T: %v", tc.msg, err, err)
		}
		if ce.Message != tc.msg {
			t.Fatalf("conflict message %q, want %q", ce.Message, tc.msg)
		}
		if ce.IsCadence() != tc.cadence {
			t.Fatalf("%q: IsCadence()=%v, want %v", tc.msg, ce.IsCadence(), tc.cadence)
		}
		if ce.Unrecognised() != tc.unrecognised || ce.IsIntegrity() != (!tc.cadence && !tc.unrecognised) {
			t.Fatalf("%q: Unrecognised()=%v IsIntegrity()=%v", tc.msg, ce.Unrecognised(), ce.IsIntegrity())
		}
		if ce.Local {
			t.Fatalf("%q: a server http.StatusConflict must not read as local", tc.msg)
		}
	}
}

func TestBookmarkConflictNonJSONBodyStillTyped(t *testing.T) {
	// A proxy or older server might strip the JSON shape; the raw body
	// must survive as the message rather than flattening the type away.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "witness bookmark rollback or fork", http.StatusConflict)
	}))
	t.Cleanup(srv.Close)
	_, err := New(srv.URL, "").Bookmark(WitnessRequest{})
	var ce *ConflictError
	if !errors.As(err, &ce) {
		t.Fatalf("plain-text http.StatusConflict must still type as *ConflictError, got %v", err)
	}
	if !strings.Contains(ce.Message, "rollback or fork") || ce.IsCadence() {
		t.Fatalf("raw-body conflict mangled: %+v", ce)
	}
}

func TestAnchorerLatchesIntegrityConflict(t *testing.T) {
	fw := newFakeWitness(t)
	var bookmarks atomic.Int64
	srv := conflictServer(t, fw, conflictMsgRollbackFork, &bookmarks)
	lg, kp := testLedger(t, 3)
	var alarms []*ConflictError
	a := NewAnchorer(New(srv.URL, ""), lg, AsIdentityKey(kp), &memEnvelopeStore{}, &memReceiptStore{}, testMinter{lg, kp}, 3, "")
	a.SetOnIntegrityConflict(func(ce *ConflictError) { alarms = append(alarms, ce) })

	err := a.CheckAndAnchor()
	var ce *ConflictError
	if !errors.As(err, &ce) || ce.IsCadence() {
		t.Fatalf("rollback http.StatusConflict must surface as an integrity ConflictError, got %v", err)
	}
	if got := a.IntegrityConflict(); got == nil || got.Message != conflictMsgRollbackFork {
		t.Fatalf("conflict not latched: %+v", got)
	}
	if len(alarms) != 1 {
		t.Fatalf("alarm seam fired %d times, want exactly once", len(alarms))
	}
	if bookmarks.Load() != 1 {
		t.Fatalf("server saw %d bookmarks, want 1", bookmarks.Load())
	}

	// The latch holds: the next pass refuses LOUDLY without resubmitting
	// the same anchor point, and the seam does not re-fire.
	err = a.CheckAndAnchor()
	if err == nil || !strings.Contains(err.Error(), "latched") {
		t.Fatalf("latched anchorer must refuse, got %v", err)
	}
	if bookmarks.Load() != 1 {
		t.Fatalf("latched anchorer resubmitted the anchor point (%d bookmarks)", bookmarks.Load())
	}
	if len(alarms) != 1 {
		t.Fatalf("alarm seam re-fired (%d)", len(alarms))
	}
}

// A 409 THIS CLIENT DOES NOT RECOGNISE REFUSES THE PASS AND LATCHES NOTHING.
// Every 409 but the cadence refusal was presumed an integrity conflict: a
// gateway's own page at the witness's address latched anchoring off and
// put the identity in SAFE, telling the resident their record had moved
// backward. Nothing in a 409 is signed. Only the witness's two integrity
// answers latch now; any other is told once and asked again next pass.
func TestA409ThisClientDoesNotRecogniseRefusesThePassAndLatchesNothing(t *testing.T) {
	page := "<html><body><h1>409 Conflict</h1>" + strings.Repeat("the gateway is busy. ", 100) + "</body></html>"
	for name, answer := range map[string]func(w http.ResponseWriter){
		"a gateway's page": func(w http.ResponseWriter) { http.Error(w, page, http.StatusConflict) },
		"the witness's words, changed": func(w http.ResponseWriter) {
			w.WriteHeader(http.StatusConflict)
			writeTestJSON(w, map[string]string{"error": "witness bookmark cadence under the hosted minimum"})
		},
	} {
		t.Run(name, func(t *testing.T) {
			fw := newFakeWitness(t)
			var bookmarks atomic.Int64
			mux := http.NewServeMux()
			mux.HandleFunc("/witness/pubkey/hash", func(w http.ResponseWriter, r *http.Request) {
				writeTestJSON(w, map[string]string{"witness_public_key_hash": sha256Prefixed(mustCanonical(t, fw.witnessEnv)), "key_id": fw.witnessEnv.KeyID})
			})
			mux.HandleFunc("/witness/pubkey", func(w http.ResponseWriter, r *http.Request) { writeTestJSON(w, fw.witnessEnv) })
			mux.HandleFunc("/status", func(w http.ResponseWriter, r *http.Request) {
				writeTestJSON(w, map[string]any{"max_range_entries": 4096})
			})
			mux.HandleFunc("/witness/bookmark", func(w http.ResponseWriter, r *http.Request) {
				bookmarks.Add(1)
				answer(w)
			})
			srv := httptest.NewServer(mux)
			t.Cleanup(srv.Close)

			lg, kp := testLedger(t, 3)
			var alarms, told []*ConflictError
			a := NewAnchorer(New(srv.URL, ""), lg, AsIdentityKey(kp), &memEnvelopeStore{}, &memReceiptStore{}, testMinter{lg, kp}, 3, "")
			a.SetOnIntegrityConflict(func(ce *ConflictError) { alarms = append(alarms, ce) })
			a.SetOnUnrecognisedConflict(func(ce *ConflictError) { told = append(told, ce) })

			for pass := 1; pass <= 2; pass++ {
				err := a.CheckAndAnchor()
				var ce *ConflictError
				if !errors.As(err, &ce) || !ce.Unrecognised() || ce.IsIntegrity() || ce.IsCadence() {
					t.Fatalf("pass %d: the reply must come back as a conflict this client does not recognise: %v", pass, err)
				}
				if len(ce.Message) > tlsfault.MaxSaid+len("…")+utf8.UTFMax {
					t.Fatalf("pass %d: the reply's %d bytes are all kept as the message", pass, len(ce.Message))
				}
				if got := a.IntegrityConflict(); got != nil || len(alarms) != 0 {
					t.Fatalf("pass %d: AN UNSIGNED REPLY THIS CLIENT DOES NOT RECOGNISE WAS LATCHED AS EVIDENCE AGAINST THE RECORD: %v (alarms %d)", pass, got, len(alarms))
				}
				// Nothing is latched: the next pass asks again.
				if bookmarks.Load() != int64(pass) {
					t.Fatalf("pass %d: the witness's address was asked %d times", pass, bookmarks.Load())
				}
			}
			if len(told) != 1 {
				t.Fatalf("the same reply was told %d times in two passes, want once", len(told))
			}
		})
	}
}

func TestAnchorerCadenceConflictNotLatched(t *testing.T) {
	fw := newFakeWitness(t)
	var bookmarks atomic.Int64
	srv := conflictServer(t, fw, conflictMsgCadence, &bookmarks)
	lg, kp := testLedger(t, 3)
	fired := false
	a := NewAnchorer(New(srv.URL, ""), lg, AsIdentityKey(kp), &memEnvelopeStore{}, &memReceiptStore{}, testMinter{lg, kp}, 3, "")
	a.SetOnIntegrityConflict(func(*ConflictError) { fired = true })

	if err := a.CheckAndAnchor(); err == nil || !strings.Contains(err.Error(), "cadence") {
		t.Fatalf("cadence http.StatusConflict must surface as a cadence refusal, got %v", err)
	}
	if a.IntegrityConflict() != nil || fired {
		t.Fatal("a cadence conflict is pacing, not integrity — it must not latch or alarm")
	}
	// Pacing self-heals: the next pass tries again.
	_ = a.CheckAndAnchor()
	if bookmarks.Load() != 2 {
		t.Fatalf("cadence conflicts must stay retryable (%d bookmarks, want 2)", bookmarks.Load())
	}
}

func TestLocalForkLatches(t *testing.T) {

	fw := newFakeWitness(t)
	lg, kp := testLedger(t, 3)
	envelopes := &memEnvelopeStore{}
	a := NewAnchorer(New(fw.server.URL, ""), lg, AsIdentityKey(kp), envelopes, &memReceiptStore{}, testMinter{lg, kp}, 3, "")
	if err := a.CheckAndAnchor(); err != nil {
		t.Fatal(err)
	}
	fake := WitnessReceipt{LedgerOrdinal: 3, LedgerHash: "sha256:" + strings.Repeat("a", 64)}
	fakeJSON, _ := json.Marshal(fake)
	receipts2 := &memReceiptStore{}
	receipts2.SeedWitnessReceipt(3, fakeJSON)
	a2 := NewAnchorer(New(fw.server.URL, ""), lg, AsIdentityKey(kp), envelopes, receipts2, testMinter{lg, kp}, 1, "")
	err := a2.CheckAndAnchor()
	var ce *ConflictError
	if !errors.As(err, &ce) || !ce.Local {
		t.Fatalf("local fork must be a Local ConflictError, got %v", err)
	}
	if a2.IntegrityConflict() == nil {
		t.Fatal("local fork must latch")
	}
}

// --- the local witness-tail file ---

// heldToTail is the check as boot and the verify commands make it: the
// tail beside dir is read, the chain is verified with the check INSIDE the
// walk, and the verdict is asked of the walk that verified.
func heldToTail(dir, ledgerPath string, pubKey []byte) error {
	tail, err := ReadLocalTail(dir)
	if err != nil {
		return err
	}
	check := NewTailCheck(tail)
	if _, err := ledger.VerifyChainVisiting(ledgerPath, pubKey, nil, check.Visit); err != nil {
		return err
	}
	return check.Held()
}

func TestLocalTailWriteReadback(t *testing.T) {
	dir := t.TempDir()
	want := LocalTail{LedgerOrdinal: 7, LedgerHash: "sha256:" + strings.Repeat("b", 64),
		WitnessedAt: "2026-08-20T00:00:00Z", WitnessKeyFingerprint: "sha256:" + strings.Repeat("c", 64)}
	if err := writeLocalTail(dir, want); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, TailFileName))
	if err != nil {
		t.Fatal(err)
	}
	var got LocalTail
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("readback %+v, want %+v", got, want)
	}
	// Atomicity's visible half: no temp residue after a clean write.
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.Contains(e.Name(), ".tmp-") {
			t.Fatalf("temp residue after clean write: %s", e.Name())
		}
	}
	// And a rewrite replaces, never appends.
	want.LedgerOrdinal = 9
	if err := writeLocalTail(dir, want); err != nil {
		t.Fatal(err)
	}
	raw2, _ := os.ReadFile(filepath.Join(dir, TailFileName))
	if err := json.Unmarshal(raw2, &got); err != nil || got.LedgerOrdinal != 9 {
		t.Fatalf("rewrite readback: %+v (%v)", got, err)
	}
}

// A reader holding the tail — a verify, a snapshot copying it — must not
// cost an anchor its tail. On Windows a replace over an open file is
// refused while the handle stands, and the bare rename gave up at once;
// unix renames over an open file, so there this holds trivially.
func TestTheTailIsWrittenWhileAReaderHoldsIt(t *testing.T) {
	dir := t.TempDir()
	tail := LocalTail{LedgerOrdinal: 7, LedgerHash: "sha256:" + strings.Repeat("b", 64),
		WitnessedAt: "2026-08-20T00:00:00Z", WitnessKeyFingerprint: "sha256:" + strings.Repeat("c", 64)}
	if err := writeLocalTail(dir, tail); err != nil {
		t.Fatal(err)
	}
	reader, err := os.Open(TailPath(dir))
	if err != nil {
		t.Fatal(err)
	}
	released := make(chan struct{})
	go func() {
		defer close(released)
		time.Sleep(100 * time.Millisecond)
		reader.Close()
	}()
	tail.LedgerOrdinal = 9
	err = writeLocalTail(dir, tail)
	<-released
	if err != nil {
		t.Fatalf("a reader holding the tail for 100 ms cost the anchor its tail: %v", err)
	}
	got, err := ReadLocalTail(dir)
	if err != nil || got == nil || got.LedgerOrdinal != 9 {
		t.Fatalf("readback %+v (%v), want ordinal 9", got, err)
	}
}

func TestAnchorWritesLocalTail(t *testing.T) {
	fw := newFakeWitness(t)
	lg, kp := testLedger(t, 5)
	a := NewAnchorer(New(fw.server.URL, ""), lg, AsIdentityKey(kp), &memEnvelopeStore{}, &memReceiptStore{}, testMinter{lg, kp}, 5, "")
	if err := a.CheckAndAnchor(); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(filepath.Dir(lg.Path()), TailFileName))
	if err != nil {
		t.Fatalf("anchor must write the tail file beside the ledger: %v", err)
	}
	var tail LocalTail
	if err := json.Unmarshal(raw, &tail); err != nil {
		t.Fatal(err)
	}
	events, _ := ledger.ReadAll(lg.Path())
	if tail.LedgerOrdinal != 5 || tail.LedgerHash != events[4].EntryHash() {
		t.Fatalf("tail records (%d,%s), want (5,%s)", tail.LedgerOrdinal, tail.LedgerHash, events[4].EntryHash())
	}
	wm, _ := fw.witnessEnv.FindPublicKey(AlgMLDSA87)
	if tail.WitnessKeyFingerprint != wm.PublicKeyFingerprint {
		t.Fatal("tail must record the witness key fingerprint the receipt verified against")
	}
	if tail.WitnessedAt == "" {
		t.Fatal("tail must record witnessed_at")
	}
	// The freshly anchored ledger passes its own boot check.
	if err := heldToTail(filepath.Dir(lg.Path()), lg.Path(), kp.PublicKeyBytes()); err != nil {
		t.Fatalf("intact ledger must hold the tail its own anchor wrote: %v", err)
	}
}

func TestCheckLocalTailAbsentIsOK(t *testing.T) {
	lg, kp := testLedger(t, 2)
	if err := heldToTail(t.TempDir(), lg.Path(), kp.PublicKeyBytes()); err != nil {
		t.Fatalf("absent tail file is first-boot, must pass: %v", err)
	}
}

func TestCheckLocalTailDetectsTruncation(t *testing.T) {
	lg, kp := testLedger(t, 6)
	dir := filepath.Dir(lg.Path())
	events, _ := ledger.ReadAll(lg.Path())
	if err := writeLocalTail(dir, LocalTail{
		LedgerOrdinal: 6, LedgerHash: events[5].EntryHash(), WitnessedAt: "2026-08-20T00:00:00Z",
	}); err != nil {
		t.Fatal(err)
	}
	// Truncate the last two events off — the crash_test pattern: rewrite
	// the file to a prefix of its own lines. The survivor is a SHORTER
	// VALID chain; VerifyChain alone cannot see the cut.
	if err := lg.Close(); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(lg.Path())
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.SplitAfter(string(raw), "\n")
	if err := os.WriteFile(lg.Path(), []byte(strings.Join(lines[:4], "")), 0o600); err != nil {
		t.Fatal(err)
	}
	reopened, err := ledger.New(lg.Path())
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if _, err := ledger.VerifyChain(lg.Path(), kp.PublicKeyBytes(), nil); err != nil {
		t.Fatalf("the truncated chain must still be internally valid (that is the attack): %v", err)
	}
	err = heldToTail(dir, lg.Path(), kp.PublicKeyBytes())
	var refusal *TailRefusal
	if !errors.As(err, &refusal) || !refusal.Truncation || refusal.LastSeq != 4 || !strings.Contains(err.Error(), "TRUNCATION") {
		t.Fatalf("truncation must be detected and typed, got %v", err)
	}
}

func TestCheckLocalTailDetectsFork(t *testing.T) {
	lg, kp := testLedger(t, 4)
	dir := filepath.Dir(lg.Path())
	// Same ordinal, different content hash: a resealed history.
	if err := writeLocalTail(dir, LocalTail{
		LedgerOrdinal: 4, LedgerHash: "sha256:" + strings.Repeat("f", 64), WitnessedAt: "2026-08-20T00:00:00Z",
	}); err != nil {
		t.Fatal(err)
	}
	err := heldToTail(dir, lg.Path(), kp.PublicKeyBytes())
	var refusal *TailRefusal
	if !errors.As(err, &refusal) || refusal.Truncation || refusal.Observed == "" || !strings.Contains(err.Error(), "FORK") {
		t.Fatalf("fork must be detected and typed, got %v", err)
	}
}

func TestCheckLocalTailTornTempInvisible(t *testing.T) {
	// A crash between temp-write and rename leaves only the temp file:
	// the real name is absent, the check stays advisory — the atomic
	// write's failure mode is absence, never a half-written authority.
	lg, kp := testLedger(t, 2)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "."+TailFileName+".tmp-123"), []byte(`{"ledger_ordinal":`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := heldToTail(dir, lg.Path(), kp.PublicKeyBytes()); err != nil {
		t.Fatalf("a stranded temp file must not become authority: %v", err)
	}
	// But a CORRUPT real file is loud — present is authoritative.
	if err := os.WriteFile(filepath.Join(dir, TailFileName), []byte(`{"ledger_ordinal":`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := heldToTail(dir, lg.Path(), kp.PublicKeyBytes()); err == nil {
		t.Fatal("a corrupt present tail file must not soft-pass as absent")
	}
}
