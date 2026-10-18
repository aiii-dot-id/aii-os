package pluginfacility

import (
	"context"
	"errors"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/supervisor"
)

type terminalFixture struct {
	*fakeRunning
	done     chan struct{}
	cause    error
	restarts []time.Time
}

func (r *terminalFixture) Terminated() <-chan struct{}   { return r.done }
func (r *terminalFixture) Failure() ([]time.Time, error) { return r.restarts, r.cause }

type terminalRuntime struct {
	*sharedRuntime
	started  chan *terminalFixture
	muStop   sync.Mutex
	stopGate chan struct{}
}

func (r *terminalRuntime) Start(ctx context.Context, p Prepared, l *Lease) (Running, error) {
	base, err := r.sharedRuntime.Start(ctx, p, l)
	if err != nil {
		return nil, err
	}
	x := &terminalFixture{fakeRunning: base.(*fakeRunning), done: make(chan struct{})}
	r.started <- x
	return x, nil
}
func (r *terminalRuntime) Redirect(from, to Running) error {
	var old Running
	if from != nil {
		old = from.(*terminalFixture).fakeRunning
	}
	return r.sharedRuntime.Redirect(old, to.(*terminalFixture).fakeRunning)
}
func (r *terminalRuntime) Stop(ctx context.Context, x Running) (Retirement, error) {
	r.muStop.Lock()
	gate := r.stopGate
	r.muStop.Unlock()
	if gate != nil {
		select {
		case <-gate:
		case <-ctx.Done():
			return Retirement{}, ctx.Err()
		}
	}
	return r.sharedRuntime.Stop(ctx, x.(*terminalFixture).fakeRunning)
}

func TestRuntimeDeathReportsItsGenerationAndRecovers(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		t.Run(map[bool]string{false: "window", true: "operator"}[explicit], func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				rt := &terminalRuntime{sharedRuntime: newSharedRuntime(), started: make(chan *terminalFixture, 4)}
				f := auditFacility(t, rt)
				f.Observe(obs("one.aiiospkg"), Policy{Revision: 1})
				synctest.Wait()
				if f.stateOf("id.example.one") != StateActive {
					t.Fatal("fixture not active")
				}
				first := <-rt.started
				now := time.Now()
				first.restarts = []time.Time{now, now, now, now, now}
				first.cause = &supervisor.RestartCeilingError{PluginID: first.id, Restarts: 5, RetryAt: now.Add(10 * time.Minute), Last: errors.New("guest trapped")}
				close(first.done)
				synctest.Wait()
				f.mu.Lock()
				i := f.instances[first.id]
				active, ref, spent, retry := i.Active(), i.LastRefusalUnder(intentOf(i.ID, i.Package, i.PackageHash, i.ManifestHash, f.policy)), i.Budget.Spent(time.Now()), i.RetryAt
				f.mu.Unlock()
				if active != nil || ref == nil || ref.Stage != StageCrashed || spent != 6 || !retry.Equal(now.Add(10*time.Minute)) {
					t.Fatalf("terminal state: active=%v ref=%v spent=%d retry=%s", active, ref, spent, retry)
				}
				select {
				case <-rt.started:
					t.Fatal("crash budget was bypassed")
				default:
				}
				if explicit {
					if !f.Retry(first.id) {
						t.Fatal("retry did not find plugin")
					}
				} else {
					time.Sleep(10 * time.Minute)
				}
				synctest.Wait()
				if f.stateOf(first.id) != StateActive {
					t.Fatalf("recovery did not activate: %+v", f.Snapshot())
				}
				second := <-rt.started
				if first == second {
					t.Fatal("recovery reused dead runtime")
				}
				// Late/duplicate failure cannot take down the newly serving child.
				f.mu.Lock()
				current := f.instances[first.id].Active()
				currentGen := current.Gen
				f.mu.Unlock()
				f.record(Event{PluginID: first.id, Gen: currentGen - 1, Kind: EventRefused, Refusal: ref, Restarts: first.restarts})
				synctest.Wait()
				f.mu.Lock()
				after := f.instances[first.id]
				remains := after.Active() != nil && after.Active().Gen == currentGen
				count := after.Budget.Spent(time.Now())
				cleanRetry := after.RetryAt.IsZero()
				f.mu.Unlock()
				if !remains || count != 1 || !cleanRetry {
					t.Fatalf("stale failure affected successor: remains=%v spent=%d retry-cleared=%v", remains, count, cleanRetry)
				}
			})
		})
	}
}

func TestRuntimeDeathDoesNotPublishSuccessfulRetirement(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rt := &terminalRuntime{sharedRuntime: newSharedRuntime(), started: make(chan *terminalFixture, 4)}
		rt.stopPendingFor["one.aiiospkg"] = true
		f := auditFacility(t, rt)
		f.Observe(obs("one.aiiospkg"), Policy{Revision: 1})
		synctest.Wait()
		first := <-rt.started
		first.cause = errors.New("child stopped but containment not released")
		close(first.done)
		synctest.Wait()
		f.mu.Lock()
		i := f.instances[first.id]
		a := i.Activations[0]
		pending := a.Role == RoleRetiring && a.Refusal != nil && a.Refusal.Cleanup != nil && !a.Lease.Discharged()
		f.mu.Unlock()
		if !pending {
			t.Fatalf("cleanup failure was called retired: %+v", f.Snapshot())
		}
		f.Retry(first.id)
		synctest.Wait()
		select {
		case <-rt.started:
			t.Fatal("new child started before old custody released")
		default:
		}
		rt.mu.Lock()
		rt.stopPendingFor["one.aiiospkg"] = false
		rt.mu.Unlock()
		f.Settle(first.id)
		f.Retry(first.id)
		synctest.Wait()
		if f.stateOf(first.id) != StateActive {
			t.Fatalf("cleanup recovery did not restore service: %+v", f.Snapshot())
		}
	})
}

func TestShutdownDuringRuntimeDeathCannotRestart(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rt := &terminalRuntime{sharedRuntime: newSharedRuntime(), started: make(chan *terminalFixture, 4), stopGate: make(chan struct{})}
		f := auditFacility(t, rt)
		f.Observe(obs("one.aiiospkg"), Policy{Revision: 1})
		synctest.Wait()
		first := <-rt.started
		first.cause = errors.New("fixture crash")
		close(first.done)
		synctest.Wait() // terminal handling is waiting in retirement
		f.mu.Lock()
		i := f.instances[first.id]
		visible := i.Active() == nil && i.Retiring() != nil && i.LastRefusal() != nil && i.LastRefusal().Stage == StageCrashed
		f.mu.Unlock()
		if !visible {
			t.Fatal("dead child was still advertised Active during cleanup")
		}
		closed := make(chan struct{})
		go func() { f.Close(); close(closed) }()
		synctest.Wait()
		close(rt.stopGate)
		synctest.Wait()
		select {
		case <-closed:
		default:
			t.Fatal("Close did not join retirement")
		}
		if f.Retry(first.id) {
			t.Fatal("closed facility accepted retry")
		}
		select {
		case <-rt.started:
			t.Fatal("shutdown restarted the plugin")
		default:
		}
	})
}
