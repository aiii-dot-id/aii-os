//go:build (linux && !android) || (darwin && !ios) || windows

package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
	"github.com/aiii-dot-id/aii-os/internal/pluginfacility"
	"github.com/aiii-dot-id/aii-os/internal/pluginhost"
)

// swapFetch serves the package's models and says what it was asked for. A
// URL can fail, wait for its gate, or deliver part and then wait for the
// host to stop.
type swapFetch struct {
	mu    sync.Mutex
	asked []string
	fail  map[string]error
	gate  map[string]chan struct{}
	part  map[string]int
}

func newSwapFetch() *swapFetch {
	return &swapFetch{fail: map[string]error{}, gate: map[string]chan struct{}{}, part: map[string]int{}}
}

func (f *swapFetch) fetch(ctx context.Context, url string, offset int64, w io.Writer) (int64, error) {
	f.mu.Lock()
	f.asked = append(f.asked, fmt.Sprintf("%s@%d", url, offset))
	fail, gate, part := f.fail[url], f.gate[url], f.part[url]
	delete(f.part, url)
	f.mu.Unlock()
	if fail != nil {
		return 0, fail
	}
	body := componentModelBody(url)[offset:]
	if part > 0 {
		n, _ := w.Write(body[:part])
		<-ctx.Done()
		return int64(n), ctx.Err()
	}
	if gate != nil {
		select {
		case <-gate:
		case <-ctx.Done():
			return 0, ctx.Err()
		}
	}
	n, err := w.Write(body)
	return int64(n), err
}

func (f *swapFetch) urls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.asked...)
}

const (
	fullURL  = "https://example.invalid/full"
	smallURL = "https://example.invalid/small"
)

// swapStand is one standing engine of a set.
type swapStand struct {
	*pluginhost.Activation
	set   string
	ended chan struct{}
}

func (e *swapStand) Terminated() <-chan struct{} { return e.ended }
func (e *swapStand) Failure() ([]time.Time, error) {
	return nil, errors.New("the engine's process exited")
}
func (e *swapStand) HostBytesHeld() (int64, bool) { return 0, false }
func (e *swapStand) PluginID() string             { return e.ID }
func (h *swapEngines) Health(_ context.Context, r pluginfacility.Running) error {
	return h.healthErr[r.(*swapStand).set]
}

// swapEngines is the product's runtime — Verify, Prepare and Acquire — with
// the engine stood in: Start stands up the set the staged package chose,
// Redirect adopts it into the application's view as the product's commit
// does, Stop releases it.
type swapEngines struct {
	pluginRuntime
	mu        sync.Mutex
	starts    []string
	stops     []string
	live      map[*swapStand]bool
	most      int
	readyErr  map[string]error
	healthErr map[string]error
	stands    []*swapStand
	// started is told each set as its engine starts.
	started func(set string)
}

func newSwapEngines(a *App) *swapEngines {
	return &swapEngines{pluginRuntime: newPluginRuntime(a), live: map[*swapStand]bool{}, readyErr: map[string]error{}, healthErr: map[string]error{}}
}

func (h *swapEngines) Start(_ context.Context, p pluginfacility.Prepared, _ *pluginfacility.Lease) (pluginfacility.Running, error) {
	set := p.Handle.(*pluginhost.Staged).Selection().VariantID
	h.mu.Lock()
	started := h.started
	h.mu.Unlock()
	if started != nil {
		started(set)
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.starts = append(h.starts, set)
	if err := h.readyErr[set]; err != nil {
		return nil, err
	}
	ev := p.Evidence
	e := &swapStand{Activation: &pluginhost.Activation{ID: ev.ID, Version: ev.Version, Package: ev.Package, Hash: ev.PackageHash, ManifestHash: ev.ManifestHash,
		Plugin: &pluginhost.ActivePlugin{ID: ev.ID, Version: ev.Version, VariantID: set}}, set: set, ended: make(chan struct{})}
	h.live[e] = true
	h.most = max(h.most, len(h.live))
	h.stands = append(h.stands, e)
	return e, nil
}

func (h *swapEngines) Redirect(_, to pluginfacility.Running) error {
	next := to.(*swapStand)
	h.a.pluginMu.Lock()
	if h.a.activeMeta == nil {
		h.a.activeMeta = map[string]activePkgMeta{}
	}
	h.a.activeMeta[next.ID] = activePkgMeta{pkg: next.Package, owner: next.Activation}
	h.a.pluginMu.Unlock()
	h.a.forgetAcquisition(next.ID)
	return nil
}

func (h *swapEngines) Stop(_ context.Context, r pluginfacility.Running) (pluginfacility.Retirement, error) {
	e := r.(*swapStand)
	h.a.releaseActivation(e.Activation)
	h.mu.Lock()
	h.stops = append(h.stops, e.set)
	delete(h.live, e)
	h.mu.Unlock()
	return pluginfacility.Retirement{Established: true}, nil
}

func (h *swapEngines) record() (starts, stops []string, live, most int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string(nil), h.starts...), append([]string(nil), h.stops...), len(h.live), h.most
}

