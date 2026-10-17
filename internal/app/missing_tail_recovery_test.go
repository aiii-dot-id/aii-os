package app

import (
	"crypto/sha256"
	"os"
	"path/filepath"
	"testing"
)

func TestACompletelySealedIdentityRecoversAMissingEmptyTail(t *testing.T) {
	f := newSealedFixture(t)
	head := f.bootAndAnchor(t)
	if tail, err := os.ReadFile(f.ledgerPath); err != nil || len(tail) != 0 {
		t.Fatalf("fixture must be completely sealed: %d bytes, %v", len(tail), err)
	}
	segment := filepath.Join(f.dir, "segment-1-"+itoa(head)+".jsonl.gz")
	before, err := os.ReadFile(segment)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(f.ledgerPath); err != nil {
		t.Fatal(err)
	}
	a := New(f.cfg)
	choice, err := a.chooseBoot()
	if err != nil || choice != bootLive {
		t.Fatalf("intact sealed identity cannot reach its existing verified recovery: %v %v", choice, err)
	}
	if err := startLiveForTest(a); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { stopApp(t, a) })
	if why, safe := a.SafeMode(); safe {
		t.Fatalf("intact sealed identity entered SAFE: %s", why)
	}
	if a.ledger.LastSeq() != head || a.ledger.SealedSeq() != head {
		t.Fatalf("recovery changed history: %d/%d, want %d", a.ledger.LastSeq(), a.ledger.SealedSeq(), head)
	}
	appendFixtureEvent(t, a, f.keyPath, "life_after_tail_recovery")
	if a.ledger.LastSeq() != head+1 {
		t.Fatal("recovered identity cannot continue")
	}
	if after, err := os.ReadFile(segment); err != nil || sha256.Sum256(before) != sha256.Sum256(after) {
		t.Fatalf("recovery changed sealed history: %v", err)
	}
}

func TestAMissingTailWithAcknowledgedUnsealedHistoryStillBootsSafe(t *testing.T) {
	f := newSealedFixture(t)
	f.bootAndAnchor(t)
	a := New(f.cfg)
	if err := startLiveForTest(a); err != nil {
		t.Fatal(err)
	}
	appendFixtureEvent(t, a, f.keyPath, "acknowledged_unsealed_record")
	stopApp(t, a)
	if err := os.Remove(f.ledgerPath); err != nil {
		t.Fatal(err)
	}
	restarted := New(f.cfg)
	if choice, err := restarted.chooseBoot(); err != nil || choice != bootLive {
		t.Fatalf("damaged existing identity cannot reach verified SAFE recovery: %v %v", choice, err)
	}
	if err := startLiveForTest(restarted); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { stopApp(t, restarted) })
	if why, safe := restarted.SafeMode(); !safe {
		t.Fatalf("missing acknowledged history silently admitted as intact: %s", why)
	}
	if _, err := os.Lstat(f.ledgerPath); !os.IsNotExist(err) {
		t.Fatalf("incomplete record opening created a replacement tail: %v", err)
	}
}
