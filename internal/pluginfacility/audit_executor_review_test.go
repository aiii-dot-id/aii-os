// .
// .
// .
// .
// .
// .
// .
// .

package pluginfacility

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"
)

// .
// .
type auditExecutorRuntime struct {
	*fakeRuntime
	startHook    func(context.Context, Prepared, *Lease) (Running, error)
	healthHook   func(context.Context, Running) error
	stopHook     func(context.Context, Running) (Retirement, error)
	redirectHook func(Running, Running) error
}

func (r *auditExecutorRuntime) Start(ctx context.Context, p Prepared, l *Lease) (Running, error) {
	if r.startHook != nil {
		return r.startHook(ctx, p, l)
	}
	return r.fakeRuntime.Start(ctx, p, l)
}

func (r *auditExecutorRuntime) Health(ctx context.Context, running Running) error {
	if r.healthHook != nil {
		return r.healthHook(ctx, running)
	}
	return r.fakeRuntime.Health(ctx, running)
}

func (r *auditExecutorRuntime) Stop(ctx context.Context, running Running) (Retirement, error) {
	if r.stopHook != nil {
		return r.stopHook(ctx, running)
	}
	return r.fakeRuntime.Stop(ctx, running)
}

func (r *auditExecutorRuntime) Redirect(from, to Running) error {
	if r.redirectHook != nil {
		return r.redirectHook(from, to)
	}
	return r.fakeRuntime.Redirect(from, to)
}

func auditExecutorBarrier() (chan struct{}, func()) {
	ch := make(chan struct{})
	var once sync.Once
	return ch, func() { once.Do(func() { close(ch) }) }
}

func auditExecutorWait(t *testing.T, ch <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for %s", what)
	}
}

func auditExecutorCommand(t *testing.T, e *executor, kind commandKind, pkg string) *command {
	t.Helper()
	c := newCommand(kind, pkg, "sha256:aa")
	e.Send(c)
	waitDone(t, c, string(kind))
	return c
}

func TestAuditExecutorReleasesActiveLeaseOnDeactivate(t *testing.T) {
	rt, col := newFakeRuntime(), &collector{}
	e, _ := newTestExecutor(t, rt, col)
	auditExecutorCommand(t, e, cmdActivate, "one")
	auditExecutorCommand(t, e, cmdDeactivate, "")
	waitFor(t, "the binding to be released by the retirement worker", func() bool { return sawCall(rt, "release:binding") })
	got := strings.Join(rt.seen(), ",")
	if !strings.Contains(got, "release:binding") {
		t.Errorf("active activation's lease was never released: %s", got)
	}
	if ev := col.last(EventRetired); ev != nil && ev.Retirement.Established && !strings.Contains(got, "release:binding") {
		t.Error("retirement reported established while the activation still owns its binding")
	}
}

func TestAuditExecutorDoesNotAdmitThirdWhileRetirementPending(t *testing.T) {
	rt, col := newFakeRuntime(), &collector{}
	rt.stopEstablished = false
	rt.stopResidue = []string{"child not yet reaped"}
	e, _ := newTestExecutor(t, rt, col)
	auditExecutorCommand(t, e, cmdActivate, "one")
	auditExecutorCommand(t, e, cmdUpdate, "two")
	waitFor(t, "the predecessor's pending retirement", func() bool { return col.last(EventRetired) != nil })
	if ev := col.last(EventRetired); ev == nil || ev.Retirement.Established {
		t.Fatal("fixture did not establish a pending predecessor")
	}
	third := newCommand(cmdUpdate, "three", "sha256:aa")
	e.Send(third)
	// .
	select {
	case <-third.done:
	case <-time.After(250 * time.Millisecond):
	}
	e.Close()
	e.Wait()
	if got := strings.Join(rt.seen(), ","); strings.Contains(got, "start:three") {
		t.Errorf("third engine started while first retirement remained pending: %s", got)
	}
}

