package pluginfacility

import (
	"context"
	"strings"
	"testing"
	"time"
)

// .
// .
// .
// .
// .

const gib = int64(1) << 30

type fixedCapacity struct{ total, avail int64 }

func (c fixedCapacity) Measure() Availability {
	return Availability{HostKnown: true, HostTotal: c.total, HostAvailable: c.avail}
}

func facilityWith(t *testing.T, rt Runtime, capacity Capacity) *Facility {
	t.Helper()
	f := New(Config{Runtime: rt, Capacity: capacity})
	ctx, cancel := context.WithCancel(context.Background())
	f.Attach(ctx)
	t.Cleanup(func() { cancel(); f.Close() })
	return f
}

func viewOf(f *Facility, id string) InstanceView {
	for _, v := range f.Snapshot().Instances {
		if v.ID == id {
			return v
		}
	}
	return InstanceView{}
}

func TestAdmissionWaitsForRoomAndAdmitsWhenARetirementEstablishes(t *testing.T) {
	rt := newSharedRuntime()
	rt.hostBytesFor["one.aiiospkg"], rt.hostBytesFor["two.aiiospkg"] = 3*gib, 3*gib
	f := facilityWith(t, rt, fixedCapacity{total: 8 * gib, avail: 4 * gib})
	f.Observe(obs("one.aiiospkg", "two.aiiospkg"), Policy{Revision: 1})
	until(t, "one of them to serve", func() bool {
		return viewOf(f, idOf("one.aiiospkg")).State == StateActive || viewOf(f, idOf("two.aiiospkg")).State == StateActive
	})
	// .
	until(t, "the other to wait for room", func() bool {
		a, b := viewOf(f, idOf("one.aiiospkg")), viewOf(f, idOf("two.aiiospkg"))
		w := b
		if a.State == StateAdmitting {
			w = a
		}
		return w.State == StateAdmitting && strings.Contains(w.Admission, "waiting for 3.0 GB of host memory")
	})
	if strings.Contains(rt.seen(), "start:one.aiiospkg") && strings.Contains(rt.seen(), "start:two.aiiospkg") {
		t.Fatalf("both started beside each other in 4 GB: %s", rt.seen())
	}
	// .
	serving := idOf("one.aiiospkg")
	if viewOf(f, serving).State != StateActive {
		serving = idOf("two.aiiospkg")
	}
	if got := viewOf(f, serving).Admission; !strings.Contains(got, "admitted (host 3.0 GB") {
		t.Fatalf("the serving one's admission is on its record: %q", got)
	}
	// .
	// .
	waiting := idOf("two.aiiospkg")
	if serving == waiting {
		waiting = idOf("one.aiiospkg")
	}
	keep := strings.TrimPrefix(waiting, "id.example.") + ".aiiospkg"
	f.Observe(obs(keep), Policy{Revision: 1})
	until(t, "the waiting one to serve once room is returned", func() bool { return viewOf(f, waiting).State == StateActive })
}

func TestUnknownCapacityAdmitsOneStartAtATimeAndSaysSo(t *testing.T) {
	rt := newSharedRuntime()
	rt.hostBytesFor["one.aiiospkg"], rt.hostBytesFor["two.aiiospkg"] = gib, gib
	gate := make(chan struct{})
	rt.mu.Lock()
	rt.gate = gate
	rt.mu.Unlock()
	f := facilityWith(t, rt, nil)
	f.Observe(obs("one.aiiospkg", "two.aiiospkg"), Policy{Revision: 1})
	// .
	// .
	until(t, "one start in flight and one waiting", func() bool {
		a, b := viewOf(f, idOf("one.aiiospkg")), viewOf(f, idOf("two.aiiospkg"))
		return (a.State == StateStarting && b.State == StateAdmitting) || (b.State == StateStarting && a.State == StateAdmitting)
	})
	for _, id := range []string{idOf("one.aiiospkg"), idOf("two.aiiospkg")} {
		if v := viewOf(f, id); v.State == StateAdmitting && !strings.Contains(v.Admission, "one engine starts at a time") {
			t.Fatalf("unknown capacity is said, not guessed at: %q", v.Admission)
		}
	}
	close(gate)
	until(t, "both to serve, one after the other", func() bool {
		return viewOf(f, idOf("one.aiiospkg")).State == StateActive && viewOf(f, idOf("two.aiiospkg")).State == StateActive
	})
	for _, id := range []string{idOf("one.aiiospkg"), idOf("two.aiiospkg")} {
		if got := viewOf(f, id).Admission; !strings.Contains(got, "capacity unknown") || !strings.Contains(got, "residency not bounded") {
			t.Fatalf("what was admitted under unknown capacity says so on its record: %q", got)
		}
	}
}

func TestABudgetAdmitsBySizeWhereCapacityIsUnknown(t *testing.T) {
	rt := newSharedRuntime()
	rt.hostBytesFor["one.aiiospkg"], rt.hostBytesFor["two.aiiospkg"] = 3*gib, 3*gib
	f := facilityWith(t, rt, nil)
	pol := Policy{Revision: 1, Admission: AdmissionPolicy{BudgetBytes: 4 * gib}}
	f.Observe(obs("one.aiiospkg", "two.aiiospkg"), pol)
	until(t, "one serving and one waiting on the budget", func() bool {
		a, b := viewOf(f, idOf("one.aiiospkg")), viewOf(f, idOf("two.aiiospkg"))
		return (a.State == StateActive && b.State == StateAdmitting) || (b.State == StateActive && a.State == StateAdmitting)
	})
	for _, id := range []string{idOf("one.aiiospkg"), idOf("two.aiiospkg")} {
		if v := viewOf(f, id); v.State == StateAdmitting && !strings.Contains(v.Admission, "free of 4.0 GB") {
			t.Fatalf("the budget is what it waits against: %q", v.Admission)
		}
	}
}

