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

// swapEngine is one standing engine of a set.
type swapEngine struct {
	id, set string
	ended   chan struct{} // closed: it stopped serving for good
	failing func()        // runs as its failure is read
}

func (e *swapEngine) PluginID() string            { return e.id }
func (e *swapEngine) Terminated() <-chan struct{} { return e.ended }
func (e *swapEngine) Failure() ([]time.Time, error) {
	if e.failing != nil {
		e.failing()
	}
	return nil, errors.New("the engine's process exited")
}
func (e *swapEngine) String() string { return e.set }
func (r *swapRuntime) note(f string, a ...any) {
	r.mu.Lock()
	r.calls = append(r.calls, fmt.Sprintf(f, a...))
	r.mu.Unlock()
}
func (r *swapRuntime) seen() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return strings.Join(r.calls, ",")
}
func (r *swapRuntime) Health(_ context.Context, x Running) error {
	return r.healthErr[x.(*swapEngine).set]
}

// swapRuntime prepares the installed "small" set, Interim, while nothing
// serves and the "large" set once something does — as pluginhost does when
// the large set's material is still to come. The large set's material
// arrives when material is closed.
type swapRuntime struct {
	mu        sync.Mutex
	calls     []string
	serving   *swapEngine
	live      map[*swapEngine]bool
	most      int
	material  chan struct{}
	startErr  map[string]error
	healthErr map[string]error
	engines   []*swapEngine
	interim   bool   // the runtime says Interim at all
	failing   func() // given to every engine it starts
}

func newSwapRuntime() *swapRuntime {
	return &swapRuntime{live: map[*swapEngine]bool{}, material: make(chan struct{}), startErr: map[string]error{}, healthErr: map[string]error{}, interim: true}
}

func (r *swapRuntime) Verify(_ context.Context, pkg string) (Evidence, error) {
	return Evidence{ID: idOf(pkg), Version: "1.0.0", Package: pkg, PackageHash: "sha256:" + pkg, Tier: "T3"}, nil
}

func (r *swapRuntime) Prepare(_ context.Context, ev Evidence) (Prepared, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	set := "large"
	if r.serving == nil && r.interim {
		set = "small"
	}
	r.calls = append(r.calls, "prepare:"+set)
	p := Prepared{Evidence: ev, Handle: set, HostBytes: 1 << 30, Selection: &Selection{Variant: set}}
	if set == "small" {
		p.Interim, p.Present, p.Selection.Better = true, true, "large"
	}
	return p, nil
}

