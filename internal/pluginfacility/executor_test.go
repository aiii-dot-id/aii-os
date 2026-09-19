package pluginfacility

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

// .

type fakeRunning struct {
	id, tag string
	lease   *Lease
}

func (f *fakeRunning) PluginID() string { return f.id }

type fakeRuntime struct {
	mu sync.Mutex

	verifyErr   error
	prepareErr  error
	startErr    error
	healthErr   error
	redirectErr error
	present     bool

	// .
	// .
	startGate chan struct{}
	entered   chan string
	cancelled chan string

	stopEstablished bool
	stopResidue     []string
	stopErr         error
	// .
	// .
	// .
	stopGate     map[string]chan struct{}
	redirectGate chan struct{}
	healthPanics bool

	calls []string
}

func newFakeRuntime() *fakeRuntime {
	return &fakeRuntime{present: true, stopEstablished: true,
		entered: make(chan string, 16), cancelled: make(chan string, 16)}
}

func (f *fakeRuntime) note(s string) {
	f.mu.Lock()
	f.calls = append(f.calls, s)
	f.mu.Unlock()
}

func (f *fakeRuntime) seen() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...)
}

func (f *fakeRuntime) Verify(ctx context.Context, pkg string) (Evidence, error) {
	f.note("verify:" + pkg)
	if f.verifyErr != nil {
		return Evidence{}, f.verifyErr
	}
	return Evidence{ID: "id.example.p", Version: "1.0.0", Package: pkg, PackageHash: "sha256:aa"}, nil
}

func (f *fakeRuntime) Prepare(ctx context.Context, ev Evidence) (Prepared, error) {
	f.note("prepare")
	if f.prepareErr != nil {
		return Prepared{}, f.prepareErr
	}
	return Prepared{Evidence: ev, Present: f.present}, nil
}

func (f *fakeRuntime) Acquire(ctx context.Context, p Prepared, progress func(MaterialStatus)) error {
	f.note("acquire")
	progress(MaterialStatus{BytesPresent: 1, BytesTotal: 2, FilesPresent: 1, FilesTotal: 2})
	return nil
}

func (f *fakeRuntime) Start(ctx context.Context, p Prepared, lease *Lease) (Running, error) {
	f.note("start:" + p.Evidence.Package)
	select {
	case f.entered <- p.Evidence.Package:
	default:
	}
	lease.Hold("binding", func() error { f.note("release:binding"); return nil })
	if f.startGate != nil {
		select {
		case <-f.startGate:
		case <-ctx.Done():
			select {
			case f.cancelled <- p.Evidence.Package:
			default:
			}
			return nil, ctx.Err()
		}
	}
	if f.startErr != nil {
		return nil, f.startErr
	}
	return &fakeRunning{id: p.Evidence.ID, tag: p.Evidence.Package, lease: lease}, nil
}

func (f *fakeRuntime) Health(ctx context.Context, r Running) error {
	f.note("health")
	if f.healthPanics {
		panic("the health probe fell over")
	}
	return f.healthErr
}

func (f *fakeRuntime) Redirect(from, to Running) error {
	if from == nil {
		f.note("admit")
	} else {
		f.note("redirect")
	}
	if f.redirectGate != nil {
		<-f.redirectGate
	}
	return f.redirectErr
}

func (f *fakeRuntime) Stop(ctx context.Context, r Running) (Retirement, error) {
	tag := r.(*fakeRunning).tag
	f.note("stop:" + tag)
	if gate := f.stopGate[tag]; gate != nil {
		select {
		case <-gate:
		case <-ctx.Done():
			return Retirement{Established: false, Residue: []string{"child " + tag + " still stopping: " + ctx.Err().Error()}}, nil
		}
	}
	return Retirement{Established: f.stopEstablished, Residue: f.stopResidue}, f.stopErr
}

// .

type collector struct {
	mu sync.Mutex
	ev []Event
}

func (c *collector) emit(e Event) {
	c.mu.Lock()
	c.ev = append(c.ev, e)
	c.mu.Unlock()
}

func (c *collector) kinds() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]string, 0, len(c.ev))
	for _, e := range c.ev {
		out = append(out, string(e.Kind))
	}
	return out
}