func TestAReservationIsHeldUntilRetirementIsEstablished(t *testing.T) {
	rt := newSharedRuntime()
	rt.hostBytesFor["one.aiiospkg"], rt.hostBytesFor["two.aiiospkg"] = 3*gib, 3*gib
	rt.stopPendingFor["one.aiiospkg"] = true
	f := facilityWith(t, rt, fixedCapacity{total: 8 * gib, avail: 4 * gib})
	f.Observe(obs("one.aiiospkg"), Policy{Revision: 1})
	until(t, "the first to serve", func() bool { return viewOf(f, idOf("one.aiiospkg")).State == StateActive })
	// .
	// .
	f.Observe(obs("two.aiiospkg"), Policy{Revision: 1})
	until(t, "the first to be retiring with residue and the second to wait", func() bool {
		return viewOf(f, idOf("one.aiiospkg")).State == StateDraining && viewOf(f, idOf("two.aiiospkg")).State == StateAdmitting
	})
	time.Sleep(50 * time.Millisecond)
	if strings.Contains(rt.seen(), "start:two.aiiospkg") {
		t.Fatalf("the second started on memory a pending retirement still holds: %s", rt.seen())
	}
	// .
	// .
	rt.mu.Lock()
	rt.stopPendingFor["one.aiiospkg"] = false
	rt.mu.Unlock()
	f.Poke("child reaped")
	until(t, "the second to serve once retirement is established", func() bool { return viewOf(f, idOf("two.aiiospkg")).State == StateActive })
}

func TestAPackageThatCanNeverFitIsRefusedForGood(t *testing.T) {
	rt := newSharedRuntime()
	rt.hostBytesFor["huge.aiiospkg"] = 8 * gib
	f := facilityWith(t, rt, fixedCapacity{total: 4 * gib, avail: 3 * gib})
	f.Observe(obs("huge.aiiospkg"), Policy{Revision: 1})
	until(t, "the refusal", func() bool { return viewOf(f, idOf("huge.aiiospkg")).State == StateRefused })
	r := viewOf(f, idOf("huge.aiiospkg")).Refusal
	if r == nil || r.Class != ClassPermanent || r.Stage != StageAdmit {
		t.Fatalf("a package this host can never hold is refused for good at admission: %+v", r)
	}
	if !strings.Contains(r.Error(), "needs 8.0 GB") || !strings.Contains(r.Remedy, "memory") {
		t.Fatalf("the refusal names what it needs and what to do: %v / %s", r, r.Remedy)
	}
	if strings.Contains(rt.seen(), "start:huge.aiiospkg") {
		t.Fatal("nothing was started for it")
	}
}

func TestMaxConcurrentStartsCapsStartsInFlight(t *testing.T) {
	rt := newSharedRuntime()
	rt.hostBytesFor["one.aiiospkg"], rt.hostBytesFor["two.aiiospkg"] = gib, gib
	gate := make(chan struct{})
	rt.mu.Lock()
	rt.gate = gate
	rt.mu.Unlock()
	f := facilityWith(t, rt, ampleCapacity{})
	f.Observe(obs("one.aiiospkg", "two.aiiospkg"), Policy{Revision: 1, Admission: AdmissionPolicy{MaxConcurrentStarts: 1}})
	until(t, "one in flight, one waiting on the operator's cap", func() bool {
		a, b := viewOf(f, idOf("one.aiiospkg")), viewOf(f, idOf("two.aiiospkg"))
		w := b
		if a.State == StateAdmitting {
			w = a
		}
		return w.State == StateAdmitting && strings.Contains(w.Admission, "the operator allows 1 at once")
	})
	close(gate)
	until(t, "both to serve in turn", func() bool {
		return viewOf(f, idOf("one.aiiospkg")).State == StateActive && viewOf(f, idOf("two.aiiospkg")).State == StateActive
	})
}

func TestTheVoiceFamilyIsAdmittedFirst(t *testing.T) {
	rt := newSharedRuntime()
	rt.hostBytesFor["first.aiiospkg"], rt.hostBytesFor["tool.aiiospkg"], rt.hostBytesFor["voice.aiiospkg"] = 3*gib, 3*gib, 3*gib
	rt.familyFor["voice.aiiospkg"] = "voice_interface"
	rt.familyFor["tool.aiiospkg"] = "tool_bridge"
	f := facilityWith(t, rt, fixedCapacity{total: 8 * gib, avail: 4 * gib})
	f.Observe(obs("first.aiiospkg"), Policy{Revision: 1})
	until(t, "the first to serve", func() bool { return viewOf(f, idOf("first.aiiospkg")).State == StateActive })
	// .
	f.Observe(obs("first.aiiospkg", "tool.aiiospkg"), Policy{Revision: 1})
	until(t, "the tool to wait", func() bool { return viewOf(f, idOf("tool.aiiospkg")).State == StateAdmitting })
	f.Observe(obs("first.aiiospkg", "tool.aiiospkg", "voice.aiiospkg"), Policy{Revision: 1})
	until(t, "the voice engine to wait too", func() bool { return viewOf(f, idOf("voice.aiiospkg")).State == StateAdmitting })
	// .
	// .
	f.Observe(obs("tool.aiiospkg", "voice.aiiospkg"), Policy{Revision: 1})
	until(t, "the voice engine to serve", func() bool { return viewOf(f, idOf("voice.aiiospkg")).State == StateActive })
	if strings.Contains(rt.seen(), "start:tool.aiiospkg") {
		t.Fatalf("the tool started ahead of the voice engine: %s", rt.seen())
	}
}
