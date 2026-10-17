package app

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/crypto"
	"github.com/aiii-dot-id/aii-os/internal/ledger"
	"github.com/aiii-dot-id/aii-os/internal/witness"
)

func TestAppOpeningPreservesOverlapBeforeRejectingSegmentProof(t *testing.T) {
	f := newSealedFixture(t)
	a := New(f.cfg)
	if err := startLiveForTest(a); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { stopApp(t, a) })
	original := lockedRead(t, f.ledgerPath)
	if err := a.anchorer.CheckAndAnchor(); err != nil {
		t.Fatal(err)
	}
	head := a.ledger.LastSeq()
	stopApp(t, a)
	segment := filepath.Join(f.dir, "segment-1-"+itoa(head)+".jsonl.gz")
	good := lockedRead(t, segment)
	gz, err := gzip.NewReader(bytes.NewReader(good))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := io.ReadAll(gz)
	if err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	lines := bytes.Split(bytes.TrimSuffix(raw, []byte("\n")), []byte("\n"))
	// Exactly the writer's full signed tail at the segment-published boundary:
	// original pre-anchor records followed by the newly signed witness head.
	overlap := append(append(original, lines[len(lines)-1]...), '\n')
	lockedWrite(t, f.ledgerPath, overlap)
	kp, err := crypto.LoadKeyPair(f.keyPath)
	if err != nil {
		t.Fatal(err)
	}
	heads, err := a.bootHeadVerifier(*f.cfg)
	if err != nil {
		t.Fatal(err)
	}
	if n, err := ledger.VerifyChain(f.ledgerPath, kp.PublicKeyBytes(), heads); err != nil || uint64(n) != head {
		t.Fatalf("control overlap: n=%d err=%v", n, err)
	}
	var closing ledger.Event
	if err := json.Unmarshal(lines[len(lines)-1], &closing); err != nil {
		t.Fatal(err)
	}
	closing.Sig = "AAAA"
	lines[len(lines)-1], err = json.Marshal(closing)
	if err != nil {
		t.Fatal(err)
	}
	var compressed bytes.Buffer
	zw := gzip.NewWriter(&compressed)
	if _, err := zw.Write(append(bytes.Join(lines, []byte("\n")), '\n')); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	lockedWrite(t, segment, compressed.Bytes())
	beforeDB, _ := fileDigest(t, f.dbPath)
	rejected := New(f.cfg)
	if err := startLiveForTest(rejected); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { stopApp(t, rejected) })
	if reason, safe := rejected.SafeMode(); !safe || !strings.Contains(reason, "chain verification") {
		t.Fatalf("want diagnostic SAFE, got %t %s", safe, reason)
	}
	if rejected.ledger != nil || rejected.live.Load() || rejected.timeFac != nil {
		t.Fatal("refused boot published live resources")
	}
	if !bytes.Equal(overlap, lockedRead(t, f.ledgerPath)) || !bytes.Equal(compressed.Bytes(), lockedRead(t, segment)) {
		t.Fatal("rejected boot removed recovery evidence")
	}
	if after, _ := fileDigest(t, f.dbPath); beforeDB != after {
		t.Fatal("rejected boot changed the projection")
	}
	stopApp(t, rejected)
	// Fixture restores the original segment; the opener must have released
	// its lock and must recover normally, not latch an operational freeze.
	lockedWrite(t, segment, good)
	recovered := New(f.cfg)
	if err := startLiveForTest(recovered); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { stopApp(t, recovered) })
	if reason, safe := recovered.SafeMode(); safe {
		t.Fatalf("healthy restart is SAFE: %s", reason)
	}
	appendFixtureEvent(t, recovered, f.keyPath, "after_verified_recovery")
}

func TestAppOpeningRecoversFirstTornAppendAfterSeal(t *testing.T) {
	f := newSealedFixture(t)
	head := f.bootAndAnchor(t)
	suffix := []byte(`{"seq":`)
	lockedWrite(t, f.ledgerPath, suffix)
	a := New(f.cfg)
	if err := startLiveForTest(a); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { stopApp(t, a) })
	if reason, safe := a.SafeMode(); safe {
		t.Fatalf("recoverable append entered SAFE: %s", reason)
	}
	if a.ledger.LastSeq() != head {
		t.Fatal("verified boundary changed")
	}
	paths, err := filepath.Glob(f.ledgerPath + ".torn-*")
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) != 1 || !bytes.Equal(suffix, lockedRead(t, paths[0])) {
		t.Fatal("exact torn bytes were not preserved")
	}
	appendFixtureEvent(t, a, f.keyPath, "after_first_torn_append")
	want := a.ledger.LastSeq()
	stopApp(t, a)
	a = New(f.cfg)
	if err := startLiveForTest(a); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { stopApp(t, a) })
	if reason, safe := a.SafeMode(); safe || a.ledger.LastSeq() != want {
		t.Fatalf("restart failed: %s", reason)
	}
}

