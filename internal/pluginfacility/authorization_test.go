package pluginfacility

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// .
// .
// .
// .
// .
// .
// .

func sealedLease(t *testing.T) *Lease {
	t.Helper()
	l := NewLease(1)
	if err := l.Release(); err != nil {
		t.Fatal(err)
	}
	if !l.Seal() {
		t.Fatal("an empty released ledger did not seal")
	}
	return l
}

func TestASealedLedgerAcknowledgesEveryLateHandOver(t *testing.T) {
	t.Run("an open ledger takes custody and says nothing", func(t *testing.T) {
		l := NewLease(1)
		if err := l.Hold("child", func() error { return nil }); err != nil {
			t.Fatalf("an open ledger refused custody: %v", err)
		}
		if got := l.Holds(); len(got) != 1 {
			t.Fatalf("held %v", got)
		}
	})
	t.Run("given straight back", func(t *testing.T) {
		l, calls := sealedLease(t), 0
		err := l.Hold("child", func() error { calls++; return nil })
		var sealed *SealedHoldError
		if !errors.As(err, &sealed) || !sealed.Released || !errors.Is(err, ErrSealed) || calls != 1 {
			t.Fatalf("err=%v calls=%d", err, calls)
		}
		if !l.Discharged() || len(l.Holds()) != 0 {
			t.Error("a sealed ledger recorded what it gave back")
		}
	})
	for _, mode := range []string{"error", "panic"} {
		t.Run("giving it back fails: "+mode, func(t *testing.T) {
			l, calls := sealedLease(t), 0
			err := l.Hold("child", func() error {
				calls++
				if mode == "panic" {
					panic("late stop panicked")
				}
				return errors.New("not yet reaped")
			})
			var sealed *SealedHoldError
			if !errors.As(err, &sealed) || sealed.Released || sealed.Err == nil || !errors.Is(err, ErrSealed) {
				t.Fatalf("the producer was not told it still owns the resource: %v", err)
			}
			// .
			// .
			if !l.Discharged() || len(l.Holds()) != 0 || len(l.Residue()) != 0 {
				t.Errorf("residue on a pruned ledger: holds=%v residue=%v", l.Holds(), l.Residue())
			}
			if calls != 1 {
				t.Errorf("the ledger asked %d times; the retry is the producer's now", calls)
			}
			if err := l.Release(); err != nil {
				t.Errorf("a later release of the sealed ledger found something: %v", err)
			}
		})
	}
	t.Run("a cleanup that blocks holds its producer, never the ledger", func(t *testing.T) {
		l := sealedLease(t)
		gate, entered, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
		go func() { done <- l.Hold("child", func() error { close(entered); <-gate; return nil }) }()
		<-entered
		// .
		read := make(chan struct{})
		go func() { _ = l.Discharged(); _ = l.Holds(); _ = l.Residue(); _ = l.Seal(); close(read) }()
		select {
		case <-read:
		case <-time.After(2 * time.Second):
			t.Fatal("a blocked late cleanup holds the ledger's lock")
		}
		close(gate)
		var sealed *SealedHoldError
		if err := <-done; !errors.As(err, &sealed) || !sealed.Released {
			t.Fatalf("after it unblocked: %v", err)
		}
	})
}

// .
// .
func TestHoldRacingSealLeavesExactlyOneOwner(t *testing.T) {
	for i := 0; i < 400; i++ {
		l := NewLease(1)
		if err := l.Release(); err != nil {
			t.Fatal(err)
		}
		var calls atomic.Int32
		var holdErr error
		var sealedOK bool
		var wg sync.WaitGroup
		wg.Add(2)
		go func() { defer wg.Done(); holdErr = l.Hold("late", func() error { calls.Add(1); return nil }) }()
		go func() { defer wg.Done(); sealedOK = l.Seal() }()
		wg.Wait()
		switch {
		case holdErr == nil:
			// .
			// .
			if sealedOK {
				t.Fatalf("round %d: sealed WITH something held: %v", i, l.Holds())
			}
			if len(l.Holds()) != 1 || calls.Load() != 0 {
				t.Fatalf("round %d: custody taken but holds=%v calls=%d", i, l.Holds(), calls.Load())
			}
			if l.Discharged() {
				t.Fatalf("round %d: discharged while it holds the late resource", i)
			}
			if err := l.Release(); err != nil || calls.Load() != 1 {
				t.Fatalf("round %d: the owner's release: err=%v calls=%d", i, err, calls.Load())
			}
		default:
			var sealed *SealedHoldError
			if !errors.As(holdErr, &sealed) || !sealed.Released || calls.Load() != 1 || len(l.Holds()) != 0 {
				t.Fatalf("round %d: refused custody but err=%v calls=%d holds=%v", i, holdErr, calls.Load(), l.Holds())
			}
		}
	}
}

