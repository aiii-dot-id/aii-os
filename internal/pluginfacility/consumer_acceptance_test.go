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

// .
// .
// .
// .
// .
// .
// .
// .

// .

func TestABudgetOnlyEverTightensAMeasuredLimit(t *testing.T) {
	for _, tc := range []struct {
		name                string
		capacity            Capacity
		reserve, held, need int64
		want                bool
	}{
		{"below the reserve", fixedCapacity{8 * gib, gib}, 2 * gib, 0, gib, false},
		{"exactly at the reserve", fixedCapacity{8 * gib, 2 * gib}, 2 * gib, 0, gib, false},
		{"exactly what is free", fixedCapacity{8 * gib, 4 * gib}, gib, 0, 3 * gib, true},
		{"held reservations exhaust it", fixedCapacity{8 * gib, 4 * gib}, 0, 4 * gib, gib, false},
		{"known room and no budget", fixedCapacity{8 * gib, 6 * gib}, gib, 0, 2 * gib, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			decide := func(budget int64) bool {
				f := New(Config{Capacity: tc.capacity})
				f.policy.Admission = AdmissionPolicy{ReserveBytes: tc.reserve, BudgetBytes: budget}
				if tc.held > 0 {
					f.reserved[1] = &reservation{gen: 1, id: "held", host: tc.held, serving: true}
				}
				w := &admitWaiter{seq: 1, gen: 2, id: "candidate"}
				f.queue = []*admitWaiter{w}
				f.mu.Lock()
				defer f.mu.Unlock()
				ok, why, never := f.admissibleLocked(w, Prepared{HostBytes: tc.need})
				if never != nil {
					t.Fatalf("refused for good where it should only wait: %v", never)
				}
				if !ok && !strings.Contains(why, "waiting for") {
					t.Errorf("a wait that does not say what it waits for: %q", why)
				}
				return ok
			}
			if got := decide(0); got != tc.want {
				t.Errorf("without a budget: admitted=%v, want %v", got, tc.want)
			}
			// .
			if got := decide(7 * gib); got != tc.want {
				t.Errorf("with a 7 GB budget: admitted=%v, want %v — a budget may tighten a measured limit, never loosen it", got, tc.want)
			}
		})
	}
	t.Run("unknown capacity is admitted by the budget, and only by it", func(t *testing.T) {
		f := New(Config{Capacity: UnknownCapacity{}})
		f.policy.Admission = AdmissionPolicy{BudgetBytes: 4 * gib}
		f.reserved[1] = &reservation{gen: 1, id: "held", host: 2 * gib, serving: true}
		w := &admitWaiter{seq: 1, gen: 2, id: "candidate"}
		f.queue = []*admitWaiter{w}
		f.mu.Lock()
		defer f.mu.Unlock()
		if ok, why, _ := f.admissibleLocked(w, Prepared{HostBytes: 2 * gib}); !ok {
			t.Errorf("2 GB under a 4 GB budget with 2 GB held did not fit: %s", why)
		}
		if ok, _, _ := f.admissibleLocked(w, Prepared{HostBytes: 3 * gib}); ok {
			t.Error("3 GB was admitted with 2 GB of a 4 GB budget already held")
		}
	})
}

// .

// .
// .
type heldStage struct {
	*sharedRuntime
	stage   string
	first   atomic.Bool
	ctxSeen chan context.Context
	release chan struct{}
}

func newHeldStage(stage string) *heldStage {
	rt := &heldStage{sharedRuntime: newSharedRuntime(), stage: stage, ctxSeen: make(chan context.Context, 1), release: make(chan struct{})}
	rt.present = stage != "acquire"
	return rt
}

func (h *heldStage) hold(ctx context.Context, stage string) {
	if h.stage == stage && h.first.CompareAndSwap(false, true) {
		h.ctxSeen <- ctx
		select {
		case <-ctx.Done():
		case <-h.release:
		}
	}
}

