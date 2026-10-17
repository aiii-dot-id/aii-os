package pluginfacility

import (
	"errors"
	"testing"
	"time"
)

// THE PLAN ITSELF, with no goroutine in the way: what one pass decides,
// given what exists. A pass is where "should this run again?" is
// answered, so the answer is checked here rather than inferred from how
// fast something happened to be dispatched.
func planFor(t *testing.T, build func(*Instance)) []string {
	t.Helper()
	f := New(Config{Runtime: newSharedRuntime()})
	inst := &Instance{ID: "id.example.one", Package: "one.aiiospkg", PackageHash: "sha256:one",
		Verified: true, Desired: Desired{Active: true, Package: "one.aiiospkg", Hash: "sha256:one"}}
	f.policy = Policy{Revision: 1}
	build(inst)
	f.instances[inst.ID] = inst
	intent := intentOf(inst.ID, inst.Package, inst.PackageHash, inst.ManifestHash, f.policy)
	// A pass stamps every activation it issues with the admission it was
	// issued under; a fixture that left them blank would be testing a
	// state the controller never produces.
	for _, a := range inst.Activations {
		if a.Intent == "" {
			a.Intent = intent
		}
	}

	f.mu.Lock()
	defer f.mu.Unlock()
	inst.Budget.For(intent)
	kind, want := f.plan(inst, intent, time.Now())
	var dispatched []string
	if want {
		dispatched = append(dispatched, string(kind))
	}
	return dispatched
}

func refusedAt(inst *Instance, gen Generation, stage Stage) {
	a := NewActivation(gen, inst.ID, "1.0.0", inst.Package, inst.PackageHash, time.Now())
	a.Role = RoleRefused
	a.Refusal = NewRefusal(inst.ID, "1.0.0", gen, stage, errors.New("because"))
	_ = a.Lease.Release()
	inst.Activations = append(inst.Activations, a)
}

func TestAPassRetriesWhatRecoversAndLeavesWhatDoesNot(t *testing.T) {
	// Nothing yet: it is wanted, so it is dispatched.
	if got := planFor(t, func(i *Instance) {}); len(got) != 1 {
		t.Fatalf("a wanted package with nothing in flight must be dispatched: %v", got)
	}
	// A PERMANENT refusal waits on an input, not on a pass. Looking
	// again does not make a signature valid.
	for _, stage := range []Stage{StageVerify, StagePolicy, StageContain, StageRegister} {
		got := planFor(t, func(i *Instance) { refusedAt(i, 1, stage) })
		if len(got) != 0 {
			t.Fatalf("%s is permanent and must not be retried by a pass: %v", stage, got)
		}
	}
	// A CANCELLATION IS NOT AN ANSWER. It is classed permanent because no
	// clock recovers it, but it was never about the package: the question
	// was taken back. While the package is still wanted under the very
	// same intent — an uninstall during a start, and the same bytes put
	// back before the record was forgotten — it is simply asked again.
	// This list used to include it, and that pair of steps left a plugin
	// refused for ever with nothing wrong with it. The retry budget is
	// what bounds it, as it bounds every other attempt.
	if got := planFor(t, func(i *Instance) { refusedAt(i, 1, StageCancelled) }); len(got) != 1 {
		t.Fatalf("a withdrawn attempt must not stand in the way of a package that is wanted: %v", got)
	}
	// A TRANSIENT one is exactly what a pass is for.
	for _, stage := range []Stage{StageStart, StageReadiness, StageHealth, StageMaterial, StageCrashed} {
		got := planFor(t, func(i *Instance) { refusedAt(i, 1, stage) })
		if len(got) != 1 {
			t.Fatalf("%s recovers and must be retried: %v", stage, got)
		}
	}
	// An attempt already on its way is not doubled.
	if got := planFor(t, func(i *Instance) {
		i.Activations = append(i.Activations, NewActivation(1, i.ID, "1.0.0", i.Package, i.PackageHash, time.Now()))
	}); len(got) != 0 {
		t.Fatalf("an attempt in flight must not be doubled: %v", got)
	}
	// What already serves these exact bytes is left alone.
	if got := planFor(t, func(i *Instance) {
		a := NewActivation(1, i.ID, "1.0.0", i.Package, i.PackageHash, time.Now())
		a.Role = RoleActive
		i.Activations = append(i.Activations, a)
	}); len(got) != 0 {
		t.Fatalf("what already serves must be left alone: %v", got)
	}
	// A PREDECESSOR THAT STILL OWES holds back the next candidate: two
	// engines at a time, never three, each with its own reservation and
	// its own models.
	if got := planFor(t, func(i *Instance) {
		serving := NewActivation(1, i.ID, "1.0.0", i.Package, "sha256:old", time.Now())
		serving.Role = RoleActive
		leaving := NewActivation(2, i.ID, "0.9.0", i.Package, "sha256:older", time.Now())
		leaving.Lease.Hold("child", func() error { return errors.New("not yet reaped") })
		_ = leaving.Lease.Release()
		leaving.Retiring(nil)
		i.Activations = append(i.Activations, serving, leaving)
	}); len(got) != 0 {
		t.Fatalf("a candidate must wait for the retiring predecessor: %v", got)
	}
	// Once it owes nothing, the update is planned.
	if got := planFor(t, func(i *Instance) {
		serving := NewActivation(1, i.ID, "1.0.0", i.Package, "sha256:old", time.Now())
		serving.Role = RoleActive
		i.Activations = append(i.Activations, serving)
	}); len(got) != 1 || got[0] != string(cmdUpdate) {
		t.Fatalf("new bytes for a serving id are an update: %v", got)
	}

	// An exhausted budget stops even a transient one, until its window
	// reopens or the operator says so.
	if got := planFor(t, func(i *Instance) {
		refusedAt(i, 1, StageReadiness)
		i.Budget = Budget{Max: 2, Window: time.Hour}
		i.Budget.For(intentOf(i.ID, i.Package, i.PackageHash, i.ManifestHash, Policy{Revision: 1}))
		i.Budget.Record(time.Now())
		i.Budget.Record(time.Now())
	}); len(got) != 0 {
		t.Fatalf("an exhausted budget admits nothing: %v", got)
	}
}

