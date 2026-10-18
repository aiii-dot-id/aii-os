package store

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestWakeNoticeAndWorkCommitTogether(t *testing.T) {
	s := wqStore(t)
	item := func(id string) *WorkItem {
		return &WorkItem{ID: "wake_" + id, Kind: "wake.timer", Payload: "{}", DedupKey: id, Source: "time"}
	}
	if _, err := s.DB().Exec(`CREATE TRIGGER refuse_wake BEFORE INSERT ON work_queue BEGIN SELECT RAISE(ABORT,'wake refused'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddOutboxNoticeWithWork("first", "kept together", item("first")); err == nil {
		t.Fatal("failed wake was acknowledged")
	}
	var floors, work, notices int
	for query, dst := range map[string]*int{`SELECT COUNT(*) FROM outbox`: &floors, `SELECT COUNT(*) FROM work_queue`: &work, `SELECT COUNT(*) FROM conversations`: &notices} {
		if err := s.DB().QueryRow(query).Scan(dst); err != nil {
			t.Fatal(err)
		}
	}
	if floors != 0 || work != 0 || notices != 0 {
		t.Fatalf("partial transaction: floor=%d work=%d transcript=%d", floors, work, notices)
	}
	if _, err := s.DB().Exec(`DROP TRIGGER refuse_wake`); err != nil {
		t.Fatal(err)
	}
	if added, err := s.AddOutboxNoticeWithWork("first", "kept together", item("first")); err != nil || !added {
		t.Fatalf("retry: %t %v", added, err)
	}
	w, err := s.ClaimWork(nil, time.Now().UnixMilli())
	if err != nil || w == nil {
		t.Fatalf("no committed work: %+v %v", w, err)
	}
	if err := s.CompleteWork(w.ID); err != nil {
		t.Fatal(err)
	}
	// A fresh rendering has a different clock stamp, not a new firing.
	if added, err := s.AddOutboxNoticeWithWork("first", "rendered later", item("first")); err != nil || added {
		t.Fatalf("completed firing duplicated: %t %v", added, err)
	}
	if _, err := s.AddOutboxNoticeOnce("legacy", "operator", "", "original legacy notice", nil); err != nil {
		t.Fatal(err)
	}
	if added, err := s.AddOutboxNoticeWithWork("legacy", "rendered later", item("legacy")); err != nil || !added {
		t.Fatalf("legacy floor silently lost its wake: %t %v", added, err)
	}
	var content string
	if err := s.DB().QueryRow(`SELECT content FROM outbox WHERE id='legacy'`).Scan(&content); err != nil || content != "original legacy notice" {
		t.Fatalf("legacy notice rewritten: %q %v", content, err)
	}
	bad := item("bad")
	bad.DedupKey = ""
	if _, err := s.AddOutboxNoticeWithWork("bad", "missing firing key", bad); err == nil {
		t.Fatal("wake without a firing key accepted")
	}
	changed := item("first")
	changed.Payload = `{"different":true}`
	if _, err := s.AddOutboxNoticeWithWork("first", "other", changed); err == nil {
		t.Fatal("one firing accepted different work")
	}
}

func TestWakeNoticeFreezeIsWholeAndRecoveryCanEnqueue(t *testing.T) {
	s := wqStore(t)
	w := &WorkItem{ID: "wake-frozen", Kind: "wake.timer", DedupKey: "frozen@1", Payload: "{}"}
	s.SetFrozen(true, "fixture SAFE")
	var frozen *FrozenError
	if _, err := s.AddOutboxNoticeWithWork("floor-frozen", "owed", w); !errors.As(err, &frozen) {
		t.Fatalf("SAFE wake must refuse with its typed cause: %v", err)
	}
	var n int
	if err := s.DB().QueryRow(`SELECT COUNT(*) FROM outbox`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("SAFE wrote floor: %d %v", n, err)
	}
	s.SetFrozen(false, "")
	if added, err := s.AddOutboxNoticeWithWork("floor-frozen", "owed", w); err != nil || !added {
		t.Fatalf("recovery lost wake: %t %v", added, err)
	}
}

func wqStore(t *testing.T) *Store {
	t.Helper()
	return testStore(t)
}

func TestEnqueueDedupSuppressesInFlightOnly(t *testing.T) {
	s := wqStore(t)

	id1, _ := s.EnqueueWork(&WorkItem{Kind: "wake.timer", Payload: "{}", DedupKey: "t7@123", Source: "time"})
	id2, _ := s.EnqueueWork(&WorkItem{Kind: "wake.timer", Payload: "{}", DedupKey: "t7@123", Source: "time"})
	if id1 != id2 {
		t.Fatalf("duplicate in-flight enqueue must suppress (same id), got %s vs %s", id1, id2)
	}

	// Claim it (in flight), then a third enqueue still suppresses.
	w, _ := s.ClaimWork(nil, time.Now().UnixMilli())
	if w == nil || w.ID != id1 {
		t.Fatalf("claim must return the pending item, got %+v", w)
	}
	id3, _ := s.EnqueueWork(&WorkItem{Kind: "wake.timer", Payload: "{}", DedupKey: "t7@123", Source: "time"})
	if id3 != id1 {
		t.Fatal("claimed item's dedup still suppresses")
	}

	// Terminal DONE does NOT block: a new firing (new deadline) is new work.
	s.CompleteWork(id1)
	id4, _ := s.EnqueueWork(&WorkItem{Kind: "wake.timer", Payload: "{}", DedupKey: "t7@123", Source: "time"})
	if id4 == id1 {
		t.Fatal("terminal rows must not block new work")
	}
}

func TestLeaseSweepRecoversCrashedWork(t *testing.T) {
	s := wqStore(t)
	id, _ := s.EnqueueWork(&WorkItem{Kind: "alarm.dream", Payload: "{}", Source: "time", LeaseMs: 50})

	w, _ := s.ClaimWork(nil, time.Now().UnixMilli())
	if w == nil || w.ID != id {
		t.Fatal("claim failed")
	}
	// Simulated crash: nothing completes. Lease elapses.
	time.Sleep(70 * time.Millisecond)
	n, err := s.SweepExpiredLeases(time.Now().UnixMilli())
	if err != nil || n != 1 {
		t.Fatalf("sweep must recover 1 expired lease, got %d %v", n, err)
	}
	// Back to PENDING — but NOT instantly: an expired lease is a
	// failure, and reclaiming it in the same breath repeats it.
	if w2, _ := s.ClaimWork(nil, time.Now().UnixMilli()); w2 != nil {
		t.Fatal("swept work was reclaimable immediately — the backoff is not applied")
	}
	// Past the backoff, claimable again with retry_count incremented.
	w2, _ := s.ClaimWork(nil, time.Now().UnixMilli()+retryBackoffBaseMs+100)
	if w2 == nil || w2.ID != id || w2.RetryCount != 1 {
		t.Fatalf("swept work must be reclaimable with retry_count=1, got %+v", w2)
	}
}

func TestFailWorkRetriesThenFailsTerminally(t *testing.T) {
	s := wqStore(t)
	item := &WorkItem{Kind: "witness.anchor", Payload: "{}", Source: "substrate", MaxRetries: 2}
	id, _ := s.EnqueueWork(item)

	// Each attempt waits out its own backoff — 1s, then 2s. Claiming
	// with an advanced clock rather than sleeping keeps the test honest
	// about the delay without paying it.
	at := time.Now().UnixMilli()
	for i := 0; i < 2; i++ {
		w, _ := s.ClaimWork(nil, at)
		if w == nil {
			t.Fatalf("attempt %d: must claim", i)
		}
		if err := s.FailWork(id, "network down"); err != nil {
			t.Fatal(err)
		}
		// The row is not claimable again until its backoff elapses.
		if again, _ := s.ClaimWork(nil, at); again != nil {
			t.Fatalf("attempt %d: a failed row was reclaimed in the same pass — %d attempts burn back to back", i, 2)
		}
		at += retryBackoffBaseMs << (i + 1)
	}
	// retries exhausted → FAILED terminal, error preserved (forensics)
	w, _ := s.ClaimWork(nil, at)
	if w != nil {
		t.Fatal("exhausted work must not be claimable")
	}
	var state, errMsg string
	var retries int
	s.mu.RLock()
	s.db.QueryRow(`SELECT state, error_msg, retry_count FROM work_queue WHERE id = ?`, id).
		Scan(&state, &errMsg, &retries)
	s.mu.RUnlock()
	if state != "FAILED" || errMsg != "network down" || retries != 2 {
		t.Fatalf("forensics: state=%s err=%q retries=%d", state, errMsg, retries)
	}
	// Terminal rows are never swept.
	n, _ := s.SweepExpiredLeases(time.Now().UnixMilli() + 999999)
	if n != 0 {
		t.Fatal("sweep must never touch terminal rows")
	}
}

func TestSafeFreezeIsAForensicSnapshot(t *testing.T) {
	s := wqStore(t)
	id, _ := s.EnqueueWork(&WorkItem{Kind: "alarm.dream", Payload: "{}", Source: "time", LeaseMs: 10})
	w, _ := s.ClaimWork(nil, time.Now().UnixMilli())
	_ = w

	s.SetFrozen(true, "fixture SAFE")
	defer s.SetFrozen(false, "")

	// No claims, no sweeps under freeze — CLAIMED row stays exactly as
	// it was (the snapshot of what was in flight).
	if w2, _ := s.ClaimWork(nil, time.Now().UnixMilli()); w2 != nil {
		t.Fatal("freeze: no claims")
	}
	time.Sleep(20 * time.Millisecond)
	if n, err := s.SweepExpiredLeases(time.Now().UnixMilli()); n != 0 || err != nil {
		t.Fatalf("freeze: no sweeps — in-flight stays frozen as evidence, and SAFE reads as nothing to sweep: %d %v", n, err)
	}

	if _, err := s.EnqueueWork(&WorkItem{Kind: "alarm.dream", Payload: "{}", Source: "time"}); !errors.As(err, new(*FrozenError)) {
		t.Fatalf("freeze: enqueue must be refused — SAFE is a snapshot, and a snapshot that grows is not one: %v", err)
	}
	if _, _, err := s.EnqueueWorkWithSessionBelowLimit(
		&WorkItem{Kind: "subagent.run", Payload: "{}", Source: "verb"},
		10, "ws_frozen", SubagentDescription("frozen"),
	); !errors.As(err, new(*FrozenError)) {
		t.Fatalf("freeze: the below-limit enqueue path is the same write and must refuse too: %v", err)
	}
	// And nothing was added: the snapshot is exactly what was in flight.
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM work_queue`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("the frozen queue grew to %d rows", n)
	}
	_ = id
}

