package pluginfacility

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"
)

// A PLUGIN'S ATTEMPTS ARE ITS OPERATOR'S FIGURE. Five attempts in ten
// minutes was every plugin's on every machine; the operator's
// plugins.runtime.restart_attempts reaches the facility on its policy. A
// first install that never reports ready, given two, is started twice and
// then held with its remedy saying two were spent; the default's five is
// the control.
func TestAPluginsAttemptsAreItsOperatorsFigure(t *testing.T) {
	for _, c := range []struct {
		name     string
		attempts int
		want     int
	}{
		{"the operator's two", 2, 2},
		{"none set: the default's", 0, DefaultBudget().Max},
	} {
		t.Run(c.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				rt := newSharedRuntime()
				rt.startErrFor["one.aiiospkg"] = notReady{}
				f := auditFacility(t, rt)
				f.Observe(obs("one.aiiospkg"), Policy{Revision: 1, RestartAttempts: c.attempts})
				for i := 0; i < 12; i++ { // two hours of virtual time, in ten-minute steps
					synctest.Wait()
					time.Sleep(10 * time.Minute)
				}
				synctest.Wait()
				starts := strings.Count(rt.seen(), "start:")
				view, ok := refusalOf(f, "id.example.one")
				if !ok || starts != c.want {
					t.Fatalf("with %d attempts set, a plugin that never reports ready was started %d times, want %d: %s", c.attempts, starts, c.want, rt.seen())
				}
				if r := view.Refusal; r.Class != ClassPermanent || !strings.Contains(r.Remedy, fmt.Sprintf("Its %d attempts are spent", c.want)) {
					t.Fatalf("not held with its remedy: %+v", r)
				}
			})
		})
	}
}

// stopDeadline is the shared runtime whose stop notes how long it was
// given, and which says what its child's stop and reap are.
type stopDeadline struct {
	*sharedRuntime
	child time.Duration
	mu    sync.Mutex
	given time.Duration
}

func (s *stopDeadline) RetireBound() time.Duration { return s.child }

func (s *stopDeadline) Stop(ctx context.Context, r Running) (Retirement, error) {
	if d, ok := ctx.Deadline(); ok {
		s.mu.Lock()
		s.given = time.Until(d)
		s.mu.Unlock()
	}
	return s.sharedRuntime.Stop(ctx, r)
}

// A RETIREMENT ALLOWS THE STOP ITS CHILD WAS GIVEN. A retirement was
// thirty seconds whatever the child's stop: a stop the operator set longer
// than that would have been cut by the retirement around it, and the child
// killed with no SIGTERM. The retirement takes the child's stop and reap,
// as its runtime says them (RetireBounder), when they are longer; a
// shorter one leaves the facility's own thirty seconds.
func TestARetirementAllowsTheStopItsChildWasGiven(t *testing.T) {
	for _, c := range []struct {
		name        string
		child       time.Duration
		least, most time.Duration
	}{
		{"a stop and reap of 50s", 50 * time.Second, 45 * time.Second, 50 * time.Second},
		{"a stop and reap of 10s: the facility's own", 10 * time.Second, 25 * time.Second, DefaultRetireTimeout},
	} {
		t.Run(c.name, func(t *testing.T) {
			rt := &stopDeadline{sharedRuntime: newSharedRuntime(), child: c.child}
			f := New(Config{Runtime: rt, Capacity: ampleCapacity{}})
			ctx, cancel := context.WithCancel(context.Background())
			f.Attach(ctx)
			t.Cleanup(func() { cancel(); f.Close() })
			f.Observe(obs("one.aiiospkg"), Policy{Revision: 1})
			auditWaitFacility(t, "the package to serve", func() bool { return viewOf(f, idOf("one.aiiospkg")).State == StateActive })
			f.Close()
			rt.mu.Lock()
			given := rt.given
			rt.mu.Unlock()
			if given < c.least || given > c.most {
				t.Fatalf("a child whose stop and reap are %s was retired under %s, want %s to %s", c.child, given, c.least, c.most)
			}
		})
	}
}
