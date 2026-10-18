package pluginfacility

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// checkRuntime is a runtime whose candidates are checked.
type checkRuntime struct {
	// hold, when set, keeps an attempt told of a failed set in Prepare
	// until it is closed: the refusal before it stays the newest.
	hold     chan struct{}
	mu       sync.Mutex
	order    []string
	outcome  func(pkg, set string) Checks
	prepared []map[string]string // what each Prepare was told had failed
	starts   []string            // pkg:set, each start
	live     map[*swapEngine]bool
	serving  *swapEngine
	// refuses, when set, is the error Validate returns beside the checks
	// of a set: a candidate the runtime itself refuses after its checks
	// ran (a check's session the engine never ended).
	refuses func(set string) error
}

func newCheckRuntime(outcome func(pkg, set string) Checks) *checkRuntime {
	return &checkRuntime{order: []string{"large", "small"}, outcome: outcome, live: map[*swapEngine]bool{}}
}

// checkedID is the one plugin every checked package here is a release of.
const checkedID = "id.example.checked"

// checkedObs is pkg observed as a release of checkedID.
func checkedObs(pkg string) []Observed {
	return []Observed{{ID: checkedID, Dir: "plugins/" + checkedID, Package: pkg, Hash: "sha256:" + pkg}}
}

func (r *checkRuntime) Verify(_ context.Context, pkg string) (Evidence, error) {
	return Evidence{ID: checkedID, Version: strings.TrimSuffix(strings.TrimPrefix(pkg, "checked-"), ".aiiospkg"), Package: pkg, PackageHash: "sha256:" + pkg, Tier: "T3"}, nil
}

func (r *checkRuntime) Prepare(ctx context.Context, ev Evidence) (Prepared, error) {
	if r.hold != nil && len(ev.ChecksFailed) > 0 {
		select {
		case <-r.hold:
		case <-ctx.Done():
			return Prepared{}, ctx.Err()
		}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.prepared = append(r.prepared, ev.ChecksFailed)
	// The judgment the card shows, as pluginhost makes it: every set in
	// the order, the chosen one, and why each one passed over was.
	sel := &Selection{}
	for _, set := range r.order {
		v := SetVerdict{Variant: set}
		if why, failed := ev.ChecksFailed[set]; failed {
			v.Reasons = []Reason{{Words: "its publisher's checks failed on this machine: " + why, Remedy: "Try again"}}
		} else if sel.Variant == "" {
			sel.Variant, v.Chosen = set, true
		}
		sel.Sets = append(sel.Sets, v)
	}
	if sel.Variant == "" {
		return Prepared{}, errors.New("no set this machine can run is left")
	}
	return Prepared{Evidence: ev, Handle: sel.Variant, Present: true, HostBytes: 1 << 20, Selection: sel}, nil
}

func (r *checkRuntime) Acquire(context.Context, Prepared, func(MaterialStatus)) error { return nil }

func (r *checkRuntime) Start(_ context.Context, p Prepared, _ *Lease) (Running, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	e := &swapEngine{id: p.Evidence.ID, set: p.Evidence.Package + ":" + p.Handle.(string), ended: make(chan struct{})}
	r.starts = append(r.starts, e.set)
	r.live[e] = true
	return e, nil
}

func (r *checkRuntime) Health(context.Context, Running) error { return nil }

func (r *checkRuntime) Validate(_ context.Context, x Running) (Checks, error) {
	pkg, set, _ := strings.Cut(x.(*swapEngine).set, ":")
	c := r.outcome(pkg, set)
	c.Variant = set
	if r.refuses != nil {
		return c, r.refuses(set)
	}
	return c, nil
}

func (r *checkRuntime) Redirect(_, to Running) error {
	r.mu.Lock()
	r.serving = to.(*swapEngine)
	r.mu.Unlock()
	return nil
}

func (r *checkRuntime) Stop(_ context.Context, x Running) (Retirement, error) {
	r.mu.Lock()
	delete(r.live, x.(*swapEngine))
	r.mu.Unlock()
	return Retirement{Established: true}, nil
}

func (r *checkRuntime) servingSet() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.serving == nil {
		return ""
	}
	return r.serving.set
}

