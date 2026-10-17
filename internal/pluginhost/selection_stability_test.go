//go:build (linux && !android) || (darwin && !ios) || windows

package pluginhost

import (
	"context"
	"errors"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
	"github.com/aiii-dot-id/aii-os/internal/pluginfacility"
)

// fetchLog is a fetcher pair for an Acquirer: it serves the fixture's bytes,
// or — while held — blocks until released or cancelled, recording both.
type fetchLog struct {
	c         componentSets
	mu        sync.Mutex
	hold      bool
	started   []string
	cancelled []string
	began     chan string
	release   chan struct{}
}

func newFetchLog(c componentSets, hold bool) *fetchLog {
	return &fetchLog{c: c, hold: hold, began: make(chan string, 16), release: make(chan struct{})}
}

func (f *fetchLog) fetch(ctx context.Context, url string, offset int64, w io.Writer) (int64, error) {
	f.mu.Lock()
	f.started = append(f.started, url)
	hold := f.hold
	f.mu.Unlock()
	f.began <- url
	if hold {
		select {
		case <-f.release:
		case <-ctx.Done():
			f.mu.Lock()
			f.cancelled = append(f.cancelled, url)
			f.mu.Unlock()
			return 0, ctx.Err()
		}
	}
	var body []byte
	switch url {
	case f.c.fullURL, f.c.smlURL:
		body = f.c.weights
	case f.c.full.decl.URL:
		body = f.c.full.archive
	case f.c.small.decl.URL:
		body = f.c.small.archive
	default:
		return 0, errors.New("not a declared URL")
	}
	n, err := w.Write(body[offset:])
	return int64(n), err
}

// next is the next download to begin.
func (f *fetchLog) next(t *testing.T) string {
	t.Helper()
	select {
	case url := <-f.began:
		return url
	case <-time.After(10 * time.Second):
		t.Fatal("no download began")
		return ""
	}
}

func (f *fetchLog) seen() (started, cancelled []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.started...), append([]string(nil), f.cancelled...)
}

// attachedAcquirer is an Acquirer running under a lifetime the test ends,
// and waits out, before its temporary directories are removed; its writer
// is opts' store.
func attachedAcquirer(t *testing.T, opts *Options, f *fetchLog, ready func(string)) *Acquirer {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	w := openStore(t)
	opts.StoreDir = w.Dir
	acq := storeAcquirer(w, f.fetch, AcquirerConfig{Ready: ready,
		After: func(time.Duration) <-chan time.Time { return nil }, Logf: func(string, ...any) {},
		Spawn: func(fn func()) bool { wg.Add(1); go func() { defer wg.Done(); fn() }(); return true }})
	acq.Attach(ctx)
	t.Cleanup(func() { cancel(); wg.Wait() })
	return acq
}

func TestAComponentSetStaysChosenWhileItsMaterialIsInFlight(t *testing.T) {
	c := componentSetsFor(t, "1.0.0")
	room := pluginfacility.Availability{HostKnown: true, HostTotal: 32 << 30, HostAvailable: 16 << 30,
		Devices: map[string]pluginfacility.DeviceAvailability{"cuda": {Known: true, Total: 8 << 30, Available: 8 << 30}}}
	fetches := newFetchLog(c, true)
	opts := &Options{Roots: c.roots, WorkerBinary: "unused-before-start", StoreDir: t.TempDir(),
		ResourceFacts: func(context.Context) (pluginfacility.Availability, error) { return room, nil }}
	opts.Acquirer = attachedAcquirer(t, opts, fetches, nil)
	res, err := Verify(c.pkg, opts)
	if err != nil {
		t.Fatal(err)
	}
	attempt := func() string {
		t.Helper()
		staged, err := StagePreparedContext(t.Context(), c.pkg, res, opts)
		if err != nil {
			t.Fatal(err)
		}
		var acquiring *AcquiringError
		if err := staged.Acquire(t.Context()); !errors.As(err, &acquiring) {
			t.Fatalf("the material is not in flight: %v", err)
		}
		return staged.Selection().VariantID
	}
	first := attempt()
	if url := fetches.next(t); url != c.fullURL {
		t.Fatalf("the first download is %s", url)
	}
	room.HostAvailable = 7 << 30 // a browser opened during the download
	second := attempt()
	room.HostAvailable = 16 << 30
	third := attempt()
	started, cancelled := fetches.seen()
	if first != "full" || second != "full" || third != "full" {
		t.Fatalf("attempts selected %s, %s, %s; the set in flight must stay chosen", first, second, third)
	}
	if len(cancelled) != 0 || len(started) != 1 {
		t.Fatalf("the download was replaced: started %v, cancelled %v", started, cancelled)
	}
}

