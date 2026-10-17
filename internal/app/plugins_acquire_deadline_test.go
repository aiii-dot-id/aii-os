//go:build (linux && !android) || (darwin && !ios) || windows

package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
	"github.com/aiii-dot-id/aii-os/internal/packagefmt/packagetest"
	"github.com/aiii-dot-id/aii-os/internal/pluginfacility"
	"github.com/aiii-dot-id/aii-os/internal/pluginhost"
	"github.com/aiii-dot-id/aii-os/internal/tools"
)

// The voice platform's probe, kept as the host answers it: the model
// fetch used to see the sweep's 30-second activation deadline on both
// the first-activation and the shadow-update paths, whatever the
// readiness allowance said. Now neither path fetches at all — the want
// is handed to the Acquirer, whose job runs under the host's lifetime
// with no deadline — and a shadow update whose material is still
// arriving leaves the prior release serving. No download, engine launch
// or identity is involved: the injected fetcher records its context and
// stops.
// acquisitionProbePackage signs a native T3 package for this host that
// declares one model, never fetched; its child is never executed.
func acquisitionProbePackage(t *testing.T) (packagefmt.TrustRoots, string, string) {
	t.Helper()
	root, status, err := packagetest.NewPlatformRelease("aiii_test_platform_release")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, packagetest.StatusFilePlatform), status, 0o600); err != nil {
		t.Fatal(err)
	}
	roots := packagefmt.TrustRoots{PlatformRelease: root.Env}
	roots.Revocation = packagefmt.LoadRevocationStatus(dir, roots, nil)
	const id = "com.example.acquisition-deadline"
	vid := packagefmt.HostPlatform() + "-" + packagefmt.HostArch() + "-native"
	sum := sha256.Sum256([]byte("model"))
	models, err := json.Marshal([]pluginhost.ModelDecl{{Name: "weights", URL: "https://example.invalid/weights", Size: 5, SHA256: hex.EncodeToString(sum[:])}})
	if err != nil {
		t.Fatal(err)
	}
	files := map[string][]byte{
		"interfaces/quarantine.probe.v1.schema.json": []byte(`{"interface":"quarantine.probe","v":1}`),
		"variants/" + vid + "/child":                 []byte("not executed"),
		pluginhost.ModelsFile:                        models,
	}
	manifest := packagetest.BuildManifestJSON(id, "0.1.0",
		[]packagetest.InterfaceSpec{{ID: "quarantine.probe", Version: 1, SchemaFile: "interfaces/quarantine.probe.v1.schema.json", Methods: []string{"ping"}}},
		[]packagetest.VariantSpec{{ID: vid, Platform: packagefmt.HostPlatform(), Arch: packagefmt.HostArch(), Topology: packagefmt.HostTopology(), Runtime: "native_t3_component", Profile: "platform_reserved", Entrypoint: "variants/" + vid + "/child"}}, files, nil)
	spec := packagetest.PackageSpec{Root: id + "-0.1.0", Manifest: manifest, InstallFiles: files}
	if err := root.SignT3(&spec); err != nil {
		t.Fatal(err)
	}
	pkg := filepath.Join(dir, "probe.aiiospkg")
	if err := os.WriteFile(pkg, packagetest.Build(spec), 0o600); err != nil {
		t.Fatal(err)
	}
	return roots, dir, pkg
}

// ACQUISITION RUNS UNDER THE HOST'S LIFETIME, NOT AN ATTEMPT'S
// DEADLINE. Its length is the declared bytes, not the child's
// liveness, and a deadline that cut it short would refuse a complete
// install on a slow disk every time — while the host stopping still
// interrupts it.
func TestAcquisitionRunsUnderTheHostsLifetimeNotTheActivationDeadline(t *testing.T) {
	const id = "com.example.acquisition-deadline"
	roots, dir, pkg := acquisitionProbePackage(t)
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	type seen struct {
		bounded   bool
		remaining time.Duration
	}
	reached := make(chan seen, 1)
	lifetime, cancel := context.WithCancel(context.Background())
	defer cancel()
	store := newTestStore(t)
	acq := store.acquirer(func(ctx context.Context, _ string, _ int64, _ io.Writer) (int64, error) {
		var s seen
		if deadline, ok := ctx.Deadline(); ok {
			s.bounded, s.remaining = true, time.Until(deadline)
		}
		select {
		case reached <- s:
		default:
		}
		return 0, errors.New("stopped by the probe")
	}, pluginhost.AcquirerConfig{
		After: func(time.Duration) <-chan time.Time { return nil },
		Logf:  func(string, ...any) {},
	})
	acq.Attach(lifetime)
	a := &App{pluginToolReg: tools.NewRegistry(t.TempDir(), nil, tools.Timeouts{}),
		pluginOpts: &pluginhost.Options{Roots: roots, WorkerBinary: exe,
			ReadyTimeout: map[string]time.Duration{id: 180 * time.Second},
			StoreDir:     store.w.Dir, Acquirer: acq}}
	f := a.pluginFacility()
	f.Attach(lifetime)
	t.Cleanup(f.Close)
	ev, err := newPluginRuntime(a).Verify(lifetime, pkg)
	if err != nil {
		t.Fatal(err)
	}
	f.Observe([]pluginfacility.Observed{{ID: ev.ID, Dir: dir, Package: pkg, Hash: ev.PackageHash, ManifestHash: ev.ManifestHash}},
		pluginfacility.Policy{Revision: 1})

	select {
	case s := <-reached:
		if s.bounded {
			t.Fatalf("model acquisition inherited a deadline: remaining=%s", s.remaining)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the acquirer never reached the fetcher")
	}
	if _, ok := acq.Status(id); !ok {
		t.Fatal("no acquisition is in hand after the attempt")
	}
	a.pluginMu.Lock()
	activated := len(a.plugins)
	a.pluginMu.Unlock()
	if activated != 0 {
		t.Fatal("a plugin activated without its model")
	}
}

// The refusal is real at the application: a native package whose model
// is declared but absent, on a host with no way to fetch it, refuses —
// and the page names the reason instead of offering Install again.
func TestARefusedActivationIsNamedWithItsReason(t *testing.T) {
	const id = "com.example.acquisition-deadline"
	roots, dir, pkg := acquisitionProbePackage(t)
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	a := &App{pluginToolReg: tools.NewRegistry(t.TempDir(), nil, tools.Timeouts{}),
		pluginOpts: &pluginhost.Options{Roots: roots, WorkerBinary: exe, StoreDir: t.TempDir()}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	f := a.pluginFacility()
	f.Attach(ctx)
	t.Cleanup(f.Close)
	ev, err := newPluginRuntime(a).Verify(ctx, pkg)
	if err != nil {
		t.Fatal(err)
	}
	f.Observe([]pluginfacility.Observed{{ID: ev.ID, Dir: dir, Package: pkg, Hash: ev.PackageHash, ManifestHash: ev.ManifestHash}},
		pluginfacility.Policy{Revision: 1})

	deadline := time.Now().Add(15 * time.Second)
	for {
		a.applyFacilitySnapshot()
		views := a.pluginPendingViews()
		if len(views) == 1 && views[0].Phase == "refused" && strings.Contains(views[0].Summary, "declared models are missing") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the refusal is not named: %+v", views)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if marks := a.pendingSummaries(); marks[id].phase != "refused" {
		t.Fatalf("the catalog would offer Install again: %+v", marks)
	}
}
