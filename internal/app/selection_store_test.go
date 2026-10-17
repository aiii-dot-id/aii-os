//go:build (linux && !android) || (darwin && !ios) || windows

package app

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/pluginfacility"
	"github.com/aiii-dot-id/aii-os/internal/pluginhost"
)

// THROUGH THE RUNTIME THE FACILITY DRIVES: each attempt is Prepare and then
// Acquire, and the second attempt used to choose afresh from the free memory
// of its own instant — the small set, replacing the full set's download.
func TestTheRuntimeKeepsAComponentSetAcrossAttempts(t *testing.T) {
	roots, pkg := componentSetsPackage(t, "1.0.0", fullSet, smallSet)
	var mu sync.Mutex
	room := roomy()
	facts := func() pluginfacility.Availability { mu.Lock(); defer mu.Unlock(); return room }
	began, cancelled := make(chan string, 8), make(chan string, 8)
	lifetime, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	store := newTestStore(t)
	acq := store.acquirer(func(ctx context.Context, url string, _ int64, _ io.Writer) (int64, error) {
		began <- url
		<-ctx.Done() // a download that takes longer than the test
		cancelled <- url
		return 0, ctx.Err()
	}, pluginhost.AcquirerConfig{
		After: func(time.Duration) <-chan time.Time { return nil }, Logf: func(string, ...any) {},
		Spawn: func(fn func()) bool { wg.Add(1); go func() { defer wg.Done(); fn() }(); return true },
	})
	acq.Attach(lifetime)
	a := selectionApp(t, roots, facts, acq)
	a.pluginOpts.StoreDir = store.w.Dir
	t.Cleanup(func() { cancel(); wg.Wait() })
	h := newPluginRuntime(a)
	ev, err := h.Verify(t.Context(), pkg)
	if err != nil {
		t.Fatal(err)
	}
	attempt := func() int64 {
		t.Helper()
		p, err := h.Prepare(t.Context(), ev)
		if err != nil {
			t.Fatal(err)
		}
		// The attempt hands its material over and is then withdrawn: the
		// download is the Acquirer's, under the host's lifetime.
		actx, acancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
		defer acancel()
		if err := h.Acquire(actx, p, nil); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("the material is not in flight: %v", err)
		}
		return p.HostBytes
	}
	if got := attempt(); got != fullSet.host {
		t.Fatalf("the first attempt reserved %d, not the full set", got)
	}
	select {
	case <-began:
	case <-time.After(10 * time.Second):
		t.Fatal("the full set's download never began")
	}
	mu.Lock()
	room.HostAvailable = 7 << 30 // the dip, mid-download
	mu.Unlock()
	if got := attempt(); got != fullSet.host {
		t.Fatalf("the second attempt reserved %d: the set in flight was swapped", got)
	}
	select {
	case url := <-cancelled:
		t.Fatalf("the download of %s was cancelled by a re-selection", url)
	default:
	}
}

// THE CARD READS THE CHOICE AS DATA. Through the facility the application
// runs: an attempt still downloading the set it chose shows which set, and
// why the other was passed over, on its row.
func TestThePendingRowCarriesTheChosenSetAndWhy(t *testing.T) {
	roots, pkg := componentSetsPackage(t, "1.0.0", fullSet, smallSet)
	lifetime, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	store := newTestStore(t)
	acq := store.acquirer(func(ctx context.Context, _ string, _ int64, _ io.Writer) (int64, error) {
		<-ctx.Done()
		return 0, ctx.Err()
	}, pluginhost.AcquirerConfig{
		After: func(time.Duration) <-chan time.Time { return nil }, Logf: func(string, ...any) {},
		Spawn: func(fn func()) bool { wg.Add(1); go func() { defer wg.Done(); fn() }(); return true },
	})
	acq.Attach(lifetime)
	noGPU := func() pluginfacility.Availability {
		return pluginfacility.Availability{HostKnown: true, HostTotal: 32 << 30, HostAvailable: 16 << 30}
	}
	a := selectionApp(t, roots, noGPU, acq)
	a.pluginOpts.StoreDir = store.w.Dir
	f := a.pluginFacility()
	f.Attach(lifetime)
	t.Cleanup(func() { f.Close(); cancel(); wg.Wait() })
	ev, err := newPluginRuntime(a).Verify(lifetime, pkg)
	if err != nil {
		t.Fatal(err)
	}
	f.Observe([]pluginfacility.Observed{{ID: ev.ID, Dir: filepath.Dir(pkg), Package: pkg, Hash: ev.PackageHash, ManifestHash: ev.ManifestHash}},
		pluginfacility.Policy{Revision: 1})
	deadline := time.Now().Add(10 * time.Second)
	for {
		for _, row := range a.pluginPendingViews() {
			if row.ID != componentSetsID || row.Selection == nil {
				continue
			}
			s := row.Selection
			if s.Variant != "small" || s.Kept || len(s.Excluded) != 1 || s.Excluded[0].Variant != "full" ||
				!strings.Contains(strings.Join(s.Excluded[0].Reasons, "; "), "accelerator:cuda availability is unknown") {
				t.Fatalf("the row says %+v", s)
			}
			raw, err := json.Marshal(row)
			if err != nil || !strings.Contains(string(raw), `"selection":{"variant":"small","excluded":[{"variant":"full","reasons":[`) {
				t.Fatalf("the wire carries %s (%v)", raw, err)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("no pending row carries the selection: %+v", a.pluginPendingViews())
		}
		time.Sleep(10 * time.Millisecond)
	}
}
