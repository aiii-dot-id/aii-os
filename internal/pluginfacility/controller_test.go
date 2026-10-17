package pluginfacility

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// A runtime shared by several instances, which records what each id did
// and can hold every start until released.
type sharedRuntime struct {
	// hostBytesFor, familyFor and stopPendingFor shape one package's
	// admission envelope, its declared family, and whether its stop
	// establishes retirement — the admission proofs' knobs.
	hostBytesFor   map[string]int64
	familyFor      map[string]string
	stopPendingFor map[string]bool
	mu             sync.Mutex
	calls          []string
	gate           chan struct{}
	entered        chan string
	present        bool

	verifyErrFor map[string]error
	startErrFor  map[string]error
}

func newSharedRuntime() *sharedRuntime {
	return &sharedRuntime{entered: make(chan string, 32), present: true,
		verifyErrFor: map[string]error{}, startErrFor: map[string]error{},
		hostBytesFor: map[string]int64{}, familyFor: map[string]string{}, stopPendingFor: map[string]bool{}}
}

// ampleCapacity is a machine with room for everything a unit test
// starts: admission by size never waits on it, so the tests that are
// not about admission see the parallel starts they assert.
type ampleCapacity struct{}

func (ampleCapacity) Measure() Availability {
	return Availability{HostKnown: true, HostTotal: 64 << 30, HostAvailable: 60 << 30}
}

func (s *sharedRuntime) note(f string, a ...any) {
	s.mu.Lock()
	s.calls = append(s.calls, fmt.Sprintf(f, a...))
	s.mu.Unlock()
}

func (s *sharedRuntime) seen() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return strings.Join(s.calls, ",")
}

func idOf(pkg string) string { return "id.example." + strings.TrimSuffix(pkg, ".aiiospkg") }

func (s *sharedRuntime) Verify(ctx context.Context, pkg string) (Evidence, error) {
	s.note("verify:%s", pkg)
	s.mu.Lock()
	err := s.verifyErrFor[pkg]
	s.mu.Unlock()
	if err != nil {
		return Evidence{}, err
	}
	s.mu.Lock()
	fam := s.familyFor[pkg]
	s.mu.Unlock()
	return Evidence{ID: idOf(pkg), Version: "1.0.0", Package: pkg, PackageHash: "sha256:" + pkg, Tier: "T3", Family: fam}, nil
}

func (s *sharedRuntime) Prepare(ctx context.Context, ev Evidence) (Prepared, error) {
	s.note("prepare:%s", ev.Package)
	s.mu.Lock()
	host := s.hostBytesFor[ev.Package]
	s.mu.Unlock()
	return Prepared{Evidence: ev, Present: s.present, HostBytes: host}, nil
}

func (s *sharedRuntime) Acquire(ctx context.Context, p Prepared, progress func(MaterialStatus)) error {
	s.note("acquire:%s", p.Evidence.Package)
	progress(MaterialStatus{BytesPresent: 5, BytesTotal: 10, FilesPresent: 1, FilesTotal: 2})
	return nil
}