// A REFUSAL BEFORE SPAWN IS NOT A REASON TO SWAP. Start's own capacity check
// refuses a set that no longer fits (step 5 of the selection rule); the
// facility retries — and the retry used to choose afresh: the small set,
// downloaded beside the full set already verified on disk.
func TestARefusalBeforeSpawnRetriesTheSameSet(t *testing.T) {
	c := componentSetsFor(t, "1.0.0")
	room := pluginfacility.Availability{HostKnown: true, HostTotal: 32 << 30, HostAvailable: 16 << 30,
		Devices: map[string]pluginfacility.DeviceAvailability{"cuda": {Known: true, Total: 8 << 30, Available: 8 << 30}}}
	fetches := newFetchLog(c, false)
	ready := make(chan string, 4)
	opts := &Options{Roots: c.roots, WorkerBinary: "unused-before-start", StoreDir: t.TempDir(),
		ResourceFacts: func(context.Context) (pluginfacility.Availability, error) { return room, nil }}
	opts.Acquirer = attachedAcquirer(t, opts, fetches, func(id string) { ready <- id })
	res, err := Verify(c.pkg, opts)
	if err != nil {
		t.Fatal(err)
	}
	stage := func() *Staged {
		t.Helper()
		staged, err := StagePreparedContext(t.Context(), c.pkg, res, opts)
		if err != nil {
			t.Fatal(err)
		}
		return staged
	}
	var acquiring *AcquiringError
	if err := stage().Acquire(t.Context()); !errors.As(err, &acquiring) {
		t.Fatalf("the first attempt hands its material over: %v", err)
	}
	select {
	case <-ready:
	case <-time.After(10 * time.Second):
		t.Fatal("the material never arrived")
	}
	staged := stage()
	if err := staged.Acquire(t.Context()); err != nil {
		t.Fatalf("the material is on disk: %v", err)
	}
	room.HostAvailable = 0 // capacity lost before spawn
	var refused *VariantSelectionError
	if _, err := staged.Start(t.Context(), newRegistry(t), false); !errors.As(err, &refused) {
		t.Fatalf("stale capacity reached start: %v", err)
	}
	room.HostAvailable = 7 << 30 // the facility's retry, the host still busy
	retry := stage()
	if got := retry.Selection().VariantID; got != "full" {
		t.Fatalf("the retry chose %s; the set on disk must stay chosen", got)
	}
	if err := retry.Acquire(t.Context()); err != nil {
		t.Fatalf("the retry's material is on disk: %v", err)
	}
	if started, _ := fetches.seen(); len(started) != 2 {
		t.Fatalf("downloads %v; only the full set's model and runtime", started)
	}
}

