package pluginfacility

import (
	"errors"
	"testing"
	"time"
)

func at(sec int) time.Time { return time.Unix(1700000000+int64(sec), 0) }

func newInst() *Instance {
	return &Instance{ID: "id.example.engine", Version: "1.0.0", Package: "plugins/id.example.engine/p.aiiospkg",
		PackageHash: "sha256:aa", Verified: true, Desired: Desired{Active: true, Package: "plugins/id.example.engine/p.aiiospkg", Hash: "sha256:aa"}}
}

func (i *Instance) add(gen Generation, role Role) *Activation {
	a := NewActivation(gen, i.ID, i.Version, i.Package, i.PackageHash, at(0))
	a.Role = role
	i.Activations = append(i.Activations, a)
	return a
}

// .
// .
// .
func TestTheStateFollowsFromTheActivations(t *testing.T) {
	bare := &Instance{ID: "id.example.engine"}
	if got := bare.State(); got != StateRemoved {
		t.Fatalf("nothing present, nothing wanted: %s", got)
	}
	bare.Package = "p.aiiospkg"
	if got := bare.State(); got != StateDiscovered {
		t.Fatalf("a package nobody has verified: %s", got)
	}
	bare.Verified = true
	if got := bare.State(); got != StateVerified {
		t.Fatalf("verified and not yet wanted: %s", got)
	}
	bare.Desired = Desired{Skipped: "below the T2 auto-load level"}
	if got := bare.State(); got != StateSkipped {
		t.Fatalf("present, verified, and policy keeps it off: %s", got)
	}

	i := newInst()
	if got := i.State(); got != StateWanted {
		t.Fatalf("intended, nothing in flight: %s", got)
	}
	starting := i.add(1, RoleStarting)
	if got := i.State(); got != StateStarting {
		t.Fatalf("on its way up: %s", got)
	}
	starting.Role = RoleActive
	if got := i.State(); got != StateActive {
		t.Fatalf("serving: %s", got)
	}
	cand := i.add(2, RoleCandidate)
	if got := i.State(); got != StateUpdating {
		t.Fatalf("a candidate beside the serving one: %s", got)
	}
	// .
	// .
	// .
	// .
	cand.Role = RoleActive
	starting.Retiring(nil)
	if got := i.State(); got != StateActive {
		t.Fatalf("after the redirect the successor serves: %s", got)
	}
	if i.Retiring() != starting {
		t.Fatal("and the predecessor is still retiring, with its resources still charged")
	}
	if i.CanAdmitCandidate() {
		t.Fatal("a further update waits for that predecessor")
	}
	if err := starting.Lease.Release(); err != nil {
		t.Fatal(err)
	}
	if i.Retiring() != nil || !i.CanAdmitCandidate() {
		t.Fatal("once its retirement is established it is out of the way")
	}

	// .
	drain := &Instance{ID: "id.example.engine", Package: "p", Verified: true, Desired: Desired{Active: true}}
	d := drain.add(3, RoleStarting)
	d.Retiring(nil)
	d.Lease.Hold("child", func() error { return errors.New("not yet reaped") })
	_ = d.Lease.Release()
	if got := drain.State(); got != StateDraining {
		t.Fatalf("a predecessor whose cleanup is unresolved: %s", got)
	}

	// .
	ref := newInst()
	r := ref.add(4, RoleRefused)
	r.Refusal = NewRefusal(ref.ID, ref.Version, 4, StageReadiness, errors.New("no readiness mark"))
	if got := ref.State(); got != StateRefused {
		t.Fatalf("the last attempt refused and nothing serves: %s", got)
	}
	if ref.LastRefusal() == nil || ref.LastRefusal().Stage != StageReadiness {
		t.Fatalf("the refusal is the one the page shows: %+v", ref.LastRefusal())
	}
}

// .
// .
// .
// .
// .
func TestATiringPredecessorBlocksTheNextCandidate(t *testing.T) {
	i := newInst()
	active := i.add(1, RoleActive)
	if !i.CanAdmitCandidate() {
		t.Fatal("a serving activation alone admits a candidate")
	}
	cand := i.add(2, RoleCandidate)
	if i.CanAdmitCandidate() {
		t.Fatal("a candidate is already standing up: a second would be a third engine")
	}

	// .
	// .
	cand.Retiring(NewRefusal(i.ID, i.Version, 2, StageCancelled, errors.New("superseded")))
	reaped := false
	cand.Lease.Hold("child", func() error {
		if reaped {
			return nil
		}
		return errors.New("not yet reaped")
	})
	_ = cand.Lease.Release()
	if i.CanAdmitCandidate() {
		t.Fatal("a cancelled candidate whose cleanup is unresolved still owns its resources")
	}
	if i.Retiring() != cand {
		t.Fatal("the retiring activation is the cancelled candidate")
	}

	// .
	// .
	// .
	reaped = true
	if err := cand.Lease.Release(); err != nil {
		t.Fatalf("the retry: %v", err)
	}
	if !i.CanAdmitCandidate() {
		t.Fatal("once retirement is established the next candidate may start")
	}
	if i.Active() != active {
		t.Fatal("none of this disturbed what is serving")
	}
}

