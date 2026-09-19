package pluginfacility

import (
	"errors"
	"testing"
	"time"
)

// .
// .
// .
// .
func planFor(t *testing.T, build func(*Instance)) []string {
	t.Helper()
	f := New(Config{Runtime: newSharedRuntime()})
	inst := &Instance{ID: "id.example.one", Package: "one.aiiospkg", PackageHash: "sha256:one",
		Verified: true, Desired: Desired{Active: true, Package: "one.aiiospkg", Hash: "sha256:one"}}
	f.policy = Policy{Revision: 1}
	build(inst)
	f.instances[inst.ID] = inst
	intent := intentOf(inst.ID, inst.Package, inst.PackageHash, f.policy)
	// .
	// .
	// .
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
	// .
	if got := planFor(t, func(i *Instance) {}); len(got) != 1 {
		t.Fatalf("a wanted package with nothing in flight must be dispatched: %v", got)
	}
	// .
	// .
	for _, stage := range []Stage{StageVerify, StagePolicy, StageContain, StageRegister} {
		got := planFor(t, func(i *Instance) { refusedAt(i, 1, stage) })
		if len(got) != 0 {
			t.Fatalf("%s is permanent and must not be retried by a pass: %v", stage, got)
		}
	}
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	if got := planFor(t, func(i *Instance) { refusedAt(i, 1, StageCancelled) }); len(got) != 1 {
		t.Fatalf("a withdrawn attempt must not stand in the way of a package that is wanted: %v", got)
	}
	// .
	for _, stage := range []Stage{StageStart, StageReadiness, StageHealth, StageMaterial, StageCrashed} {
		got := planFor(t, func(i *Instance) { refusedAt(i, 1, stage) })
		if len(got) != 1 {
			t.Fatalf("%s recovers and must be retried: %v", stage, got)
		}
	}
	// .
	if got := planFor(t, func(i *Instance) {
		i.Activations = append(i.Activations, NewActivation(1, i.ID, "1.0.0", i.Package, i.PackageHash, time.Now()))
	}); len(got) != 0 {
		t.Fatalf("an attempt in flight must not be doubled: %v", got)
	}
	// .
	if got := planFor(t, func(i *Instance) {
		a := NewActivation(1, i.ID, "1.0.0", i.Package, i.PackageHash, time.Now())
		a.Role = RoleActive
		i.Activations = append(i.Activations, a)
	}); len(got) != 0 {
		t.Fatalf("what already serves must be left alone: %v", got)
	}
	// .
	// .
	// .
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
	// .
	if got := planFor(t, func(i *Instance) {
		serving := NewActivation(1, i.ID, "1.0.0", i.Package, "sha256:old", time.Now())
		serving.Role = RoleActive
		i.Activations = append(i.Activations, serving)
	}); len(got) != 1 || got[0] != string(cmdUpdate) {
		t.Fatalf("new bytes for a serving id are an update: %v", got)
	}

	// .
	// .
	if got := planFor(t, func(i *Instance) {
		refusedAt(i, 1, StageReadiness)
		i.Budget = Budget{Max: 2, Window: time.Hour}
		i.Budget.For(intentOf(i.ID, i.Package, i.PackageHash, Policy{Revision: 1}))
		i.Budget.Record(time.Now())
		i.Budget.Record(time.Now())
	}); len(got) != 0 {
		t.Fatalf("an exhausted budget admits nothing: %v", got)
	}
}

// .
// .
// .
// .
// .
// .
func TestARefusalStopsBlockingOnceItsInputHasMoved(t *testing.T) {
	stale := func(i *Instance) {
		refusedAt(i, 1, StagePolicy)
		// .
		i.Activations[len(i.Activations)-1].Intent = intentOf(i.ID, i.Package, i.PackageHash, Policy{Revision: 0})
	}
	if got := planFor(t, stale); len(got) != 1 || got[0] != string(cmdActivate) {
		t.Fatalf("a refusal from a policy that has since changed must not hold the plugin down: %v", got)
	}
	// .
	if got := planFor(t, func(i *Instance) { refusedAt(i, 1, StagePolicy) }); len(got) != 0 {
		t.Fatalf("a refusal still answering the current question must hold: %v", got)
	}
	// .
	if got := planFor(t, func(i *Instance) {
		refusedAt(i, 1, StageVerify)
		i.Activations[len(i.Activations)-1].Intent = intentOf(i.ID, "other.aiiospkg", "sha256:something-else", Policy{Revision: 1})
	}); len(got) != 1 {
		t.Fatalf("a refusal about other bytes must not hold these down: %v", got)
	}
}

// .
// .
// .
// .
// .
func TestAServingReleaseIsAskedAgainWhenItsAdmissionMoved(t *testing.T) {
	serving := func(intent string) func(*Instance) {
		return func(i *Instance) {
			a := NewActivation(1, i.ID, "1.0.0", i.Package, i.PackageHash, time.Now())
			a.Role, a.Intent = RoleActive, intent
			i.Activations = append(i.Activations, a)
		}
	}
	current := intentOf("id.example.one", "one.aiiospkg", "sha256:one", Policy{Revision: 1})
	if got := planFor(t, serving(current)); len(got) != 0 {
		t.Fatalf("a release serving under the current admission needs nothing: %v", got)
	}
	old := intentOf("id.example.one", "one.aiiospkg", "sha256:one", Policy{Revision: 0})
	if got := planFor(t, serving(old)); len(got) != 1 || got[0] != string(cmdReadmit) {
		t.Fatalf("a release whose admission moved must be asked again, not left alone: %v", got)
	}
	// .
	if got := planFor(t, func(i *Instance) {
		serving(old)(i)
		i.ReadmitFor = current
	}); len(got) != 0 {
		t.Fatalf("one question at a time: %v", got)
	}
}

// .
// .
// .
// .
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
	// .
	// .
	// .
	// .
	// .
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
	// .
	// .
	// .
	// .
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

// .
// .
// .
// .
// .
// .
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
	// .
	if got := planFor(t, func(i *Instance) {
		done := NewActivation(1, i.ID, "0.9.0", i.Package, i.PackageHash, time.Now())
		_ = done.Lease.Release()
		done.Retiring(nil)
		i.Activations = append(i.Activations, done)
	}); len(got) != 1 || got[0] != string(cmdActivate) {
		t.Fatalf("a discharged predecessor holds nothing back: %v", got)
	}
}

// .
// .
// .
// .
// .
// .
// .
func TestANewPackageIsAnUpdateEvenWhenItsHashIsUnchanged(t *testing.T) {
	serving := func(pkg string) func(*Instance) {
		return func(i *Instance) {
			a := NewActivation(1, i.ID, "0.1.0", pkg, i.PackageHash, time.Now())
			a.Role, a.Intent = RoleActive, intentOf(i.ID, pkg, i.PackageHash, Policy{Revision: 1})
			i.Activations = append(i.Activations, a)
		}
	}
	// .
	if got := planFor(t, serving("one-0.1.0.aiiospkg")); len(got) != 1 || got[0] != string(cmdUpdate) {
		t.Fatalf("different bytes on disk are an update whatever the declared hash says: %v", got)
	}
	// .
	if got := planFor(t, serving("one.aiiospkg")); len(got) != 0 {
		t.Fatalf("the release already serving needs nothing: %v", got)
	}
}