func (h *heldStage) Acquire(ctx context.Context, p Prepared, progress func(MaterialStatus)) error {
	h.hold(ctx, "acquire")
	if err := ctx.Err(); err != nil {
		return err
	}
	return h.sharedRuntime.Acquire(ctx, p, progress)
}

func (h *heldStage) Start(ctx context.Context, p Prepared, l *Lease) (Running, error) {
	h.hold(ctx, "start")
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return h.sharedRuntime.Start(ctx, p, l)
}

// .
// .
func (h *heldStage) Health(ctx context.Context, r Running) error {
	h.hold(ctx, "health")
	return nil
}

func TestANewerIntentWithdrawsTheAttemptInFlightAtEveryStage(t *testing.T) {
	changes := map[string]func([]Observed, *Policy){
		"replacement bytes": func(set []Observed, _ *Policy) { set[0].Hash = "sha256:replacement" },
		"policy revision":   func(_ []Observed, p *Policy) { p.Revision++ },
		"trust generation":  func(_ []Observed, p *Policy) { p.TrustGen++ },
	}
	for _, stage := range []string{"acquire", "start", "health"} {
		for name, change := range changes {
			t.Run(stage+"/"+name, func(t *testing.T) {
				rt := newHeldStage(stage)
				f := auditFacility(t, rt)
				t.Cleanup(func() { close(rt.release) })
				pol := Policy{Revision: 1, TrustGen: 1}
				f.Observe(obs("one.aiiospkg"), pol)
				var old context.Context
				select {
				case old = <-rt.ctxSeen:
				case <-time.After(3 * time.Second):
					t.Fatalf("the attempt never reached %s", stage)
				}
				set := obs("one.aiiospkg")
				change(set, &pol)
				f.Observe(set, pol)
				// .
				select {
				case <-old.Done():
				default:
					t.Fatal("Observe returned with the overtaken attempt still running under a live context")
				}
				id := idOf("one.aiiospkg")
				auditWaitFacility(t, "the successor to serve", func() bool { return viewOf(f, id).State == StateActive })
				// .
				if n := strings.Count(rt.seen(), "admit:"); n != 1 {
					t.Errorf("%d admissions for one serving attempt — the overtaken one became reachable: %s", n, rt.seen())
				}
				// .
				v := viewOf(f, id)
				if v.Refusal != nil || !v.RetryAt.IsZero() {
					t.Errorf("the withdrawn attempt left its mark on the successor: refusal=%v retry_at=%v", v.Refusal, v.RetryAt)
				}
				if got := f.rolesOf(id); got[RoleActive] != 1 || got[RoleStarting]+got[RoleCandidate] != 0 {
					t.Errorf("roles after the hand-over: %v", got)
				}
			})
		}
	}
}

func TestANewerIntentWithdrawsAnAttemptWaitingForRoom(t *testing.T) {
	rt := newSharedRuntime()
	rt.hostBytesFor["one.aiiospkg"] = 3 * gib
	f := New(Config{Runtime: rt, Capacity: fixedCapacity{16 * gib, 4 * gib}})
	auditAttachFacility(t, f)
	id := idOf("one.aiiospkg")
	pol := Policy{Revision: 1, Admission: AdmissionPolicy{ReserveBytes: 2 * gib}}
	f.Observe(obs("one.aiiospkg"), pol)
	auditWaitFacility(t, "the attempt to wait for room", func() bool { return viewOf(f, id).State == StateAdmitting })
	first := viewOf(f, id).Activations[0].Gen
	// .
	// .
	pol.Admission.ReserveBytes = 0
	f.Observe(obs("one.aiiospkg"), pol)
	auditWaitFacility(t, "it to serve under the new reserve", func() bool { return viewOf(f, id).State == StateActive })
	v := viewOf(f, id)
	for _, a := range v.Activations {
		if a.Role == RoleActive && a.Gen == first {
			t.Error("the attempt admitted under the old policy is what serves; the new intent never withdrew it")
		}
	}
	if n := strings.Count(rt.seen(), "start:one.aiiospkg"); n != 1 {
		t.Errorf("started %d times for one serving engine: %s", n, rt.seen())
	}
}

