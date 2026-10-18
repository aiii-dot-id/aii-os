package pluginfacility

import (
	"context"
	"errors"
	"fmt"
	"runtime/debug"
	"strings"
	"sync"
	"time"
)

type commandKind string

const (
	cmdActivate   commandKind = "activate"
	cmdUpdate     commandKind = "update"
	cmdDeactivate commandKind = "deactivate"

	cmdReadmit commandKind = "readmit"
)

type command struct {
	kind         commandKind
	pkg          string
	hash         string
	manifestHash string
	version      string

	intent string

	gen   Generation
	lease *Lease

	done chan struct{}

	outcome string
}

func newCommand(kind commandKind, pkg, hash string) *command {
	return &command{kind: kind, pkg: pkg, hash: hash, done: make(chan struct{})}
}

func (c *command) withActivation(gen Generation, lease *Lease) *command {
	c.gen, c.lease = gen, lease
	return c
}

func (c *command) settle(outcome string) {
	if c == nil {
		return
	}
	c.outcome = outcome
	close(c.done)
}

type attempt struct {
	gen Generation

	cancel context.CancelCauseFunc

	lease *Lease

	committed bool
}

func (a *attempt) takeBack() {
	if a == nil {
		return
	}
	a.endWith(nil)
}

func (a *attempt) endWith(cause error) {
	a.lease.Withdraw()
	a.cancel(cause)
}

type executorDeps struct {
	Runtime Runtime

	NextGen func() Generation

	Emit func(Event)

	Spawn func(func()) bool

	Now func() time.Time

	Allows func(Evidence) (bool, string)

	Admit func(ctx context.Context, gen Generation, p Prepared, waiting func(string)) (release func(), sentence string, err error)

	Commit func(gen Generation) bool

	ChecksFailed func(packageHash string) map[string]string
	Checked      func(packageHash string, c Checks) (ChecksVerdict, error)

	RetireTimeout time.Duration
}

const DefaultRetireTimeout = 30 * time.Second

type executor struct {
	id   string
	deps executorDeps

	mu      sync.Mutex
	pending *command
	running *attempt
	closed  bool

	active      Running
	activeGen   Generation
	activeLease *Lease

	retiring []*Lease

	left []string

	wake chan struct{}
	done chan struct{}
}

func newExecutor(id string, deps executorDeps, ctx context.Context) *executor {
	e := &executor{id: id, deps: deps, wake: make(chan struct{}, 1), done: make(chan struct{})}
	spawn := deps.Spawn
	if spawn == nil {
		spawn = func(f func()) bool { go f(); return true }
	}
	if !spawn(func() { e.loop(ctx) }) {
		close(e.done)
		e.mu.Lock()
		e.closed = true
		e.mu.Unlock()
	}
	return e
}

func (e *executor) Send(c *command) {
	e.mu.Lock()
	if e.closed {
		e.mu.Unlock()
		c.settle("the host is stopping")
		return
	}
	dropped := e.pending
	e.pending = c

	var overtaken *attempt
	if c.kind != cmdReadmit && e.running != nil && !e.running.committed {
		overtaken = e.running
	}
	e.mu.Unlock()
	e.drop(dropped, "superseded")
	overtaken.takeBack()
	select {
	case e.wake <- struct{}{}:
	default:
	}
}

func (e *executor) drop(c *command, outcome string) {
	if c == nil {
		return
	}
	if (c.kind == cmdActivate || c.kind == cmdUpdate) && c.gen != 0 && c.lease != nil {
		ref := NewRefusal(e.id, c.version, c.gen, StageCancelled, errors.New(outcome+" before it began"))
		ref.Cleanup = c.lease.Release()
		e.emit(Event{PluginID: e.id, Gen: c.gen, Kind: EventRefused, Version: c.version, Refusal: ref, At: e.now()})
	}
	c.settle(outcome)
}

func (e *executor) withdraw(gen Generation) {
	e.mu.Lock()
	var overtaken *attempt
	var dropped *command
	switch {
	case e.running != nil && e.running.gen == gen:
		overtaken = e.running
	case e.pending != nil && e.pending.gen == gen:
		dropped, e.pending = e.pending, nil
	}
	e.mu.Unlock()
	overtaken.takeBack()
	e.drop(dropped, "withdrawn")
}

