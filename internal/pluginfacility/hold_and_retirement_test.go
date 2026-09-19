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

func leaseOf(rt *publishingRuntime, pkg string) *Lease {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	return rt.leases[pkg]
}

// .

func TestRetirementBeginsAndAuthorizationEnds(t *testing.T) {
	serve := func(t *testing.T) (*Facility, *publishingRuntime, *Lease) {
		rt := newPublishingRuntime()
		close(rt.proceed)
		// .
		rt.stopPendingFor["one.aiiospkg"] = true
		f := auditFacility(t, rt)
		f.Observe(obs("one.aiiospkg"), Policy{Revision: 1})
		auditWaitFacility(t, "it to serve", func() bool { return viewOf(f, idOf("one.aiiospkg")).State == StateActive })
		l := leaseOf(rt, "one.aiiospkg")
		if !l.Authorized() {
			t.Fatal("a serving activation is not authorized")
		}
		return f, rt, l
	}
	t.Run("uninstall: before Observe returns", func(t *testing.T) {
		f, _, l := serve(t)
		f.Observe(nil, Policy{Revision: 1})
		if l.Authorized() {
			t.Fatal("Observe returned with the uninstalled release still open to new calls")
		}
		auditWaitFacility(t, "it to drain", func() bool { return viewOf(f, idOf("one.aiiospkg")).State == StateDraining })
		if l.Authorized() {
			t.Error("a draining activation is open to new calls")
		}
	})
	t.Run("policy refusal at the re-ask", func(t *testing.T) {
		f, _, l := serve(t)
		f.Observe(obs("one.aiiospkg"), Policy{Revision: 2, Allows: func(Evidence) (bool, string) { return false, "the level was raised" }})
		auditWaitFacility(t, "it to be withdrawn", func() bool { return !l.Authorized() })
		auditWaitFacility(t, "it to stop serving", func() bool { return viewOf(f, idOf("one.aiiospkg")).State != StateActive })
	})
	t.Run("the host stopping", func(t *testing.T) {
		f, _, l := serve(t)
		f.Close()
		if l.Authorized() {
			t.Error("the host stopped and what it was stopping stayed open to new calls")
		}
	})
	t.Run("an update: the predecessor closes, the successor does not", func(t *testing.T) {
		f, rt, old := serve(t)
		f.Observe(obs("one.aiiospkg", "two.aiiospkg"), Policy{Revision: 1})
		set := obs("one.aiiospkg")
		set[0].Package, set[0].Hash = "one-v2.aiiospkg", "sha256:one-v2"
		f.Observe(append(set, obs("two.aiiospkg")...), Policy{Revision: 1})
		auditWaitFacility(t, "the successor to serve", func() bool {
			l := leaseOf(rt, "one-v2.aiiospkg")
			return l != nil && viewOf(f, idOf("one.aiiospkg")).State == StateActive && !old.Authorized()
		})
		if l := leaseOf(rt, "one-v2.aiiospkg"); !l.Authorized() {
			t.Error("the successor was closed with its predecessor")
		}
		auditWaitFacility(t, "the other plugin to serve", func() bool { return viewOf(f, idOf("two.aiiospkg")).State == StateActive })
		if l := leaseOf(rt, "two.aiiospkg"); !l.Authorized() {
			t.Error("another plugin's authorization was touched")
		}
	})
}

// .

