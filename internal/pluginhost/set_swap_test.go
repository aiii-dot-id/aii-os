//go:build (linux && !android) || (darwin && !ios) || windows

package pluginhost

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
	"github.com/aiii-dot-id/aii-os/internal/pluginfacility"
	"github.com/aiii-dot-id/aii-os/internal/plugstore"
	"github.com/aiii-dot-id/aii-os/internal/tools"
)

// swapHost is the disk and the machine one identity's selections run
// against, the device's free memory moved by the test.
type swapHost struct {
	s                  shape
	store              *plugstore.Writer
	mu                 sync.Mutex
	room               pluginfacility.Availability
	operatorTreeLimits packagefmt.TreeLimits
}

func newSwapHost(t *testing.T, s shape) *swapHost {
	return &swapHost{s: s, store: openStore(t), room: gpuHost(11 * gib)}
}

func (h *swapHost) setRoom(a pluginfacility.Availability) { h.mu.Lock(); h.room = a; h.mu.Unlock() }

func (h *swapHost) opts(acq *Acquirer, serving *ServingSet) *Options {
	return &Options{Roots: h.s.roots, WorkerBinary: "unused-before-start", StoreDir: h.store.Dir,
		Acquirer: acq, Serving: serving, RuntimeLimits: h.operatorTreeLimits,
		FreeDisk: func(string) (int64, error) { return 500 * gib, nil },
		ResourceFacts: func(context.Context) (pluginfacility.Availability, error) {
			h.mu.Lock()
			defer h.mu.Unlock()
			return h.room, nil
		}}
}

// installCompact puts the compact set's material on disk, as an identity
// that last ran it has: chosen while others held the device.
func (h *swapHost) installCompact(t *testing.T) {
	t.Helper()
	h.setRoom(gpuHost(2 * gib))
	f := &fetchRecord{s: h.s}
	acq, ready := attachedRecorder(t, f, h.store)
	if sel := acquiredBy(t, h.s, h.opts(acq, nil), ready).Selection(); sel.VariantID != "compact" {
		t.Fatalf("the busy device installed %s", sel.VariantID)
	}
	h.setRoom(gpuHost(11 * gib))
}