func (r *checkRuntime) seen() (starts []string, prepared []map[string]string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.starts), slices.Clone(r.prepared)
}

// memVerdicts is the store the host keeps the verdicts in, in memory.
type memVerdicts struct {
	mu  sync.Mutex
	raw []byte
}

func (m *memVerdicts) LoadVerdicts() ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return slices.Clone(m.raw), nil
}

func (m *memVerdicts) SaveVerdicts(raw []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.raw = slices.Clone(raw)
	return nil
}

// clockAt is a deterministic clock the test moves.
type clockAt struct {
	mu  sync.Mutex
	now time.Time
}

func (c *clockAt) Now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.now }
func (c *clockAt) move(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	c.mu.Unlock()
}

// checkedFacility is one start of the host: a facility over rt, keeping
// its verdicts in store under build.
func checkedFacility(t *testing.T, rt Runtime, store VerdictStore, build string, clock *clockAt) *Facility {
	t.Helper()
	f := New(Config{Runtime: rt, Capacity: ampleCapacity{}, Verdicts: store, HostBuild: build, Now: clock.Now})
	ctx, cancel := context.WithCancel(context.Background())
	f.Attach(ctx)
	t.Cleanup(func() { cancel(); f.Close() })
	return f
}

func (f *Facility) servingChecks(id string) *ChecksVerdict {
	for _, v := range f.Snapshot().Instances {
		if v.ID != id {
			continue
		}
		for _, a := range v.Activations {
			if a.Role == RoleActive {
				return a.Checks
			}
		}
	}
	return nil
}

func (f *Facility) refusalOf(id string) *Refusal {
	for _, v := range f.Snapshot().Instances {
		if v.ID == id {
			for _, a := range v.Activations {
				if a.Refusal != nil {
					return a.Refusal
				}
			}
		}
	}
	return nil
}

func passes(string, string) Checks { return Checks{Ran: 2} }

func failing(set string, outcome CheckOutcome) func(pkg, s string) Checks {
	return func(_, s string) Checks {
		if s == set {
			return Checks{Ran: 2, Failed: &CheckFailure{Check: "embeds the memory", Outcome: outcome, Words: "came back at 0.800 of its reference, under the 0.950 it asks"}}
		}
		return Checks{Ran: 2}
	}
}

var start = time.Date(2026, 10, 9, 3, 0, 0, 0, time.UTC)

func servedVerdict(t *testing.T, f *Facility, id string) *ChecksVerdict {
	t.Helper()
	var v *ChecksVerdict
	until(t, "the facility's verdict for what serves", func() bool { v = f.servingChecks(id); return v != nil })
	return v
}

// A CHECK THAT PASSES: the set serves, and the card says when its
// publisher's checks passed on this machine.
func TestAPassingCheckServesAndTheCardSaysWhen(t *testing.T) {
	clock := &clockAt{now: start}
	rt := newCheckRuntime(passes)
	f := checkedFacility(t, rt, &memVerdicts{}, "b1", clock)
	id := checkedID
	f.Observe(checkedObs("checked-1.aiiospkg"), Policy{Revision: 1})
	until(t, "the set serving", func() bool { return rt.servingSet() == "checked-1.aiiospkg:large" })
	if v := servedVerdict(t, f, id); v.State != ChecksPassed || !v.At.Equal(start) || v.Variant != "large" {
		t.Fatalf("the card's verdict: %+v", v)
	}
}