// .
// .
// .
func TestAStartThatNeverBeganStillResolvesItsRecord(t *testing.T) {
	var events []Event
	var mu sync.Mutex
	e := &executor{id: "id.example.one", wake: make(chan struct{}, 1), done: make(chan struct{}),
		deps: executorDeps{Emit: func(ev Event) { mu.Lock(); events = append(events, ev); mu.Unlock() }}}
	lease := NewLease(7)
	c := newCommand(cmdActivate, "one.aiiospkg", "sha256:one").withActivation(7, lease)
	e.pending = c
	e.withdraw(7)
	select {
	case <-c.done:
	default:
		t.Fatal("the withdrawn command never settled")
	}
	mu.Lock()
	defer mu.Unlock()
	if len(events) != 1 || events[0].Kind != EventRefused || events[0].Gen != 7 || events[0].Refusal.Stage != StageCancelled {
		t.Fatalf("a start that never began must be reported, once, as cancelled under its own generation: %+v", events)
	}
	if !lease.Discharged() {
		t.Error("its empty ledger was left open")
	}
}

// .

// .
// .
type heldStop struct {
	*sharedRuntime
	tag     string
	calls   atomic.Int32
	entered chan struct{}
	gate    chan struct{}
}

func (h *heldStop) Stop(ctx context.Context, r Running) (Retirement, error) {
	if r.(*fakeRunning).tag != h.tag {
		return h.sharedRuntime.Stop(ctx, r)
	}
	h.note("stop:%s", h.tag)
	switch h.calls.Add(1) {
	case 1:
		return Retirement{Established: false, Residue: []string{"child not yet reaped"}}, nil
	case 2:
		if _, bounded := ctx.Deadline(); !bounded {
			h.note("unbounded-retry")
		}
		close(h.entered)
		<-h.gate
	}
	return Retirement{Established: true}, nil
}

func TestAHeldRetirementRetryHoldsNobodyElse(t *testing.T) {
	rt := &heldStop{sharedRuntime: newSharedRuntime(), tag: "a.aiiospkg", entered: make(chan struct{}), gate: make(chan struct{})}
	rt.hostBytesFor["a.aiiospkg"], rt.hostBytesFor["b.aiiospkg"], rt.hostBytesFor["d.aiiospkg"] = 3*gib, gib, 3*gib
	f := New(Config{Runtime: rt, Capacity: fixedCapacity{8 * gib, 5 * gib}})
	auditAttachFacility(t, f)
	var once sync.Once
	release := func() { once.Do(func() { close(rt.gate) }) }
	t.Cleanup(release)
	a, b, d := idOf("a.aiiospkg"), idOf("b.aiiospkg"), idOf("d.aiiospkg")

	f.Observe(obs("a.aiiospkg", "b.aiiospkg"), Policy{Revision: 1})
	auditWaitFacility(t, "a and b to serve", func() bool {
		return viewOf(f, a).State == StateActive && viewOf(f, b).State == StateActive
	})
	// .
	f.Observe(obs("b.aiiospkg", "d.aiiospkg"), Policy{Revision: 1})
	auditExecutorWait(t, rt.entered, "the controller's retry of a's retirement")
	if strings.Contains(rt.seen(), "unbounded-retry") {
		t.Error("the retry ran under no deadline")
	}
	auditWaitFacility(t, "d to wait for the room a still holds", func() bool { return viewOf(f, d).State == StateAdmitting })

	// .
	// .
	f.Observe(obs("d.aiiospkg"), Policy{Revision: 2})
	auditWaitFacility(t, "b to stop while a's retry is still held", func() bool { return strings.Contains(rt.seen(), "stop:b.aiiospkg") })
	auditWaitFacility(t, "b's record to go", func() bool { return viewOf(f, b).ID == "" })

	// .
	// .
	// .
	auditWaitPass(t, f)
	if strings.Contains(rt.seen(), "start:d.aiiospkg") {
		t.Fatal("d started in memory a retiring child had not given back")
	}
	if got := viewOf(f, a); got.State != StateDraining || len(got.Residue) == 0 {
		t.Errorf("a is not shown draining with what it holds: state=%s residue=%v", got.State, got.Residue)
	}

	release()
	auditWaitFacility(t, "d to serve once a's retirement is established", func() bool { return viewOf(f, d).State == StateActive })
	auditWaitFacility(t, "a's record to go", func() bool { return viewOf(f, a).ID == "" })
	if n := rt.calls.Load(); n != 2 {
		t.Errorf("a was asked to stop %d times; one first asking and one retry were enough", n)
	}
}