func TestAppOpeningDoesNotDiscardAnAcknowledgedUnterminatedRecord(t *testing.T) {
	dir := t.TempDir()
	key, path, db := birthFixture(t, dir, "Acknowledged")
	buildPriorProjection(t, path, db)
	original := lockedRead(t, path)
	input := bytes.TrimSuffix(original, []byte("\n"))
	lockedWrite(t, path, input)
	before, _ := fileDigest(t, db)
	a := New(safebootConfig(t, dir, "Acknowledged", key, path, db))
	if err := startLiveForTest(a); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { stopApp(t, a) })
	if !bytes.Equal(input, lockedRead(t, path)) {
		t.Fatal("acknowledged record was removed")
	}
	if reason, safe := a.SafeMode(); !safe {
		t.Fatalf("want acknowledgment refusal, got %t %s", safe, reason)
	}
	if after, _ := fileDigest(t, db); before != after {
		t.Fatal("acknowledgment refusal changed database")
	}
}

func TestAppOpeningLockFailureIsNotLedgerCorruption(t *testing.T) {
	dir := t.TempDir()
	key, path, db := birthFixture(t, dir, "Locked")
	held, err := ledger.New(path)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	a := New(safebootConfig(t, dir, "Locked", key, path, db))
	err = startLiveForTest(a)
	defer stopApp(t, a)
	if !errors.Is(err, ledger.ErrLedgerInUse) {
		t.Fatalf("lock failure: %v", err)
	}
	if reason, safe := a.SafeMode(); safe {
		t.Fatalf("lock contention declared corruption: %s", reason)
	}
}

func lockedRead(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestAppOpeningWitnessAheadPreservesUnterminatedRecord(t *testing.T) {
	dir := t.TempDir()
	key, path, db := birthFixture(t, dir, "WitnessAhead")
	events, err := ledger.ReadAll(path)
	if err != nil {
		t.Fatal(err)
	}
	last := events[len(events)-1]
	tail, err := json.Marshal(witness.LocalTail{LedgerOrdinal: int64(last.Seq), LedgerHash: last.EntryHash(), WitnessedAt: "fixture"})
	if err != nil {
		t.Fatal(err)
	}
	lockedWrite(t, witness.TailPath(dir), tail)
	input := bytes.TrimSuffix(lockedRead(t, path), []byte("\n"))
	lockedWrite(t, path, input)
	a := New(safebootConfig(t, dir, "WitnessAhead", key, path, db))
	if err := startLiveForTest(a); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { stopApp(t, a) })
	if !bytes.Equal(input, lockedRead(t, path)) {
		t.Fatal("witnessed record removed")
	}
	if reason, safe := a.SafeMode(); !safe || !strings.Contains(reason, "witness-tail check failed") {
		t.Fatalf("want witness-tail refusal: %t %s", safe, reason)
	}
}

// A record that cannot be read from storage is no finding about the record:
// the start stops at the page for a step that could not complete, carrying
// the read's own cause, and never at the page that calls history corrupt.
func TestAppOpeningReadFailureIsNotLedgerCorruption(t *testing.T) {
	dir := t.TempDir()
	key, path, db := birthFixture(t, dir, "ReadFailure")
	if err := os.Rename(path, path+".retained"); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	a, reason := safeAt(t, safebootConfig(t, dir, "ReadFailure", key, path, db), "a record that is a directory ended the start")
	defer stopApp(t, a)
	if !strings.Contains(reason, safePostures[safeCouldNotStart].what) || !strings.Contains(reason, "open verified ledger") || !strings.Contains(reason, filepath.Base(path)) {
		t.Fatalf("underlying read failure lost: %s", reason)
	}
	if strings.Contains(reason, safePostures[safeRecord].what) {
		t.Fatalf("storage failure declared history corrupt: %s", reason)
	}
}

func TestAppOpeningReportsChainRefusalBeforeUnreadableWitnessTail(t *testing.T) {
	dir := t.TempDir()
	key, path, db := birthFixture(t, dir, "RefusalOrder")
	lines := bytes.Split(bytes.TrimSuffix(lockedRead(t, path), []byte("\n")), []byte("\n"))
	var last ledger.Event
	if err := json.Unmarshal(lines[len(lines)-1], &last); err != nil {
		t.Fatal(err)
	}
	last.Sig = "AAAA"
	var err error
	lines[len(lines)-1], err = json.Marshal(last)
	if err != nil {
		t.Fatal(err)
	}
	input := append(bytes.Join(lines, []byte("\n")), '\n')
	lockedWrite(t, path, input)
	lockedWrite(t, witness.TailPath(dir), []byte("not-json"))
	a := New(safebootConfig(t, dir, "RefusalOrder", key, path, db))
	if err := startLiveForTest(a); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { stopApp(t, a) })
	if reason, safe := a.SafeMode(); !safe || !strings.Contains(reason, "chain verification failed") {
		t.Fatalf("chain failure must have precedence: %t %s", safe, reason)
	}
	if !bytes.Equal(input, lockedRead(t, path)) {
		t.Fatal("refusal changed history")
	}
}
func lockedWrite(t *testing.T, path string, b []byte) {
	t.Helper()
	if err := os.WriteFile(path, b, 0600); err != nil {
		t.Fatal(err)
	}
}