// A WRONG ANSWER DEMOTES THE SET: the attempt is refused at the validation
// stage, with the check in its publisher's words, and the next set the
// machine can run is asked for at once, told what failed.
func TestAWrongAnswerPassesTheSetOverForTheNext(t *testing.T) {
	clock := &clockAt{now: start}
	rt := newCheckRuntime(failing("large", CheckWrong))
	rt.hold = make(chan struct{})
	f := checkedFacility(t, rt, &memVerdicts{}, "b1", clock)
	id := checkedID
	f.Observe(checkedObs("checked-1.aiiospkg"), Policy{Revision: 1})
	var ref *Refusal
	until(t, "the first set refused at validation", func() bool { ref = f.refusalOf(id); return ref != nil })
	var failed *ChecksFailedError
	if ref.Stage != StageValidation || !errors.As(ref.Cause, &failed) || failed.Variant != "large" || !strings.Contains(ref.Remedy, "Try again") ||
		!strings.Contains(ref.Error(), `"embeds the memory": came back at 0.800`) {
		t.Fatalf("the refusal of the first set: %+v", ref)
	}
	close(rt.hold)
	until(t, "the next set serving", func() bool { return rt.servingSet() == "checked-1.aiiospkg:small" })
	starts, prepared := rt.seen()
	if strings.Join(starts, ",") != "checked-1.aiiospkg:large,checked-1.aiiospkg:small" || len(prepared) != 2 || prepared[0] != nil ||
		!strings.Contains(prepared[1]["large"], `"embeds the memory": came back at 0.800`) {
		t.Fatalf("starts %v, prepared with %v", starts, prepared)
	}
	if v := servedVerdict(t, f, id); v.State != ChecksPassed || v.Variant != "small" {
		t.Fatalf("the serving set's verdict: %+v", v)
	}
}

// A WRONG ANSWER BESIDE WHAT SERVES LEAVES IT SERVING: an update whose sets
// all answer wrongly is refused at validation and the release serving keeps
// the traffic, as a failed health check would leave it.
func TestAWrongAnswerBesideWhatServesLeavesItServing(t *testing.T) {
	clock := &clockAt{now: start}
	rt := newCheckRuntime(func(pkg, _ string) Checks {
		if pkg == "checked-2.aiiospkg" {
			return Checks{Ran: 1, Failed: &CheckFailure{Check: "answers", Outcome: CheckWrong, Words: "answered with an error: no index"}}
		}
		return Checks{Ran: 1}
	})
	f := checkedFacility(t, rt, &memVerdicts{}, "b1", clock)
	id := checkedID
	f.Observe(checkedObs("checked-1.aiiospkg"), Policy{Revision: 1})
	until(t, "the first release serving", func() bool { return rt.servingSet() == "checked-1.aiiospkg:large" })
	f.Observe(checkedObs("checked-2.aiiospkg"), Policy{Revision: 1})
	until(t, "the update refused for good", func() bool {
		starts, _ := rt.seen()
		return slices.Contains(starts, "checked-2.aiiospkg:small") && f.refusalOf(id) != nil
	})
	settle()
	if got := rt.servingSet(); got != "checked-1.aiiospkg:large" {
		t.Fatalf("what serves after a failed update: %s", got)
	}
	starts, _ := rt.seen()
	if n := strings.Count(strings.Join(starts, ","), "checked-2.aiiospkg:large"); n != 1 {
		t.Fatalf("a set that failed for good was started again: %v", starts)
	}
}

// A TIMEOUT IS MOMENTARY, AND FIXED BY REPETITION: the set serves with its
// checks not yet passed at the first and second start, and at the third
// start within 24 hours it has failed for good and the next set serves.
// Timeouts spread wider than the window never add up.
func TestATimeoutServesAndIsFixedOnlyByRepetition(t *testing.T) {
	store := &memVerdicts{}
	clock := &clockAt{now: start}
	id := checkedID
	for n := 1; n <= 2; n++ {
		rt := newCheckRuntime(failing("large", CheckTimedOut))
		f := checkedFacility(t, rt, store, "b1", clock)
		f.Observe(checkedObs("checked-1.aiiospkg"), Policy{Revision: 1})
		until(t, fmt.Sprintf("start %d serving the timed-out set", n), func() bool { return rt.servingSet() == "checked-1.aiiospkg:large" })
		if v := servedVerdict(t, f, id); v.State != ChecksPending || v.Timeouts != n || v.Failed.Outcome != CheckTimedOut {
			t.Fatalf("start %d: %+v", n, v)
		}
		f.Close()
		clock.move(time.Hour)
	}
	rt := newCheckRuntime(failing("large", CheckTimedOut))
	rt.hold = make(chan struct{})
	f := checkedFacility(t, rt, store, "b1", clock)
	f.Observe(checkedObs("checked-1.aiiospkg"), Policy{Revision: 1})
	var ref *Refusal
	until(t, "the third start refusing the set", func() bool { ref = f.refusalOf(id); return ref != nil })
	var failed *ChecksFailedError
	if !errors.As(ref.Cause, &failed) || !strings.Contains(failed.Failure.Words, "timed out at 3 separate starts within 24 hours") {
		t.Fatalf("the third timeout: %+v", ref)
	}
	close(rt.hold)
	until(t, "the third start passing the set over", func() bool { return rt.servingSet() == "checked-1.aiiospkg:small" })
	f.Close()

	// The same three, a day apart: never fixed.
	wide := &memVerdicts{}
	for n := 1; n <= 3; n++ {
		rt := newCheckRuntime(failing("large", CheckTimedOut))
		f := checkedFacility(t, rt, wide, "b1", clock)
		f.Observe(checkedObs("checked-1.aiiospkg"), Policy{Revision: 1})
		until(t, fmt.Sprintf("wide start %d serving", n), func() bool { return rt.servingSet() == "checked-1.aiiospkg:large" })
		if v := servedVerdict(t, f, id); v.State != ChecksPending || v.Timeouts != 1 {
			t.Fatalf("wide start %d: %+v", n, v)
		}
		f.Close()
		clock.move(25 * time.Hour)
	}
}