// A FACT NOT READ IN TIME IS UNKNOWN — THAT FACT, AND NOTHING ELSE. The
// measurement is bounded, and the host's figures arrive before an
// accelerator's driver tool has answered. A deadline used to fail the whole
// selection; now the accelerator is unknown, which refuses only the set that
// needs it.
func TestATimedOutFactLeavesOnlyThatFactUnknown(t *testing.T) {
	c := componentSetsFor(t, "1.0.0")
	opts := &Options{Roots: c.roots, WorkerBinary: "unused-before-start", StoreDir: t.TempDir(),
		ResourceFacts: func(context.Context) (pluginfacility.Availability, error) {
			return pluginfacility.Availability{HostKnown: true, HostTotal: 32 << 30, HostAvailable: 16 << 30}, context.DeadlineExceeded
		}}
	res, err := Verify(c.pkg, opts)
	if err != nil {
		t.Fatal(err)
	}
	staged, err := StagePreparedContext(t.Context(), c.pkg, res, opts)
	if err != nil {
		t.Fatalf("a fact the deadline cut short failed the whole selection: %v", err)
	}
	if got := staged.Selection().VariantID; got != "small" {
		t.Fatalf("selected %s; the set needing the unanswered accelerator must not be", got)
	}
	// The attempt itself ending is still the end of selection.
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := StagePreparedContext(ctx, c.pkg, res, opts); !errors.Is(err, context.Canceled) {
		t.Fatalf("a cancelled attempt selected: %v", err)
	}
}

func TestTheKeptSetIsReleasedOnlyByWhatTheRuleNames(t *testing.T) {
	c, update := componentSetsFor(t, "1.0.0"), componentSetsFor(t, "1.0.1")
	good := func() pluginfacility.Availability {
		return pluginfacility.Availability{HostKnown: true, HostTotal: 32 << 30, HostAvailable: 16 << 30,
			Devices: map[string]pluginfacility.DeviceAvailability{"cuda": {Known: true, Total: 8 << 30, Available: 8 << 30}}}
	}
	dip := func() pluginfacility.Availability { a := good(); a.HostAvailable = 7 << 30; return a }
	room := good()
	opts := &Options{Roots: c.roots, WorkerBinary: "unused-before-start", StoreDir: t.TempDir(),
		Acquirer:      NewAcquirer(AcquirerConfig{Logf: func(string, ...any) {}}), // never attached: a want is held, in flight
		ResourceFacts: func(context.Context) (pluginfacility.Availability, error) { return room, nil }}
	verify := func(pkg string) *packagefmt.Result {
		res, err := Verify(pkg, opts)
		if err != nil {
			t.Fatal(err)
		}
		return res
	}
	res, next := verify(c.pkg), verify(update.pkg)
	choose := func(pkg string, res *packagefmt.Result) (Selection, error) {
		t.Helper()
		staged, err := StagePreparedContext(t.Context(), pkg, res, opts)
		if err != nil {
			return Selection{}, err
		}
		var acquiring *AcquiringError
		if err := staged.Acquire(t.Context()); !errors.As(err, &acquiring) {
			t.Fatalf("the material is not handed over: %v", err)
		}
		return staged.Selection(), nil
	}
	mustChoose := func(pkg string, res *packagefmt.Result, want string, kept bool) Selection {
		t.Helper()
		sel, err := choose(pkg, res)
		if err != nil {
			t.Fatal(err)
		}
		if sel.VariantID != want || sel.Kept != kept {
			t.Fatalf("selected %s (kept=%v), want %s (kept=%v); refusals %v", sel.VariantID, sel.Kept, want, kept, sel.ResourceRefusals)
		}
		return sel
	}

	mustChoose(c.pkg, res, "full", false)
	room = dip()
	mustChoose(c.pkg, res, "full", true) // a dip is admission's to wait out

	// Different bytes are a new question.
	mustChoose(update.pkg, next, "small", false)
	room = good()
	mustChoose(update.pkg, next, "full", false)
	room = dip()
	mustChoose(update.pkg, next, "full", true) // and that one, once in flight, is not given up for a dip

	// The operator asking afresh, and the plugin no longer wanted.
	mustChoose(c.pkg, res, "small", false) // the update's want replaced the first package's
	opts.Acquirer.Reselect(c.id)
	room = good()
	mustChoose(c.pkg, res, "full", false)
	opts.Acquirer.Keep(map[string]bool{})
	room = dip()
	mustChoose(c.pkg, res, "small", false)

	// Impossible whatever else runs: a required accelerator unmeasured, more
	// memory than the host has in total, the operator's budget.
	opts.Acquirer.Reselect(c.id)
	room = good()
	mustChoose(c.pkg, res, "full", false)
	for name, facts := range map[string]func() pluginfacility.Availability{
		"accelerator":  func() pluginfacility.Availability { a := good(); a.Devices = nil; return a },
		"total memory": func() pluginfacility.Availability { a := good(); a.HostTotal, a.HostAvailable = 6<<30, 6<<30; return a },
	} {
		room = facts()
		sel := mustChoose(c.pkg, res, "small", false)
		if len(sel.ResourceRefusals) == 0 || sel.ResourceRefusals[0].VariantID != "full" {
			t.Fatalf("%s: the set given up must say why: %v", name, sel.ResourceRefusals)
		}
		opts.Acquirer.Reselect(c.id)
		room = good()
		mustChoose(c.pkg, res, "full", false)
	}
	opts.SelectionPolicy.BudgetBytes = 4 << 30
	mustChoose(c.pkg, res, "small", false)
	opts.SelectionPolicy.BudgetBytes = 0
	opts.Acquirer.Reselect(c.id)
	mustChoose(c.pkg, res, "full", false)

	// Unknown host memory admits nothing — and is no reason to give the
	// kept set up: nothing else could be chosen on it either.
	room = pluginfacility.Availability{}
	var refused *VariantSelectionError
	if _, err := choose(c.pkg, res); !errors.As(err, &refused) {
		t.Fatalf("unknown host memory admitted a set: %v", err)
	}
	room = dip()
	mustChoose(c.pkg, res, "full", true)
}

