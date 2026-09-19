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
// .
type sharedRuntime struct {
	// .
	// .
	// .
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

// .
// .
// .
type ampleCapacity struct{}

func (ampleCapacity) Measure() Availability {
	return Availability{HostKnown: true, HostTotal: 64 << 30, HostAvailable: 60 << 30}
}

func (s *sharedRuntime) note(f string, a ...interface{}) {
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
	// .
	// .
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

// .
// .
func settle() { time.Sleep(200 * time.Millisecond) }

// .
func (f *Facility) rolesOf(id string) map[Role]int {
	out := map[Role]int{}
	for _, v := range f.Snapshot().Instances {
		if v.ID != id {
			continue
		}
		for _, role := range v.Generations {
			out[role]++
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

// .
// .
func TestObservingAPackageActivatesItAndLosingItStopsIt(t *testing.T) {
	rt := newSharedRuntime()
	f := newFacility(t, rt)

	f.Observe(obs("one.aiiospkg"), Policy{Revision: 1})
	until(t, "the package to activate", func() bool { return f.stateOf("id.example.one") == StateActive })

	// .
	f.Observe(nil, Policy{Revision: 1})
	until(t, "the package to stop", func() bool {
		return !strings.Contains(rt.seen(), "start:two") && strings.Contains(rt.seen(), "stop:one.aiiospkg")
	})
	until(t, "the instance to be forgotten", func() bool { return len(f.Snapshot().Instances) == 0 })
}

// .
// .
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

// .
func TestNewBytesForAServingIdBecomeAnUpdate(t *testing.T) {
	rt := newSharedRuntime()
	f := newFacility(t, rt)
	// .
	f.Observe([]Observed{{ID: "id.example.one", Package: "one.aiiospkg", Hash: "sha256:one.aiiospkg"}}, Policy{Revision: 1})
	until(t, "the first release", func() bool { return f.stateOf("id.example.one") == StateActive })

	f.Observe([]Observed{{ID: "id.example.one", Package: "one.aiiospkg", Hash: "sha256:two"}}, Policy{Revision: 1})
	until(t, "the update to redirect", func() bool { return strings.Contains(rt.seen(), "redirect:") })
	until(t, "the predecessor to retire", func() bool { return strings.Contains(rt.seen(), "stop:one.aiiospkg") })
	settle()
	if f.stateOf("id.example.one") != StateActive {
		t.Fatalf("after the update the id serves: %s", f.stateOf("id.example.one"))
	}
	// .
	// .
	// .
	roles := f.rolesOf("id.example.one")
	if roles[RoleActive] != 1 || roles[RoleRetiring] != 0 {
		t.Fatalf("one generation serves and none is retiring: %v", roles)
	}
	if n := strings.Count(rt.seen(), "start:"); n != 2 {
		t.Fatalf("an update is one more start, not a fleet of them: %s", rt.seen())
	}
}

// .
// .
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

// .
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

// .
// .
func TestAPermanentRefusalWaitsAndTryAgainReopensIt(t *testing.T) {
	rt := newSharedRuntime()
	rt.verifyErrFor["one.aiiospkg"] = errors.New("the signature does not verify")
	f := newFacility(t, rt)
	f.Observe(obs("one.aiiospkg"), Policy{Revision: 1})
	until(t, "the refusal", func() bool { return f.stateOf("id.example.one") == StateRefused })
	before := strings.Count(rt.seen(), "verify:")

	// .
	// .
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

	// .
	rt.mu.Lock()
	delete(rt.verifyErrFor, "one.aiiospkg")
	rt.mu.Unlock()
	f.Retry("id.example.one")
	until(t, "the retry", func() bool { return f.stateOf("id.example.one") == StateActive })
}

// .
// .
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

	// .
	// .
	wasVersion := f.versionOf("id.example.one")
	f.record(Event{PluginID: "id.example.one", Gen: servingGen + 99, Kind: EventActive, Version: "9.9.9", At: time.Now()})
	settle()
	f.mu.Lock()
	still := inst.Active()
	f.mu.Unlock()
	if still == nil || still.Gen != servingGen {
		t.Fatalf("a stale report replaced what serves: %+v", still)
	}
	// .
	// .
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

// .
// .
func TestASlowReaderNeverBlocksTheLifecycle(t *testing.T) {
	rt := newSharedRuntime()
	f := newFacility(t, rt)
	bell, stop := f.Subscribe()
	defer stop()

	// .
	f.Observe(obs("one.aiiospkg", "two.aiiospkg"), Policy{Revision: 1})
	until(t, "both active", func() bool {
		return f.stateOf("id.example.one") == StateActive && f.stateOf("id.example.two") == StateActive
	})
	// .
	// .
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
		if v.State != StateActive || len(v.Generations) == 0 {
			t.Fatalf("%s: %+v", v.ID, v)
		}
	}
}

// .
// .
func TestAnUnattachedFacilityStartsNothing(t *testing.T) {
	rt := newSharedRuntime()
	f := New(Config{Runtime: rt})
	f.Observe(obs("one.aiiospkg"), Policy{Revision: 1})
	time.Sleep(50 * time.Millisecond)
	if rt.seen() != "" {
		t.Fatalf("an unattached facility reached the runtime: %s", rt.seen())
	}
	// .
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	f.Attach(ctx)
	defer f.Close()
	until(t, "the held want to run", func() bool { return f.stateOf("id.example.one") == StateActive })
}

// .
// .
// .
// .
// .
func TestAPendingRetirementIsChargedToThePredecessor(t *testing.T) {
	rt := newSharedRuntime()
	f := newFacility(t, rt)
	f.Observe([]Observed{{ID: "id.example.one", Package: "one.aiiospkg", Hash: "sha256:one"}}, Policy{Revision: 1})
	until(t, "the first release", func() bool { return f.stateOf("id.example.one") == StateActive })

	f.mu.Lock()
	first := f.instances["id.example.one"].Active()
	firstGen := first.Gen
	// .
	first.Lease.Hold("child", func() error { return errors.New("child 48211 not yet reaped") })
	f.mu.Unlock()

	f.Observe([]Observed{{ID: "id.example.one", Package: "one.aiiospkg", Hash: "sha256:two"}}, Policy{Revision: 1})
	until(t, "the redirect", func() bool { return strings.Contains(rt.seen(), "redirect:") })
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
	// .
	if inst.CanAdmitCandidate() {
		t.Fatal("a further update must wait for that retirement")
	}
}

// .
// .
// .
// .
func TestClosingTheFacilityIsTerminal(t *testing.T) {
	rt := newSharedRuntime()
	f := newFacility(t, rt)
	f.Observe([]Observed{{ID: "id.example.one", Package: "one.aiiospkg", Hash: "sha256:one"}}, Policy{Revision: 1})
	settle()
	if f.stateOf("id.example.one") != StateActive {
		t.Fatalf("precondition: %s", f.stateOf("id.example.one"))
	}
	before := strings.Count(rt.seen(), "start:")

	f.Close()
	f.Observe([]Observed{{ID: "id.example.two", Package: "two.aiiospkg", Hash: "sha256:two"}}, Policy{Revision: 2})
	f.Retry("id.example.two")
	f.Poke("after close")
	settle()
	if n := strings.Count(rt.seen(), "start:"); n != before {
		t.Fatalf("a closed facility started something: %s", rt.seen())
	}
	// .
	f.Close()
}

// .
// .
// .
// .
// .
func TestAKnownButOlderGenerationCannotDisplaceWhatServes(t *testing.T) {
	rt := newSharedRuntime()
	f := newFacility(t, rt)
	f.Observe([]Observed{{ID: "id.example.one", Package: "one.aiiospkg", Hash: "sha256:one"}}, Policy{Revision: 1})
	settle()

	f.mu.Lock()
	inst := f.instances["id.example.one"]
	serving := inst.Active()
	if serving == nil {
		f.mu.Unlock()
		t.Fatal("precondition: nothing serves")
	}
	servingGen := serving.Gen
	// .
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

// .
// .
// .
func TestASnapshotCannotBeUsedToReachInsideTheController(t *testing.T) {
	rt := newSharedRuntime()
	rt.startErrFor["one.aiiospkg"] = errors.New("the port was busy")
	f := newFacility(t, rt)
	f.Observe([]Observed{{ID: "id.example.one", Package: "one.aiiospkg", Hash: "sha256:one"}}, Policy{Revision: 1})
	settle()

	view := f.Snapshot().Instances
	if len(view) != 1 || view[0].Refusal == nil {
		t.Fatalf("precondition: %+v", view)
	}
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

// .
// .
// .
// .
// .
func TestAnExpiringBackoffWakesTheControllerWithoutAPoke(t *testing.T) {
	rt := newSharedRuntime()
	rt.startErrFor["one.aiiospkg"] = errors.New("the port was busy")
	f := newFacility(t, rt)
	f.Observe([]Observed{{ID: "id.example.one", Package: "one.aiiospkg", Hash: "sha256:one"}}, Policy{Revision: 1})
	settle()

	// .
	// .
	// .
	f.mu.Lock()
	inst := f.instances["id.example.one"]
	if inst.RetryAt.IsZero() {
		f.mu.Unlock()
		t.Fatal("a transient refusal must arm a wait")
	}
	inst.RetryAt = time.Now().Add(250 * time.Millisecond)
	f.mu.Unlock()
	f.Poke("re-arm the timer over the shortened wait")
	settle()
	spent := strings.Count(rt.seen(), "start:")

	// .
	time.Sleep(700 * time.Millisecond)
	if n := strings.Count(rt.seen(), "start:"); n <= spent {
		t.Fatalf("the wait ran out and nothing woke to act on it: %s", rt.seen())
	}
}

// .
// .
func TestAPassDuringTheBackoffSpendsNothing(t *testing.T) {
	rt := newSharedRuntime()
	rt.startErrFor["one.aiiospkg"] = errors.New("the port was busy")
	f := newFacility(t, rt)
	f.Observe([]Observed{{ID: "id.example.one", Package: "one.aiiospkg", Hash: "sha256:one"}}, Policy{Revision: 1})
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
	// .
	f.Retry("id.example.one")
	settle()
	if n := strings.Count(rt.seen(), "start:"); n <= spent {
		t.Fatalf("the operator asked and nothing happened: %s", rt.seen())
	}
}

// .
// .
// .
// .
// .
func TestAPolicyThatWithdrawsAdmissionStopsWhatIsAlreadyServing(t *testing.T) {
	rt := newSharedRuntime()
	f := newFacility(t, rt)
	set := []Observed{{ID: "id.example.one", Package: "one.aiiospkg", Hash: "sha256:one"}}
	f.Observe(set, Policy{Revision: 1})
	settle()
	if f.stateOf("id.example.one") != StateActive {
		t.Fatalf("precondition: %s", f.stateOf("id.example.one"))
	}

	// .
	f.Observe(set, Policy{Revision: 2, Safe: true, SafeWhy: "the identity is in SAFE"})
	settle()
	if st := f.stateOf("id.example.one"); st == StateActive {
		t.Fatalf("a plugin kept serving through SAFE because its bytes had not changed: %s", st)
	}
	if !strings.Contains(rt.seen(), "stop:one.aiiospkg") {
		t.Fatalf("it was never asked to stop: %s", rt.seen())
	}
	// .
	// .
	var found *Refusal
	for _, v := range f.Snapshot().Instances {
		if v.ID == "id.example.one" {
			found = v.Refusal
		}
	}
	if found == nil || found.Stage != StagePolicy {
		t.Fatalf("the refusal must name policy: %+v", found)
	}

	// .
	before := strings.Count(rt.seen(), "start:")
	f.Observe(set, Policy{Revision: 3})
	settle()
	if n := strings.Count(rt.seen(), "start:"); n <= before {
		t.Fatalf("lifting SAFE must bring it back, not leave it refused: %s", rt.seen())
	}
	if st := f.stateOf("id.example.one"); st != StateActive {
		t.Fatalf("after SAFE lifted it must serve again: %s", st)
	}
}

// .
// .
// .
func TestAReadmissionThatHoldsDoesNotRestartAnything(t *testing.T) {
	rt := newSharedRuntime()
	f := newFacility(t, rt)
	set := []Observed{{ID: "id.example.one", Package: "one.aiiospkg", Hash: "sha256:one"}}
	f.Observe(set, Policy{Revision: 1})
	settle()
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

// .
// .
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

// .
// .
// .
// .
// .
// .
// .
func TestASlowCleanupDoesNotStallEveryOtherPlugin(t *testing.T) {
	rt := &blockingStop{sharedRuntime: newSharedRuntime(), pkg: "one.aiiospkg",
		entered: make(chan struct{}), release: make(chan struct{})}
	var freed sync.Once
	free := func() { freed.Do(func() { close(rt.release) }) }
	t.Cleanup(free)

	f := newFacility(t, rt)
	f.Observe(obs("one.aiiospkg"), Policy{Revision: 1})
	settle()
	if f.stateOf(idOf("one.aiiospkg")) != StateActive {
		t.Fatalf("precondition: %s", f.stateOf(idOf("one.aiiospkg")))
	}

	// .
	f.Observe(nil, Policy{Revision: 1})
	select {
	case <-rt.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("the stop never ran")
	}

	// .
	done := make(chan struct{})
	go func() { defer close(done); _ = f.Snapshot() }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("a snapshot blocked on a cleanup that was still running")
	}

	// .
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

// .
// .
// .
// .
// .
// .
func TestAReadmissionCannotApproveAPolicyItNeverSaw(t *testing.T) {
	rt := newSharedRuntime()
	f := newFacility(t, rt)
	set := []Observed{{ID: "id.example.one", Package: "one.aiiospkg", Hash: "sha256:one"}}
	f.Observe(set, Policy{Revision: 1})
	settle()
	if f.stateOf("id.example.one") != StateActive {
		t.Fatalf("precondition: %s", f.stateOf("id.example.one"))
	}

	// .
	// .
	asked := make(chan struct{})
	let := make(chan struct{})
	var once sync.Once
	f.Observe(set, Policy{Revision: 2, Allows: func(Evidence) (bool, string) {
		once.Do(func() { close(asked) })
		<-let
		return true, ""
	}})
	select {
	case <-asked:
	case <-time.After(5 * time.Second):
		t.Fatal("the re-admission never reached the policy check")
	}

	// .
	// .
	f.Observe(set, Policy{Revision: 3, Safe: true, SafeWhy: "the identity is in SAFE"})
	close(let)
	settle()
	settle()

	if st := f.stateOf("id.example.one"); st == StateActive {
		t.Fatalf("an approval of revision 2 was taken as approval of SAFE: %s", st)
	}
	if !strings.Contains(rt.seen(), "stop:one.aiiospkg") {
		t.Fatalf("SAFE must stop it: %s", rt.seen())
	}
}

// .
// .
// .
// .
// .
// .
// .
func TestASupersededReadmissionAnswerChangesNothing(t *testing.T) {
	rt := newSharedRuntime()
	f := newFacility(t, rt)
	set := []Observed{{ID: "id.example.one", Package: "one.aiiospkg", Hash: "sha256:one"}}
	f.Observe(set, Policy{Revision: 1})
	settle()

	f.mu.Lock()
	inst := f.instances["id.example.one"]
	active := inst.Active()
	if active == nil {
		f.mu.Unlock()
		t.Fatal("precondition: nothing serves")
	}
	wasStamped := active.Intent
	stale := intentOf(inst.ID, inst.Package, inst.PackageHash, Policy{Revision: 2})
	live := intentOf(inst.ID, inst.Package, inst.PackageHash, Policy{Revision: 3})
	inst.ReadmitFor = live
	f.mu.Unlock()

	// .
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

	// .
	f.record(Event{PluginID: "id.example.one", Gen: active.Gen, Kind: EventReadmitted,
		Version: "1.0.0", Intent: live, At: time.Now()})

	f.mu.Lock()
	gotIntent, pending = inst.Active().Intent, inst.ReadmitFor
	f.mu.Unlock()
	if gotIntent != live || pending != "" {
		t.Fatalf("the current answer must be taken: intent=%q pending=%q", gotIntent, pending)
	}
}