// A REMEMBERED FAILURE IS NOT TRIED AGAIN UNTIL THE PACKAGE, THE SET OR THE
// HOST'S BUILD CHANGES, OR THE OPERATOR PRESSES TRY AGAIN. The host is
// started again over the same store: the failed set is passed over before
// it starts. Other bytes, another build and Try again each ask again.
func TestARememberedFailureIsNotTriedAgainUntilSomethingChanges(t *testing.T) {
	store := &memVerdicts{}
	clock := &clockAt{now: start}
	id := checkedID
	first := newCheckRuntime(failing("large", CheckWrong))
	f := checkedFacility(t, first, store, "b1", clock)
	f.Observe(checkedObs("checked-1.aiiospkg"), Policy{Revision: 1})
	until(t, "the next set serving", func() bool { return first.servingSet() == "checked-1.aiiospkg:small" })
	f.Close()

	// The same bytes, the same build: the failed set never starts.
	again := newCheckRuntime(failing("large", CheckWrong))
	f = checkedFacility(t, again, store, "b1", clock)
	f.Observe(checkedObs("checked-1.aiiospkg"), Policy{Revision: 1})
	until(t, "the next set serving at once", func() bool { return again.servingSet() == "checked-1.aiiospkg:small" })
	if starts, prepared := again.seen(); slices.Contains(starts, "checked-1.aiiospkg:large") || prepared[0]["large"] == "" {
		t.Fatalf("a remembered failure was tried again: starts %v, prepared %v", starts, prepared)
	}
	// Try again: the set is asked again.
	if !f.Retry(id) {
		t.Fatal("retry")
	}
	until(t, "the failed set asked again on Try again", func() bool {
		starts, _ := again.seen()
		return slices.Contains(starts, "checked-1.aiiospkg:large")
	})
	f.Close()

	// Other bytes of the same plugin: asked.
	other := newCheckRuntime(failing("large", CheckWrong))
	f = checkedFacility(t, other, store, "b1", clock)
	f.Observe(checkedObs("checked-2.aiiospkg"), Policy{Revision: 1})
	until(t, "other bytes asked", func() bool {
		starts, _ := other.seen()
		return slices.Contains(starts, "checked-2.aiiospkg:large")
	})
	f.Close()

	// Another build: asked.
	built := newCheckRuntime(passes)
	f = checkedFacility(t, built, store, "b2", clock)
	f.Observe(checkedObs("checked-1.aiiospkg"), Policy{Revision: 1})
	until(t, "another build asks the set again", func() bool { return built.servingSet() == "checked-1.aiiospkg:large" })
	if _, prepared := built.seen(); prepared[0] != nil {
		t.Fatalf("another build was told %v", prepared[0])
	}
}