// A PERMANENT REFUSAL IS NOT FOREVER — it is until the input it waits
// on moves. Verify waits on the package and the trust generation;
// policy waits on the policy. A refusal recorded under inputs that have
// since changed has been overtaken, and a pass that still honoured it
// would leave a plugin refused for a reason that no longer exists: the
// operator lifts SAFE, or restores a signer, and nothing happens.
func TestARefusalStopsBlockingOnceItsInputHasMoved(t *testing.T) {
	stale := func(i *Instance) {
		refusedAt(i, 1, StagePolicy)
		// Recorded when the policy was at an earlier revision.
		i.Activations[len(i.Activations)-1].Intent = intentOf(i.ID, i.Package, i.PackageHash, i.ManifestHash, Policy{Revision: 0})
	}
	if got := planFor(t, stale); len(got) != 1 || got[0] != string(cmdActivate) {
		t.Fatalf("a refusal from a policy that has since changed must not hold the plugin down: %v", got)
	}
	// The same refusal under the CURRENT policy still holds.
	if got := planFor(t, func(i *Instance) { refusedAt(i, 1, StagePolicy) }); len(got) != 0 {
		t.Fatalf("a refusal still answering the current question must hold: %v", got)
	}
	// And a refusal about OTHER bytes does not hold these ones down.
	if got := planFor(t, func(i *Instance) {
		refusedAt(i, 1, StageVerify)
		i.Activations[len(i.Activations)-1].Intent = intentOf(i.ID, "other.aiiospkg", "sha256:something-else", "", Policy{Revision: 1})
	}); len(got) != 1 {
		t.Fatalf("a refusal about other bytes must not hold these down: %v", got)
	}
}

// SAME BYTES IS NOT THE SAME QUESTION. A plugin serving the desired
// bytes needs nothing only while the admission it was granted under
// still stands. When SAFE comes on, the autoload level rises, or its
// signer is revoked, the shortcut that said "unchanged bytes, nothing to
// do" left it running with an admission nobody had re-asked about.
func TestAServingReleaseIsAskedAgainWhenItsAdmissionMoved(t *testing.T) {
	serving := func(intent string) func(*Instance) {
		return func(i *Instance) {
			a := NewActivation(1, i.ID, "1.0.0", i.Package, i.PackageHash, time.Now())
			a.Role, a.Intent = RoleActive, intent
			i.Activations = append(i.Activations, a)
		}
	}
	current := intentOf("id.example.one", "one.aiiospkg", "sha256:one", "", Policy{Revision: 1})
	if got := planFor(t, serving(current)); len(got) != 0 {
		t.Fatalf("a release serving under the current admission needs nothing: %v", got)
	}
	old := intentOf("id.example.one", "one.aiiospkg", "sha256:one", "", Policy{Revision: 0})
	if got := planFor(t, serving(old)); len(got) != 1 || got[0] != string(cmdReadmit) {
		t.Fatalf("a release whose admission moved must be asked again, not left alone: %v", got)
	}
	// And not asked twice while the first question is in flight.
	if got := planFor(t, func(i *Instance) {
		serving(old)(i)
		i.ReadmitFor = current
	}); len(got) != 0 {
		t.Fatalf("one question at a time: %v", got)
	}
}