func TestWhatIsSettledForgettableAndPruned(t *testing.T) {
	i := newInst()
	serving := i.add(1, RoleActive)
	if serving.Settled() {
		t.Fatal("what is serving is never settled")
	}
	old := i.add(2, RoleRefused)
	old.Refusal = NewRefusal(i.ID, i.Version, 2, StageStart, errors.New("first"))
	unpinned := false
	old.Lease.Hold("root", func() error {
		if unpinned {
			return nil
		}
		return errors.New("still pinned")
	})
	_ = old.Lease.Release()
	if old.Settled() {
		t.Fatal("a refusal whose cleanup is unresolved is not finished with")
	}
	// .
	gone := &Instance{ID: i.ID, Activations: []*Activation{old}}
	if gone.Forgettable() {
		t.Fatal("an instance with residue must not be forgotten — that is how a child is lost")
	}
	unpinned = true
	_ = old.Lease.Release()
	if !old.Settled() || !gone.Forgettable() {
		t.Fatal("once everything came back it can be forgotten")
	}

	// .
	// .
	newer := i.add(3, RoleRefused)
	newer.Refusal = NewRefusal(i.ID, i.Version, 3, StageReadiness, errors.New("second"))
	i.Prune()
	kept := map[Generation]bool{}
	for _, a := range i.Activations {
		kept[a.Gen] = true
	}
	if !kept[1] || !kept[3] || kept[2] {
		t.Fatalf("pruned wrong: kept %v", kept)
	}
	if i.LastRefusal() == nil || i.LastRefusal().Cause.Error() != "second" {
		t.Fatalf("the newest refusal is the one kept: %+v", i.LastRefusal())
	}
}

// .
// .
// .
// .
func TestTheBudgetSurvivesNewAttemptsAndReopensOnlyWhenItShould(t *testing.T) {
	b := Budget{Max: 3, Window: 10 * time.Minute}
	b.For("id.example.engine|sha256:aa|policy-7|trust-2")
	for n := 0; n < 3; n++ {
		if !b.Admits(at(n)) {
			t.Fatalf("attempt %d must be admitted", n+1)
		}
		b.Record(at(n))
	}
	if b.Admits(at(4)) {
		t.Fatal("a fourth attempt inside the window must not be admitted")
	}
	if b.Spent(at(4)) != 3 {
		t.Fatalf("spent: %d", b.Spent(at(4)))
	}
	// .
	when := b.Reopens(at(4))
	if !when.Equal(at(0).Add(10 * time.Minute)) {
		t.Fatalf("it reopens when the oldest attempt leaves the window, got %v", when)
	}
	if b.Admits(at(599)) {
		t.Fatal("still inside the window")
	}
	if !b.Admits(at(601)) {
		t.Fatal("the oldest attempt has left the window")
	}

	// .
	// .
	b2 := Budget{Max: 2, Window: time.Hour}
	b2.For("id|hash-a|policy-1|trust-1")
	b2.Record(at(0))
	b2.Record(at(1))
	if b2.Admits(at(2)) {
		t.Fatal("exhausted for this intent")
	}
	b2.For("id|hash-b|policy-1|trust-1")
	if !b2.Admits(at(2)) || b2.Spent(at(2)) != 0 {
		t.Fatal("a different package is a different question")
	}
	// .
	b2.Record(at(3))
	b2.Record(at(4))
	b2.For("id|hash-b|policy-1|trust-1")
	if b2.Admits(at(5)) {
		t.Fatal("re-stating the same intent must not restore attempts")
	}
	// .
	b2.Reset()
	if !b2.Admits(at(5)) {
		t.Fatal("Try again is an explicit reset")
	}
	if got := (&Budget{}).max(); got != DefaultBudget().Max {
		t.Fatalf("an unset budget takes the supervisor's own semantics: %d", got)
	}
}