// A CALL THAT COULD NOT RUN IS NOT HELD AGAINST THE SET: it serves, its
// checks not yet passed, and nothing is remembered against it.
func TestAnUnansweredCheckIsNotHeldAgainstTheSet(t *testing.T) {
	store := &memVerdicts{}
	clock := &clockAt{now: start}
	rt := newCheckRuntime(failing("large", CheckUnanswered))
	f := checkedFacility(t, rt, store, "b1", clock)
	id := checkedID
	f.Observe(checkedObs("checked-1.aiiospkg"), Policy{Revision: 1})
	until(t, "the set serving", func() bool { return rt.servingSet() == "checked-1.aiiospkg:large" })
	if v := servedVerdict(t, f, id); v.State != ChecksPending || v.Failed.Outcome != CheckUnanswered {
		t.Fatalf("the card's verdict: %+v", v)
	}
	if raw, _ := store.LoadVerdicts(); strings.Contains(string(raw), "failed") || strings.Contains(string(raw), "timed_out") {
		t.Fatalf("an unanswered check was remembered against the set: %s", raw)
	}
}

// NOTHING DECLARED: the set is used on its readiness alone, and the card
// says so; nothing is remembered.
func TestASetWithNoChecksIsUsedOnItsReadinessAlone(t *testing.T) {
	store := &memVerdicts{}
	rt := newCheckRuntime(func(string, string) Checks { return Checks{} })
	f := checkedFacility(t, rt, store, "b1", &clockAt{now: start})
	id := checkedID
	f.Observe(checkedObs("checked-1.aiiospkg"), Policy{Revision: 1})
	until(t, "the set serving", func() bool { return rt.servingSet() == "checked-1.aiiospkg:large" })
	if v := servedVerdict(t, f, id); v.State != ChecksNone {
		t.Fatalf("the card's verdict: %+v", v)
	}
	if raw, _ := store.LoadVerdicts(); len(raw) != 0 {
		t.Fatalf("a set with no checks was remembered: %s", raw)
	}
}

var errUnended = errors.New("the engine did not end the check's session after its abort")

// WHAT THE CHECKS CAME TO IS REMEMBERED BEFORE THE CANDIDATE IS REFUSED. A
// runtime that refuses a candidate after its checks ran — the check timed
// out and the engine never ended its session, so the candidate is stopped
// — still says what the checks came to, and the repetition rule counts it:
// refused at the first and second start, at the third within 24 hours the
// set has failed for good and the next set serves. Without the record,
// such a set would be retried for ever, never fixed.
func TestAVerdictIsRecordedBeforeTheCandidateIsRefused(t *testing.T) {
	store := &memVerdicts{}
	clock := &clockAt{now: start}
	id := checkedID
	unended := func(set string) error {
		if set == "large" {
			return errUnended
		}
		return nil
	}
	for n := 1; n <= 2; n++ {
		rt := newCheckRuntime(failing("large", CheckTimedOut))
		rt.refuses = unended
		f := checkedFacility(t, rt, store, "b1", clock)
		f.Observe(checkedObs("checked-1.aiiospkg"), Policy{Revision: 1})
		var ref *Refusal
		until(t, fmt.Sprintf("start %d refused at validation", n), func() bool { ref = f.refusalOf(id); return ref != nil })
		if ref.Stage != StageValidation || !errors.Is(ref.Cause, errUnended) {
			t.Fatalf("start %d: the candidate is refused for its unended session: %+v", n, ref)
		}
		f.Close()
		clock.move(time.Hour)
	}
	rt := newCheckRuntime(failing("large", CheckTimedOut))
	rt.refuses = unended
	rt.hold = make(chan struct{})
	f := checkedFacility(t, rt, store, "b1", clock)
	f.Observe(checkedObs("checked-1.aiiospkg"), Policy{Revision: 1})
	var ref *Refusal
	until(t, "the third start refused", func() bool { ref = f.refusalOf(id); return ref != nil })
	var failed *ChecksFailedError
	if !errors.As(ref.Cause, &failed) || !strings.Contains(failed.Failure.Words, "timed out at 3 separate starts within 24 hours") {
		t.Fatalf("the third unended start within 24 hours fails the set for good: %+v", ref)
	}
	close(rt.hold)
	until(t, "the next set serving", func() bool { return rt.servingSet() == "checked-1.aiiospkg:small" })
}