func (e *executor) Close() {
	e.mu.Lock()
	if e.closed {
		e.mu.Unlock()
		return
	}
	e.closed = true
	pending, running := e.pending, e.running
	e.pending = nil
	e.mu.Unlock()
	e.drop(pending, "the host is stopping")
	running.takeBack()
	select {
	case e.wake <- struct{}{}:
	default:
	}
}

func (e *executor) Wait() { <-e.done }

func (e *executor) loop(ctx context.Context) {
	defer close(e.done)

	defer e.shutdown()
	if ctx == nil {
		ctx = context.Background()
	}
	for {
		e.mu.Lock()
		c, closed := e.pending, e.closed
		active, gen := e.active, e.activeGen
		e.pending = nil
		e.mu.Unlock()
		if c == nil {
			if closed {
				return
			}
			var terminal <-chan struct{}
			if r, ok := active.(terminalRunning); ok {
				terminal = r.Terminated()
			}
			select {
			case <-terminal:
				e.failedServing(gen)
				continue
			case <-e.wake:
				continue
			case <-ctx.Done():
				e.Close()
				return
			}
		}
		if closed {
			e.drop(c, "the host is stopping")
			return
		}
		e.run(ctx, c)
	}
}

func (e *executor) failedServing(gen Generation) {
	e.mu.Lock()
	if e.closed || e.active == nil || e.activeGen != gen {
		e.mu.Unlock()
		return
	}
	r, ok := e.active.(terminalRunning)
	if !ok {
		e.mu.Unlock()
		return
	}
	lease := e.activeLease
	e.active, e.activeGen, e.activeLease = nil, 0, nil
	e.mu.Unlock()
	restarts, cause := r.Failure()
	if cause == nil {
		cause = errors.New("runtime stopped without reporting a cause")
	}
	ref := NewRefusal(e.id, "", gen, StageCrashed, cause)
	lease.Withdraw()

	e.emit(Event{PluginID: e.id, Gen: gen, Kind: EventRefused, Refusal: ref, Restarts: restarts, At: e.now()})
	ctx, cancel := context.WithTimeout(context.Background(), e.retireTimeout())
	cleanup := e.releaseGuarded(ctx, lease)
	cancel()
	e.keepRetiring(lease)
	settled := *ref
	settled.Cleanup = cleanup
	ret := Retirement{Established: lease.Discharged(), Residue: lease.Residue()}
	e.emit(Event{PluginID: e.id, Gen: gen, Kind: EventRetired, Refusal: &settled, Retirement: &ret, At: e.now()})
}

func (e *executor) shutdown() {
	e.mu.Lock()
	running, gen, lease := e.active, e.activeGen, e.activeLease
	e.active, e.activeGen, e.activeLease = nil, 0, nil
	owing := append([]*Lease(nil), e.retiring...)
	e.mu.Unlock()
	if running != nil {
		lease.Withdraw()
		e.retireLeaving(gen, lease)
	}
	for _, l := range owing {
		if l != lease && !l.Discharged() {
			e.retireLeaving(l.Gen, l)
		}
	}
}

const leavingRetry = 25 * time.Millisecond

func (e *executor) retireLeaving(gen Generation, lease *Lease) {
	bound := e.retireTimeout()
	deadline := time.Now().Add(bound)
	for {
		ran, ret := retireOnce(bound, lease)
		if !ran {
			return
		}

		if ret.Established || !ret.Busy || !time.Now().Before(deadline) {
			e.emit(Event{PluginID: e.id, Gen: gen, Kind: EventRetired, Retirement: &ret, At: e.now()})
			if !ret.Established && ret.Busy {
				e.mu.Lock()
				e.left = append(e.left, fmt.Sprintf("%s: %s", e.id, strings.Join(ret.Residue, "; ")))
				e.mu.Unlock()
			}
			return
		}
		time.Sleep(leavingRetry)
	}
}

func (e *executor) leftBehind() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]string(nil), e.left...)
}

func (e *executor) retireNow(gen Generation, lease *Lease) {
	if ran, ret := retireOnce(e.retireTimeout(), lease); ran {
		e.emit(Event{PluginID: e.id, Gen: gen, Kind: EventRetired, Retirement: &ret, At: e.now()})
	}
}