func (c *collector) last(kind EventKind) *Event {
	c.mu.Lock()
	defer c.mu.Unlock()
	for i := len(c.ev) - 1; i >= 0; i-- {
		if c.ev[i].Kind == kind {
			e := c.ev[i]
			return &e
		}
	}
	return nil
}

func newTestExecutor(t *testing.T, rt Runtime, col *collector) (*executor, context.CancelFunc) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	var gen Generation
	var mu sync.Mutex
	e := newExecutor("id.example.p", executorDeps{
		Runtime: rt,
		NextGen: func() Generation { mu.Lock(); defer mu.Unlock(); gen++; return gen },
		Emit:    col.emit,
	}, ctx)
	t.Cleanup(func() { cancel(); e.Close(); e.Wait() })
	return e, cancel
}

func waitDone(t *testing.T, c *command, what string) {
	t.Helper()
	select {
	case <-c.done:
	case <-time.After(10 * time.Second):
		t.Fatalf("timed out waiting for %s", what)
	}
}

// .

func TestAnActivationRunsItsStagesInOrderAndServes(t *testing.T) {
	rt, col := newFakeRuntime(), &collector{}
	rt.present = false
	e, _ := newTestExecutor(t, rt, col)

	c := newCommand(cmdActivate, "p.aiiospkg", "sha256:aa")
	e.Send(c)
	waitDone(t, c, "the activation")
	if c.outcome != "active" {
		t.Fatalf("outcome: %s", c.outcome)
	}
	if got := strings.Join(rt.seen(), ","); got != "verify:p.aiiospkg,prepare,acquire,start:p.aiiospkg,health,admit" {
		t.Fatalf("stages ran out of order: %s", got)
	}
	if got := strings.Join(col.kinds(), ","); got != "started,progress,active" {
		t.Fatalf("events: %s", got)
	}
}

// .
func TestARefusalGivesBackWhatTheAttemptTook(t *testing.T) {
	for _, tc := range []struct {
		name  string
		spoil func(*fakeRuntime)
		stage Stage
		// .
		// .
		stops bool
	}{
		{"verification", func(f *fakeRuntime) { f.verifyErr = errors.New("the signature does not verify") }, StageVerify, false},
		{"material", func(f *fakeRuntime) { f.prepareErr = errors.New("declared models are missing") }, StageMaterial, false},
		{"the start", func(f *fakeRuntime) { f.startErr = errors.New("the wall could not be built") }, StageStart, false},
		{"health", func(f *fakeRuntime) { f.healthErr = errors.New("the candidate answered nothing") }, StageHealth, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rt, col := newFakeRuntime(), &collector{}
			tc.spoil(rt)
			e, _ := newTestExecutor(t, rt, col)
			c := newCommand(cmdActivate, "p.aiiospkg", "sha256:aa")
			e.Send(c)
			waitDone(t, c, "the refusal")
			if c.outcome != "refused" {
				t.Fatalf("outcome: %s", c.outcome)
			}
			ev := col.last(EventRefused)
			if ev == nil || ev.Refusal == nil || ev.Refusal.Stage != tc.stage {
				t.Fatalf("refusal: %+v", ev)
			}
			if ev.Refusal.Cleanup != nil {
				t.Fatalf("a clean release must leave no cleanup failure: %v", ev.Refusal.Cleanup)
			}
			seen := strings.Join(rt.seen(), ",")
			if !strings.Contains(seen, "release:binding") && strings.Contains(seen, "start:") {
				t.Fatalf("a started attempt must give its binding back: %s", seen)
			}
			if tc.stops && !strings.Contains(seen, "stop:") {
				t.Fatalf("a refusal after the child started must stop it: %s", seen)
			}
		})
	}
}