func TestAuditExecutorRetirementUsesOwnedGeneration(t *testing.T) {
	for _, kind := range []commandKind{cmdUpdate, cmdDeactivate} {
		t.Run(string(kind), func(t *testing.T) {
			rt, col := newFakeRuntime(), &collector{}
			e, _ := newTestExecutor(t, rt, col)
			auditExecutorCommand(t, e, cmdActivate, "one")
			owner := col.last(EventActive).Gen
			auditExecutorCommand(t, e, kind, "two")
			waitFor(t, "the retirement to be published", func() bool { return col.last(EventRetired) != nil })
			ret := col.last(EventRetired)
			if ret == nil || ret.Gen != owner {
				t.Errorf("retirement of generation %d was attributed to %+v", owner, ret)
			}
		})
	}
}

func TestAuditExecutorFencesLateHealth(t *testing.T) {
	for _, action := range []string{"withdraw", "replace", "close"} {
		t.Run(action, func(t *testing.T) {
			rt := &auditExecutorRuntime{fakeRuntime: newFakeRuntime()}
			col := &collector{}
			gate, release := auditExecutorBarrier()
			entered := make(chan struct{})
			rt.healthHook = func(ctx context.Context, running Running) error {
				if running.(*fakeRunning).tag == "one" {
					close(entered)
					<-gate
				}
				// .
				return nil
			}
			e, _ := newTestExecutor(t, rt, col)
			defer release()
			first := newCommand(cmdActivate, "one", "sha256:aa")
			e.Send(first)
			auditExecutorWait(t, entered, "candidate Health")
			var next *command
			switch action {
			case "withdraw":
				next = newCommand(cmdDeactivate, "", "")
				e.Send(next)
			case "replace":
				next = newCommand(cmdUpdate, "two", "sha256:bb")
				e.Send(next)
			case "close":
				e.Close()
			}
			release()
			waitDone(t, first, "stale candidate")
			if next != nil {
				waitDone(t, next, "new desired state")
			}
			e.Close()
			e.Wait()
			col.mu.Lock()
			defer col.mu.Unlock()
			for _, ev := range col.ev {
				if ev.Kind == EventActive && ev.Gen == 1 {
					t.Errorf("candidate published Active after %s; outcome=%q", action, first.outcome)
				}
			}
		})
	}
}

func TestAuditExecutorCloseBeforeAttemptRegistration(t *testing.T) {
	rt, col := newFakeRuntime(), &collector{}
	gate, release := auditExecutorBarrier()
	entered := make(chan struct{})
	e := newExecutor("id.example.p", executorDeps{
		Runtime: rt, Emit: col.emit,
		NextGen: func() Generation { close(entered); <-gate; return 1 },
	}, context.Background())
	t.Cleanup(func() { release(); e.Close(); e.Wait() })
	c := newCommand(cmdActivate, "one", "sha256:aa")
	e.Send(c)
	auditExecutorWait(t, entered, "generation assignment")
	e.Close()
	release()
	waitDone(t, c, "command admitted during Close")
	e.Wait()
	if got := strings.Join(rt.seen(), ","); strings.Contains(got, "start:") {
		t.Errorf("Start ran after Close had returned: %s; outcome=%q", got, c.outcome)
	}
}

func TestAuditExecutorFencesHeldRedirect(t *testing.T) {
	rt := &auditExecutorRuntime{fakeRuntime: newFakeRuntime()}
	col := &collector{}
	gate, release := auditExecutorBarrier()
	entered := make(chan struct{})
	rt.redirectHook = func(from, to Running) error {
		if from != nil {
			close(entered)
			<-gate
		}
		return rt.fakeRuntime.Redirect(from, to)
	}
	e, _ := newTestExecutor(t, rt, col)
	defer release()
	auditExecutorCommand(t, e, cmdActivate, "one")
	update := newCommand(cmdUpdate, "two", "sha256:bb")
	e.Send(update)
	auditExecutorWait(t, entered, "candidate redirect before commit")
	e.Close()
	release()
	waitDone(t, update, "update returned after Close")
	e.Wait()
	if ev := col.last(EventActive); ev != nil && ev.Gen == 2 {
		t.Errorf("replacement published Active after Close while Redirect was held: %+v", ev)
	}
}