type swapCapacity struct{}

func (swapCapacity) Measure() pluginfacility.Availability {
	return pluginfacility.Availability{HostKnown: true, HostTotal: 64 << 30, HostAvailable: 48 << 30}
}

// swapIdentity is one process of an identity: its App, Acquirer and
// facility over a models directory that outlives it.
type swapIdentity struct {
	a     *App
	f     *pluginfacility.Facility
	eng   *swapEngines
	fetch *swapFetch
	id    string
	stop  func()
}

// bootSwap starts an identity's process over models, on a machine whose
// facts are room().
func bootSwap(t *testing.T, roots packagefmt.TrustRoots, pkg string, models *testStore, room func() pluginfacility.Availability, fetch *swapFetch, shape ...func(*swapEngines)) *swapIdentity {
	t.Helper()
	lifetime, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	var f *pluginfacility.Facility
	var fmu sync.Mutex
	acq := models.acquirer(fetch.fetch, pluginhost.AcquirerConfig{
		After: func(time.Duration) <-chan time.Time { return nil }, Logf: func(string, ...any) {},
		Spawn: func(fn func()) bool { wg.Add(1); go func() { defer wg.Done(); fn() }(); return true },
		Ready: func(string) {
			fmu.Lock()
			defer fmu.Unlock()
			if f != nil {
				f.Poke("material ready")
			}
		}})
	acq.Attach(lifetime)
	a := selectionApp(t, roots, room, acq)
	a.pluginOpts.StoreDir = models.w.Dir
	a.pluginOpts.FreeDisk = func(string) (int64, error) { return 100 << 30, nil }
	eng := newSwapEngines(a)
	for _, fn := range shape {
		fn(eng)
	}
	fmu.Lock()
	f = pluginfacility.New(pluginfacility.Config{Runtime: eng, Capacity: swapCapacity{}})
	fmu.Unlock()
	a.facilityOnce.Do(func() { a.facility = f })
	f.Attach(lifetime)
	ev, err := eng.Verify(t.Context(), pkg)
	if err != nil {
		t.Fatal(err)
	}
	f.Observe([]pluginfacility.Observed{{ID: ev.ID, Dir: models.dir, Package: pkg, Hash: ev.PackageHash, ManifestHash: ev.ManifestHash}}, pluginfacility.Policy{Revision: 1})
	var once sync.Once
	s := &swapIdentity{a: a, f: f, eng: eng, fetch: fetch, id: ev.ID, stop: func() { once.Do(func() { f.Close(); cancel(); wg.Wait() }) }}
	t.Cleanup(s.stop)
	return s
}

func (s *swapIdentity) view() pluginfacility.InstanceView {
	for _, v := range s.f.Snapshot().Instances {
		if v.ID == s.id {
			return v
		}
	}
	return pluginfacility.InstanceView{}
}

// serving is the set the serving activation runs, "" when none serves.
func (s *swapIdentity) serving() string {
	for _, a := range s.view().Activations {
		if a.Role == pluginfacility.RoleActive && a.Selection != nil {
			return a.Selection.Variant
		}
	}
	return ""
}