func TestAnInstalledSetServesWhileTheBetterOneArrives(t *testing.T) {
	s := realShape(t)
	large := s.sets[0].fixture

	// A FIRST INSTALL, nothing on disk: the set the machine would run is
	// fetched and then started, as it always was.
	first := newSwapHost(t, s)
	f := &fetchRecord{s: s}
	acq, ready := attachedRecorder(t, f, first.store)
	if sel := acquiredBy(t, s, first.opts(acq, nil), ready).Selection(); sel.VariantID != "large" || sel.Better != "" {
		t.Fatalf("a first install chose %s for %q", sel.VariantID, sel.Better)
	}
	if got := f.urls(); !slices.Equal(got, []string{"https://cdn.example.org/large-model", large.decl.URL}) {
		t.Fatalf("a first install fetched %v", got)
	}

	// THE COMPACT SET INSTALLED, the device free: nothing serves, so the
	// compact set serves at once for the large one, and nothing is fetched.
	host := newSwapHost(t, s)
	host.installCompact(t)
	f = &fetchRecord{s: s}
	acq, ready = attachedRecorder(t, f, host.store)
	staged := acquiredBy(t, s, host.opts(acq, nil), ready)
	sel := staged.Selection()
	if sel.VariantID != "compact" || sel.Better != "large" || len(f.urls()) != 0 {
		t.Fatalf("with the compact set on disk and nothing serving: %s for %q, fetching %v", sel.VariantID, sel.Better, f.urls())
	}
	compact, arriving := verdictOf(t, sel.Sets, "compact"), verdictOf(t, sel.Sets, "large")
	if !compact.Chosen || !strings.Contains(compact.Why, "serves while large arrives") ||
		arriving.Chosen || !arriving.Arriving || len(arriving.Reasons) != 0 || !strings.Contains(arriving.Why, "fetched in the background while compact serves") ||
		!strings.Contains(arriving.Bandwidth, "to download from cdn.example.org") {
		t.Fatalf("the sets in words: %+v", sel.Sets)
	}
	// Through the runtime the facility drives: the attempt is Interim.
	rt := NewRuntime(RuntimeWires{Options: func() *Options { return host.opts(acq, nil) },
		Policy:   func() pluginfacility.AdmissionPolicy { return pluginfacility.AdmissionPolicy{} },
		Serving:  func(string) *ServingSet { return nil },
		Acquirer: func() *Acquirer { return acq }})
	ev, err := rt.Verify(t.Context(), s.pkg)
	if err != nil {
		t.Fatal(err)
	}
	p, err := rt.Prepare(t.Context(), ev)
	if err != nil || !p.Interim || p.Selection == nil || p.Selection.Variant != "compact" || p.Selection.Better != "large" {
		t.Fatalf("the prepared attempt: %+v, %v", p, err)
	}

	// THE CANDIDATE BESIDE IT, during a dip: the set it would move to is not
	// possible this instant, and there is nothing to move to — the compact
	// set stays, and the judgment says why and what admits the large one.
	serving := servingOf(staged)
	host.setRoom(gpuHost(2 * gib))
	res, err := Verify(s.pkg, host.opts(acq, serving))
	if err != nil {
		t.Fatal(err)
	}
	_, err = StagePreparedContext(t.Context(), s.pkg, res, host.opts(acq, serving))
	var none *NoBetterSetError
	if !errors.As(err, &none) || none.Serving != "compact" {
		t.Fatalf("a candidate during a dip: %v", err)
	}
	if j := none.Judgment(); j == nil || !strings.Contains(reasonsText(verdictOf(t, j.Sets, "large")), "once that memory is free") {
		t.Fatalf("the judgment of a candidate with nothing to move to: %+v", j)
	}
	if len(f.urls()) != 0 {
		t.Fatalf("a candidate with nothing to move to fetched %v", f.urls())
	}

	// THE CANDIDATE, the device free: the large set, and only its material.
	host.setRoom(gpuHost(11 * gib))
	staged = acquiredBy(t, s, host.opts(acq, serving), ready)
	if sel := staged.Selection(); sel.VariantID != "large" || sel.Better != "" {
		t.Fatalf("the candidate chose %s for %q", sel.VariantID, sel.Better)
	}
	if got := f.urls(); !slices.Equal(got, []string{"https://cdn.example.org/large-model", large.decl.URL}) {
		t.Fatalf("the swap fetched %v; only the large set's model and runtime", got)
	}

	// A RELEASE OF OTHER BYTES on the same set is an update, not a move to
	// nothing: it is prepared whatever set serves.
	other := &ServingSet{PackageHash: "sha256:another-release", ManifestHash: "sha256:another-release", Variant: "large"}
	if _, err := StagePreparedContext(t.Context(), s.pkg, res, host.opts(acq, other)); err != nil {
		t.Fatalf("an update beside a release of other bytes: %v", err)
	}
}