// Credit is the replaced release's declared reservation, never more than the
// platform has, and only on a fact that was measured.
func TestCreditGivesBackOnlyWhatTheReplacedReleaseHolds(t *testing.T) {
	const gib = int64(1 << 30)
	device := 6 * gib
	serving := &AcceleratorProfile{MemoryBytes: 8 * gib, DeviceMemoryBytes: &device, RequiredAccelerators: []string{"cuda", "vulkan"}}
	a := pluginfacility.Availability{HostKnown: true, HostTotal: 16 * gib, HostAvailable: 10 * gib, Devices: map[string]pluginfacility.DeviceAvailability{
		"cuda":   {Known: true, Total: 8 * gib, Available: 1 * gib},
		"vulkan": {Known: false},
		"metal":  {Known: true, Total: 16 * gib, Available: 10 * gib, Unified: true},
	}}
	got := credited(a, serving)
	if got.HostAvailable != 16*gib || got.Devices["cuda"].Available != 7*gib || got.Devices["vulkan"].Known || got.Devices["metal"].Available != 10*gib {
		t.Fatalf("credited %+v", got)
	}
	if a.Devices["cuda"].Available != 1*gib {
		t.Fatal("credit wrote through to the measurement it was given")
	}
	if got := credited(a, nil); got.HostAvailable != a.HostAvailable {
		t.Fatal("nothing serving, yet credit was given")
	}
	for _, bad := range []pluginfacility.Availability{
		{HostKnown: false, HostTotal: 16 * gib, HostAvailable: 1 * gib},
		{HostKnown: true, HostTotal: 16 * gib, HostAvailable: 17 * gib},
		{HostKnown: true, HostTotal: 16 * gib, HostAvailable: -1},
	} {
		if got := credited(bad, serving); got.HostAvailable != bad.HostAvailable {
			t.Fatalf("an invalid measurement was repaired by credit: %+v", got)
		}
	}
}