// .

// .
// .
// .
type publishingRuntime struct {
	*sharedRuntime
	mu        sync.Mutex
	leases    map[string]*Lease
	opened    map[*Lease]bool
	entered   chan struct{}
	proceed   chan struct{}
	published atomic.Int32
	refused   atomic.Int32
}

func newPublishingRuntime() *publishingRuntime {
	return &publishingRuntime{sharedRuntime: newSharedRuntime(), leases: map[string]*Lease{}, opened: map[*Lease]bool{}, entered: make(chan struct{}, 8), proceed: make(chan struct{})}
}

func (p *publishingRuntime) Start(ctx context.Context, pr Prepared, l *Lease) (Running, error) {
	r, err := p.sharedRuntime.Start(ctx, pr, l)
	if err == nil {
		p.mu.Lock()
		p.leases[pr.Evidence.Package] = l
		p.mu.Unlock()
	}
	return r, err
}

func (p *publishingRuntime) Redirect(from, to Running) error {
	p.entered <- struct{}{}
	<-p.proceed
	p.mu.Lock()
	l := p.leases[to.(*fakeRunning).tag]
	p.mu.Unlock()
	if !l.Authorized() {
		p.refused.Add(1)
		return errors.New("withdrawn before it was published")
	}
	p.published.Add(1)
	p.mu.Lock()
	p.opened[l] = true
	p.mu.Unlock()
	return p.sharedRuntime.Redirect(from, to)
}

func TestAWithdrawnAttemptIsUnauthorizedBeforeTheWithdrawingCallReturns(t *testing.T) {
	for name, withdraw := range map[string]func(*Facility){
		"policy":    func(f *Facility) { f.Observe(obs("one.aiiospkg"), Policy{Revision: 2, Safe: true}) },
		"trust":     func(f *Facility) { f.Observe(obs("one.aiiospkg"), Policy{Revision: 1, TrustGen: 9}) },
		"uninstall": func(f *Facility) { f.Observe(nil, Policy{Revision: 1}) },
		"close":     func(f *Facility) { f.Close() },
	} {
		t.Run(name, func(t *testing.T) {
			rt := newPublishingRuntime()
			f := auditFacility(t, rt)
			var once sync.Once
			release := func() { once.Do(func() { close(rt.proceed) }) }
			t.Cleanup(release)
			f.Observe(obs("one.aiiospkg"), Policy{Revision: 1})
			select {
			case <-rt.entered:
			case <-time.After(3 * time.Second):
				t.Fatal("the redirect was never entered")
			}
			rt.mu.Lock()
			lease := rt.leases["one.aiiospkg"]
			rt.mu.Unlock()
			if !lease.Authorized() {
				t.Fatal("unauthorized before anything withdrew it")
			}
			if name == "close" {
				// .
				// .
				// .
				go withdraw(f)
				auditWaitFacility(t, "Close to withdraw the attempt it is waiting on", func() bool { return !lease.Authorized() })
			} else {
				withdraw(f)
				// .
				if lease.Authorized() {
					t.Fatalf("%s returned with the overtaken attempt still authorized to publish", name)
				}
			}
			release()
			auditWaitFacility(t, "the publication to be refused", func() bool { return rt.refused.Load() == 1 })
			// .
			// .
			rt.mu.Lock()
			opened := rt.opened[lease]
			rt.mu.Unlock()
			if opened {
				t.Error("a withdrawn attempt was published")
			}
			auditWaitFacility(t, "its child to be stopped", func() bool { return strings.Contains(rt.seen(), "stop:one.aiiospkg") })
		})
	}
}

// .
// .
func TestAnUnchangedIntentIsPublishedAndStaysAuthorized(t *testing.T) {
	rt := newPublishingRuntime()
	f := auditFacility(t, rt)
	close(rt.proceed)
	f.Observe(obs("one.aiiospkg"), Policy{Revision: 1})
	id := idOf("one.aiiospkg")
	auditWaitFacility(t, "it to serve", func() bool { return viewOf(f, id).State == StateActive })
	rt.mu.Lock()
	lease := rt.leases["one.aiiospkg"]
	rt.mu.Unlock()
	// .
	// .
	f.Observe(obs("one.aiiospkg"), Policy{Revision: 1})
	f.Observe(obs("one.aiiospkg"), Policy{Revision: 2})
	auditWaitPass(t, f)
	auditWaitFacility(t, "the readmission to settle", func() bool { return viewOf(f, id).State == StateActive })
	if !lease.Authorized() || rt.published.Load() != 1 || rt.refused.Load() != 0 {
		t.Errorf("authorized=%v published=%d refused=%d", lease.Authorized(), rt.published.Load(), rt.refused.Load())
	}
}