func (s *sharedRuntime) Start(ctx context.Context, p Prepared, lease *Lease) (Running, error) {
	s.note("start:%s", p.Evidence.Package)
	select {
	case s.entered <- p.Evidence.Package:
	default:
	}
	lease.Hold("binding", func() error { return nil })
	s.mu.Lock()
	gate, err := s.gate, s.startErrFor[p.Evidence.Package]
	s.mu.Unlock()
	if gate != nil {
		select {
		case <-gate:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if err != nil {
		return nil, err
	}
	return &fakeRunning{id: p.Evidence.ID, tag: p.Evidence.Package}, nil
}

func (s *sharedRuntime) Health(ctx context.Context, r Running) error { return nil }
func (s *sharedRuntime) Redirect(from, to Running) error {
	// A nil predecessor is a FIRST admission — this is where the id
	// becomes reachable at all.
	if from == nil {
		s.note("admit:%s", to.(*fakeRunning).tag)
		return nil
	}
	s.note("redirect:%s->%s", from.(*fakeRunning).tag, to.(*fakeRunning).tag)
	return nil
}
func (s *sharedRuntime) Stop(ctx context.Context, r Running) (Retirement, error) {
	tag := r.(*fakeRunning).tag
	s.note("stop:%s", tag)
	s.mu.Lock()
	pending := s.stopPendingFor[tag]
	s.mu.Unlock()
	if pending {
		return Retirement{Established: false, Residue: []string{"child " + tag + " not yet reaped"}}, nil
	}
	return Retirement{Established: true}, nil
}

func newFacility(t *testing.T, rt Runtime) *Facility {
	t.Helper()
	f := New(Config{Runtime: rt, Capacity: ampleCapacity{}})
	ctx, cancel := context.WithCancel(context.Background())
	f.Attach(ctx)
	t.Cleanup(func() { cancel(); f.Close() })
	return f
}

func obs(pkgs ...string) []Observed {
	out := make([]Observed, 0, len(pkgs))
	for _, p := range pkgs {
		out = append(out, Observed{ID: idOf(p), Dir: "plugins/" + idOf(p), Package: p, Hash: "sha256:" + p})
	}
	return out
}

func until(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// settle lets the controller reach a standstill, so an assertion judges
// what it settled on rather than what it was passing through.
func settle() { time.Sleep(200 * time.Millisecond) }

// rolesOf counts the roles an instance's activations hold.
func (f *Facility) rolesOf(id string) map[Role]int {
	out := map[Role]int{}
	for _, v := range f.Snapshot().Instances {
		if v.ID != id {
			continue
		}
		for _, a := range v.Activations {
			out[a.Role]++
		}
	}
	return out
}

func (f *Facility) versionOf(id string) string {
	for _, v := range f.Snapshot().Instances {
		if v.ID == id {
			return v.Version
		}
	}
	return ""
}

func (f *Facility) stateOf(id string) State {
	for _, v := range f.Snapshot().Instances {
		if v.ID == id {
			return v.State
		}
	}
	return StateRemoved
}

// WHAT IS ON DISK IS WHAT RUNS. The controller is told what it found and
// works out the rest.
func TestObservingAPackageActivatesItAndLosingItStopsIt(t *testing.T) {
	rt := newSharedRuntime()
	f := newFacility(t, rt)

	f.Observe(obs("one.aiiospkg"), Policy{Revision: 1})
	until(t, "the package to activate", func() bool { return f.stateOf("id.example.one") == StateActive })

	// It leaves the directory: nothing is wanted of it any more.
	f.Observe(nil, Policy{Revision: 1})
	until(t, "the package to stop", func() bool {
		return !strings.Contains(rt.seen(), "start:two") && strings.Contains(rt.seen(), "stop:one.aiiospkg")
	})
	until(t, "the instance to be forgotten", func() bool { return len(f.Snapshot().Instances) == 0 })
}

// A FORGOTTEN INSTANCE'S EXECUTOR IS CLOSED BY THE PASS THAT FORGETS IT,
// after the controller's lock, and the loop it ends belongs to the owner
// that spawned it: no goroutine of its own closes and waits on it.
func TestAForgottenInstancesExecutorEndsUnderItsOwner(t *testing.T) {
	var running atomic.Int64
	spawn := func(fn func()) bool {
		running.Add(1)
		go func() {
			defer running.Add(-1)
			fn()
		}()
		return true
	}
	f := New(Config{Runtime: newSharedRuntime(), Capacity: ampleCapacity{}, Spawn: spawn})
	ctx, cancel := context.WithCancel(context.Background())
	f.Attach(ctx)
	t.Cleanup(func() { cancel(); f.Close() })
	settle()
	before := running.Load()

	f.Observe(obs("one.aiiospkg"), Policy{Revision: 1})
	until(t, "the package to activate", func() bool { return f.stateOf("id.example.one") == StateActive })
	if running.Load() <= before {
		t.Fatal("inconclusive: the executor's loop was not started by the owner's spawn")
	}
	f.Observe(nil, Policy{Revision: 1})
	until(t, "the instance to be forgotten", func() bool { return len(f.Snapshot().Instances) == 0 })
	until(t, "the executor's loop to end under its owner", func() bool { return running.Load() == before })
}

// DIFFERENT PLUGINS START AT THE SAME TIME — the boot that no longer
// waits, through the whole facility rather than one executor.
func TestTheControllerStartsEveryWantedPluginAtOnce(t *testing.T) {
	rt := newSharedRuntime()
	rt.gate = make(chan struct{})
	f := newFacility(t, rt)

	f.Observe(obs("one.aiiospkg", "two.aiiospkg", "three.aiiospkg", "four.aiiospkg"), Policy{Revision: 1})
	for i := 0; i < 4; i++ {
		select {
		case <-rt.entered:
		case <-time.After(15 * time.Second):
			t.Fatalf("only %d of 4 plugins had started; they are still waiting for each other", i)
		}
	}
	close(rt.gate)
	for _, id := range []string{"id.example.one", "id.example.two", "id.example.three", "id.example.four"} {
		until(t, id+" active", func() bool { return f.stateOf(id) == StateActive })
	}
}

// A NEWER PACKAGE FOR A SERVING ID IS AN UPDATE, not a second engine.
func TestNewBytesForAServingIdBecomeAnUpdate(t *testing.T) {
	rt := newSharedRuntime()
	f := newFacility(t, rt)
	// Both packages resolve to the same id.
	f.Observe([]Observed{{ID: "id.example.one", Package: "one.aiiospkg", Hash: "sha256:one.aiiospkg"}}, Policy{Revision: 1})
	until(t, "the first release", func() bool { return f.stateOf("id.example.one") == StateActive })

	f.Observe([]Observed{{ID: "id.example.one", Package: "one.aiiospkg", Hash: "sha256:two"}}, Policy{Revision: 1})
	until(t, "the update to redirect", func() bool { return strings.Contains(rt.seen(), "redirect:") })
	until(t, "the predecessor to retire", func() bool { return strings.Contains(rt.seen(), "stop:one.aiiospkg") })
	// The stop is noted before its retirement is recorded, and the record
	// is what drops the predecessor.
	until(t, "the predecessor's retirement to be recorded", func() bool {
		roles := f.rolesOf("id.example.one")
		return roles[RoleActive] == 1 && roles[RoleRetiring] == 0
	})
	// A start that must not follow has nothing to wait on.
	settle()
	if f.stateOf("id.example.one") != StateActive {
		t.Fatalf("after the update the id serves: %s", f.stateOf("id.example.one"))
	}
	// The predecessor retires under ITS OWN generation. Reporting the
	// successor's would retire the release that just took over, and the
	// controller would start another to replace it, for ever.
	roles := f.rolesOf("id.example.one")
	if roles[RoleActive] != 1 || roles[RoleRetiring] != 0 {
		t.Fatalf("one generation serves and none is retiring: %v", roles)
	}
	if n := strings.Count(rt.seen(), "start:"); n != 2 {
		t.Fatalf("an update is one more start, not a fleet of them: %s", rt.seen())
	}
}

// POLICY IS ASKED WITH THE EVIDENCE IN HAND, and a package it keeps off
// is present and named, never started and never forgotten.
func TestPolicyKeepsAPackageOffWithoutStartingIt(t *testing.T) {
	rt := newSharedRuntime()
	f := newFacility(t, rt)
	pol := Policy{Revision: 1, Allows: func(ev Evidence) (bool, string) {
		return false, "verified T3 is below plugins.autoload none"
	}}
	f.Observe(obs("one.aiiospkg"), pol)
	until(t, "the refusal", func() bool { return f.stateOf("id.example.one") == StateSkipped })
	if strings.Contains(rt.seen(), "start:") {
		t.Fatalf("a package policy keeps off must never be started: %s", rt.seen())
	}
	if !strings.Contains(rt.seen(), "verify:") {
		t.Fatalf("policy is asked with the evidence in hand: %s", rt.seen())
	}
	v := f.Snapshot().Instances[0]
	if v.Refusal == nil || v.Refusal.Stage != StagePolicy || !strings.Contains(v.Refusal.Cause.Error(), "below plugins.autoload") {
		t.Fatalf("the reason must be the one an operator reads: %+v", v.Refusal)
	}
}

// SAFE keeps everything off, and says so.
func TestSafeKeepsEveryPluginOff(t *testing.T) {
	rt := newSharedRuntime()
	f := newFacility(t, rt)
	f.Observe(obs("one.aiiospkg"), Policy{Revision: 1, Safe: true, SafeWhy: "the record is frozen"})
	until(t, "the refusal", func() bool { return f.stateOf("id.example.one") == StateSkipped })
	if strings.Contains(rt.seen(), "start:") {
		t.Fatalf("SAFE must start nothing: %s", rt.seen())
	}
	if v := f.Snapshot().Instances[0]; v.Refusal == nil || !strings.Contains(v.Refusal.Cause.Error(), "frozen") {
		t.Fatalf("SAFE must say why: %+v", v.Refusal)
	}
}

// A PERMANENT REFUSAL IS NOT RETRIED ON A TIMER, and the operator's Try
// again is what reopens it.
func TestAPermanentRefusalWaitsAndTryAgainReopensIt(t *testing.T) {
	rt := newSharedRuntime()
	rt.verifyErrFor["one.aiiospkg"] = errors.New("the signature does not verify")
	f := newFacility(t, rt)
	f.Observe(obs("one.aiiospkg"), Policy{Revision: 1})
	until(t, "the refusal", func() bool { return f.stateOf("id.example.one") == StateRefused })
	before := strings.Count(rt.seen(), "verify:")

	// Poke it repeatedly: a verification refusal does not become valid
	// because someone looked again.
	for i := 0; i < 10; i++ {
		f.Poke("nothing changed")
		time.Sleep(20 * time.Millisecond)
	}
	settle()
	if after := strings.Count(rt.seen(), "verify:"); after != before {
		t.Fatalf("a permanent refusal was retried %d more times by pokes alone", after-before)
	}
	if f.stateOf("id.example.one") != StateRefused {
		t.Fatalf("it stays refused, waiting on an input: %s", f.stateOf("id.example.one"))
	}

	// The operator says try again, and the package now verifies.
	rt.mu.Lock()
	delete(rt.verifyErrFor, "one.aiiospkg")
	rt.mu.Unlock()
	f.Retry("id.example.one")
	until(t, "the retry", func() bool { return f.stateOf("id.example.one") == StateActive })
}

func TestAStartThePackageRefusesWaitsOnThePackage(t *testing.T) {
	for _, bound := range []bool{true, false} {
		t.Run(fmt.Sprint("bound=", bound), func(t *testing.T) {
			rt := newSharedRuntime()
			rt.startErrFor["one.aiiospkg"] = fmt.Errorf("activate: %w", packageDecided{bound: bound})
			f := newFacility(t, rt)
			f.Observe(obs("one.aiiospkg"), Policy{Revision: 1})
			refused := func() (InstanceView, bool) {
				for _, v := range f.Snapshot().Instances {
					if v.ID == "id.example.one" && v.Refusal != nil {
						return v, true
					}
				}
				return InstanceView{}, false
			}
			until(t, "the refusal", func() bool { _, ok := refused(); return ok })
			view, _ := refused()
			r := view.Refusal
			if r.Stage != StageStart {
				t.Fatalf("precondition: %+v", r)
			}
			if !bound {
				if r.Class != ClassTransient || view.RetryAt.IsZero() {
					t.Fatalf("a cause the package does not decide keeps the start stage's answer: %s, retry at %v", r.Class, view.RetryAt)
				}
				return
			}
			if r.Class != ClassPermanent || !r.WaitsOn(InputPackage) || r.WaitsOn(InputPolicy) || r.Remedy == "" {
				t.Fatalf("a start the package refuses waits on the package: %+v", r)
			}
			if !view.RetryAt.IsZero() {
				t.Fatalf("no clock is armed for it: %v", view.RetryAt)
			}
			for i := 0; i < 10; i++ {
				f.Poke("nothing changed")
				time.Sleep(20 * time.Millisecond)
			}
			settle()
			if n := strings.Count(rt.seen(), "start:"); n != 1 {
				t.Fatalf("the same bytes are not asked again: %s", rt.seen())
			}

			rt.mu.Lock()
			delete(rt.startErrFor, "one.aiiospkg")
			rt.mu.Unlock()
			set := obs("one.aiiospkg")
			set[0].Hash = "sha256:corrected"
			f.Observe(set, Policy{Revision: 1})
			until(t, "the corrected package", func() bool { return f.stateOf("id.example.one") == StateActive })
		})
	}
}

// packageDecided is a runtime cause saying whether only different
// package bytes could change it.
type packageDecided struct{ bound bool }

func (e packageDecided) Error() string        { return "the package declares what this host cannot honor" }
func (e packageDecided) WaitsOnPackage() bool { return e.bound }

// A REPORT FROM A SUPERSEDED ATTEMPT CANNOT PUBLISH ITSELF over the
// successor that replaced it.
func TestAStaleReportCannotTakeOverTheSuccessor(t *testing.T) {
	rt := newSharedRuntime()
	f := newFacility(t, rt)
	f.Observe(obs("one.aiiospkg"), Policy{Revision: 1})
	until(t, "the activation", func() bool { return f.stateOf("id.example.one") == StateActive })

	f.mu.Lock()
	inst := f.instances["id.example.one"]
	serving := inst.Active()
	servingGen := serving.Gen
	f.mu.Unlock()

	// A report from a generation that never served — an attempt cancelled
	// long ago — arrives late, claiming to be active.
	wasVersion := f.versionOf("id.example.one")
	f.record(Event{PluginID: "id.example.one", Gen: servingGen + 99, Kind: EventActive, Version: "9.9.9", At: time.Now()})
	settle()
	f.mu.Lock()
	still := inst.Active()
	f.mu.Unlock()
	if still == nil || still.Gen != servingGen {
		t.Fatalf("a stale report replaced what serves: %+v", still)
	}
	// It could not write ITSELF onto the serving activation either: a
	// report acts on the generation it names or on nothing.
	if got := f.versionOf("id.example.one"); got != wasVersion {
		t.Fatalf("a stale report rewrote the serving release: %q, was %q", got, wasVersion)
	}
	if roles := f.rolesOf("id.example.one"); roles[RoleActive] != 1 {
		t.Fatalf("one generation serves: %v", roles)
	}
	if f.stateOf("id.example.one") != StateActive {
		t.Fatalf("state after a stale report: %s", f.stateOf("id.example.one"))
	}
}

// A SLOW READER BLOCKS NOTHING, and recovers everything from the
// snapshot it reads whenever it gets there.
func TestASlowReaderNeverBlocksTheLifecycle(t *testing.T) {
	rt := newSharedRuntime()
	f := newFacility(t, rt)
	bell, stop := f.Subscribe()
	defer stop()

	// Never read the doorbell while the lifecycle runs.
	f.Observe(obs("one.aiiospkg", "two.aiiospkg"), Policy{Revision: 1})
	until(t, "both active", func() bool {
		return f.stateOf("id.example.one") == StateActive && f.stateOf("id.example.two") == StateActive
	})
	// The doorbell has at most one pending ring, and the snapshot is
	// complete whenever the reader arrives.
	select {
	case <-bell:
	default:
		t.Fatal("a subscriber that never read was never rung")
	}
	snap := f.Snapshot()
	if snap.Revision == 0 || len(snap.Instances) != 2 {
		t.Fatalf("the snapshot carries everything: %+v", snap)
	}
	for _, v := range snap.Instances {
		if v.State != StateActive || len(v.Activations) == 0 {
			t.Fatalf("%s: %+v", v.ID, v)
		}
	}
}

// NOTHING RUNS BEFORE THE HOST'S PLANE EXISTS: a facility that was
// never attached plans nothing, however much it is told.
func TestAnUnattachedFacilityStartsNothing(t *testing.T) {
	rt := newSharedRuntime()
	f := New(Config{Runtime: rt})
	f.Observe(obs("one.aiiospkg"), Policy{Revision: 1})
	time.Sleep(50 * time.Millisecond)
	if rt.seen() != "" {
		t.Fatalf("an unattached facility reached the runtime: %s", rt.seen())
	}
	// Attaching runs the pass it was holding.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	f.Attach(ctx)
	defer f.Close()
	until(t, "the held want to run", func() bool { return f.stateOf("id.example.one") == StateActive })
}

// A RETIREMENT BELONGS TO THE GENERATION THAT INCURRED IT. When a
// predecessor's stop does not complete, the residue is the
// PREDECESSOR's — reporting it against the successor would charge the
// release that just took over with the debt of the one it replaced, and
// the facility would treat the serving engine as the one still owing.
func TestAPendingRetirementIsChargedToThePredecessor(t *testing.T) {
	rt := newSharedRuntime()
	f := newFacility(t, rt)
	f.Observe([]Observed{{ID: "id.example.one", Package: "one.aiiospkg", Hash: "sha256:one"}}, Policy{Revision: 1})
	until(t, "the first release", func() bool { return f.stateOf("id.example.one") == StateActive })

	f.mu.Lock()
	first := f.instances["id.example.one"].Active()
	firstGen := first.Gen
	// Its child will not reap when it is asked to stop.
	first.Lease.Hold("child", func() error { return errors.New("child 48211 not yet reaped") })
	f.mu.Unlock()

	f.Observe([]Observed{{ID: "id.example.one", Package: "one.aiiospkg", Hash: "sha256:two"}}, Policy{Revision: 1})
	until(t, "the redirect", func() bool { return strings.Contains(rt.seen(), "redirect:") })
	// The successor is recorded as serving after the redirect, and the
	// predecessor's residue is written once its stop has been asked, on a
	// worker of its own.
	until(t, "the successor to serve and the predecessor's stop to be asked", func() bool {
		f.mu.Lock()
		serving := f.instances["id.example.one"].Active()
		f.mu.Unlock()
		return serving != nil && serving.Gen != firstGen && len(first.Lease.Residue()) > 0
	})
	// A retirement that must not be reported against the successor has
	// nothing to wait on.
	settle()

	f.mu.Lock()
	inst := f.instances["id.example.one"]
	serving := inst.Active()
	retiring := inst.Retiring()
	f.mu.Unlock()

	if serving == nil || serving.Gen == firstGen {
		t.Fatalf("the successor must be serving: %+v", serving)
	}
	if retiring == nil || retiring.Gen != firstGen {
		t.Fatalf("the predecessor is the one that still owes: %+v", retiring)
	}
	if serving.Lease.Residue() != nil && len(serving.Lease.Residue()) > 0 {
		t.Fatalf("the successor was charged the predecessor's debt: %v", serving.Lease.Residue())
	}
	if res := retiring.Lease.Residue(); len(res) == 0 || !strings.Contains(res[0], "not yet reaped") {
		t.Fatalf("the predecessor's residue must name what it still holds: %v", res)
	}
	// And with a predecessor still owing, no further candidate is admitted.
	if inst.CanAdmitCandidate() {
		t.Fatal("a further update must wait for that retirement")
	}
}

// CLOSE IS TERMINAL. A facility that could be woken again after the host
// finished shutting down would spawn a child at the one moment nothing
// may be spawned — and the executors it would need have already been
// closed and joined.
func TestClosingTheFacilityIsTerminal(t *testing.T) {
	rt := newSharedRuntime()
	f := newFacility(t, rt)
	f.Observe([]Observed{{ID: "id.example.one", Package: "one.aiiospkg", Hash: "sha256:one"}}, Policy{Revision: 1})
	until(t, "the first release", func() bool { return f.stateOf("id.example.one") == StateActive })
	before := strings.Count(rt.seen(), "start:")

	f.Close()
	f.Observe([]Observed{{ID: "id.example.two", Package: "two.aiiospkg", Hash: "sha256:two"}}, Policy{Revision: 2})
	f.Retry("id.example.two")
	f.Poke("after close")
	settle()
	if n := strings.Count(rt.seen(), "start:"); n != before {
		t.Fatalf("a closed facility started something: %s", rt.seen())
	}
	// And closing twice is not an error.
	f.Close()
}

// A LATE REPORT FROM A SUPERSEDED ATTEMPT NEVER TAKES THE SEAT — even
// when its generation is one the controller knows. A slow start that
// finally readies after its replacement is already serving would
// otherwise retire the successor and promote itself, and the next pass
// would replace it again, and so on for ever.
func TestAKnownButOlderGenerationCannotDisplaceWhatServes(t *testing.T) {
	rt := newSharedRuntime()
	f := newFacility(t, rt)
	f.Observe([]Observed{{ID: "id.example.one", Package: "one.aiiospkg", Hash: "sha256:one"}}, Policy{Revision: 1})
	until(t, "the first release", func() bool { return f.stateOf("id.example.one") == StateActive })

	f.mu.Lock()
	inst := f.instances["id.example.one"]
	serving := inst.Active()
	if serving == nil {
		f.mu.Unlock()
		t.Fatal("precondition: nothing serves")
	}
	servingGen := serving.Gen
	// An earlier generation the controller does issue and remember.
	stale := NewActivation(servingGen-1, inst.ID, "0.9.0", inst.Package, inst.PackageHash, time.Now())
	stale.Role, stale.Intent = RoleStarting, serving.Intent
	inst.Activations = append(inst.Activations, stale)
	f.mu.Unlock()

	f.record(Event{PluginID: "id.example.one", Gen: servingGen - 1, Kind: EventActive, Version: "0.9.0", At: time.Now()})

	f.mu.Lock()
	defer f.mu.Unlock()
	if got := f.instances["id.example.one"].Active(); got == nil || got.Gen != servingGen {
		t.Fatalf("the serving generation must keep the seat, got %v", got)
	}
	if stale.Role == RoleActive {
		t.Fatalf("a superseded attempt took the seat: %s", stale.Role)
	}
}

// A VIEW IS A STILL FRAME. A reader handed the controller's own refusal
// could watch its class change under it, or edit it — and a page or a
// log is exactly the kind of reader that holds one across a pass.
func TestASnapshotCannotBeUsedToReachInsideTheController(t *testing.T) {
	rt := newSharedRuntime()
	rt.startErrFor["one.aiiospkg"] = errors.New("the port was busy")
	f := newFacility(t, rt)
	f.Observe([]Observed{{ID: "id.example.one", Package: "one.aiiospkg", Hash: "sha256:one"}}, Policy{Revision: 1})
	var view []InstanceView
	until(t, "the refusal", func() bool {
		view = f.Snapshot().Instances
		return len(view) == 1 && view[0].Refusal != nil
	})
	view[0].Refusal.Class = ClassPermanent
	view[0].Refusal.Stage = "tampered"
	view[0].Refusal.WakeOn = append(view[0].Refusal.WakeOn, "invented")

	again := f.Snapshot().Instances
	if again[0].Refusal.Class != ClassTransient || again[0].Refusal.Stage == "tampered" {
		t.Fatalf("a reader edited the controller's state through its view: %+v", again[0].Refusal)
	}
	if len(again[0].Refusal.WakeOn) != 0 {
		t.Fatalf("the wake inputs were reachable too: %v", again[0].Refusal.WakeOn)
	}
}

// THE PASS COMES BACK ON ITS OWN. A backoff that runs out is a moment
// the controller must act at, and on a quiet host no poke is coming to
// carry it there: without a timer, the retry scheduled for thirty
// seconds away simply never happens, and the plugin stays down until
// something unrelated wakes the loop.
func TestAnExpiringBackoffWakesTheControllerWithoutAPoke(t *testing.T) {
	rt := newSharedRuntime()
	rt.startErrFor["one.aiiospkg"] = errors.New("the port was busy")
	f := newFacility(t, rt)
	f.Observe([]Observed{{ID: "id.example.one", Package: "one.aiiospkg", Hash: "sha256:one"}}, Policy{Revision: 1})
	until(t, "the transient refusal to arm a wait", func() bool {
		f.mu.Lock()
		defer f.mu.Unlock()
		return !f.instances["id.example.one"].RetryAt.IsZero()
	})

	// It refused transiently, so a wait was armed. Shorten it to
	// something a test can sit through — the schedule is proved in
	// plan_test; what is proved here is that the CLOCK carries the pass.
	// What the runtime has seen is counted BEFORE the wait is shortened:
	// counted after it, on a host that took longer than the shortened
	// wait to get here, the clock's own retry was already in the count
	// and the loops below waited for a second one that never comes.
	woken := strings.Count(rt.seen(), "verify:")
	spent := strings.Count(rt.seen(), "start:")
	f.mu.Lock()
	inst := f.instances["id.example.one"]
	inst.RetryAt = time.Now().Add(250 * time.Millisecond)
	f.mu.Unlock()
	f.Poke("re-arm the timer over the shortened wait")

	// Not a single poke from here: only the clock. The loop below only
	// reads what the runtime saw; the bound is generous because a loaded
	// host delays the pass, and a pass that never comes misses it anyway.
	// Waking is the retry's first stage — verify is dispatched only once
	// the gate admits the attempt — and the retry is its reaching start.
	deadline := time.Now().Add(10 * time.Second)
	for strings.Count(rt.seen(), "verify:") <= woken {
		if time.Now().After(deadline) {
			t.Fatalf("the wait ran out and nothing woke to act on it: %s", rt.seen())
		}
		time.Sleep(10 * time.Millisecond)
	}
	for strings.Count(rt.seen(), "start:") <= spent {
		if time.Now().After(deadline) {
			t.Fatalf("the clock woke the controller but the retry never started: %s", rt.seen())
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// AND THE WAIT IS REAL. A pass that happens while the backoff still has
// time left must not spend an attempt.
func TestAPassDuringTheBackoffSpendsNothing(t *testing.T) {
	rt := newSharedRuntime()
	rt.startErrFor["one.aiiospkg"] = errors.New("the port was busy")
	f := newFacility(t, rt)
	f.Observe([]Observed{{ID: "id.example.one", Package: "one.aiiospkg", Hash: "sha256:one"}}, Policy{Revision: 1})
	until(t, "the first attempt", func() bool { return strings.Contains(rt.seen(), "start:") })
	// A burst that must not follow it has nothing to wait on.
	settle()
	spent := strings.Count(rt.seen(), "start:")
	if spent != 1 {
		t.Fatalf("one attempt, not a burst: %s", rt.seen())
	}
	for i := 0; i < 20; i++ {
		f.Poke("impatient")
	}
	settle()
	if n := strings.Count(rt.seen(), "start:"); n != spent {
		t.Fatalf("twenty passes inside the wait spent %d attempts: %s", n-spent, rt.seen())
	}
	// The operator asking explicitly is not a pass: Try again means now.
	f.Retry("id.example.one")
	until(t, "the attempt the operator asked for", func() bool { return strings.Count(rt.seen(), "start:") > spent })
}

// A RUNNING PLUGIN IS NOT EXEMPT FROM A POLICY THAT CHANGED. Unchanged
// bytes said "nothing to do", so SAFE coming on, the autoload level
// rising, or a signer being revoked left the plugin serving under an
// admission nobody had re-asked about — the one case where the answer
// matters most, because the engine is already running.
func TestAPolicyThatWithdrawsAdmissionStopsWhatIsAlreadyServing(t *testing.T) {
	rt := newSharedRuntime()
	f := newFacility(t, rt)
	set := []Observed{{ID: "id.example.one", Package: "one.aiiospkg", Hash: "sha256:one"}}
	f.Observe(set, Policy{Revision: 1})
	until(t, "the first release", func() bool { return f.stateOf("id.example.one") == StateActive })

	// SAFE, with the SAME bytes and the same package on disk.
	f.Observe(set, Policy{Revision: 2, Safe: true, SafeWhy: "the identity is in SAFE"})
	// And the refusal says which input withdrew it, so lifting that
	// input is what brings it back. The stop and the refusal are written
	// by different steps, so the wait is for both.
	var found *Refusal
	until(t, "SAFE to stop a plugin whose bytes had not changed, and to say why", func() bool {
		found = nil
		for _, v := range f.Snapshot().Instances {
			if v.ID == "id.example.one" && v.State != StateActive {
				found = v.Refusal
			}
		}
		return found != nil && strings.Contains(rt.seen(), "stop:one.aiiospkg")
	})
	if found.Stage != StagePolicy {
		t.Fatalf("the refusal must name policy: %+v", found)
	}

	// Lifting SAFE is a new question, and the answer is different.
	before := strings.Count(rt.seen(), "start:")
	f.Observe(set, Policy{Revision: 3})
	until(t, "lifting SAFE to start it and bring it back to serving", func() bool {
		return strings.Count(rt.seen(), "start:") > before && f.stateOf("id.example.one") == StateActive
	})
}

// AND ONE THAT STILL HOLDS IS LEFT ALONE. Re-asking is not restarting:
// a policy revision that changes nothing about this plugin must not
// cost it its process, its models or its working set.
func TestAReadmissionThatHoldsDoesNotRestartAnything(t *testing.T) {
	rt := newSharedRuntime()
	f := newFacility(t, rt)
	set := []Observed{{ID: "id.example.one", Package: "one.aiiospkg", Hash: "sha256:one"}}
	f.Observe(set, Policy{Revision: 1})
	until(t, "the first release", func() bool { return f.stateOf("id.example.one") == StateActive })
	starts := strings.Count(rt.seen(), "start:")

	for r := uint64(2); r < 6; r++ {
		f.Observe(set, Policy{Revision: r})
	}
	settle()
	if n := strings.Count(rt.seen(), "start:"); n != starts {
		t.Fatalf("re-asking restarted the plugin %d time(s): %s", n-starts, rt.seen())
	}
	if strings.Contains(rt.seen(), "stop:one.aiiospkg") {
		t.Fatalf("a plugin that is still admitted must not be stopped: %s", rt.seen())
	}
	if f.stateOf("id.example.one") != StateActive {
		t.Fatalf("it must still be serving: %s", f.stateOf("id.example.one"))
	}
}

// blockingStop holds ONE package's stop open, so a test can ask what
// the rest of the facility can do while a cleanup is in flight.
type blockingStop struct {
	*sharedRuntime
	pkg     string
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (b *blockingStop) Stop(ctx context.Context, r Running) (Retirement, error) {
	if fr, ok := r.(*fakeRunning); ok && fr.tag == b.pkg {
		b.once.Do(func() { close(b.entered) })
		<-b.release
	}
	return b.sharedRuntime.Stop(ctx, r)
}

// GIVING A RESOURCE BACK IS WORK, NOT A FIELD ACCESS. Stopping a child,
// unpinning a runtime root, waiting for a reap: it takes as long as it
// takes. It used to take that long with the custody ledger's own lock
// held — and the facility reads that ledger, under the facility lock,
// to decide whether a candidate may start. So one plugin's slow stop
// stalled every other plugin's pass, and a cleanup that had reached
// back into the facility would have closed a lock cycle instead.
func TestASlowCleanupDoesNotStallEveryOtherPlugin(t *testing.T) {
	rt := &blockingStop{sharedRuntime: newSharedRuntime(), pkg: "one.aiiospkg",
		entered: make(chan struct{}), release: make(chan struct{})}
	var freed sync.Once
	free := func() { freed.Do(func() { close(rt.release) }) }
	t.Cleanup(free)

	f := newFacility(t, rt)
	f.Observe(obs("one.aiiospkg"), Policy{Revision: 1})
	until(t, "the first plugin to serve", func() bool { return f.stateOf(idOf("one.aiiospkg")) == StateActive })

	// It leaves the directory, so its stop runs — and blocks.
	f.Observe(nil, Policy{Revision: 1})
	select {
	case <-rt.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("the stop never ran")
	}

	// The ledger is still readable while its own cleanup is in flight.
	done := make(chan struct{})
	go func() { defer close(done); _ = f.Snapshot() }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("a snapshot blocked on a cleanup that was still running")
	}

	// And an unrelated plugin still comes up.
	f.Observe(obs("two.aiiospkg"), Policy{Revision: 1})
	deadline := time.After(5 * time.Second)
	for f.stateOf(idOf("two.aiiospkg")) != StateActive {
		select {
		case <-deadline:
			t.Fatalf("one plugin's cleanup stalled the whole facility: %s", f.stateOf(idOf("two.aiiospkg")))
		default:
			time.Sleep(20 * time.Millisecond)
		}
	}
	free()
}

// AN APPROVAL IS OF THE QUESTION THAT WAS ASKED. A re-admission runs
// while the world keeps moving: hold the policy check open, let a newer
// policy arrive that would refuse, then release the old approval. The
// plugin used to be stamped with whatever was current when the answer
// landed — so an approval given under one revision silently became an
// approval of SAFE, and nothing asked again.
func TestAReadmissionCannotApproveAPolicyItNeverSaw(t *testing.T) {
	rt := newSharedRuntime()
	f := newFacility(t, rt)
	set := []Observed{{ID: "id.example.one", Package: "one.aiiospkg", Hash: "sha256:one"}}
	f.Observe(set, Policy{Revision: 1})
	until(t, "the first release", func() bool { return f.stateOf("id.example.one") == StateActive })

	// Revision 2 asks again, and its answer is held open inside the
	// policy check — where a real one waits on a store or a signer.
	asked := make(chan struct{})
	let := make(chan struct{})
	var once sync.Once
	f.Observe(set, Policy{Revision: 2, Allows: func(Evidence) (bool, string) {
		once.Do(func() { close(asked) })
		<-let
		return true, "" // what revision 2 says, and only revision 2
	}})
	select {
	case <-asked:
	case <-time.After(5 * time.Second):
		t.Fatal("the re-admission never reached the policy check")
	}

	// Revision 3 arrives while that answer is still in the air, and it
	// would refuse.
	f.Observe(set, Policy{Revision: 3, Safe: true, SafeWhy: "the identity is in SAFE"})
	close(let)

	// SAFE is asked about only once revision 2's answer is out of the
	// air, and the stop is the last thing that question brings.
	until(t, "SAFE to stop what an approval of revision 2 left serving", func() bool {
		return f.stateOf("id.example.one") != StateActive && strings.Contains(rt.seen(), "stop:one.aiiospkg")
	})
}

// AND A SUPERSEDED ANSWER CHANGES NOTHING. While one re-admission is
// being answered the inputs can move again, and the controller asks a
// fresh question. The old answer then arrives about a policy nobody is
// asking about any more: taking it would stamp the running engine as
// admitted under inputs that were never checked against it, and
// clearing the pending question with it would leave the live one
// looking unasked.
func TestASupersededReadmissionAnswerChangesNothing(t *testing.T) {
	rt := newSharedRuntime()
	f := newFacility(t, rt)
	set := []Observed{{ID: "id.example.one", Package: "one.aiiospkg", Hash: "sha256:one"}}
	f.Observe(set, Policy{Revision: 1})
	until(t, "the first release", func() bool { return f.stateOf("id.example.one") == StateActive })

	f.mu.Lock()
	inst := f.instances["id.example.one"]
	active := inst.Active()
	if active == nil {
		f.mu.Unlock()
		t.Fatal("precondition: nothing serves")
	}
	wasStamped := active.Intent
	stale := intentOf(inst.ID, inst.Package, inst.PackageHash, inst.ManifestHash, Policy{Revision: 2})
	live := intentOf(inst.ID, inst.Package, inst.PackageHash, inst.ManifestHash, Policy{Revision: 3})
	inst.ReadmitFor = live // the question actually outstanding
	f.mu.Unlock()

	// The answer to the question that was superseded.
	f.record(Event{PluginID: "id.example.one", Gen: active.Gen, Kind: EventReadmitted,
		Version: "1.0.0", Intent: stale, At: time.Now()})

	f.mu.Lock()
	gotIntent, pending := inst.Active().Intent, inst.ReadmitFor
	f.mu.Unlock()
	if gotIntent != wasStamped {
		t.Fatalf("a superseded answer re-stamped the running engine: %q -> %q", wasStamped, gotIntent)
	}
	if pending != live {
		t.Fatalf("the live question was cleared by an answer to an older one: %q", pending)
	}

	// The answer to the question that IS outstanding is taken.
	f.record(Event{PluginID: "id.example.one", Gen: active.Gen, Kind: EventReadmitted,
		Version: "1.0.0", Intent: live, At: time.Now()})

	f.mu.Lock()
	gotIntent, pending = inst.Active().Intent, inst.ReadmitFor
	f.mu.Unlock()
	if gotIntent != live || pending != "" {
		t.Fatalf("the current answer must be taken: intent=%q pending=%q", gotIntent, pending)
	}
}
