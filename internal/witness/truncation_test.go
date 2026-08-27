package witness

// The 2026-08-20 hardening's battery: typed 409s, the integrity latch,
// and the local witness-tail file (write, readback, truncation, fork,
// absence, atomicity). Server behavior mirrored here is cited to
// ai3-witnessd by file:line at the sites it is faked.

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

	"github.com/aiii-dot-id/aii-os/internal/ledger"
)

// conflictServer answers every bookmark with a 409 carrying the REAL
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
		writeTestJSON(w, map[string]interface{}{"max_range_entries": 4096})
	})
	mux.HandleFunc("/witness/bookmark", func(w http.ResponseWriter, r *http.Request) {
		bookmarks.Add(1)
		w.WriteHeader(409)
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
		msg     string
		cadence bool
	}{
		{conflictMsgRollbackFork, false},
		{conflictMsgIdentityMismatch, false},
		{conflictMsgCadence, true},
		{"witness state advanced after reconstruction", false}, // store.go:154 — unknown-to-us leans integrity
	}
	for _, tc := range cases {
		srv := conflictServer(t, fw, tc.msg, &n)
		_, err := New(srv.URL, "").Bookmark(WitnessRequest{IdentityID: "did:test", LedgerOrdinal: 1})
		var ce *ConflictError
		if !errors.As(err, &ce) {
			t.Fatalf("%q: Bookmark 409 must be a *ConflictError, got %T: %v", tc.msg, err, err)
		}
		if ce.Message != tc.msg {
			t.Fatalf("conflict message %q, want %q", ce.Message, tc.msg)
		}
		if ce.IsCadence() != tc.cadence {
			t.Fatalf("%q: IsCadence()=%v, want %v", tc.msg, ce.IsCadence(), tc.cadence)
		}
		if ce.Local {
			t.Fatalf("%q: a server 409 must not read as local", tc.msg)
		}
	}
}

func TestBookmarkConflictNonJSONBodyStillTyped(t *testing.T) {
	// A proxy or older server might strip the JSON shape; the raw body
	// must survive as the message rather than flattening the type away.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "witness bookmark rollback or fork", 409)
	}))
	t.Cleanup(srv.Close)
	_, err := New(srv.URL, "").Bookmark(WitnessRequest{})
	var ce *ConflictError
	if !errors.As(err, &ce) {
		t.Fatalf("plain-text 409 must still type as *ConflictError, got %v", err)
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
		t.Fatalf("rollback 409 must surface as an integrity ConflictError, got %v", err)
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

func TestAnchorerCadenceConflictNotLatched(t *testing.T) {
	fw := newFakeWitness(t)
	var bookmarks atomic.Int64
	srv := conflictServer(t, fw, conflictMsgCadence, &bookmarks)
	lg, kp := testLedger(t, 3)
	fired := false
	a := NewAnchorer(New(srv.URL, ""), lg, AsIdentityKey(kp), &memEnvelopeStore{}, &memReceiptStore{}, testMinter{lg, kp}, 3, "")
	a.SetOnIntegrityConflict(func(*ConflictError) { fired = true })

	if err := a.CheckAndAnchor(); err == nil || !strings.Contains(err.Error(), "cadence") {
		t.Fatalf("cadence 409 must surface as a cadence refusal, got %v", err)
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
	// The client-side chain-continuity mismatch (a receipt chaining a
	// different previous anchor) is the same integrity class as a server
	// 409 — after the 2026-08-20 hardening it latches too.
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
	if tail.LedgerOrdinal != 5 || tail.LedgerHash != events[4].ContentHash {
		t.Fatalf("tail records (%d,%s), want (5,%s)", tail.LedgerOrdinal, tail.LedgerHash, events[4].ContentHash)
	}
	wm, _ := fw.witnessEnv.FindPublicKey(AlgMLDSA87)
	if tail.WitnessKeyFingerprint != wm.PublicKeyFingerprint {
		t.Fatal("tail must record the witness key fingerprint the receipt verified against")
	}
	if tail.WitnessedAt == "" {
		t.Fatal("tail must record witnessed_at")
	}
	// The freshly anchored ledger passes its own boot check.
	if err := CheckLocalTail(filepath.Dir(lg.Path()), lg); err != nil {
		t.Fatalf("intact ledger must pass CheckLocalTail: %v", err)
	}
}

func TestCheckLocalTailAbsentIsOK(t *testing.T) {
	lg, _ := testLedger(t, 2)
	if err := CheckLocalTail(t.TempDir(), lg); err != nil {
		t.Fatalf("absent tail file is first-boot, must pass: %v", err)
	}
}

func TestCheckLocalTailDetectsTruncation(t *testing.T) {
	lg, kp := testLedger(t, 6)
	dir := filepath.Dir(lg.Path())
	events, _ := ledger.ReadAll(lg.Path())
	if err := writeLocalTail(dir, LocalTail{
		LedgerOrdinal: 6, LedgerHash: events[5].ContentHash, WitnessedAt: "2026-08-20T00:00:00Z",
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
	if err := ledger.VerifyChain(lg.Path(), map[string][]byte{kp.Fingerprint(): kp.PublicKeyBytes()}); err != nil {
		t.Fatalf("the truncated chain must still be internally valid (that is the attack): %v", err)
	}
	err = CheckLocalTail(dir, reopened)
	if err == nil || !strings.Contains(err.Error(), "TRUNCATION") {
		t.Fatalf("truncation must be detected, got %v", err)
	}
}

func TestCheckLocalTailDetectsFork(t *testing.T) {
	lg, _ := testLedger(t, 4)
	dir := filepath.Dir(lg.Path())
	// Same ordinal, different content hash: a resealed history.
	if err := writeLocalTail(dir, LocalTail{
		LedgerOrdinal: 4, LedgerHash: "sha256:" + strings.Repeat("f", 64), WitnessedAt: "2026-08-20T00:00:00Z",
	}); err != nil {
		t.Fatal(err)
	}
	err := CheckLocalTail(dir, lg)
	if err == nil || !strings.Contains(err.Error(), "FORK") {
		t.Fatalf("fork must be detected, got %v", err)
	}
}

func TestCheckLocalTailTornTempInvisible(t *testing.T) {
	// A crash between temp-write and rename leaves only the temp file:
	// the real name is absent, the check stays advisory — the atomic
	// write's failure mode is absence, never a half-written authority.
	lg, _ := testLedger(t, 2)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, TailFileName+".tmp-123"), []byte(`{"ledger_ordinal":`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := CheckLocalTail(dir, lg); err != nil {
		t.Fatalf("a stranded temp file must not become authority: %v", err)
	}
	// But a CORRUPT real file is loud — present is authoritative.
	if err := os.WriteFile(filepath.Join(dir, TailFileName), []byte(`{"ledger_ordinal":`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := CheckLocalTail(dir, lg); err == nil {
		t.Fatal("a corrupt present tail file must not soft-pass as absent")
	}
}