// .
func TestAnOwedRetirementIsAskedAgainOnABackoffNotInALoop(t *testing.T) {
	rt := newSharedRuntime()
	rt.stopPendingFor["one.aiiospkg"] = true
	f := auditFacility(t, rt)
	id := idOf("one.aiiospkg")
	f.Observe(obs("one.aiiospkg"), Policy{Revision: 1})
	auditWaitFacility(t, "it to serve", func() bool { return viewOf(f, id).State == StateActive })
	f.Observe(nil, Policy{Revision: 1})
	auditWaitFacility(t, "it to drain", func() bool { return viewOf(f, id).State == StateDraining })
	time.Sleep(700 * time.Millisecond)
	// .
	if n := strings.Count(rt.seen(), "stop:one.aiiospkg"); n < 2 || n > 5 {
		t.Errorf("asked to stop %d times in 700 ms; a backoff asks two to four times, a loop thousands", n)
	}
	// .
	auditWaitFacility(t, "the snapshot to say when the cleanup is asked again", func() bool { return !viewOf(f, id).CleanupAt.IsZero() })
	// .
	rt.mu.Lock()
	rt.stopPendingFor["one.aiiospkg"] = false
	rt.mu.Unlock()
	f.Settle(id)
	auditWaitFacility(t, "its record to go", func() bool { return viewOf(f, id).ID == "" })
}

// .

func TestAdmissionLooksAgainOnItsOwnClock(t *testing.T) {
	rt := newSharedRuntime()
	rt.hostBytesFor["one.aiiospkg"] = 2 * gib
	cap := &reviewCapacity{}
	cap.available.Store(gib)
	f := New(Config{Runtime: rt, Capacity: cap, AdmissionRefresh: 40 * time.Millisecond})
	auditAttachFacility(t, f)
	id := idOf("one.aiiospkg")
	f.Observe(obs("one.aiiospkg"), Policy{Revision: 1})
	auditWaitFacility(t, "it to wait for room", func() bool { return viewOf(f, id).State == StateAdmitting })
	// .
	// .
	cap.available.Store(4 * gib)
	auditWaitFacility(t, "it to be admitted with nobody telling the facility anything", func() bool { return viewOf(f, id).State == StateActive })
}

// .
// .
// .
// .
// .
// .
func TestOneThatCannotFitStandsInNobodysWay(t *testing.T) {
	rt := newSharedRuntime()
	rt.hostBytesFor["big.aiiospkg"], rt.hostBytesFor["small.aiiospkg"] = 6*gib, 2*gib
	cap := &reviewCapacity{}
	cap.available.Store(4 * gib)
	f := New(Config{Runtime: rt, Capacity: cap, AdmissionRefresh: 40 * time.Millisecond})
	auditAttachFacility(t, f)
	big, small := idOf("big.aiiospkg"), idOf("small.aiiospkg")
	f.Observe(obs("big.aiiospkg"), Policy{Revision: 1})
	auditWaitFacility(t, "big to wait for room", func() bool { return viewOf(f, big).State == StateAdmitting })
	f.Observe(obs("big.aiiospkg", "small.aiiospkg"), Policy{Revision: 1})
	auditWaitFacility(t, "small to serve although big arrived first", func() bool { return viewOf(f, small).State == StateActive })
	if strings.Contains(rt.seen(), "start:big.aiiospkg") {
		t.Fatal("the one that does not fit was started")
	}
	if s := viewOf(f, big).Admission; !strings.Contains(s, "waiting for 6.0 GB of host memory") {
		t.Errorf("big is waiting for memory and should say so: %q", s)
	}
	// .
	cap.available.Store(12 * gib)
	auditWaitFacility(t, "big to serve once it fits", func() bool { return viewOf(f, big).State == StateActive })
}