func TestWorkAndSessionAreAtomic(t *testing.T) {
	s := wqStore(t)
	if err := s.StartWorkSession(t.Context(), "ws_duplicate", "existing"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.EnqueueWorkWithSessionBelowLimit(
		&WorkItem{Kind: "subagent.run", Payload: "{}", DedupKey: "ws_duplicate", Source: "identity"},
		1, "ws_duplicate", SubagentDescription("new"),
	); err == nil {
		t.Fatal("duplicate session must fail the spawn transaction")
	}
	var queued int
	if err := s.db.QueryRow(
		`SELECT COUNT(*) FROM work_queue WHERE dedup_key = 'ws_duplicate'`,
	).Scan(&queued); err != nil {
		t.Fatal(err)
	}
	if queued != 0 {
		t.Fatalf("failed session insert left %d runnable queue rows", queued)
	}
}

func TestFreezeBlocksCompleteAndFail(t *testing.T) {
	s := wqStore(t)
	id, _ := s.EnqueueWork(&WorkItem{Kind: "alarm.dream", Payload: "{}", Source: "time"})
	w, _ := s.ClaimWork(nil, time.Now().UnixMilli())
	if w == nil || w.ID != id {
		t.Fatal("claim")
	}
	s.SetFrozen(true, "fixture SAFE")
	defer s.SetFrozen(false, "")

	if err := s.CompleteWork(id); !errors.As(err, new(*FrozenError)) || !strings.Contains(err.Error(), "stays CLAIMED") {
		t.Fatalf("freeze must refuse completion — forensic snapshot: %v", err)
	}
	if err := s.FailWork(id, "x"); !errors.As(err, new(*FrozenError)) || !strings.Contains(err.Error(), "stays CLAIMED") {
		t.Fatalf("freeze must refuse failure recording — forensic snapshot: %v", err)
	}
	var state string
	s.mu.RLock()
	s.db.QueryRow(`SELECT state FROM work_queue WHERE id = ?`, id).Scan(&state)
	s.mu.RUnlock()
	if state != "CLAIMED" {
		t.Fatalf("row must stay CLAIMED under freeze, got %s", state)
	}
}

// Priority zero-value is DEFAULT (5), not highest — a caller that
// forgets priority must not preempt everything.
func TestPriorityZeroMeansDefault(t *testing.T) {
	s := wqStore(t)
	forgot := &WorkItem{Kind: "plugin.agent.task", Payload: "{}", Source: "plugin:test"}
	id, _ := s.EnqueueWork(forgot)
	w, _ := s.ClaimWork(nil, time.Now().UnixMilli())
	if w == nil || w.Priority != 5 {
		t.Fatalf("unset priority must default to 5, got %+v", w)
	}
	_ = id

	// And explicit priorities survive.
	s.EnqueueWork(&WorkItem{Kind: "alarm.timers", Payload: "{}", Source: "time", Priority: 1})
	w2, _ := s.ClaimWork(nil, time.Now().UnixMilli())
	if w2 == nil || w2.Priority != 1 {
		t.Fatalf("explicit priority 1 must survive, got %+v", w2)
	}
}
