package store

// workqueue_concurrency_test.go — the store's first concurrent tests.
//
// The store carries 107 locking methods and, until this file, not one
// test that ran two of them at once. Everything below is ordinary
// usage from several goroutines; the point is that the laws hold when
// the calls overlap, and that -race gets to watch the lock paths.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

// A claimed row belongs to exactly one worker. This is the queue's
// whole reason to exist, and it is a deterministic law: any double
// claim is a real defect, not a timing artefact.
func TestClaimIsExclusiveUnderConcurrency(t *testing.T) {
	s := wqStore(t)
	const items, workers = 200, 8
	for i := 0; i < items; i++ {
		if _, err := s.EnqueueWork(&WorkItem{
			Kind: "wake.timer", Payload: "{}",
			DedupKey: fmt.Sprintf("exclusive-%d", i), Source: "time",
		}); err != nil {
			t.Fatal(err)
		}
	}

	var mu sync.Mutex
	claims := map[string]int{}
	var wg sync.WaitGroup
	now := time.Now().UnixMilli()
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				item, err := s.ClaimWork(nil, now)
				if err != nil {
					t.Errorf("claim: %v", err)
					return
				}
				if item == nil {
					return // drained
				}
				mu.Lock()
				claims[item.ID]++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()

	if len(claims) != items {
		t.Errorf("%d distinct rows claimed, want %d — work was lost or invented", len(claims), items)
	}
	for id, n := range claims {
		if n != 1 {
			t.Errorf("row %s claimed %d times: two workers were handed the same work", id, n)
		}
	}
}

// Enqueue and claim racing each other must still never lose a row nor
// hand one out twice. Producers and consumers overlap here, so the
// enqueue path and the claim path hold the same lock in anger.
func TestEnqueueAndClaimOverlapWithoutLoss(t *testing.T) {
	s := wqStore(t)
	const items, producers, consumers = 120, 4, 4

	var mu sync.Mutex
	claims := map[string]int{}
	now := time.Now().UnixMilli()

	record := func(item *WorkItem) {
		mu.Lock()
		claims[item.ID]++
		mu.Unlock()
	}

	// Bounded attempts, never an open spin: consumers racing an empty
	// queue would otherwise hammer SQLite through the store lock and
	// starve the producers they are supposed to be overlapping with.
	var cwg sync.WaitGroup
	for c := 0; c < consumers; c++ {
		cwg.Add(1)
		go func() {
			defer cwg.Done()
			for i := 0; i < items; i++ {
				item, err := s.ClaimWork(nil, now)
				if err != nil {
					t.Errorf("claim: %v", err)
					return
				}
				if item != nil {
					record(item)
				}
			}
		}()
	}

	var pwg sync.WaitGroup
	for p := 0; p < producers; p++ {
		pwg.Add(1)
		go func(p int) {
			defer pwg.Done()
			for i := 0; i < items/producers; i++ {
				if _, err := s.EnqueueWork(&WorkItem{
					Kind: "wake.timer", Payload: "{}",
					DedupKey: fmt.Sprintf("overlap-%d-%d", p, i), Source: "time",
				}); err != nil {
					t.Errorf("enqueue: %v", err)
					return
				}
			}
		}(p)
	}
	pwg.Wait()
	cwg.Wait()

	// Serial drain. Whatever the overlap left behind is accounted for
	// here, so the totals below need no timing luck to be exact.
	for {
		item, err := s.ClaimWork(nil, now)
		if err != nil {
			t.Fatalf("drain: %v", err)
		}
		if item == nil {
			break
		}
		record(item)
	}

	if len(claims) != items {
		t.Errorf("%d distinct rows claimed, want %d", len(claims), items)
	}
	for id, n := range claims {
		if n != 1 {
			t.Errorf("row %s claimed %d times under producer/consumer overlap", id, n)
		}
	}
}

// The SAFE freeze is a stop, and it stops every mutator — not most of
// them. Once SetFrozen has returned, the rows are a forensic
// snapshot: nothing may enter, be handed out, be completed, be failed,
// or be swept back to pending.
func TestFreezeStopsEveryMutator(t *testing.T) {
	s := wqStore(t)
	now := time.Now().UnixMilli()
	if _, err := s.EnqueueWork(&WorkItem{
		Kind: "wake.timer", Payload: "{}", DedupKey: "frozen-live", Source: "time",
	}); err != nil {
		t.Fatal(err)
	}

	if _, err := s.EnqueueWork(&WorkItem{
		Kind: "wake.timer", Payload: "{}", DedupKey: "frozen-pending", Source: "time",
	}); err != nil {
		t.Fatal(err)
	}
	live, err := s.ClaimWork(nil, now)
	if err != nil || live == nil {
		t.Fatalf("setup claim: %v %+v", err, live)
	}

	s.SetFrozen(true, "fixture SAFE")

	if _, err := s.EnqueueWork(&WorkItem{
		Kind: "wake.timer", Payload: "{}", DedupKey: "frozen-new", Source: "time",
	}); !errors.As(err, new(*FrozenError)) {
		t.Errorf("enqueue must refuse under a SAFE freeze: %v", err)
	}
	if n, err := s.PendingWorkCount(); err != nil || n == 0 {
		t.Fatalf("inconclusive: a row must still be PENDING for the refusal to mean anything (n=%d, %v)", n, err)
	}
	if item, err := s.ClaimWork(nil, now); err != nil || item != nil {
		t.Errorf("claim must hand out nothing under a SAFE freeze — a row was available and was handed over anyway: %+v %v", item, err)
	}
	if err := s.CompleteWork(live.ID); !errors.As(err, new(*FrozenError)) {
		t.Errorf("completion must be withheld: the row is the forensic record: %v", err)
	}
	if err := s.FailWork(live.ID, "x"); !errors.As(err, new(*FrozenError)) {
		t.Errorf("failure must be withheld: the row is the forensic record: %v", err)
	}
	// Far past any lease: unfrozen this would sweep the claimed row
	// back to pending, which is precisely a row changing state.
	if n, err := s.SweepExpiredLeases(now + 86_400_000); err != nil || n != 0 {
		t.Errorf("sweep must move nothing under a SAFE freeze, moved %d (%v)", n, err)
	}
}