// .
// .
func TestAmongThoseThatCanGoTheOneInFrontGoesFirst(t *testing.T) {
	f := New(Config{Capacity: fixedCapacity{total: 16 * gib, avail: 4 * gib}})
	first := &admitWaiter{seq: 1, gen: 1, id: "first"}
	second := &admitWaiter{seq: 2, gen: 2, id: "second"}
	talker := &admitWaiter{seq: 3, gen: 3, id: "talker", voice: true}
	f.queue = []*admitWaiter{first, second}
	f.mu.Lock()
	defer f.mu.Unlock()
	need := Prepared{HostBytes: 3 * gib}
	if ok, why, _ := f.admissibleLocked(first, need); !ok {
		t.Fatalf("the one in front, which fits, waits: %s", why)
	}
	// .
	// .
	if ok, why, _ := f.admissibleLocked(second, need); ok || !strings.Contains(why, "waiting behind first") {
		t.Errorf("second: admitted=%v %q", ok, why)
	}
	// .
	f.queue = append(f.queue, talker)
	if ok, why, _ := f.admissibleLocked(talker, need); !ok {
		t.Fatalf("the voice engine, which fits, waits: %s", why)
	}
	if ok, why, _ := f.admissibleLocked(first, need); ok || !strings.Contains(why, "waiting behind talker") {
		t.Errorf("first, with a voice engine that can go now behind it in arrival: admitted=%v %q", ok, why)
	}
}

func TestRaisingTheCapOnStartsLetsAWaiterIn(t *testing.T) {
	rt := newSharedRuntime()
	rt.gate = make(chan struct{})
	f := auditFacility(t, rt)
	t.Cleanup(func() { close(rt.gate) })
	pol := Policy{Revision: 1, Admission: AdmissionPolicy{MaxConcurrentStarts: 1}}
	f.Observe(obs("one.aiiospkg", "two.aiiospkg"), pol)
	auditWaitFacility(t, "one start in flight and one waiting on the cap", func() bool {
		s := rt.seen()
		n := strings.Count(s, "start:")
		a, b := viewOf(f, idOf("one.aiiospkg")), viewOf(f, idOf("two.aiiospkg"))
		return n == 1 && (strings.Contains(a.Admission, "allows 1 at once") || strings.Contains(b.Admission, "allows 1 at once"))
	})
	pol.Admission.MaxConcurrentStarts = 2
	f.Observe(obs("one.aiiospkg", "two.aiiospkg"), pol)
	// .
	auditWaitFacility(t, "both to have started under the raised cap", func() bool {
		s := rt.seen()
		return strings.Contains(s, "start:one.aiiospkg") && strings.Contains(s, "start:two.aiiospkg")
	})
}

// .
// .