func TestAuditExecutorPanicPreservesCleanup(t *testing.T) {
	for _, stage := range []string{"start-after-hold", "health"} {
		t.Run(stage, func(t *testing.T) {
			rt := &auditExecutorRuntime{fakeRuntime: newFakeRuntime()}
			col := &collector{}
			if stage == "start-after-hold" {
				rt.startHook = func(ctx context.Context, p Prepared, lease *Lease) (Running, error) {
					lease.Hold("binding", func() error { rt.note("release:binding"); return nil })
					panic("start failed after custody transfer")
				}
			} else {
				rt.healthHook = func(context.Context, Running) error { panic("health failed after Start") }
			}
			e, _ := newTestExecutor(t, rt, col)
			auditExecutorCommand(t, e, cmdActivate, "one")
			e.Close()
			e.Wait()
			if ev := col.last(EventRefused); ev == nil || ev.Refusal.Stage != StagePanic {
				t.Fatal("panic was not reported as a refusal")
			}
			got := strings.Join(rt.seen(), ",")
			if !strings.Contains(got, "release:binding") {
				t.Errorf("%s panic dropped custody without cleanup: %s", stage, got)
			}
			if stage == "health" && !strings.Contains(got, "stop:one") {
				t.Errorf("Health panic left its successfully started child unstopped: %s", got)
			}
		})
	}
}

func TestAuditExecutorShutdownRetiresActive(t *testing.T) {
	for _, mode := range []string{"close", "parent"} {
		t.Run(mode, func(t *testing.T) {
			rt, col := newFakeRuntime(), &collector{}
			e, cancel := newTestExecutor(t, rt, col)
			auditExecutorCommand(t, e, cmdActivate, "one")
			if mode == "close" {
				e.Close()
			} else {
				cancel()
			}
			e.Wait()
			if got := strings.Join(rt.seen(), ","); !strings.Contains(got, "stop:one") {
				t.Errorf("executor exited on %s without stopping its active child: %s", mode, got)
			}
		})
	}
}

func TestAuditExecutorRetirementDoesNotBlockCommands(t *testing.T) {
	rt := &auditExecutorRuntime{fakeRuntime: newFakeRuntime()}
	col := &collector{}
	gate, release := auditExecutorBarrier()
	oldStop, newStop := make(chan struct{}), make(chan struct{})
	var oldOnce, newOnce sync.Once
	rt.stopHook = func(ctx context.Context, running Running) (Retirement, error) {
		if running.(*fakeRunning).tag == "one" {
			oldOnce.Do(func() { close(oldStop) })
			<-gate
		} else {
			newOnce.Do(func() { close(newStop) })
		}
		return Retirement{Established: true}, nil
	}
	e, _ := newTestExecutor(t, rt, col)
	defer release()
	auditExecutorCommand(t, e, cmdActivate, "one")
	update := newCommand(cmdUpdate, "two", "sha256:bb")
	e.Send(update)
	auditExecutorWait(t, oldStop, "predecessor retirement")
	stop := newCommand(cmdDeactivate, "", "")
	e.Send(stop)
	progressed := false
	select {
	case <-newStop:
		progressed = true
	case <-time.After(250 * time.Millisecond):
	}
	release()
	waitDone(t, update, "update after predecessor release")
	waitDone(t, stop, "deactivation after predecessor release")
	if !progressed {
		t.Error("held predecessor Stop prevented the executor from stopping its replacement")
	}
}

func TestAuditExecutorRetirementHasBoundedContext(t *testing.T) {
	rt := &auditExecutorRuntime{fakeRuntime: newFakeRuntime()}
	col := &collector{}
	contexts := make(chan context.Context, 4)
	rt.stopHook = func(ctx context.Context, running Running) (Retirement, error) {
		contexts <- ctx
		return Retirement{Established: true}, nil
	}
	e, _ := newTestExecutor(t, rt, col)
	auditExecutorCommand(t, e, cmdActivate, "one")
	auditExecutorCommand(t, e, cmdDeactivate, "")
	ctx := <-contexts
	_, bounded := ctx.Deadline()
	if ctx.Done() == nil || !bounded {
		t.Errorf("retirement got an unbounded context: done=%v deadline=%v", ctx.Done() != nil, bounded)
	}
}