func TestTheBetterSetsArchiveOutlivesTheInstalledSetsStart(t *testing.T) {
	s := realShape(t)
	large := s.sets[0].fixture
	update := shapeFor(t, "real-update", func(*testing.T) (shape, map[string]any) {
		u := s
		u.version = "1.0.1"
		return u, map[string]any{"aiios_min_version": packagefmt.VariantPreferenceMinHost, "default_variant": "large", "variant_preference": []string{"large", "compact"}}
	})
	host := newSwapHost(t, s)
	host.installCompact(t)
	cached, _ := host.store.Archive(large.decl.SHA256)
	if err := os.WriteFile(cached, large.archive, 0o400); err != nil {
		t.Fatal(err)
	}
	before := storeEntries(t, host.store)
	start := func(shp shape, serving *ServingSet) *Staged {
		t.Helper()
		f := &fetchRecord{s: shp}
		acq, ready := attachedRecorder(t, f, host.store)
		staged := acquiredBy(t, shp, host.opts(acq, serving), ready)
		// The child itself cannot run here; what the start does to the
		// store before it launches is what is asked of it.
		if ap, err := staged.Start(t.Context(), tools.NewRegistry(t.TempDir(), nil, tools.Timeouts{}), false); err == nil {
			_ = ap.Deactivate(context.Background())
		}
		if got := f.urls(); len(got) != 0 {
			t.Fatalf("starting the installed set fetched %v", got)
		}
		return staged
	}
	// The update, while others hold the device: its compact set starts
	// beside the release it replaces, and the large set waits.
	host.setRoom(gpuHost(2 * gib))
	old := &ServingSet{PackageHash: "sha256:the-release-before", ManifestHash: "sha256:the-release-before", Variant: "compact"}
	if got := start(update, old).Selection(); got.VariantID != "compact" {
		t.Fatalf("the update chose %s", got.VariantID)
	}
	// The next boot, the device free: the compact set serves for the large
	// one.
	host.setRoom(gpuHost(11 * gib))
	staged := start(update, nil)
	if sel := staged.Selection(); sel.VariantID != "compact" || sel.Better != "large" {
		t.Fatalf("the boot chose %s for %q", sel.VariantID, sel.Better)
	}
	if after := storeEntries(t, host.store); !slices.Equal(after, before) {
		t.Fatalf("starts took material out of the store:\nbefore %v\nafter  %v", before, after)
	}
	f := &fetchRecord{s: update}
	acq, ready := attachedRecorder(t, f, host.store)
	if got := acquiredBy(t, update, host.opts(acq, servingOf(staged)), ready).Selection(); got.VariantID != "large" {
		t.Fatalf("the candidate chose %s", got.VariantID)
	}
	if got := f.urls(); !slices.Equal(got, []string{"https://cdn.example.org/large-model"}) {
		t.Fatalf("the candidate fetched %v; the runtime archive was in the store", got)
	}
}

// storeEntries lists what the store holds, by kind and name.
func storeEntries(t *testing.T, w *plugstore.Writer) []string {
	t.Helper()
	var out []string
	for _, d := range []string{plugstore.DirArchives, plugstore.DirBlobs, plugstore.DirRoots, plugstore.DirViews} {
		names, err := os.ReadDir(filepath.Join(w.Dir, d))
		if err != nil {
			t.Fatal(err)
		}
		for _, n := range names {
			out = append(out, d+"/"+n.Name())
		}
	}
	return out
}

// THE OVERLAP'S PEAK IS ADMISSION'S, NOT A REASON TO GIVE THE BETTER SET UP.
// The start re-checks what selection chose against the machine now; beside
// the release it replaces, what that release holds is credited, as
// selection credited it.
func TestTheOverlapsPeakDoesNotRefuseTheBetterSetsStart(t *testing.T) {
	s := realShape(t)
	host := newSwapHost(t, s)
	host.installCompact(t)
	f := &fetchRecord{s: s}
	acq, ready := attachedRecorder(t, f, host.store)
	// What serves holds 6 GiB of the 12 GiB device; 5 GiB is free.
	held := int64(6 * gib)
	serving := &ServingSet{PackageHash: "sha256:the-release-before", ManifestHash: "sha256:the-release-before", Variant: "large",
		Profile: &AcceleratorProfile{MemoryBytes: 4 * gib, DeviceMemoryBytes: &held, RequiredAccelerators: []string{"cuda"}}}
	host.setRoom(gpuHost(5 * gib))
	staged := acquiredBy(t, s, host.opts(acq, serving), ready)
	if got := staged.Selection().VariantID; got != "large" {
		t.Fatalf("selection beside the release it replaces chose %s", got)
	}
	_, err := staged.Start(t.Context(), tools.NewRegistry(t.TempDir(), nil, tools.Timeouts{}), false)
	var refused *VariantSelectionError
	if errors.As(err, &refused) {
		t.Fatalf("the start refused the set selection chose, for what the release it replaces holds: %v", err)
	}
}

