package store

// .
// .
// .
// .
// .
// .

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

// .
// .
// .
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
					return
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

// .
// .
// .
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

	// .
	// .
	// .
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

	// .
	// .
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

// .
// .
// .
// .
func TestFreezeStopsEveryMutator(t *testing.T) {
	s := wqStore(t)
	now := time.Now().UnixMilli()
	if _, err := s.EnqueueWork(&WorkItem{
		Kind: "wake.timer", Payload: "{}", DedupKey: "frozen-live", Source: "time",
	}); err != nil {
		t.Fatal(err)
	}
	// .
	// .
	// .
	// .
	if _, err := s.EnqueueWork(&WorkItem{
		Kind: "wake.timer", Payload: "{}", DedupKey: "frozen-pending", Source: "time",
	}); err != nil {
		t.Fatal(err)
	}
	live, err := s.ClaimWork(nil, now)
	if err != nil || live == nil {
		t.Fatalf("setup claim: %v %+v", err, live)
	}

	s.SetWorkQueueFrozen(true)

	if _, err := s.EnqueueWork(&WorkItem{
		Kind: "wake.timer", Payload: "{}", DedupKey: "frozen-new", Source: "time",
	}); err == nil {
		t.Error("enqueue must refuse under a SAFE freeze")
	}
	if n, err := s.PendingWorkCount(); err != nil || n == 0 {
		t.Fatalf("inconclusive: a row must still be PENDING for the refusal to mean anything (n=%d, %v)", n, err)
	}
	if item, err := s.ClaimWork(nil, now); err != nil || item != nil {
		t.Errorf("claim must hand out nothing under a SAFE freeze — a row was available and was handed over anyway: %+v %v", item, err)
	}
	if err := s.CompleteWork(live.ID); err == nil {
		t.Error("completion must be withheld: the row is the forensic record")
	}
	if err := s.FailWork(live.ID, "x"); err == nil {
		t.Error("failure must be withheld: the row is the forensic record")
	}
	// .
	// .
	if n, err := s.SweepExpiredLeases(now + 86_400_000); err != nil || n != 0 {
		t.Errorf("sweep must move nothing under a SAFE freeze, moved %d (%v)", n, err)
	}
}

// .
// .
// .
func TestFreezeDuringConcurrentClaims(t *testing.T) {
	s := wqStore(t)
	// .
	// .
	// .
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
					return
				}
				mu.Lock()
				claims[item.ID]++
				mu.Unlock()
			}
		}()
	}
	s.SetWorkQueueFrozen(true)
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