func retireOnce(timeout time.Duration, lease *Lease) (ran bool, ret Retirement) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	var err error
	func() {
		defer func() {
			if r := recover(); r != nil {
				ran, err = true, fmt.Errorf("cleanup panicked: %v", r)
			}
		}()
		ran, err = lease.TryReleaseContext(ctx)
	}()
	if !ran {
		return false, Retirement{}
	}
	if err != nil {
		return true, Retirement{Established: false, Residue: lease.Residue(), Busy: errors.Is(err, errRetirementBusy)}
	}
	return true, Retirement{Established: true}
}

var errRetirementBusy = errors.New("the activation is busy with a call")

func (e *executor) releaseGuarded(ctx context.Context, lease *Lease) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("cleanup panicked: %v", r)
		}
	}()
	return lease.ReleaseContext(ctx)
}

func (e *executor) retireTimeout() time.Duration {
	if e.deps.RetireTimeout > 0 {
		return e.deps.RetireTimeout
	}
	return DefaultRetireTimeout
}

func (e *executor) run(parent context.Context, c *command) {
	gen := c.gen
	if gen == 0 && e.deps.NextGen != nil {
		gen = e.deps.NextGen()
	}
	ctx, cancel := context.WithCancelCause(parent)
	e.mu.Lock()

	if e.closed {
		e.mu.Unlock()
		cancel(nil)
		e.drop(c, "the host is stopping")
		return
	}

	if c.lease == nil && (c.kind == cmdActivate || c.kind == cmdUpdate) {
		c.lease = NewLease(gen)
	}
	att := &attempt{gen: gen, cancel: cancel, lease: c.lease}
	e.running = att
	e.mu.Unlock()
	defer func() {
		e.mu.Lock()
		e.running = nil
		e.mu.Unlock()
		cancel(nil)
	}()

	defer func() {
		if r := recover(); r != nil {
			ref := NewRefusal(e.id, c.version, gen, StagePanic, fmt.Errorf("%v", r))
			ref.Evidence = string(debug.Stack())
			ref.Remedy = "This is a defect in the host or the plugin's lane, not something to retry by hand."

			if lease := c.lease; lease != nil {
				ctx, cancel := context.WithTimeout(context.Background(), e.retireTimeout())
				ref.Cleanup = e.releaseGuarded(ctx, lease)
				cancel()
				e.mu.Lock()
				if e.activeLease == lease {
					e.active, e.activeGen, e.activeLease = nil, 0, nil
				}
				e.mu.Unlock()
				e.keepRetiring(lease)
			}
			e.emit(Event{PluginID: e.id, Gen: gen, Kind: EventRefused, Refusal: ref, At: e.now()})
			c.settle("panicked")
		}
	}()

	switch c.kind {
	case cmdDeactivate:
		e.deactivate(ctx, gen, c)
	case cmdReadmit:
		e.readmit(ctx, c)
	default:
		beside := e.besideServing(att)
		defer beside()
		e.activate(ctx, gen, c, beside)
	}
}

var errServingEnded = errors.New("the activation it was standing up beside stopped serving; this attempt ends with it, and the plugin starts again under its restart budget")

func (e *executor) besideServing(att *attempt) (end func()) {
	e.mu.Lock()
	r, ok := e.active.(terminalRunning)
	e.mu.Unlock()
	if !ok {
		return func() {}
	}
	ended := r.Terminated()
	done, exited := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(exited)
		select {
		case <-ended:
			att.endWith(errServingEnded)
		case <-done:
		}
	}()
	var once sync.Once
	return func() { once.Do(func() { close(done); <-exited }) }
}

func (e *executor) retirementPending() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	kept := e.retiring[:0]
	for _, l := range e.retiring {
		if !l.Discharged() {
			kept = append(kept, l)
		}
	}
	e.retiring = kept
	return len(e.retiring) > 0
}

func (e *executor) hold(lease *Lease, name string, release func() error) error {
	err := lease.Hold(name, release)
	var sealed *SealedHoldError
	if errors.As(err, &sealed) && !sealed.Released {
		if rerr := releaseHeld(Held{Name: name, Release: release}); rerr != nil {
			return fmt.Errorf("%w; and the producer could not give it back either: %v", err, rerr)
		}
	}
	return err
}