func TestAHoldWithdrawsWhatIsOnItsWayAndTouchesNothingThatServes(t *testing.T) {
	rt := newPublishingRuntime()
	f := auditFacility(t, rt)
	var once sync.Once
	release := func() { once.Do(func() { close(rt.proceed) }) }
	t.Cleanup(release)
	serving, rising := idOf("serving.aiiospkg"), idOf("rising.aiiospkg")

	// .
	f.Observe(obs("serving.aiiospkg"), Policy{Revision: 1})
	<-rt.entered
	rt.proceed <- struct{}{}
	auditWaitFacility(t, "the first to serve", func() bool { return viewOf(f, serving).State == StateActive })
	f.Observe(obs("serving.aiiospkg", "rising.aiiospkg"), Policy{Revision: 1})
	select {
	case <-rt.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("the second never reached its redirect")
	}
	stopsBefore := strings.Count(rt.seen(), "stop:serving.aiiospkg")
	verifiesBefore := strings.Count(rt.seen(), "verify:serving.aiiospkg")

	f.Hold("the identity is in SAFE")
	// .
	if leaseOf(rt, "rising.aiiospkg").Authorized() {
		t.Fatal("Hold returned with an attempt in flight still authorized to publish")
	}
	// .
	if !leaseOf(rt, "serving.aiiospkg").Authorized() {
		t.Fatal("the hold closed a serving activation")
	}
	release()
	auditWaitFacility(t, "the withdrawn attempt's publication to be refused", func() bool { return rt.refused.Load() == 1 })
	auditWaitFacility(t, "the withdrawn attempt's child to be stopped", func() bool { return strings.Contains(rt.seen(), "stop:rising.aiiospkg") })
	auditWaitPass(t, f)

	v := viewOf(f, rising)
	if v.State == StateActive || v.Held == "" || !strings.Contains(v.Held, "SAFE") {
		t.Errorf("what the hold keeps from starting is not shown held: state=%s held=%q", v.State, v.Held)
	}
	if s := viewOf(f, serving); s.State != StateActive || s.Held != "" {
		t.Errorf("what serves is not simply serving: state=%s held=%q", s.State, s.Held)
	}

	// .
	// .
	starts := strings.Count(rt.seen(), "start:")
	f.Observe(obs("serving.aiiospkg", "rising.aiiospkg"), Policy{Revision: 1, Hold: true, HoldWhy: "the identity is in SAFE"})
	f.Retry(rising)
	auditWaitPass(t, f)
	time.Sleep(100 * time.Millisecond)
	if n := strings.Count(rt.seen(), "start:"); n != starts {
		t.Errorf("%d start(s) while the host held still: %s", n-starts, rt.seen())
	}
	// .
	set := obs("serving.aiiospkg", "rising.aiiospkg")
	set[0].Package, set[0].Hash = "serving-v2.aiiospkg", "sha256:v2"
	f.Observe(set, Policy{Revision: 1, Hold: true, HoldWhy: "the identity is in SAFE"})
	auditWaitPass(t, f)
	if s := viewOf(f, serving); s.State != StateActive || s.Held == "" {
		t.Errorf("an update waiting behind a hold: state=%s held=%q", s.State, s.Held)
	}
	if strings.Contains(rt.seen(), "start:serving-v2.aiiospkg") {
		t.Error("new code was started while the host held still")
	}
	// .
	if n := strings.Count(rt.seen(), "stop:serving.aiiospkg"); n != stopsBefore {
		t.Errorf("the hold stopped what serves (%d)", n-stopsBefore)
	}
	if n := strings.Count(rt.seen(), "verify:serving.aiiospkg"); n != verifiesBefore {
		t.Errorf("the hold re-asked what serves (%d verifies)", n-verifiesBefore)
	}

	// .
	f.Observe(obs("rising.aiiospkg"), Policy{Revision: 1, Hold: true, HoldWhy: "the identity is in SAFE"})
	auditWaitFacility(t, "an uninstall during the hold to stop it", func() bool { return strings.Contains(rt.seen(), "stop:serving.aiiospkg") })

	// .
	// .
	// .
	f.Observe(obs("rising.aiiospkg"), Policy{Revision: 1})
	auditWaitPass(t, f)
	if viewOf(f, rising).State == StateActive {
		t.Fatal("an observation without the hold ended a hold the host had declared")
	}
	if !f.Unhold(func() bool { return false }) {
		t.Fatal("the host could not release its own hold")
	}
	f.Observe(obs("rising.aiiospkg"), Policy{Revision: 1})
	auditWaitFacility(t, "the held plugin to serve once the hold ends", func() bool { return viewOf(f, rising).State == StateActive })
}

// .

// .
// .
type blockingReask struct {
	*sharedRuntime
	calls   atomic.Int32
	entered chan struct{}
}

func (b *blockingReask) Verify(ctx context.Context, pkg string) (Evidence, error) {
	if pkg == "one.aiiospkg" && b.calls.Add(1) == 2 {
		close(b.entered)
		<-ctx.Done()
		return Evidence{}, ctx.Err()
	}
	return b.sharedRuntime.Verify(ctx, pkg)
}