func (s *swapIdentity) until(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			starts, stops, live, _ := s.eng.record()
			t.Fatalf("timed out waiting for %s: %+v; starts %v stops %v live %d; fetched %v", what, s.view(), starts, stops, live, s.fetch.urls())
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// installSmall puts the small set's material on disk, as an identity that
// last ran it has: chosen on a machine whose device was not measured.
func installSmall(t *testing.T, roots packagefmt.TrustRoots, pkg string, models *testStore) {
	t.Helper()
	fetch := newSwapFetch()
	noGPU := func() pluginfacility.Availability {
		return pluginfacility.Availability{HostKnown: true, HostTotal: 32 << 30, HostAvailable: 16 << 30}
	}
	s := bootSwap(t, roots, pkg, models, noGPU, fetch)
	s.until(t, "the small set serving", func() bool { return s.serving() == "small" })
	s.stop()
	if got := fetch.urls(); !slices.Equal(got, []string{smallURL + "@0"}) {
		t.Fatalf("installing the small set fetched %v", got)
	}
}

// (a) A boot with the small set's material on disk and the full set's not:
// the plugin serves AT ONCE on the small set (before, it was unavailable
// until the download), the full set is fetched while it serves, then
// started beside it, ready, healthy, given the traffic; the small set
// drains; exactly the full set's material is fetched.
func TestABootServesTheInstalledSetAndSwapsInTheBetterOne(t *testing.T) {
	roots, pkg := componentSetsPackage(t, "1.0.0", fullSet, smallSet)
	models := newTestStore(t)
	installSmall(t, roots, pkg, models)

	fetch := newSwapFetch()
	arrive := make(chan struct{})
	fetch.gate[fullURL] = arrive
	s := bootSwap(t, roots, pkg, models, roomy, fetch)
	s.until(t, "the small set serving while the full set downloads", func() bool {
		return s.serving() == "small" && slices.Contains(fetch.urls(), fullURL+"@0")
	})
	v := s.view()
	if v.State != pluginfacility.StateUpdating {
		t.Fatalf("while the full set arrives: %s", v.State)
	}
	// The page: the card of what serves says which set and why, and names
	// the one arriving; the download is the acquisition's row.
	var card *pluginfacility.Selection
	for _, act := range v.Activations {
		if act.Role == pluginfacility.RoleActive {
			card = act.Selection
		}
	}
	sets := selectionView(card)
	if sets == nil || sets.Variant != "small" || sets.Better != "full" || len(sets.Sets) != 2 || !sets.Sets[0].Arriving || !sets.Sets[1].Chosen ||
		!strings.Contains(sets.Sets[1].Why, "serves while full arrives") {
		t.Fatalf("the serving card's sets: %+v", sets)
	}
	s.until(t, "the download's row on the page", func() bool {
		s.a.applyFacilitySnapshot()
		for _, p := range s.a.pluginPendingViews() {
			if p.ID == s.id {
				return p.Phase == pluginhost.PhaseFetching && p.FilesTotal == 1 && p.FilesPresent == 0
			}
		}
		return false
	})
	close(arrive)
	s.until(t, "the full set serving and the small set drained", func() bool {
		_, stops, live, _ := s.eng.record()
		return s.serving() == "full" && live == 1 && slices.Equal(stops, []string{"small"})
	})
	starts, _, _, most := s.eng.record()
	if !slices.Equal(starts, []string{"small", "full"}) || most != 2 {
		t.Fatalf("starts %v, at most %d engines", starts, most)
	}
	if got := fetch.urls(); !slices.Equal(got, []string{fullURL + "@0"}) {
		t.Fatalf("the swap fetched %v; exactly the full set's material", got)
	}
	if got := s.a.serving(s.id); got == nil || got.Variant != "full" {
		t.Fatalf("what the application says serves: %+v", got)
	}
}

// (b) The full set fails its readiness: the small set stays serving, and the
// card of what serves says why, with the stage and when it is tried again.
func TestABetterSetThatFailsReadinessLeavesTheInstalledOneServing(t *testing.T) {
	roots, pkg := componentSetsPackage(t, "1.0.0", fullSet, smallSet)
	models := newTestStore(t)
	installSmall(t, roots, pkg, models)
	fetch := newSwapFetch()
	s := bootSwap(t, roots, pkg, models, roomy, fetch, func(e *swapEngines) {
		e.readyErr["full"] = errors.New("the full set's child did not report ready within its allowance")
	})
	s.until(t, "the full set refused", func() bool { return s.view().Refusal != nil })
	v := s.view()
	card := lifecycleView(v)
	if s.serving() != "small" || v.State != pluginfacility.StateActive || card.Refusal == nil || card.Refusal.Stage != "start" ||
		!strings.Contains(card.Refusal.Cause, "did not report ready") || card.RetryAt == "" {
		t.Fatalf("after the full set failed: %+v; card %+v", v, card)
	}
	_, stops, live, _ := s.eng.record()
	if len(stops) != 0 || live != 1 {
		t.Fatalf("a failed swap tore something down: stops %v, live %d", stops, live)
	}
	// Its material arrived and is not "activating" beside the card of what
	// serves.
	s.a.applyFacilitySnapshot()
	for _, p := range s.a.pluginPendingViews() {
		if p.ID == s.id {
			t.Fatalf("a row beside the serving card says %+v", p)
		}
	}
}

// (c) The full set's download fails: the small set stays serving, and the
// page shows the failure and the next attempt on the download's row.
func TestABetterSetWhoseDownloadFailsLeavesTheInstalledOneServing(t *testing.T) {
	roots, pkg := componentSetsPackage(t, "1.0.0", fullSet, smallSet)
	models := newTestStore(t)
	installSmall(t, roots, pkg, models)
	fetch := newSwapFetch()
	fetch.fail[fullURL] = &pluginhost.FetchStatusError{Status: 503}
	s := bootSwap(t, roots, pkg, models, roomy, fetch)
	var row bool
	s.until(t, "the download's failure on the page", func() bool {
		s.a.applyFacilitySnapshot()
		for _, p := range s.a.pluginPendingViews() {
			if p.ID == s.id {
				row = p.Phase == pluginhost.PhaseWaiting && strings.Contains(p.LastError, "503") && p.RetryAt != ""
			}
		}
		return row
	})
	if s.serving() != "small" {
		t.Fatalf("while the full set's download fails: %+v", s.view())
	}
	_, stops, live, _ := s.eng.record()
	if len(stops) != 0 || live != 1 {
		t.Fatalf("a failed download tore something down: stops %v, live %d", stops, live)
	}
}

// (d) A dip does not swap a running set away. While the move is pending a
// dip refuses only the move, which is asked again once the device is free;
// once the full set serves, a dip — through a pass, a re-ask of its
// admission, and the engine starting again — leaves it serving and fetches
// nothing.
func TestADipDoesNotSwapARunningSetAway(t *testing.T) {
	roots, pkg := componentSetsPackage(t, "1.0.0", fullSet, smallSet)
	models := newTestStore(t)
	installSmall(t, roots, pkg, models)
	var mu sync.Mutex
	room := roomy()
	facts := func() pluginfacility.Availability { mu.Lock(); defer mu.Unlock(); return room }
	dip := func(on bool) {
		mu.Lock()
		defer mu.Unlock()
		room = roomy()
		if on {
			room.Devices = map[string]pluginfacility.DeviceAvailability{"cuda": {Known: true, Total: 8 << 30, Available: 1 << 30}}
		}
	}
	fetch := newSwapFetch()
	// The move pending, a dip as the candidate is judged — the device taken
	// the moment the small set's engine starts: the move waits.
	s := bootSwap(t, roots, pkg, models, facts, fetch, func(e *swapEngines) {
		e.started = func(set string) {
			if set == "small" {
				dip(true)
			}
		}
	})
	s.until(t, "the small set serving, the move refused for the dip", func() bool {
		v := s.view()
		return s.serving() == "small" && v.Refusal != nil && strings.Contains(v.Refusal.Error(), "nothing to move to")
	})
	if got := fetch.urls(); len(got) != 0 {
		t.Fatalf("a dip fetched %v", got)
	}
	s.eng.mu.Lock()
	s.eng.started = nil
	s.eng.mu.Unlock()
	dip(false)
	// Asked again on its own, on the visible backoff.
	s.until(t, "the full set serving once the device is free", func() bool { return s.serving() == "full" })
	// The full set serving, a dip: nothing moves it.
	dip(true)
	before, _, _, _ := s.eng.record()
	s.f.Poke("again")
	s.f.Observe([]pluginfacility.Observed{{ID: s.id, Dir: models.dir, Package: pkg, Hash: s.a.serving(s.id).PackageHash, ManifestHash: s.a.serving(s.id).ManifestHash}}, pluginfacility.Policy{Revision: 2})
	time.Sleep(300 * time.Millisecond)
	// Its engine stops for good and the plugin starts again during the dip:
	// the full set, kept, is chosen again.
	s.eng.mu.Lock()
	last := s.eng.stands[len(s.eng.stands)-1]
	s.eng.mu.Unlock()
	close(last.ended)
	s.until(t, "the crash recorded", func() bool { return s.view().Refusal != nil && s.serving() == "" })
	s.f.Retry(s.id)
	s.until(t, "the full set serving again", func() bool { return s.serving() == "full" })
	starts, _, _, most := s.eng.record()
	if got := starts[len(before):]; !slices.Equal(got, []string{"full"}) || most > 2 {
		t.Fatalf("during the dip the plugin started %v (at most %d engines)", got, most)
	}
	if got := fetch.urls(); !slices.Equal(got, []string{fullURL + "@0"}) {
		t.Fatalf("fetched %v; the full set's material once", got)
	}
}

// (e) A first install, nothing on disk: the set this machine would run is
// fetched and then started, as before; nothing serves meanwhile.
func TestAFirstInstallFetchesTheBetterSetFirst(t *testing.T) {
	roots, pkg := componentSetsPackage(t, "1.0.0", fullSet, smallSet)
	fetch := newSwapFetch()
	arrive := make(chan struct{})
	fetch.gate[fullURL] = arrive
	s := bootSwap(t, roots, pkg, newTestStore(t), roomy, fetch)
	s.until(t, "the full set's download", func() bool { return slices.Contains(fetch.urls(), fullURL+"@0") })
	time.Sleep(200 * time.Millisecond)
	if starts, _, _, _ := s.eng.record(); len(starts) != 0 || s.serving() != "" {
		t.Fatalf("a first install started %v before its material arrived", starts)
	}
	close(arrive)
	s.until(t, "the full set serving", func() bool { return s.serving() == "full" })
	starts, _, _, _ := s.eng.record()
	if !slices.Equal(starts, []string{"full"}) || !slices.Equal(fetch.urls(), []string{fullURL + "@0"}) {
		t.Fatalf("a first install started %v, fetching %v", starts, fetch.urls())
	}
}

// (f) A running set and a swap in flight survive a restart cleanly. The
// identity restarts mid-download: the next process serves the small set at
// once and resumes the full set's download where it stopped. The small
// set's engine stops for good mid-download: the move ends with it, the
// plugin starts again — never two activations of it, never a third engine —
// and the full set's download resumes; its bytes are fetched once.
func TestASwapInFlightSurvivesARestart(t *testing.T) {
	roots, pkg := componentSetsPackage(t, "1.0.0", fullSet, smallSet)
	models := newTestStore(t)
	installSmall(t, roots, pkg, models)

	// The identity restarts with two of the full model's five bytes on disk.
	first := newSwapFetch()
	first.part[fullURL] = 2
	s := bootSwap(t, roots, pkg, models, roomy, first)
	s.until(t, "the small set serving mid-download", func() bool {
		return s.serving() == "small" && slices.Contains(first.urls(), fullURL+"@0")
	})
	time.Sleep(100 * time.Millisecond)
	s.stop()
	next := newSwapFetch()
	arrive := make(chan struct{})
	next.gate[fullURL] = arrive
	s = bootSwap(t, roots, pkg, models, roomy, next)
	s.until(t, "the small set serving again at once", func() bool {
		return s.serving() == "small" && slices.Contains(next.urls(), fullURL+"@2")
	})
	close(arrive)
	s.until(t, "the full set serving", func() bool { return s.serving() == "full" })
	starts, _, _, most := s.eng.record()
	if !slices.Equal(starts, []string{"small", "full"}) || most != 2 || !slices.Equal(next.urls(), []string{fullURL + "@2"}) {
		t.Fatalf("after the restart: starts %v, at most %d engines, fetched %v", starts, most, next.urls())
	}

	// The small set's engine stops for good while the move is in flight.
	models = newTestStore(t)
	installSmall(t, roots, pkg, models)
	crash := newSwapFetch()
	crash.part[fullURL] = 2
	s = bootSwap(t, roots, pkg, models, roomy, crash)
	s.until(t, "the small set serving mid-download", func() bool {
		return s.serving() == "small" && slices.Contains(crash.urls(), fullURL+"@0")
	})
	s.eng.mu.Lock()
	small := s.eng.stands[0]
	s.eng.mu.Unlock()
	close(small.ended)
	s.until(t, "the move ended with the small set", func() bool {
		_, _, live, _ := s.eng.record()
		return live == 0 && s.view().Refusal != nil && s.serving() == ""
	})
	s.f.Retry(s.id) // the restart, without waiting out the budget
	s.until(t, "the full set serving", func() bool { return s.serving() == "full" })
	starts, _, _, most = s.eng.record()
	if !slices.Equal(starts, []string{"small", "small", "full"}) || most != 2 {
		t.Fatalf("after the engine's end: starts %v, at most %d engines", starts, most)
	}
	var fetched int64
	for _, u := range crash.urls() {
		if strings.HasPrefix(u, fullURL+"@") {
			var off int64
			fmt.Sscanf(strings.TrimPrefix(u, fullURL+"@"), "%d", &off)
			fetched = max(fetched, off)
		}
	}
	if fetched != 2 || !slices.Equal(crash.urls(), []string{fullURL + "@0", fullURL + "@2"}) {
		t.Fatalf("the full set's download: %v; resumed, never fetched twice", crash.urls())
	}
}