func (e *executor) keepRetiring(lease *Lease) {
	if lease == nil || lease.Discharged() {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, l := range e.retiring {
		if l == lease {
			return
		}
	}
	e.retiring = append(e.retiring, lease)
}

var errRetirementPending = errors.New("the previous release has not finished retiring")

var errSuperseded = errors.New("superseded: what is wanted of this plugin changed while it was on its way up")

func (e *executor) activate(ctx context.Context, gen Generation, c *command, beside func()) {
	lease := c.lease
	act := &Activation{Gen: gen, Role: RoleStarting, PluginID: e.id, Version: c.version,
		Package: c.pkg, PackageHash: c.hash, ManifestHash: c.manifestHash, Started: e.now(), Lease: lease}
	e.emit(Event{PluginID: e.id, Gen: gen, Kind: EventStarted, Version: c.version, At: e.now()})

	refuse := func(stage Stage, cause error) {

		if ctx.Err() != nil && !errors.Is(cause, errStopFailed) {
			stage, cause = StageCancelled, context.Cause(ctx)
		}
		if errors.Is(cause, errSuperseded) {
			stage = StageCancelled
		}

		var unready Unready
		if stage == StageStart && errors.As(cause, &unready) {
			stage = StageReadiness
		}
		ref := NewRefusal(e.id, c.version, gen, stage, cause)
		if stage == StageReadiness && unready != nil {
			ref.Remedy = unready.Unready()
		}
		if errors.Is(cause, errRetirementPending) {
			ref.Class = ClassTransient
			ref.Remedy = "The previous release is still retiring; this one starts once it has."
		}
		var checks *ChecksFailedError
		if errors.As(cause, &checks) {
			ref.Remedy = checksFailedRemedy
		}
		var never *NeverAdmissibleError
		if errors.As(cause, &never) {
			ref.Class = ClassPermanent
			ref.Remedy = never.Remedy
		}

		var bound PackageBound
		if errors.As(cause, &bound) && bound.WaitsOnPackage() {
			ref.Class, ref.WakeOn = ClassPermanent, []Input{InputPackage}
			ref.Remedy = "The package's own files refuse it the same way on every attempt; install a corrected release."
		}

		var final Final
		if errors.As(cause, &final) {
			ref.Class, ref.WakeOn = ClassPermanent, []Input{InputPackage, InputConfig}
			ref.Remedy = final.Final()
		}

		rctx, rcancel := context.WithTimeout(context.Background(), e.retireTimeout())
		if err := e.releaseGuarded(rctx, act.Lease); err != nil {
			ref.Cleanup = err
		}
		rcancel()
		e.keepRetiring(act.Lease)

		if errors.Is(cause, errServingEnded) {
			e.mu.Lock()
			served := e.activeGen
			e.mu.Unlock()
			e.failedServing(served)
		}
		act.Refusal = ref
		if act.Lease.Discharged() {
			act.Role = RoleRefused
		} else {
			act.Retiring(ref)
		}
		e.emit(Event{PluginID: e.id, Gen: gen, Kind: EventRefused, Version: c.version, Refusal: ref, Timings: copyTimings(act.Timings), Checks: act.Checks, At: e.now()})
		c.settle("refused")
	}

	if e.retirementPending() {
		refuse(StageAdmit, errRetirementPending)
		return
	}

	stageStart := e.now()
	took := func(st Stage) {
		now := e.now()
		act.Took(st, now.Sub(stageStart))
		stageStart = now
	}
	ev, err := e.deps.Runtime.Verify(ctx, c.pkg)
	took(StageVerify)
	if err != nil {
		refuse(StageVerify, err)
		return
	}

	if ev.ManifestHash != c.manifestHash {
		refuse(StageCancelled, errSuperseded)
		return
	}
	if ev.Version != "" {
		act.Version, c.version = ev.Version, ev.Version
	}
	if e.deps.Allows != nil {
		if ok, why := e.deps.Allows(ev); !ok {
			refuse(StagePolicy, errors.New(why))
			return
		}
	}
	if e.deps.ChecksFailed != nil {
		ev.ChecksFailed = e.deps.ChecksFailed(ev.PackageHash)
	}
	p, err := e.deps.Runtime.Prepare(ctx, ev)
	if err != nil {
		took(StageMaterial)

		var judged Judged
		if errors.As(err, &judged) {
			if s := judged.Judgment(); s != nil {
				e.emit(Event{PluginID: e.id, Gen: gen, Kind: EventPrepared, Version: c.version, Selection: copySelection(s), At: e.now()})
			}
		}
		refuse(StageMaterial, err)
		return
	}
	if p.Selection != nil || p.Interim {
		e.emit(Event{PluginID: e.id, Gen: gen, Kind: EventPrepared, Version: c.version, Selection: copySelection(p.Selection), Interim: p.Interim, At: e.now()})
	}
	if !p.Present {
		progress := func(m MaterialStatus) {
			e.emit(Event{PluginID: e.id, Gen: gen, Kind: EventProgress, Version: c.version, Material: &m, At: e.now()})
		}
		if err := e.deps.Runtime.Acquire(ctx, p, progress); err != nil {
			took(StageMaterial)
			refuse(StageMaterial, err)
			return
		}
		p.Present = true
	}
	took(StageMaterial)

	var admission *Admission
	if e.deps.Admit != nil {
		waited := false
		release, sentence, aerr := e.deps.Admit(ctx, gen, p, func(why string) {
			waited = true
			e.emit(Event{PluginID: e.id, Gen: gen, Kind: EventAdmitting, Version: c.version,
				Admission: &Admission{HostBytes: p.HostBytes, DeviceBytes: p.DeviceBytes, Backend: p.Backend, Sentence: why}, At: e.now()})
		})
		took(StageAdmit)
		if aerr != nil {
			refuse(StageAdmit, aerr)
			return
		}
		if herr := e.hold(act.Lease, "reservation", func() error { release(); return nil }); herr != nil {
			refuse(StageAdmit, herr)
			return
		}
		admission = &Admission{HostBytes: p.HostBytes, DeviceBytes: p.DeviceBytes, Backend: p.Backend, Sentence: sentence}
		if waited {

			e.emit(Event{PluginID: e.id, Gen: gen, Kind: EventAdmitted, Version: c.version, Admission: admission, At: e.now()})
		}
	}
	running, err := e.deps.Runtime.Start(ctx, p, act.Lease)
	took(StageStart)
	if err != nil {
		refuse(StageStart, err)
		return
	}

	if herr := e.hold(act.Lease, "activation", func() error {
		ret, serr := e.deps.Runtime.Stop(act.Lease.Context(), running)
		if serr != nil {
			return serr
		}
		if !ret.Established {
			if ret.Busy {
				return fmt.Errorf("retirement pending: %v: %w", ret.Residue, errRetirementBusy)
			}
			return fmt.Errorf("retirement pending: %v", ret.Residue)
		}
		return nil
	}); herr != nil {
		refuse(StageStart, herr)
		return
	}
	if err := e.deps.Runtime.Health(ctx, running); err != nil {
		took(StageHealth)
		refuse(StageHealth, err)
		return
	}
	took(StageHealth)

	if v, ok := e.deps.Runtime.(Validator); ok && e.deps.Checked != nil {
		checks, verr := v.Validate(ctx, running)
		took(StageValidation)

		if verr != nil && checks.Ran == 0 {
			refuse(StageValidation, verr)
			return
		}
		verdict, ferr := e.deps.Checked(ev.PackageHash, checks)
		act.Checks = &verdict
		if ferr == nil {
			ferr = verr
		}
		if ferr != nil {
			refuse(StageValidation, ferr)
			return
		}
	}
	beside()

	e.mu.Lock()
	if e.closed || ctx.Err() != nil {
		e.mu.Unlock()
		refuse(StageCancelled, errors.New("withdrawn before the redirect"))
		return
	}

	if e.deps.Commit != nil && !e.deps.Commit(gen) {
		e.mu.Unlock()
		lease.Withdraw()
		refuse(StageCancelled, errSuperseded)
		return
	}
	previous, prevGen, prevLease := e.active, e.activeGen, e.activeLease
	e.active, e.activeGen, e.activeLease = running, gen, lease
	e.mu.Unlock()

	stage := StageRegister
	if previous != nil {
		stage = StageUpdate
	}
	if err := e.deps.Runtime.Redirect(previous, running); err != nil {
		took(stage)
		e.mu.Lock()
		e.active, e.activeGen, e.activeLease = previous, prevGen, prevLease
		e.mu.Unlock()
		refuse(stage, err)
		return
	}
	took(stage)

	e.mu.Lock()
	if e.closed || ctx.Err() != nil {
		e.active, e.activeGen, e.activeLease = previous, prevGen, prevLease
		e.mu.Unlock()
		if previous != nil {
			e.mu.Lock()
			e.active, e.activeGen, e.activeLease = nil, 0, nil
			e.mu.Unlock()
			e.retire(prevGen, prevLease)
		}
		refuse(StageCancelled, errors.New("withdrawn while the redirect was in flight"))
		return
	}

	act.Role = RoleActive
	if e.running != nil && e.running.gen == gen {
		e.running.committed = true
	}
	e.emit(Event{PluginID: e.id, Gen: gen, Kind: EventActive, Version: c.version, Admission: admission, Timings: copyTimings(act.Timings), At: e.now(), Holding: holdingOf(running), Checks: act.Checks})
	e.mu.Unlock()
	if previous != nil {

		e.retire(prevGen, prevLease)
	}
	c.settle("active")
}

var errStopFailed = errors.New("stop did not establish retirement")

func (e *executor) deactivate(ctx context.Context, gen Generation, c *command) {
	e.mu.Lock()
	running, activeGen, activeLease := e.active, e.activeGen, e.activeLease
	e.active, e.activeGen, e.activeLease = nil, 0, nil
	e.mu.Unlock()
	if running == nil {
		c.settle("nothing to stop")
		return
	}
	e.retire(activeGen, activeLease)
	c.settle("stopped")
}

func (e *executor) readmit(ctx context.Context, c *command) {
	e.mu.Lock()
	running, activeGen := e.active, e.activeGen
	e.mu.Unlock()
	if running == nil {
		c.settle("nothing serves")
		return
	}
	ev, err := e.deps.Runtime.Verify(ctx, c.pkg)

	if ctx.Err() != nil {

		e.emit(Event{PluginID: e.id, Gen: activeGen, Kind: EventReaskEnded, Intent: c.intent, At: e.now()})
		c.settle("cancelled")
		return
	}
	stage := StageVerify
	if err == nil && e.deps.Allows != nil {
		if ok, why := e.deps.Allows(ev); !ok {
			stage, err = StagePolicy, errors.New(why)
		}
	}
	if err == nil {
		e.emit(Event{PluginID: e.id, Gen: activeGen, Kind: EventReadmitted, Version: c.version, Intent: c.intent, At: e.now()})
		c.settle("readmitted")
		return
	}
	ref := NewRefusal(e.id, c.version, activeGen, stage, err)
	ref.Remedy = "This release was admitted under inputs that have since changed, and is no longer admitted. It stops."
	e.emit(Event{PluginID: e.id, Gen: activeGen, Kind: EventRefused, Version: c.version, Refusal: ref, Intent: c.intent, At: e.now()})
	e.deactivate(ctx, activeGen, c)
}

func (e *executor) retire(gen Generation, lease *Lease) {

	lease.Withdraw()
	e.mu.Lock()
	e.retiring = append(e.retiring, lease)
	e.mu.Unlock()
	work := func() { e.retireNow(gen, lease) }
	spawn := e.deps.Spawn
	if spawn == nil {
		spawn = func(f func()) bool { go f(); return true }
	}
	if !spawn(work) {
		work()
	}
}

func copyTimings(t map[Stage]time.Duration) map[Stage]time.Duration {
	if len(t) == 0 {
		return nil
	}
	out := make(map[Stage]time.Duration, len(t))
	for k, v := range t {
		out[k] = v
	}
	return out
}

func (e *executor) emit(ev Event) {
	if e.deps.Emit != nil {
		e.deps.Emit(ev)
	}
}

func (e *executor) now() time.Time {
	if e.deps.Now != nil {
		return e.deps.Now()
	}
	return time.Now()
}
