package pluginfacility

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
)

// holds is a serving engine's measure of what its processes hold.
func holds(bytes int64) func() (int64, bool) { return func() (int64, bool) { return bytes, true } }

// A SERVING ENGINE IS COUNTED ONCE. The platform's measure of what is
// available is taken with every serving engine resident: what an engine
// holds is already out of it. Admission took the engine's whole
// declaration away from that measure again. On a 16 GB machine with one
// large engine serving, a second that fits was told to wait, and an update
// waited for ever beside the release it replaces.
//
// Each case is one engine held by the facility (what it declares, whether
// it serves, what it holds) on a machine of 16 GiB that measures some
// amount available, and one candidate.
func TestAServingEngineIsCountedByWhatItHasNotYetTaken(t *testing.T) {
	for _, tc := range []struct {
		name            string
		declared        int64
		serving         bool
		holding         func() (int64, bool)
		available       int64
		reserve, budget int64
		need            int64
		want            bool
	}{
		// 6 declared, 5 held: 1 is still owed to it, so 4 available leaves 3.
		{"a second engine beside a large one", 6 * gib, true, holds(5 * gib), 4 * gib, 0, 0, 2 * gib, true},
		{"and no more than what is left", 6 * gib, true, holds(5 * gib), 4 * gib, 0, 0, 3*gib + 1, false},
		{"what it holds is not known", 6 * gib, true, func() (int64, bool) { return 5 * gib, false }, 4 * gib, 0, 0, 2 * gib, false},
		{"it has no measure", 6 * gib, true, nil, 4 * gib, 0, 0, 2 * gib, false},
		{"it is still starting", 6 * gib, false, holds(5 * gib), 4 * gib, 0, 0, 2 * gib, false},
		// An engine that has let its model go holds little, and the room its
		// next call needs stays held back: 3 available less (2 - 1/8) owed.
		{"an idle engine keeps its room", 2 * gib, true, holds(gib / 8), 3 * gib, 0, 0, 2 * gib, false},
		{"and what is beyond its room is free", 2 * gib, true, holds(gib / 8), 3 * gib, 0, 0, gib, true},
		// Holding more than it declared owes nothing; it does not give room back.
		{"an engine over its declaration owes nothing", 2 * gib, true, holds(3 * gib), 2 * gib, 0, 0, 2 * gib, true},
		{"and adds no room", 2 * gib, true, holds(3 * gib), 2 * gib, 0, 0, 2*gib + 1, false},
		{"the operator's reserve still comes off", 6 * gib, true, holds(5 * gib), 4 * gib, 2 * gib, 0, 2 * gib, false},
		// The budget bounds what is DECLARED: 7 less 6 leaves 1, whatever is held.
		{"the budget is a bound on declarations", 6 * gib, true, holds(5 * gib), 10 * gib, 0, 7 * gib, 2 * gib, false},
		{"and admits under it", 6 * gib, true, holds(5 * gib), 10 * gib, 0, 9 * gib, 2 * gib, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := New(Config{Capacity: fixedCapacity{16 * gib, tc.available}})
			f.policy.Admission = AdmissionPolicy{ReserveBytes: tc.reserve, BudgetBytes: tc.budget}
			f.reserved[1] = &reservation{gen: 1, id: "serving", host: tc.declared, serving: tc.serving, holding: tc.holding}
			w := &admitWaiter{seq: 1, gen: 2, id: "candidate"}
			f.queue = []*admitWaiter{w}
			f.mu.Lock()
			ok, why, never := f.admissibleLocked(w, Prepared{HostBytes: tc.need})
			f.mu.Unlock()
			if ok != tc.want || never != nil {
				t.Fatalf("admitted=%v want=%v (%q, %v)", ok, tc.want, why, never)
			}
		})
	}
}

// THE WAIT SAYS WHAT IS HELD BACK, NOT WHAT WAS DECLARED. A candidate that
// does not fit is told how much is free and how much is reserved by other
// engines; the second number is what those engines may still take.
func TestTheWaitNamesWhatOtherEnginesMayStillTake(t *testing.T) {
	f := New(Config{Capacity: fixedCapacity{16 * gib, 4 * gib}})
	f.reserved[1] = &reservation{gen: 1, id: "serving", host: 6 * gib, serving: true, holding: holds(5 * gib)}
	w := &admitWaiter{seq: 1, gen: 2, id: "candidate"}
	f.queue = []*admitWaiter{w}
	f.mu.Lock()
	ok, why, _ := f.admissibleLocked(w, Prepared{HostBytes: 4 * gib})
	f.mu.Unlock()
	want := humanBytes(3*gib) + " free of " + humanBytes(16*gib) + ", " + humanBytes(gib) + " reserved by other engines"
	if ok || !strings.Contains(why, want) {
		t.Fatalf("the wait must say %q, and says: admitted=%v %q", want, ok, why)
	}
}