// THE BACKOFF IS PART OF THE PLAN. A budget bounds how many attempts a
// window holds and says nothing about how fast they are spent: without
// a wait, five attempts burn in a tenth of a second and the plugin is
// spent for ten minutes before the operator has read the first refusal.
func TestATransientRefusalWaitsBeforeItIsTriedAgain(t *testing.T) {
	if got := planFor(t, func(i *Instance) {
		refusedAt(i, 1, StageStart)
		i.RetryAt = time.Now().Add(time.Minute)
	}); len(got) != 0 {
		t.Fatalf("a transient refusal must wait out its backoff: %v", got)
	}
	if got := planFor(t, func(i *Instance) {
		refusedAt(i, 1, StageStart)
		i.RetryAt = time.Now().Add(-time.Second)
	}); len(got) != 1 {
		t.Fatalf("once the wait has run out it is tried again: %v", got)
	}
	// THE TWO SCHEDULES ARE DIFFERENT ON PURPOSE, and they are the ones
	// the design states. Material is a download — a host briefly
	// offline recovers in seconds. Starting is an engine — a process,
	// its models and its reservation — so asking again every few
	// seconds on a host that is not coming back is noise nobody reads.
	if backoffFor(StageMaterial, 1) != materialBase || backoffFor(StageMaterial, 99) != materialMax {
		t.Fatalf("material follows the acquisition schedule: %v %v", backoffFor(StageMaterial, 1), backoffFor(StageMaterial, 99))
	}
	for _, stage := range []Stage{StageStart, StageReadiness, StageHealth, StageCrashed, StagePanic} {
		if backoffFor(stage, 1) != startBase || backoffFor(stage, 99) != startMax {
			t.Fatalf("%s follows the engine schedule: %v %v", stage, backoffFor(stage, 1), backoffFor(stage, 99))
		}
	}
	if backoffFor(StageStart, 2) <= backoffFor(StageStart, 1) {
		t.Fatal("the wait must grow")
	}
	// AND IT IS JITTERED. A host that dropped every plugin at once — a
	// machine resuming, a service coming back — must not retry all of
	// them at the same instant, which is when they are least likely to
	// succeed and cost the most.
	d := backoffFor(StageStart, 3)
	spread := map[time.Duration]bool{}
	for i := 0; i < 200; i++ {
		got := jittered(d)
		if got > d || got < d-d/4 {
			t.Fatalf("jitter must stay inside the last quarter of the interval: %v for %v", got, d)
		}
		spread[got] = true
	}
	if len(spread) < 10 {
		t.Fatalf("a schedule that always returns the same instant is not jittered: %d distinct", len(spread))
	}
}

// TWO ENGINES AT A TIME IS ALSO TRUE OF THE FIRST ONE. Checking custody
// only where something was already serving left the case with no Active
// at all — an id whose single engine refused with its stop still
// pending — free to start candidate after candidate beside the one that
// had not yet let go: five engines for one plugin, each holding its own
// reservation and its own models.
func TestACandidateWaitsForARetirementEvenWithNothingServing(t *testing.T) {
	owing := func(i *Instance) {
		leaving := NewActivation(1, i.ID, "0.9.0", i.Package, i.PackageHash, time.Now())
		leaving.Lease.Hold("child", func() error { return errors.New("not yet reaped") })
		_ = leaving.Lease.Release()
		leaving.Retiring(nil)
		i.Activations = append(i.Activations, leaving)
	}
	if got := planFor(t, owing); len(got) != 0 {
		t.Fatalf("nothing serves, but the predecessor still owes: %v", got)
	}
	// Once it owes nothing, the activation goes ahead.
	if got := planFor(t, func(i *Instance) {
		done := NewActivation(1, i.ID, "0.9.0", i.Package, i.PackageHash, time.Now())
		_ = done.Lease.Release()
		done.Retiring(nil)
		i.Activations = append(i.Activations, done)
	}); len(got) != 1 || got[0] != string(cmdActivate) {
		t.Fatalf("a discharged predecessor holds nothing back: %v", got)
	}
}

// A NEW RELEASE IS A NEW RELEASE EVEN WHEN ITS HASH IS UNCHANGED.
// PackageHash is what the manifest DECLARES about the packaged
// artifact, not a digest of the archive, so two releases whose payload
// is byte-identical carry the same one — a version bump with no code
// change is exactly that. Comparing the hash alone left such an update
// undetected for ever: the host's own fixtures build 0.1.0 and 0.2.0
// that way, and the facility saw one release where there were two.
func TestANewPackageIsAnUpdateEvenWhenItsHashIsUnchanged(t *testing.T) {
	serving := func(pkg string) func(*Instance) {
		return func(i *Instance) {
			a := NewActivation(1, i.ID, "0.1.0", pkg, i.PackageHash, time.Now())
			a.Role, a.Intent = RoleActive, intentOf(i.ID, pkg, i.PackageHash, i.ManifestHash, Policy{Revision: 1})
			i.Activations = append(i.Activations, a)
		}
	}
	// Same id, same declared hash, a DIFFERENT package on disk.
	if got := planFor(t, serving("one-0.1.0.aiiospkg")); len(got) != 1 || got[0] != string(cmdUpdate) {
		t.Fatalf("different bytes on disk are an update whatever the declared hash says: %v", got)
	}
	// The same package is not.
	if got := planFor(t, serving("one.aiiospkg")); len(got) != 0 {
		t.Fatalf("the release already serving needs nothing: %v", got)
	}
}
