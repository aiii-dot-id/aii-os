package pluginfacility

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"
)

// ended is a runtime cause that ended its own retries (Final).
type ended struct{ remedy string }

func (e ended) Error() string { return "the download stopped: the server answered 404" }
func (e ended) Final() string { return e.remedy }

// notReady is a child that did not report ready (Unready).
type notReady struct{}

func (notReady) Error() string { return "no readiness mark within 30s; killed" }
func (notReady) Unready() string {
	return "raise plugins.resources.id.example.one.startup_timeout_ms, then Try again."
}

// failingAcquire is the shared runtime whose material arrives only after
// what err says.
type failingAcquire struct {
	*sharedRuntime
	mu  sync.Mutex
	err error
	n   int
}

func (r *failingAcquire) Acquire(ctx context.Context, p Prepared, progress func(MaterialStatus)) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.n++
	return r.err
}

func (r *failingAcquire) attempts() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.n
}

func refusalOf(f *Facility, id string) (InstanceView, bool) {
	for _, v := range f.Snapshot().Instances {
		if v.ID == id && v.Refusal != nil {
			return v, true
		}
	}
	return InstanceView{}, false
}

// G1: A DOWNLOAD THE RUNTIME'S RULE ENDED IS A FINAL REFUSAL: material,
// permanent, its remedy on the card, no clock armed, nothing asked again on
// a poke — and the operator's Try again asks again.
func TestADownloadTheRuleEndedIsFinalUntilTryAgain(t *testing.T) {
	rt := &failingAcquire{sharedRuntime: newSharedRuntime(), err: ended{remedy: "Nothing is at the address the package declares. Try again once it is."}}
	rt.present = false
	f := newFacility(t, rt)
	f.Observe(obs("one.aiiospkg"), Policy{Revision: 1})
	until(t, "the refusal", func() bool { _, ok := refusalOf(f, "id.example.one"); return ok })
	view, _ := refusalOf(f, "id.example.one")
	r := view.Refusal
	if r.Stage != StageMaterial || r.Class != ClassPermanent || r.Remedy != "Nothing is at the address the package declares. Try again once it is." ||
		!r.WaitsOn(InputConfig) || !r.WaitsOn(InputPackage) || !view.RetryAt.IsZero() {
		t.Fatalf("an ended download is not final with its remedy: %+v, retry at %v", r, view.RetryAt)
	}
	for i := 0; i < 5; i++ {
		f.Poke("nothing changed")
		time.Sleep(20 * time.Millisecond)
	}
	settle()
	if n := rt.attempts(); n != 1 {
		t.Fatalf("an ended download was asked again without Try again: %d", n)
	}
	rt.mu.Lock()
	rt.err = nil
	rt.mu.Unlock()
	if !f.Retry("id.example.one") {
		t.Fatal("Try again found nothing")
	}
	until(t, "Try again to bring it up", func() bool { return f.stateOf("id.example.one") == StateActive })
}

// G4: A CHILD THAT DID NOT REPORT READY IS REFUSED AT READINESS, with the
// starting layer's remedy — never as a start that failed.
func TestAChildThatDoesNotReportReadyIsRefusedAtReadiness(t *testing.T) {
	rt := newSharedRuntime()
	rt.startErrFor["one.aiiospkg"] = notReady{}
	f := newFacility(t, rt)
	f.Observe(obs("one.aiiospkg"), Policy{Revision: 1})
	until(t, "the refusal", func() bool { _, ok := refusalOf(f, "id.example.one"); return ok })
	view, _ := refusalOf(f, "id.example.one")
	if r := view.Refusal; r.Stage != StageReadiness || !strings.Contains(r.Remedy, "startup_timeout_ms") || r.Class != ClassTransient || view.RetryAt.IsZero() {
		t.Fatalf("a readiness failure: %+v, retry at %v", r, view.RetryAt)
	}
}