func TestRaisingTheBudgetReopensARefusalTheOldBudgetCaused(t *testing.T) {
	rt := newSharedRuntime()
	rt.hostBytesFor["one.aiiospkg"] = 2 * gib
	f := New(Config{Runtime: rt, Capacity: ampleCapacity{}})
	auditAttachFacility(t, f)
	id := idOf("one.aiiospkg")
	// .
	// .
	pol := Policy{Revision: 1, Admission: AdmissionPolicy{BudgetBytes: gib}}
	f.Observe(obs("one.aiiospkg"), pol)
	auditWaitFacility(t, "the refusal under a 1 GB budget", func() bool {
		v := viewOf(f, id)
		return v.State == StateRefused && v.Refusal != nil && v.Refusal.Class == ClassPermanent
	})
	// .
	starts := strings.Count(rt.seen(), "verify:")
	f.Observe(obs("one.aiiospkg"), pol)
	auditWaitPass(t, f)
	if n := strings.Count(rt.seen(), "verify:"); n != starts {
		t.Errorf("an unchanged observation retried a permanent refusal (%d verifies, was %d)", n, starts)
	}
	pol.Admission.BudgetBytes = 4 * gib
	f.Observe(obs("one.aiiospkg"), pol)
	auditWaitFacility(t, "it to serve once the budget allows it", func() bool { return viewOf(f, id).State == StateActive })
}

// .