// .
func TestARefusalWhoseCleanupFailedKeepsItsResidue(t *testing.T) {
	rt, col := newFakeRuntime(), &collector{}
	rt.healthErr = errors.New("the candidate answered nothing")
	rt.stopEstablished, rt.stopResidue = false, []string{"child 48211 not yet reaped"}
	e, _ := newTestExecutor(t, rt, col)

	c := newCommand(cmdActivate, "p.aiiospkg", "sha256:aa")
	e.Send(c)
	waitDone(t, c, "the refusal")
	ev := col.last(EventRefused)
	if ev == nil || ev.Refusal == nil {
		t.Fatal("no refusal")
	}
	if ev.Refusal.Cleanup == nil {
		t.Fatal("a cleanup that did not complete must be reported")
	}
	if !strings.Contains(ev.Refusal.Cleanup.Error(), "not yet reaped") {
		t.Fatalf("the residue must name what is still held: %v", ev.Refusal.Cleanup)
	}
	// .
	if !strings.Contains(ev.Refusal.Error(), "answered nothing") || !strings.Contains(ev.Refusal.Error(), "not yet reaped") {
		t.Fatalf("both failures must survive: %v", ev.Refusal)
	}
}

// .
// .
// .
func TestADeactivateCancelsTheAttemptInFlightAtOnce(t *testing.T) {
	rt, col := newFakeRuntime(), &collector{}
	rt.startGate = make(chan struct{})
	e, _ := newTestExecutor(t, rt, col)

	act := newCommand(cmdActivate, "p.aiiospkg", "sha256:aa")
	e.Send(act)
	select {
	case <-rt.entered:
	case <-time.After(10 * time.Second):
		t.Fatal("the start never began")
	}

	started := time.Now()
	stop := newCommand(cmdDeactivate, "", "")
	e.Send(stop)
	waitDone(t, act, "the cancelled activation")
	waitDone(t, stop, "the deactivate")
	if took := time.Since(started); took > 5*time.Second {
		t.Fatalf("the deactivate waited for the start: %s", took)
	}
	select {
	case <-rt.cancelled:
	case <-time.After(5 * time.Second):
		t.Fatal("the start never saw its context end")
	}
	ev := col.last(EventRefused)
	if ev == nil || ev.Refusal == nil || ev.Refusal.Stage != StageCancelled {
		t.Fatalf("an interrupted start is a cancellation, not the package's failure: %+v", ev)
	}
	if ev.Refusal.Class != ClassPermanent {
		t.Fatal("a cancellation is not retried on a timer")
	}
}

// .
// .
func TestAWaitingCommandIsSupersededAndSaysSo(t *testing.T) {
	rt, col := newFakeRuntime(), &collector{}
	rt.startGate = make(chan struct{})
	e, _ := newTestExecutor(t, rt, col)

	first := newCommand(cmdActivate, "one.aiiospkg", "sha256:01")
	e.Send(first)
	select {
	case <-rt.entered:
	case <-time.After(10 * time.Second):
		t.Fatal("the first start never began")
	}
	second := newCommand(cmdActivate, "two.aiiospkg", "sha256:02")
	third := newCommand(cmdActivate, "three.aiiospkg", "sha256:03")
	e.Send(second)
	e.Send(third)
	waitDone(t, second, "the superseded command")
	if second.outcome != "superseded" {
		t.Fatalf("a command replaced before it ran must say so: %q", second.outcome)
	}
	// .
	close(rt.startGate)
	waitDone(t, first, "the first")
	waitDone(t, third, "the newest")
	if third.outcome != "active" {
		t.Fatalf("the newest command must run: %q", third.outcome)
	}
	if got := strings.Join(rt.seen(), ","); strings.Contains(got, "start:two.aiiospkg") {
		t.Fatalf("a superseded command must never reach the runtime: %s", got)
	}
}

// .
func TestOneInstancesCommandsRunOneAtATime(t *testing.T) {
	rt, col := newFakeRuntime(), &collector{}
	var inFlight, peak int
	var mu sync.Mutex
	gate := make(chan struct{})
	rt.startGate = gate
	e, _ := newTestExecutor(t, rt, col)
	// .
	go func() {
		for range rt.entered {
			mu.Lock()
			inFlight++
			if inFlight > peak {
				peak = inFlight
			}
			mu.Unlock()
			time.Sleep(5 * time.Millisecond)
			mu.Lock()
			inFlight--
			mu.Unlock()
		}
	}()
	close(gate)
	for i := 0; i < 5; i++ {
		c := newCommand(cmdActivate, fmt.Sprintf("p%d.aiiospkg", i), "sha256:aa")
		e.Send(c)
		waitDone(t, c, "command")
	}
	mu.Lock()
	got := peak
	mu.Unlock()
	if got > 1 {
		t.Fatalf("one instance ran %d attempts at once", got)
	}
}