// G4: ON A FIRST INSTALL ITS ATTEMPTS STAY SPENT. Five starts that never
// report ready — on the visible backoff — and the refusal is final: no
// clock, the remedy says so, and an hour later, past every window, nothing
// has been started again. Try again starts it again.
func TestAFirstInstallThatNeverReportsReadyStopsUntilTryAgain(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rt := newSharedRuntime()
		rt.startErrFor["one.aiiospkg"] = notReady{}
		f := auditFacility(t, rt)
		f.Observe(obs("one.aiiospkg"), Policy{Revision: 1})
		for i := 0; i < 12; i++ { // two hours of virtual time, in ten-minute steps
			synctest.Wait()
			time.Sleep(10 * time.Minute)
		}
		synctest.Wait()
		starts := strings.Count(rt.seen(), "start:")
		view, ok := refusalOf(f, "id.example.one")
		if !ok || starts != DefaultBudget().Max {
			t.Fatalf("a first install that never reports ready was started %d times: %s", starts, rt.seen())
		}
		if r := view.Refusal; r.Stage != StageReadiness || r.Class != ClassPermanent || !view.RetryAt.IsZero() ||
			!strings.Contains(r.Remedy, "startup_timeout_ms") || !strings.Contains(r.Remedy, "5 attempts are spent") {
			t.Fatalf("not final with its remedy: %+v, retry at %v", r, view.RetryAt)
		}
		rt.mu.Lock()
		delete(rt.startErrFor, "one.aiiospkg")
		rt.mu.Unlock()
		if !f.Retry("id.example.one") {
			t.Fatal("Try again found nothing")
		}
		synctest.Wait()
		if f.stateOf("id.example.one") != StateActive {
			t.Fatalf("Try again did not start it: %+v", f.Snapshot())
		}
	})
}

// G6: AN INPUT TO THE JUDGMENT THAT CHANGED IS ASKED ABOUT NOW. A refusal
// waiting on the operator's configuration is asked again at once — past its
// backoff, whatever its class — and one that waits only on the package's
// bytes is not.
func TestARejudgmentAsksAgainNowWhatWaitsOnTheInput(t *testing.T) {
	for _, c := range []struct {
		name  string
		cause error
		again bool
	}{
		{"a transient refusal, past its backoff", errors.New("the port was busy"), true},
		{"a final download", ended{remedy: "Try again."}, true},
		{"what only the package's bytes change", packageDecided{bound: true}, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			rt := newSharedRuntime()
			rt.startErrFor["one.aiiospkg"] = c.cause
			f := newFacility(t, rt)
			f.Observe(obs("one.aiiospkg"), Policy{Revision: 1})
			until(t, "the refusal", func() bool { _, ok := refusalOf(f, "id.example.one"); return ok })
			settle()
			before := strings.Count(rt.seen(), "start:")
			f.Rejudge(InputConfig, "a limit changed")
			settle()
			after := strings.Count(rt.seen(), "start:")
			if again := after > before; again != c.again {
				t.Fatalf("asked again: %v (starts %d -> %d)", again, before, after)
			}
		})
	}
}

// judgedRuntime serves the set its package orders second, the first passed
// over for a reason; a candidate of the same bytes finds nothing better.
type judgedRuntime struct {
	*sharedRuntime
	mu       sync.Mutex
	prepares int
}

func (r *judgedRuntime) Prepare(ctx context.Context, ev Evidence) (Prepared, error) {
	r.mu.Lock()
	r.prepares++
	n := r.prepares
	r.mu.Unlock()
	if n > 1 {
		return Prepared{}, errors.New("nothing to move to: the set serving is the one this machine would run")
	}
	p, err := r.sharedRuntime.Prepare(ctx, ev)
	p.Selection = &Selection{Variant: "lesser", Sets: []SetVerdict{
		{Variant: "better", Reasons: []Reason{{Words: "its runtime is larger than the operator's ceiling"}}},
		{Variant: "lesser", Chosen: true},
	}}
	return p, err
}

func (r *judgedRuntime) prepared() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.prepares
}

// G6: WHAT SERVES A LESSER SET IS ASKED, ONCE, WHETHER THE BETTER ONE IS
// POSSIBLE NOW: a candidate of the same bytes. Here it is not; what serves
// keeps serving, and nothing asks again on a clock or claims it will.
func TestARejudgmentAsksWhatServesALesserSetOnce(t *testing.T) {
	rt := &judgedRuntime{sharedRuntime: newSharedRuntime()}
	f := newFacility(t, rt)
	f.Observe(obs("one.aiiospkg"), Policy{Revision: 1})
	until(t, "the lesser set serving", func() bool { return f.stateOf("id.example.one") == StateActive })
	settle()
	if n := rt.prepared(); n != 1 {
		t.Fatalf("a serving set was asked again before anything changed: %d", n)
	}
	f.Rejudge(InputConfig, "a ceiling was raised")
	until(t, "the candidate", func() bool { return rt.prepared() == 2 })
	settle()
	for i := 0; i < 5; i++ {
		f.Poke("nothing changed")
		time.Sleep(20 * time.Millisecond)
	}
	settle()
	view, _ := refusalOf(f, "id.example.one")
	if n := rt.prepared(); n != 2 || f.stateOf("id.example.one") != StateActive || !view.RetryAt.IsZero() {
		t.Fatalf("asked %d times, state %s, retry at %v", n, f.stateOf("id.example.one"), view.RetryAt)
	}
}