// ONE WRITER ACROSS THE SWITCH. The release serving finishes its operation in
// flight before its successor takes any: the names do not move under it, a
// call that arrives meanwhile waits and is then refused with nothing run, and
// a predecessor still busy at the bound keeps serving while the switch is
// refused.
func TestTheSwitchWaitsForThePredecessorsOperation(t *testing.T) {
	reg := newRegistry(t)
	old, err := Activate(context.Background(), buildPkg(t, "org.example.handover", "0.1.0", []string{"ping"}, "responder.wasm"), reg, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = old.Deactivate(context.Background()) })
	shadow := func() *ActivePlugin {
		next, err := ActivateShadow(context.Background(), buildPkg(t, "org.example.handover", "0.2.0", []string{"ping"}, "responder.wasm"), reg, nil)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = next.Deactivate(context.Background()) })
		return next
	}
	const name = "pl_org_example_handover_ping"
	owner := func() any {
		got, ok := reg.Get(name)
		if !ok {
			return nil
		}
		return got.(interface{ DispatchOwner() any }).DispatchOwner()
	}
	runtimeWith := func(bound time.Duration) *Runtime {
		return NewRuntime(RuntimeWires{HandOver: bound})
	}

	// Busy, then done within the bound: the switch waits for it.
	next := shadow()
	if err := old.inFlight.acquire(t.Context()); err != nil {
		t.Fatal(err)
	}
	switched := make(chan error, 1)
	go func() {
		switched <- runtimeWith(10*time.Second).Redirect(&Activation{ID: old.ID, Plugin: old}, &Activation{ID: next.ID, Plugin: next})
	}()
	arrived := make(chan tools.Result, 1)
	go func() { r, _ := reg.Execute(t.Context(), name, map[string]any{}); arrived <- r }()
	time.Sleep(200 * time.Millisecond)
	select {
	case err := <-switched:
		old.inFlight.release()
		t.Fatalf("the successor took the names while the predecessor's operation was in flight (%v)", err)
	default:
	}
	if owner() != old || next.admitted.Load() {
		old.inFlight.release()
		t.Fatalf("the names moved under the predecessor's operation: %v", owner())
	}
	old.inFlight.release() // the operation in flight finishes
	select {
	case err := <-switched:
		if err != nil {
			t.Fatalf("the switch once the predecessor was idle: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the switch never followed the predecessor's operation")
	}
	if owner() != next || !old.superseded.Load() {
		t.Fatalf("after the switch the name is %v's", owner())
	}
	select {
	case r := <-arrived:
		// It reached the predecessor's slot first or the successor's route
		// after the switch; either way it never ran beside the other.
		if r.Error != "" && r.ReasonCode != tools.ReasonDispatchTargetChanged {
			t.Fatalf("the call that arrived during the switch: %+v", r)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the call that arrived during the switch never answered")
	}

	// Busy past the bound: nothing moves, and the switch is refused.
	last := shadow()
	if err := next.inFlight.acquire(t.Context()); err != nil {
		t.Fatal(err)
	}
	err = runtimeWith(50*time.Millisecond).Redirect(&Activation{ID: next.ID, Plugin: next}, &Activation{ID: last.ID, Plugin: last})
	next.inFlight.release()
	if !errors.Is(err, ErrHandOverBusy) || owner() != next || next.superseded.Load() || last.admitted.Load() {
		t.Fatalf("a predecessor busy past the bound: %v; the name is %v's", err, owner())
	}
}