// What can pass on its own and what cannot, as the kept set is judged.
func TestCapacityRefusalsSeparateTheMomentaryFromTheFixed(t *testing.T) {
	const gib = int64(1 << 30)
	p := preferenceProfile(8*gib, 6*gib, "cuda")
	for _, tc := range []struct {
		name             string
		facts            pluginfacility.Availability
		policy           pluginfacility.AdmissionPolicy
		fixed, momentary int
	}{
		{"fits", pluginfacility.Availability{HostKnown: true, HostTotal: 32 * gib, HostAvailable: 16 * gib, Devices: map[string]pluginfacility.DeviceAvailability{"cuda": {Known: true, Total: 8 * gib, Available: 8 * gib}}}, pluginfacility.AdmissionPolicy{}, 0, 0},
		{"busy", pluginfacility.Availability{HostKnown: true, HostTotal: 32 * gib, HostAvailable: 2 * gib, Devices: map[string]pluginfacility.DeviceAvailability{"cuda": {Known: true, Total: 8 * gib, Available: 1 * gib}}}, pluginfacility.AdmissionPolicy{}, 0, 2},
		{"reserve-now", pluginfacility.Availability{HostKnown: true, HostTotal: 32 * gib, HostAvailable: 10 * gib, Devices: map[string]pluginfacility.DeviceAvailability{"cuda": {Known: true, Total: 8 * gib, Available: 8 * gib}}}, pluginfacility.AdmissionPolicy{ReserveBytes: 4 * gib}, 0, 1},
		{"small-host", pluginfacility.Availability{HostKnown: true, HostTotal: 6 * gib, HostAvailable: 6 * gib, Devices: map[string]pluginfacility.DeviceAvailability{"cuda": {Known: true, Total: 4 * gib, Available: 4 * gib}}}, pluginfacility.AdmissionPolicy{}, 2, 0},
		{"reserve-total", pluginfacility.Availability{HostKnown: true, HostTotal: 10 * gib, HostAvailable: 10 * gib, Devices: map[string]pluginfacility.DeviceAvailability{"cuda": {Known: true, Total: 8 * gib, Available: 8 * gib}}}, pluginfacility.AdmissionPolicy{ReserveBytes: 4 * gib}, 1, 0},
		{"budget", pluginfacility.Availability{HostKnown: true, HostTotal: 32 * gib, HostAvailable: 16 * gib, Devices: map[string]pluginfacility.DeviceAvailability{"cuda": {Known: true, Total: 8 * gib, Available: 8 * gib}}}, pluginfacility.AdmissionPolicy{BudgetBytes: 4 * gib}, 1, 0},
		{"unknown", pluginfacility.Availability{}, pluginfacility.AdmissionPolicy{}, 2, 0},
	} {
		fixed, momentary := capacityRefusals(p, tc.facts, tc.policy)
		if len(fixed) != tc.fixed || len(momentary) != tc.momentary {
			t.Fatalf("%s: fixed %v, momentary %v", tc.name, fixed, momentary)
		}
	}
}

// The measurement's own bound, met for real: what was read stands and the
// rest is unknown; only the attempt itself ending is an error.
func TestTheMeasurementBoundKeepsWhatWasRead(t *testing.T) {
	read := pluginfacility.Availability{HostKnown: true, HostTotal: 8 << 30, HostAvailable: 4 << 30}
	slow := func(own time.Duration) *Options { // a driver tool that never answers, in a measurement held to its own time
		return &Options{ResourceFacts: func(ctx context.Context) (pluginfacility.Availability, error) {
			ctx, cancel := context.WithTimeout(ctx, own)
			defer cancel()
			<-ctx.Done()
			return read, ctx.Err()
		}}
	}
	got, err := selectionAvailability(t.Context(), slow(20*time.Millisecond))
	if err != nil || !got.HostKnown || len(got.Devices) != 0 {
		t.Fatalf("the bound failed the measurement: %+v, %v", got, err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	if _, err := selectionAvailability(ctx, slow(time.Hour)); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("the attempt's own deadline must end selection: %v", err)
	}
	broken := errors.New("the measurer itself failed")
	if _, err := selectionAvailability(t.Context(), &Options{ResourceFacts: func(context.Context) (pluginfacility.Availability, error) { return read, broken }}); !errors.Is(err, broken) {
		t.Fatalf("a failed measurement read as a measured one: %v", err)
	}
}