func TestACancelledReaskStopsNothing(t *testing.T) {
	rt := &blockingReask{sharedRuntime: newSharedRuntime(), entered: make(chan struct{})}
	f := auditFacility(t, rt)
	id := idOf("one.aiiospkg")
	f.Observe(obs("one.aiiospkg"), Policy{Revision: 1})
	auditWaitFacility(t, "it to serve", func() bool { return viewOf(f, id).State == StateActive })
	f.Observe(obs("one.aiiospkg"), Policy{Revision: 2})
	auditExecutorWait(t, rt.entered, "the re-ask to be verifying")
	// .
	// .
	set := obs("one.aiiospkg")
	set[0].Package, set[0].Hash = "one-v2.aiiospkg", "sha256:one-v2"
	f.Observe(set, Policy{Revision: 2})
	auditWaitFacility(t, "the successor to serve", func() bool {
		v := viewOf(f, id)
		return v.State == StateActive && strings.Contains(rt.seen(), "redirect:one.aiiospkg->one-v2.aiiospkg")
	})
	// .
	// .
	seen := rt.seen()
	if stop, redirect := strings.Index(seen, "stop:one.aiiospkg"), strings.Index(seen, "redirect:one.aiiospkg->one-v2.aiiospkg"); stop >= 0 && stop < redirect {
		t.Errorf("a cancelled re-ask stopped the serving engine before its successor served: %s", seen)
	}
	if r := viewOf(f, id).Refusal; r != nil && errors.Is(r.Cause, context.Canceled) {
		t.Errorf("a cancellation is on the card as a refusal: %v", r)
	}
}

// .
// .
// .
func TestACancelledReaskIsAskedAgain(t *testing.T) {
	rt := &blockingReask{sharedRuntime: newSharedRuntime(), entered: make(chan struct{})}
	rt.startErrFor["one-v2.aiiospkg"] = errors.New("the new release does not start")
	f := auditFacility(t, rt)
	id := idOf("one.aiiospkg")
	f.Observe(obs("one.aiiospkg"), Policy{Revision: 1})
	auditWaitFacility(t, "it to serve", func() bool { return viewOf(f, id).State == StateActive })
	f.Observe(obs("one.aiiospkg"), Policy{Revision: 2})
	auditExecutorWait(t, rt.entered, "the re-ask to be verifying")
	set := obs("one.aiiospkg")
	set[0].Package, set[0].Hash = "one-v2.aiiospkg", "sha256:one-v2"
	f.Observe(set, Policy{Revision: 2})
	auditWaitFacility(t, "the failed update", func() bool { return strings.Contains(rt.seen(), "start:one-v2.aiiospkg") })
	// .
	// .
	f.Observe(obs("one.aiiospkg"), Policy{Revision: 2})
	auditWaitFacility(t, "the unanswered question to be asked again", func() bool { return rt.calls.Load() >= 3 })
	auditWaitFacility(t, "it to go on serving", func() bool { return viewOf(f, id).State == StateActive })
}

// .

// .
// .
// .
// .
// .
func TestASelectedWithdrawalThatLosesTheRaceLeavesNoZombie(t *testing.T) {
	rt := newPublishingRuntime()
	f := auditFacility(t, rt)
	id := idOf("one.aiiospkg")
	prior := AuditAfterWithdrawalSelected
	t.Cleanup(func() { AuditAfterWithdrawalSelected = prior })

	f.Observe(obs("one.aiiospkg"), Policy{Revision: 1})
	<-rt.entered
	first := leaseOf(rt, "one.aiiospkg")
	AuditAfterWithdrawalSelected = func() {
		// .
		close(rt.proceed)
		auditWaitFacility(t, "the selected candidate to complete", func() bool { return viewOf(f, id).State == StateActive })
	}
	f.Observe(obs("one.aiiospkg"), Policy{Revision: 2})
	AuditAfterWithdrawalSelected = prior
	if first.Authorized() {
		t.Fatal("the selected activation kept its authorization")
	}
	// .
	// .
	auditWaitFacility(t, "the unauthorized activation to be retired and replaced", func() bool {
		l := leaseOf(rt, "one.aiiospkg")
		return l != nil && l != first && l.Authorized() && viewOf(f, id).State == StateActive && strings.Contains(rt.seen(), "stop:one.aiiospkg")
	})
}