func TestReleaseProgressSurvivesPanicsErrorsAndLateHolds(t *testing.T) {
	l := NewLease(1)
	calls := map[string]int{}
	var mu sync.Mutex
	count := func(name string) { mu.Lock(); calls[name]++; mu.Unlock() }
	panics, fails := 2, 1
	l.Hold("reservation", func() error { count("reservation"); return nil })
	l.Hold("root", func() error {
		count("root")
		if fails > 0 {
			fails--
			return errors.New("root still pinned")
		}
		return nil
	})
	l.Hold("child", func() error {
		count("child")
		if panics > 0 {
			panics--
			panic("stop panicked")
		}
		return nil
	})
	l.Hold("route-a", func() error { count("route-a"); return nil })
	l.Hold("route-b", func() error {
		count("route-b")
		// .
		l.Hold("late", func() error { count("late"); return nil })
		return nil
	})
	e := &executor{}
	// .
	for i, want := range []string{"reservation,root,child,late", "reservation,root,child", "reservation,root", ""} {
		err := e.releaseGuarded(context.Background(), l)
		if got := strings.Join(l.Holds(), ","); got != want {
			t.Fatalf("after release %d: held %q, want %q (err %v)", i+1, got, want, err)
		}
		if (err == nil) != (want == "") {
			t.Fatalf("after release %d: err=%v with %q held", i+1, err, want)
		}
		if l.Context() != context.Background() {
			t.Fatalf("after release %d: the release context was left set", i+1)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	for name, want := range map[string]int{"route-b": 1, "route-a": 1, "late": 1, "child": 3, "root": 2, "reservation": 1} {
		if calls[name] != want {
			t.Errorf("%s was given back %d times, want %d", name, calls[name], want)
		}
	}
	if !l.Discharged() {
		t.Error("everything came back and the ledger is not discharged")
	}
}

// .

func TestDiscoveryRefusalsStayLegibleUntilThePathsChange(t *testing.T) {
	rt := newSharedRuntime()
	rt.verifyErrFor["broken.aiiospkg"] = errors.New("the signature does not verify")
	rt.verifyErrFor["mangled.aiiospkg"] = errors.New("not a package archive")
	var mu sync.Mutex
	disc := Discovery{
		Found: []Found{
			{Dir: "plugins/a", Package: "one.aiiospkg", Size: 1, MTime: 1},
			{Dir: "plugins/broken", Package: "broken.aiiospkg", Size: 1, MTime: 1},
			{Dir: "plugins/mangled", Package: "mangled.aiiospkg", Size: 1, MTime: 1},
		},
		Ambiguous: []string{"plugins/two-of-them"},
	}
	f := New(Config{Runtime: &sameID{sharedRuntime: rt}, Capacity: ampleCapacity{}, Discover: func() Discovery {
		mu.Lock()
		defer mu.Unlock()
		return Discovery{Found: append([]Found(nil), disc.Found...), Ambiguous: append([]string(nil), disc.Ambiguous...)}
	}})
	// .
	mu.Lock()
	disc.Found = append(disc.Found, Found{Dir: "plugins/z-copy", Package: "one-copy.aiiospkg", Size: 1, MTime: 1})
	mu.Unlock()

	kinds := func() map[string]Skip {
		out := map[string]Skip{}
		for _, s := range f.Snapshot().Skips {
			out[s.Dir] = s
		}
		return out
	}
	check := func(when string) {
		t.Helper()
		got := kinds()
		for dir, kind := range map[string]string{"plugins/broken": SkipUnverified, "plugins/mangled": SkipUnverified,
			"plugins/two-of-them": SkipAmbiguous, "plugins/z-copy": SkipDuplicate} {
			s, ok := got[dir]
			if !ok || s.Kind != kind || s.Reason == "" {
				t.Fatalf("%s: %s is not in the snapshot as %s with a reason: %+v", when, dir, kind, got)
			}
			// .
			if (s.ID != "") != (kind == SkipDuplicate) {
				t.Errorf("%s: %s carries id %q", when, dir, s.ID)
			}
		}
	}
	f.Rescan(Policy{Revision: 1})
	check("after the first scan")
	verifies := strings.Count(rt.seen(), "verify:broken.aiiospkg")
	f.Rescan(Policy{Revision: 1})
	check("after an unchanged rescan")
	if n := strings.Count(rt.seen(), "verify:broken.aiiospkg"); n != verifies {
		t.Errorf("an unchanged archive was read again (%d, was %d)", n, verifies)
	}
	// .
	// .
	rt.mu.Lock()
	delete(rt.verifyErrFor, "broken.aiiospkg")
	rt.mu.Unlock()
	mu.Lock()
	disc.Found[1].MTime = 2
	disc.Ambiguous = nil
	mu.Unlock()
	f.Rescan(Policy{Revision: 1})
	got := kinds()
	if _, still := got["plugins/broken"]; still {
		t.Error("a repaired archive is still listed as refused")
	}
	if _, still := got["plugins/two-of-them"]; still {
		t.Error("a directory that is no longer ambiguous is still listed")
	}
	if _, ok := got["plugins/mangled"]; !ok {
		t.Error("an archive nobody touched lost its refusal")
	}
}

// .
type sameID struct{ *sharedRuntime }

func (s *sameID) Verify(ctx context.Context, pkg string) (Evidence, error) {
	ev, err := s.sharedRuntime.Verify(ctx, pkg)
	if err == nil && pkg == "one-copy.aiiospkg" {
		ev.ID = idOf("one.aiiospkg")
	}
	return ev, err
}

// .

func TestRetryAsksAgainAndKeepsTheRecordUntilThereIsANewOne(t *testing.T) {
	rt := newSharedRuntime()
	rt.verifyErrFor["one.aiiospkg"] = errors.New("the signature does not verify")
	f := auditFacility(t, rt)
	id := idOf("one.aiiospkg")
	f.Observe([]Observed{{ID: id, Package: "one.aiiospkg", Hash: "sha256:one.aiiospkg"}}, Policy{Revision: 1})
	auditWaitFacility(t, "the permanent refusal", func() bool { return viewOf(f, id).State == StateRefused })
	if f.Retry("id.example.nobody") {
		t.Error("an id the facility has never heard of was retried")
	}
	// .
	// .
	rt.mu.Lock()
	delete(rt.verifyErrFor, "one.aiiospkg")
	rt.gate = make(chan struct{})
	rt.mu.Unlock()
	if !f.Retry(id) {
		t.Fatal("a known id was not retried")
	}
	auditWaitFacility(t, "the new attempt to be starting", func() bool { return viewOf(f, id).State == StateStarting })
	if r := viewOf(f, id).Refusal; r == nil || !strings.Contains(fmt.Sprint(r.Cause), "does not verify") {
		t.Errorf("the last refusal left the card while the new attempt is still on its way: %v", r)
	}
	close(rt.gate)
	auditWaitFacility(t, "it to serve", func() bool { return viewOf(f, id).State == StateActive })
	if r := viewOf(f, id).Refusal; r != nil {
		t.Errorf("it serves, and the card still says its last attempt was refused: %v", r)
	}
}
