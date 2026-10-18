//go:build (linux && !android) || (darwin && !ios)

package pluginhost

import (
	"context"
	"os"
	"runtime"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/firewall"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
)

// A CARRIER STARTS ITS WORKER FROM ITS RUNTIME ROOT UNDER THE WALL, through
// the whole activation: the package verified, the tree published, the
// child contained as the host contains every native plugin. A real engine
// is two programs, a carrier the host starts and a worker the carrier
// starts from the verified tree; on macOS the wall lets a contained child
// start nothing but its own programs, so the launch has to name the
// runtime root as the child's own or no engine starts at all. The carrier
// here also tries a program of the system's: on macOS that is refused, on
// Linux it runs inside the same namespaces and filter as the carrier.
func TestACarrierStartsItsWorkerFromItsRuntimeRootUnderTheWall(t *testing.T) {
	skipWhereNativeIsRefused(t)
	skipWhereTheSandboxCannotBeEstablished(t)
	roots, role := platformRootsForTest(t)
	child, err := os.ReadFile(fakechildBin)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	const id = "org.example.carrier"
	fx := newRuntimeFixtureOf(t, map[string][]byte{
		"bin/worker.exe":         child,
		"carrier.fakechild-mode": []byte("the carrier's mode, by its name\n"),
	})
	pkg := nativePackage(t, id, child, role, runtimesJSON(t, fx))
	capture := logsink.CaptureForTest(t)
	opts := supervisedOpts(Options{Roots: roots})
	store := useStore(t, opts, fx.fetch)
	opts.NativePlaces = func() ([]*firewall.Rule, error) { return []*firewall.Rule{nativePlace(store.Dir)}, nil }
	ap, err := Activate(ctx, pkg, newRegistry(t), opts)
	if err != nil {
		if !strings.Contains(err.Error(), "native lane unavailable") && !strings.Contains(err.Error(), "cannot contain") {
			t.Fatalf("activation: %v", err)
		}
		t.Skipf("the launch needs a host that can contain a native child: %v", err)
	}
	defer func() { _ = ap.Deactivate(ctx) }()
	want := "fakechild: carrier worker=ran system=" + map[string]string{"darwin": "refused", "linux": "ran"}[runtime.GOOS]
	if !waitUntil(func() bool { return strings.Contains(capture.String(), "fakechild: carrier ") }) {
		t.Fatalf("the carrier never reported; its log:\n%s", capture.String())
	}
	if !strings.Contains(capture.String(), want) {
		t.Fatalf("want %q; the carrier's log:\n%s", want, capture.String())
	}
}