// .
// .
// .
func TestDifferentInstancesStartConcurrently(t *testing.T) {
	const n = 4
	gate := make(chan struct{})
	entered := make(chan string, n)
	rts := make([]*fakeRuntime, n)
	cmds := make([]*command, n)
	for i := 0; i < n; i++ {
		rt := newFakeRuntime()
		rt.startGate = gate
		rt.entered = entered
		rts[i] = rt
		col := &collector{}
		ctx := context.Background()
		var gen Generation
		var mu sync.Mutex
		e := newExecutor(fmt.Sprintf("id.example.p%d", i), executorDeps{
			Runtime: rt,
			NextGen: func() Generation { mu.Lock(); defer mu.Unlock(); gen++; return gen },
			Emit:    col.emit,
		}, ctx)
		defer func() { e.Close(); e.Wait() }()
		c := newCommand(cmdActivate, fmt.Sprintf("p%d.aiiospkg", i), "sha256:aa")
		cmds[i] = c
		e.Send(c)
	}
	// .
	// .
	for i := 0; i < n; i++ {
		select {
		case <-entered:
		case <-time.After(10 * time.Second):
			t.Fatalf("only %d of %d instances had started; they are still serial", i, n)
		}
	}
	close(gate)
	for i, c := range cmds {
		waitDone(t, c, fmt.Sprintf("instance %d", i))
		if c.outcome != "active" {
			t.Fatalf("instance %d: %s", i, c.outcome)
		}
	}
}

// .
// .
func TestAnUpdateRedirectsBeforeItRetiresThePredecessor(t *testing.T) {
	rt, col := newFakeRuntime(), &collector{}
	e, _ := newTestExecutor(t, rt, col)
	first := newCommand(cmdActivate, "one.aiiospkg", "sha256:01")
	e.Send(first)
	waitDone(t, first, "the first activation")

	second := newCommand(cmdUpdate, "two.aiiospkg", "sha256:02")
	e.Send(second)
	waitDone(t, second, "the update")
	if second.outcome != "active" {
		t.Fatalf("the update: %s", second.outcome)
	}
	// .
	waitFor(t, "the predecessor to be stopped", func() bool { return sawCall(rt, "stop:one.aiiospkg") })
	got := strings.Join(rt.seen(), ",")
	ri, si := strings.Index(got, "redirect"), strings.Index(got, "stop:one.aiiospkg")
	if ri < 0 || si < 0 || si < ri {
		t.Fatalf("the redirect must commit before the predecessor is stopped: %s", got)
	}
	if strings.Contains(got, "stop:two.aiiospkg") {
		t.Fatal("the successor was stopped by its own update")
	}
}

// .
func TestAFailedRedirectLeavesThePredecessorServing(t *testing.T) {
	rt, col := newFakeRuntime(), &collector{}
	e, _ := newTestExecutor(t, rt, col)
	first := newCommand(cmdActivate, "one.aiiospkg", "sha256:01")
	e.Send(first)
	waitDone(t, first, "the first activation")

	rt.redirectErr = errors.New("a tool name is owned by another origin")
	second := newCommand(cmdUpdate, "two.aiiospkg", "sha256:02")
	e.Send(second)
	waitDone(t, second, "the refused update")
	if second.outcome != "refused" {
		t.Fatalf("outcome: %s", second.outcome)
	}
	ev := col.last(EventRefused)
	if ev == nil || ev.Refusal.Stage != StageUpdate {
		t.Fatalf("refusal: %+v", ev)
	}
	// .
	got := strings.Join(rt.seen(), ",")
	if !strings.Contains(got, "stop:two.aiiospkg") {
		t.Fatalf("the refused candidate must be stopped: %s", got)
	}
	if strings.Contains(got, "stop:one.aiiospkg") {
		t.Fatalf("the release that keeps serving must not be stopped: %s", got)
	}
	e.mu.Lock()
	active := e.active
	e.mu.Unlock()
	if active == nil || active.(*fakeRunning).tag != "one.aiiospkg" {
		t.Fatalf("the predecessor must still be what serves: %+v", active)
	}
}

