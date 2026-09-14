package store

import (
	"path/filepath"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/crypto"
	"github.com/aiii-dot-id/aii-os/internal/ledger"
)

func TestReplayAll(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "aii.db")
	ledgerPath := filepath.Join(dir, "ledger.jsonl")

	kp, _ := crypto.GenerateKeyPair()
	lg, _ := ledger.New(ledgerPath)

	// .
	evt1, _ := lg.Append(ledger.EventRing0Genesis, kp.Fingerprint(), 0,
		map[string]string{"name": "test"}, kp)
	evt2, _ := lg.Append(ledger.EventBeliefUpsert, kp.Fingerprint(), 3,
		map[string]interface{}{"id": "b1", "statement": "test belief", "ring": 3, "confidence": 0.8}, kp)
	evt3, _ := lg.Append(ledger.EventExperienceCreate, kp.Fingerprint(), 3,
		map[string]string{"id": "e1", "content": "hello", "category": "test"}, kp)
	lg.Close()

	// .
	s1, _ := New(dbPath)
	s1.Materialize(evt1)
	s1.Materialize(evt2)
	s1.Materialize(evt3)
	s1.AddConversationTurn("operator", "hello")

	// .
	beliefs, _ := s1.ListBeliefs()
	if len(beliefs) != 1 {
		t.Fatalf("expected 1 belief before replay, got %d", len(beliefs))
	}

	turns, _ := s1.RecentTurns(10)
	if len(turns) != 1 {
		t.Fatalf("expected 1 turn before replay, got %d", len(turns))
	}

	s1.Close()

	// .
	s2, _ := New(dbPath)
	defer s2.Close()

	// .
	err := s2.ReplayFromFile(ledgerPath)
	if err != nil {
		t.Fatalf("ReplayFromFile failed: %v", err)
	}

	// .
	beliefs, _ = s2.ListBeliefs()
	if len(beliefs) != 1 {
		t.Errorf("expected 1 belief after replay, got %d", len(beliefs))
	}
	if len(beliefs) > 0 && beliefs[0].Statement != "test belief" {
		t.Errorf("belief statement = %q", beliefs[0].Statement)
	}

	// .
	// .
	// .
	// .
	turns, _ = s2.RecentTurns(10)
	if len(turns) != 1 {
		t.Errorf("expected 1 turn to survive replay (conversations are store-only), got %d", len(turns))
	}

	// .
	stats, _ := s2.GetStats()
	if stats.LedgerSeq != 3 {
		t.Errorf("ledger seq = %d, want 3", stats.LedgerSeq)
	}
}

// .
// .
// .
// .
func TestReplayPreservesStoreOnlyTables(t *testing.T) {
	s := testStore(t)
	kp := testKeyPair(t)
	l := testLedger(t, s)

	evt, _ := l.Append(ledger.EventRing0Genesis, kp.Fingerprint(), 0,
		map[string]string{"name": "T"}, kp)
	s.Materialize(evt)
	s.AddConversationTurn("operator", "hello")
	s.AddOutboxMessage("m1", "operator", "", "msg", nil)

	events, _ := ledger.ReadAll(l.Path())
	if err := s.ReplayAll(EventSlice(events)); err != nil {
		t.Fatalf("replay: %v", err)
	}

	turns, err := s.RecentTurns(10)
	if err != nil || len(turns) != 1 || turns[0].Content != "hello" {
		t.Errorf("conversations lost to replay: %v %v", turns, err)
	}
	msgs, _ := s.UndeliveredMessages()
	if len(msgs) != 1 {
		t.Errorf("outbox lost to replay: %d", len(msgs))
	}
}