func (r *swapRuntime) Acquire(ctx context.Context, p Prepared, progress func(MaterialStatus)) error {
	r.note("acquire:%s", p.Handle)
	progress(MaterialStatus{BytesPresent: 0, BytesTotal: 10, FilesPresent: 0, FilesTotal: 1})
	select {
	case <-r.material:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (r *swapRuntime) Start(_ context.Context, p Prepared, lease *Lease) (Running, error) {
	set := p.Handle.(string)
	r.note("start:%s", set)
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.startErr[set]; err != nil {
		return nil, err
	}
	e := &swapEngine{id: p.Evidence.ID, set: set, ended: make(chan struct{}), failing: r.failing}
	r.live[e] = true
	r.most = max(r.most, len(r.live))
	r.engines = append(r.engines, e)
	return e, nil
}

func (r *swapRuntime) Redirect(from, to Running) error {
	next := to.(*swapEngine)
	if from == nil {
		r.note("admit:%s", next.set)
	} else {
		r.note("redirect:%s->%s", from.(*swapEngine).set, next.set)
	}
	r.mu.Lock()
	r.serving = next
	r.mu.Unlock()
	return nil
}

func (r *swapRuntime) Stop(_ context.Context, x Running) (Retirement, error) {
	e := x.(*swapEngine)
	r.note("stop:%s", e.set)
	r.mu.Lock()
	delete(r.live, e)
	if r.serving == e {
		r.serving = nil
	}
	r.mu.Unlock()
	return Retirement{Established: true}, nil
}

func (r *swapRuntime) liveSets() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []string
	for e := range r.live {
		out = append(out, e.set)
	}
	return out
}

// activeSet is the set the instance's serving activation runs.
func (f *Facility) activeSet(id string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if inst := f.instances[id]; inst != nil {
		if a := inst.Active(); a != nil && a.Selection != nil {
			return a.Selection.Variant
		}
	}
	return ""
}

// standing reports whether any of v's activations is still on its way up.
func standing(v InstanceView) bool {
	for _, a := range v.Activations {
		if a.Role == RoleStarting || a.Role == RoleCandidate {
			return true
		}
	}
	return false
}

func TestAnInterimSetIsSwappedForTheBetterOneBesideIt(t *testing.T) {
	rt := newSwapRuntime()
	f := newFacility(t, rt)
	id := idOf("sets.aiiospkg")
	f.Observe(obs("sets.aiiospkg"), Policy{Revision: 1})
	// The installed set serves AT ONCE, and the better one is on its way
	// beside it, its material still arriving.
	until(t, "the installed set serving and the better one acquiring", func() bool {
		return f.activeSet(id) == "small" && strings.Contains(rt.seen(), "acquire:large")
	})
	if got := f.stateOf(id); got != StateUpdating {
		t.Fatalf("while the better set arrives: %s", got)
	}
	close(rt.material)
	until(t, "the better set serving", func() bool { return f.activeSet(id) == "large" && len(rt.liveSets()) == 1 })
	settle()
	want := "prepare:small,start:small,admit:small,prepare:large,acquire:large,start:large,redirect:small->large,stop:small"
	if got := rt.seen(); got != want {
		t.Fatalf("the swap:\n got %s\nwant %s", got, want)
	}
	if got := f.stateOf(id); got != StateActive || rt.most != 2 {
		t.Fatalf("after the swap: %s, at most %d engines", got, rt.most)
	}
}

// A BETTER SET THAT FAILS LEAVES THE INSTALLED ONE SERVING: its readiness or
// its health refused, nothing torn down, the reason on the instance and the
// move asked again on the visible backoff.
func TestAFailedSwapLeavesTheInstalledSetServing(t *testing.T) {
	for _, fails := range []string{"readiness", "health"} {
		t.Run(fails, func(t *testing.T) {
			rt := newSwapRuntime()
			close(rt.material)
			cause := errors.New("the large set's child did not report ready within its allowance")
			if fails == "readiness" {
				rt.startErr["large"] = cause
			} else {
				cause = errors.New("the large set answered unwell")
				rt.healthErr["large"] = cause
			}
			f := newFacility(t, rt)
			id := idOf("sets.aiiospkg")
			f.Observe(obs("sets.aiiospkg"), Policy{Revision: 1})
			var v InstanceView
			until(t, "the swap refused", func() bool {
				for _, x := range f.Snapshot().Instances {
					if x.ID == id {
						v = x
					}
				}
				return v.Refusal != nil
			})
			stage := map[string]Stage{"readiness": StageStart, "health": StageHealth}[fails]
			if v.State != StateActive || f.activeSet(id) != "small" || !errors.Is(v.Refusal.Cause, cause) || v.Refusal.Stage != stage ||
				v.Refusal.Class != ClassTransient || v.RetryAt.IsZero() {
				t.Fatalf("after the failed swap: %+v; refusal %+v", v, v.Refusal)
			}
			if strings.Contains(rt.seen(), "stop:small") || strings.Contains(rt.seen(), "redirect:") {
				t.Fatalf("a failed swap touched what serves: %s", rt.seen())
			}
			if live := rt.liveSets(); len(live) != 1 || live[0] != "small" {
				t.Fatalf("engines standing after the failed swap: %v", live)
			}
		})
	}
}

// NOTHING BUT AN INTERIM SET IS SWAPPED. A set that serves as the runtime's
// own choice is never stood beside again for the same bytes — not on a pass,
// not on a re-ask of its admission — whatever others hold at the instant.
func TestOnlyAnInterimSetIsSwapped(t *testing.T) {
	rt := newSwapRuntime()
	rt.interim = false
	close(rt.material)
	f := newFacility(t, rt)
	id := idOf("sets.aiiospkg")
	f.Observe(obs("sets.aiiospkg"), Policy{Revision: 1})
	until(t, "the set serving", func() bool { return f.activeSet(id) == "large" })
	for i := 0; i < 3; i++ {
		f.Poke("again")
	}
	f.Observe(obs("sets.aiiospkg"), Policy{Revision: 2}) // a re-ask
	settle()
	if got := rt.seen(); strings.Count(got, "start:") != 1 {
		t.Fatalf("a set serving as the runtime's choice was stood beside: %s", got)
	}
}

// A CANDIDATE STANDS BESIDE WHAT SERVES, AND ENDS WITH IT. The installed set
// stops for good while the better one's material is still arriving: the
// candidate ends at once, the installed set's end is recorded under the
// restart budget, and the plugin starts again — the installed set, then the
// swap — never two engines of one set, never a third engine.
func TestACandidateEndsWithTheSetItStandsBeside(t *testing.T) {
	rt := newSwapRuntime()
	f := newFacility(t, rt)
	id := idOf("sets.aiiospkg")
	f.Observe(obs("sets.aiiospkg"), Policy{Revision: 1})
	until(t, "the swap's candidate acquiring", func() bool {
		return f.activeSet(id) == "small" && strings.Contains(rt.seen(), "acquire:large")
	})
	rt.mu.Lock()
	first := rt.engines[0]
	rt.mu.Unlock()
	close(first.ended)
	var v InstanceView
	until(t, "the candidate ended with the installed set", func() bool {
		for _, x := range f.Snapshot().Instances {
			if x.ID == id {
				v = x
			}
		}
		return v.Refusal != nil && !standing(v) && len(rt.liveSets()) == 0 && strings.Contains(rt.seen(), "stop:small")
	})
	// The card says why the move ended; the installed set's end is charged
	// to the restart budget, and the plugin waits on it — the crash's
	// backoff, which the candidate's own ending never sets.
	if v.State != StateRefused || v.Refusal.Stage != StageCancelled || !errors.Is(v.Refusal.Cause, errServingEnded) ||
		time.Until(v.RetryAt) < 10*time.Second || strings.Contains(rt.seen(), "start:large") {
		t.Fatalf("after the installed set's end: %+v; refusal %+v; %s", v, v.Refusal, rt.seen())
	}
	// The operator's Try again: the installed set again, then the swap.
	if !f.Retry(id) {
		t.Fatal("retry")
	}
	until(t, "the installed set serving again", func() bool { return f.activeSet(id) == "small" })
	close(rt.material)
	until(t, "the better set serving", func() bool { return f.activeSet(id) == "large" && len(rt.liveSets()) == 1 })
	settle()
	if rt.most > 2 || strings.Count(rt.seen(), "start:large") != 1 || strings.Count(rt.seen(), "start:small") != 2 {
		t.Fatalf("starts %s; at most %d engines at once", rt.seen(), rt.most)
	}
}

// THE INSTALLED SET'S END IS SAID BEFORE THE CANDIDATE'S. The candidate ends
// because what it stood beside did, and a plan made on its refusal while the
// facility still believed the installed set served issued another candidate
// at once: the end was charged and its backoff set a moment later, and the
// command already on its way started the installed set again under no wait
// at all (native qualification, 0.1.16). Here the plan is given every
// chance to do that: reading the installed set's failure waits until the
// plan has made a new attempt, or long enough that one would have come.
func TestTheServingEndIsSaidBeforeTheCandidatesRefusal(t *testing.T) {
	rt := newSwapRuntime()
	id := idOf("sets.aiiospkg")
	var f *Facility
	var before Generation
	newest := func() Generation {
		g := Generation(0)
		for _, x := range f.Snapshot().Instances {
			for _, a := range x.Activations {
				if x.ID == id && a.Gen > g {
					g = a.Gen
				}
			}
		}
		return g
	}
	rt.failing = func() {
		for deadline := time.Now().Add(300 * time.Millisecond); time.Now().Before(deadline) && newest() <= before; {
			time.Sleep(time.Millisecond)
		}
	}
	f = newFacility(t, rt)
	f.Observe(obs("sets.aiiospkg"), Policy{Revision: 1})
	until(t, "the swap's candidate acquiring", func() bool {
		return f.activeSet(id) == "small" && strings.Contains(rt.seen(), "acquire:large")
	})
	before = newest()
	rt.mu.Lock()
	first := rt.engines[0]
	rt.mu.Unlock()
	close(first.ended)
	var v InstanceView
	until(t, "the candidate ended with the installed set", func() bool {
		for _, x := range f.Snapshot().Instances {
			if x.ID == id {
				v = x
			}
		}
		return v.Refusal != nil && !standing(v) && strings.Contains(rt.seen(), "stop:small") || newest() > before
	})
	settle()
	if got := newest(); got != before || v.State != StateRefused || v.Refusal.Stage != StageCancelled ||
		!errors.Is(v.Refusal.Cause, errServingEnded) || time.Until(v.RetryAt) < 10*time.Second {
		t.Fatalf("after the installed set's end: newest generation %d (was %d); %+v; refusal %+v; %s", got, before, v, v.Refusal, rt.seen())
	}
	if got := rt.seen(); strings.Count(got, "start:") != 1 || len(rt.liveSets()) != 0 {
		t.Fatalf("started again before its backoff ran out: %s; live %v", got, rt.liveSets())
	}
}