// AN UPDATE THAT CANNOT START BESIDE ITS PREDECESSOR IS TOLD WHAT ENDS THE
// WAIT. The release it replaces serves and holds 5 of the 6 it declared;
// 4 are measured available; the update declares 6. It does not fit beside
// its predecessor, and no other engine is in the way. The wait says so and
// says what applies the update. A different plugin waiting for the same
// room is told the ordinary thing.
func TestAnUpdateWaitingOnItsOwnPredecessorIsToldARestartAppliesIt(t *testing.T) {
	f := New(Config{Capacity: fixedCapacity{16 * gib, 4 * gib}})
	f.reserved[1] = &reservation{gen: 1, id: "id.example.engine", host: 6 * gib, serving: true, holding: holds(5 * gib)}
	ask := func(id string) (bool, string) {
		w := &admitWaiter{seq: 1, gen: 2, id: id}
		f.queue = []*admitWaiter{w}
		f.mu.Lock()
		defer f.mu.Unlock()
		ok, why, never := f.admissibleLocked(w, Prepared{Evidence: Evidence{ID: id}, HostBytes: 6 * gib})
		if never != nil {
			t.Fatalf("%s: %v", id, never)
		}
		return ok, why
	}
	if ok, why := ask("id.example.engine"); ok || !strings.Contains(why, "beside the release this update replaces") || !strings.Contains(why, "takes effect when the identity next restarts") {
		t.Fatalf("an update that cannot start beside its predecessor: admitted=%v %q", ok, why)
	}
	if ok, why := ask("id.example.other"); ok || strings.Contains(why, "restarts") || !strings.Contains(why, "reserved by other engines") {
		t.Fatalf("another plugin waiting for the same room is told the ordinary thing: admitted=%v %q", ok, why)
	}
	// A predecessor that is itself still starting is not one a restart would clear the way past.
	f.reserved[1].serving = false
	if _, why := ask("id.example.engine"); strings.Contains(why, "restarts") {
		t.Fatalf("a release still starting is not a serving predecessor: %q", why)
	}
}

// measuredRuntime is the shared runtime whose engines say what they hold.
type measuredRuntime struct {
	*sharedRuntime
	heldFor map[string]int64
}

type measuredRunning struct {
	*fakeRunning
	held int64
}

func (m *measuredRunning) HostBytesHeld() (int64, bool) { return m.held, m.held > 0 }

func (m measuredRuntime) Start(ctx context.Context, p Prepared, lease *Lease) (Running, error) {
	r, err := m.sharedRuntime.Start(ctx, p, lease)
	if err != nil {
		return nil, err
	}
	return &measuredRunning{fakeRunning: r.(*fakeRunning), held: m.heldFor[p.Evidence.Package]}, nil
}
func (m measuredRuntime) Redirect(from, to Running) error {
	var f Running
	if from != nil {
		f = from.(*measuredRunning).fakeRunning
	}
	return m.sharedRuntime.Redirect(f, to.(*measuredRunning).fakeRunning)
}
func (m measuredRuntime) Stop(ctx context.Context, r Running) (Retirement, error) {
	return m.sharedRuntime.Stop(ctx, r.(*measuredRunning).fakeRunning)
}

// stepCapacity is a machine whose measure of what is available the test moves.
type stepCapacity struct{ available atomic.Int64 }

func (c *stepCapacity) Measure() Availability {
	return Availability{HostKnown: true, HostTotal: 16 * gib, HostAvailable: c.available.Load()}
}

// THROUGH THE FACILITY: THE ENGINE THAT SERVES IS THE ONE THAT IS ASKED. One
// engine declares 6 and is started on a machine with 10 available. It
// serves and holds 5; the machine now measures 4 available. A second
// engine that declares 2 is observed. It is admitted and serves: what the
// first has not yet taken is 1, and 3 are free. With the first engine's
// measure unread, the second would wait on a machine that has room for it.
func TestAnEngineStartedBesideAServingOneIsAdmittedIntoTheRoomThereIs(t *testing.T) {
	rt := measuredRuntime{sharedRuntime: newSharedRuntime(), heldFor: map[string]int64{"large.aiiospkg": 5 * gib}}
	rt.hostBytesFor["large.aiiospkg"] = 6 * gib
	rt.hostBytesFor["small.aiiospkg"] = 2 * gib
	capacity := &stepCapacity{}
	capacity.available.Store(10 * gib)
	f := New(Config{Runtime: rt, Capacity: capacity})
	auditAttachFacility(t, f)

	f.Observe(obs("large.aiiospkg"), Policy{Revision: 1})
	auditWaitFacility(t, "the large engine to serve", func() bool { return viewOf(f, idOf("large.aiiospkg")).State == StateActive })
	capacity.available.Store(4 * gib) // measured with the large engine resident

	f.Observe(obs("large.aiiospkg", "small.aiiospkg"), Policy{Revision: 2})
	auditWaitFacility(t, "the small engine to serve beside it", func() bool { return viewOf(f, idOf("small.aiiospkg")).State == StateActive })
}