// The freeze landing WHILE claims are in flight. The invariant that
// holds without timing luck: whatever happens, no row is handed out
// twice, and once the dust settles the queue is still frozen shut.
func TestFreezeDuringConcurrentClaims(t *testing.T) {
	s := wqStore(t)
	// attempts is bounded so workers*attempts < items: the queue cannot
	// be drained, so the refusal at the end is the freeze speaking and
	// not an empty table.
	const items, workers, attempts = 150, 6, 10
	now := time.Now().UnixMilli()
	for i := 0; i < items; i++ {
		if _, err := s.EnqueueWork(&WorkItem{
			Kind: "wake.timer", Payload: "{}",
			DedupKey: fmt.Sprintf("freeze-race-%d", i), Source: "time",
		}); err != nil {
			t.Fatal(err)
		}
	}

	var mu sync.Mutex
	claims := map[string]int{}
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < attempts; i++ {
				item, err := s.ClaimWork(nil, now)
				if err != nil {
					t.Errorf("claim: %v", err)
					return
				}
				if item == nil {
					return // frozen — a stop
				}
				mu.Lock()
				claims[item.ID]++
				mu.Unlock()
			}
		}()
	}
	s.SetFrozen(true, "fixture SAFE")
	wg.Wait()

	for id, n := range claims {
		if n != 1 {
			t.Errorf("row %s claimed %d times while the freeze landed", id, n)
		}
	}
	if n, err := s.PendingWorkCount(); err != nil || n == 0 {
		t.Fatalf("inconclusive: rows must remain for the refusal to mean anything (n=%d, %v)", n, err)
	}
	if item, err := s.ClaimWork(nil, now); err != nil || item != nil {
		t.Errorf("the queue must stay shut after the freeze, yet a waiting row was handed over: %+v %v", item, err)
	}
}

func TestSafeEntryWaitsForNoOpenStatement(t *testing.T) {
	s := wqStore(t)
	now := time.Now().UnixMilli()
	for _, key := range []string{"parked", "pending"} {
		if _, err := s.EnqueueWork(&WorkItem{Kind: "wake.timer", Payload: "{}", DedupKey: key, Source: "time"}); err != nil {
			t.Fatal(err)
		}
	}
	parked, err := s.ClaimWork(nil, now)
	if err != nil || parked == nil || parked.DedupKey != "parked" {
		t.Fatalf("setup claim: %+v %v", parked, err)
	}

	// Another connection takes the database's write lock and keeps it.
	var path string
	if err := s.db.QueryRow(`SELECT file FROM pragma_database_list WHERE name = 'main'`).Scan(&path); err != nil || path == "" {
		t.Fatalf("the store's file: %q %v", path, err)
	}
	holder, err := sql.Open("sqlite", sqliteDSN(path))
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Close()
	ctx := context.Background()
	conn, err := holder.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		t.Fatal(err)
	}
	var releaseOnce sync.Once
	release := func() {
		releaseOnce.Do(func() {
			_, _ = conn.ExecContext(ctx, "ROLLBACK")
			_ = conn.Close()
		})
	}
	defer release()

	completed := make(chan error, 1)
	go func() { completed <- s.CompleteWork(parked.ID) }()
	// Parked: the completion holds the writer lock and waits on the
	// database (busy_timeout, 5 s). The handle's check is the line after
	// the lock; the pause carries it well past that, into the statement.
	for deadline := time.Now().Add(5 * time.Second); s.mu.TryRLock(); {
		s.mu.RUnlock()
		if time.Now().After(deadline) {
			t.Fatal("inconclusive: the completion never took the writer lock")
		}
		time.Sleep(time.Millisecond)
	}
	time.Sleep(100 * time.Millisecond)

	entered := make(chan struct{})
	go func() {
		s.SetFrozen(true, "fixture SAFE")
		close(entered)
	}()
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Error("SAFE entry waited behind a statement parked on the database; it waits for nothing")
	}
	select {
	case err := <-completed:
		t.Fatalf("inconclusive: the completion was not parked while SAFE entry ran: %v", err)
	default:
	}

	// The database frees, and the statement open when SAFE arrived finishes.
	release()
	select {
	case err := <-completed:
		if err != nil {
			t.Fatalf("the statement open when SAFE arrived was refused, not finished: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the parked completion never returned")
	}
	<-entered
	var state string
	if err := s.db.QueryRow(`SELECT state FROM work_queue WHERE id = ?`, parked.ID).Scan(&state); err != nil || state != "DONE" {
		t.Fatalf("the open statement's write did not land: %q %v", state, err)
	}

	// Nothing new moves: the handle refuses every queue write.
	if item, err := s.ClaimWork(nil, now); err != nil || item != nil {
		t.Errorf("a claim after SAFE entry handed out a waiting row: %+v %v", item, err)
	}
	if _, err := s.EnqueueWork(&WorkItem{Kind: "wake.timer", Payload: "{}", DedupKey: "after", Source: "time"}); !errors.As(err, new(*FrozenError)) {
		t.Errorf("an enqueue after SAFE entry was not refused by the handle: %v", err)
	}
	if n, err := s.PendingWorkCount(); err != nil || n != 1 {
		t.Errorf("the snapshot moved: %d outstanding rows, want the one PENDING (%v)", n, err)
	}
}