// .
func TestAPanickingStageBecomesARefusal(t *testing.T) {
	rt, col := newFakeRuntime(), &collector{}
	rt.verifyErr = nil
	panicking := &panicRuntime{fakeRuntime: rt}
	e, _ := newTestExecutor(t, panicking, col)
	c := newCommand(cmdActivate, "p.aiiospkg", "sha256:aa")
	e.Send(c)
	waitDone(t, c, "the panic")
	if c.outcome != "panicked" {
		t.Fatalf("outcome: %s", c.outcome)
	}
	ev := col.last(EventRefused)
	if ev == nil || ev.Refusal.Stage != StagePanic || !strings.Contains(ev.Refusal.Error(), "the lane came apart") {
		t.Fatalf("a panic must arrive as a refusal naming it: %+v", ev)
	}
	// .
	panicking.stop = true
	next := newCommand(cmdActivate, "p.aiiospkg", "sha256:aa")
	e.Send(next)
	waitDone(t, next, "the command after the panic")
	if next.outcome != "active" {
		t.Fatalf("after a panic the executor must keep working: %s", next.outcome)
	}
}

type panicRuntime struct {
	*fakeRuntime
	stop bool
}

func (p *panicRuntime) Prepare(ctx context.Context, ev Evidence) (Prepared, error) {
	if !p.stop {
		panic("the lane came apart")
	}
	return p.fakeRuntime.Prepare(ctx, ev)
}

// .
func TestTheHostStoppingEndsTheExecutor(t *testing.T) {
	rt, col := newFakeRuntime(), &collector{}
	rt.startGate = make(chan struct{})
	e, cancel := newTestExecutor(t, rt, col)
	c := newCommand(cmdActivate, "p.aiiospkg", "sha256:aa")
	e.Send(c)
	select {
	case <-rt.entered:
	case <-time.After(10 * time.Second):
		t.Fatal("the start never began")
	}
	cancel()
	e.Close()
	waitDone(t, c, "the interrupted attempt")
	e.Wait()
	after := newCommand(cmdActivate, "p.aiiospkg", "sha256:aa")
	e.Send(after)
	waitDone(t, after, "the command after the host stopped")
	if after.outcome != "the host is stopping" {
		t.Fatalf("nothing starts once the host is stopping: %q", after.outcome)
	}
}

// .
// .
// .
// .
// .
// .
// .
func TestAFailedFirstAdmissionStopsTheChildAndRefusesPermanently(t *testing.T) {
	rt, col := newFakeRuntime(), &collector{}
	rt.redirectErr = errors.New("a tool name is owned by another origin")
	e, _ := newTestExecutor(t, rt, col)

	c := newCommand(cmdActivate, "one.aiiospkg", "sha256:01")
	e.Send(c)
	waitDone(t, c, "the first activation")
	if c.outcome != "refused" {
		t.Fatalf("outcome: %s", c.outcome)
	}
	ev := col.last(EventRefused)
	if ev == nil || ev.Refusal.Stage != StageRegister {
		t.Fatalf("a first admission refuses at registration: %+v", ev)
	}
	if ev.Refusal.Class != ClassPermanent {
		t.Fatalf("a name another origin owns does not heal on a timer: %+v", ev.Refusal)
	}
	got := strings.Join(rt.seen(), ",")
	if !strings.Contains(got, "health,admit") {
		t.Fatalf("the child is health-checked before it is reachable: %s", got)
	}
	if !strings.Contains(got, "stop:one.aiiospkg") {
		t.Fatalf("a child that cannot be admitted must not be left running: %s", got)
	}
	e.mu.Lock()
	active := e.active
	e.mu.Unlock()
	if active != nil {
		t.Fatalf("nothing serves this id: %v", active)
	}
}
