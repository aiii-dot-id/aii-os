package witness

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestAskedAgainWhileHeldARefusalIsNotCleared(t *testing.T) {
	fw := newFakeWitness(t)
	var bookmarks atomic.Int64
	srv := conflictServer(t, fw, conflictMsgRollbackFork, &bookmarks)
	lg, kp := testLedger(t, 3)
	dir := filepath.Dir(lg.Path())
	if err := keepConflict(dir, KeptConflict{WitnessURL: srv.URL, Answer: "earlier", Record: 3, KeptAt: "2026-10-20T12:00:00Z"}); err != nil {
		t.Fatal(err)
	}
	held := NewAnchorer(New(srv.URL, ""), lg, AsIdentityKey(kp), &memEnvelopeStore{}, &memReceiptStore{}, nil, 100, "")
	cleared, err := held.AskWhileHeld(context.Background())
	if cleared || err == nil || bookmarks.Load() != 1 {
		t.Fatalf("a refusal asked again cleared the hold, or the witness was not asked under no cadence (cleared %v, %d bookmarks): %v", cleared, bookmarks.Load(), err)
	}
	kept, kerr := ReadKeptConflict(dir)
	if kerr != nil || kept == nil || !strings.Contains(kept.Answer, conflictMsgRollbackFork) || kept.Answer == "earlier" {
		t.Fatalf("the answer was not kept again: %+v (%v)", kept, kerr)
	}
	if lg.LastSeq() != 3 {
		t.Fatalf("a held ask appended to the record: it ends at %d", lg.LastSeq())
	}
	if p, perr := readPendingReceipt(dir); perr != nil || p != nil {
		t.Fatalf("a refusal left a receipt beside the ledger: %+v (%v)", p, perr)
	}
	// Asked again, again: the latch of one ask does not refuse the next.
	if cleared, _ := held.AskWhileHeld(context.Background()); cleared || bookmarks.Load() != 2 {
		t.Fatalf("the second ask was not made (%d bookmarks)", bookmarks.Load())
	}
}

// A RECEIPT PENDING BESIDE THE LEDGER IS SETTLED BY A RUNNING IDENTITY ONLY:
// the witness's next receipt would chain from it, so a held start does not
// ask over it.
func TestAskedAgainWhileHeldNotOverAPendingReceipt(t *testing.T) {
	fw := newFakeWitness(t)
	var bookmarks atomic.Int64
	srv := conflictServer(t, fw, conflictMsgRollbackFork, &bookmarks)
	lg, kp := testLedger(t, 3)
	dir := filepath.Dir(lg.Path())
	if err := writePendingReceipt(dir, WitnessReceipt{IdentityID: "id", LedgerOrdinal: 2, LedgerHash: "sha256:" + strings.Repeat("a", 64)}, time.Now()); err != nil {
		t.Fatal(err)
	}
	held := NewAnchorer(New(srv.URL, ""), lg, AsIdentityKey(kp), &memEnvelopeStore{}, &memReceiptStore{}, nil, 100, "")
	if cleared, err := held.AskWhileHeld(context.Background()); cleared || err == nil || bookmarks.Load() != 0 {
		t.Fatalf("a held start asked over a pending receipt (cleared %v, %d bookmarks): %v", cleared, bookmarks.Load(), err)
	}
}

// SET ASIDE, NOT DELETED: the kept answer stays beside the record under its
// cleared name, with the time.
func TestAKeptAnswerIsSetAsideWithItsTime(t *testing.T) {
	dir := t.TempDir()
	if err := keepConflict(dir, KeptConflict{WitnessURL: "https://w.example", Answer: "a fork", Record: 3, KeptAt: "2026-10-20T12:00:00Z"}); err != nil {
		t.Fatal(err)
	}
	name, err := SetKeptConflictAside(dir, time.Date(2026, 10, 20, 21, 30, 0, 0, time.UTC))
	if err != nil || name != "witness-conflict.cleared-20261020T213000Z.json" {
		t.Fatalf("set aside as %q: %v", name, err)
	}
	if _, err := os.Lstat(ConflictPath(dir)); !os.IsNotExist(err) {
		t.Fatalf("the kept answer still holds: %v", err)
	}
	if raw, err := os.ReadFile(filepath.Join(dir, name)); err != nil || !strings.Contains(string(raw), "a fork") {
		t.Fatalf("the answer set aside is not the answer: %q (%v)", raw, err)
	}
}
