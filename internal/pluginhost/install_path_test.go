//go:build (linux && !android) || (darwin && !ios) || windows

package pluginhost

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/logsink"
	"github.com/aiii-dot-id/aii-os/internal/pluginfacility"
)

// G2: THE RUNTIME ARCHIVE IS COUNTED AND SHOWN LIKE A MODEL FILE, and its
// failures reach the page: a runtime-only package's download is material
// the facility sees arriving (its state is acquiring, not starting), its
// partial bytes are progress, and a download the rule ends says why.
func TestTheRuntimeArchiveIsCountedLikeAModelFile(t *testing.T) {
	fx := newRuntimeFixture(t)
	s := &scripted{steps: []func(int64, io.Writer) (int64, error){serve(fx.archive, 300), answer(&FetchStatusError{Status: 404})}}
	logf, _ := sinkLogf(t)
	store := openStore(t)
	q := storeAcquirer(store, s.fetch, AcquirerConfig{After: neverAfter, Logf: logf})
	q.Attach(t.Context())
	m := runtimeMaterial(t, store, "org.example.engine", "1", fx)
	q.Want(m)
	waitFor(t, "the first attempt", func() bool { st, _ := q.Status(m.PluginID); return st.Phase == PhaseWaiting })
	st, _ := q.Status(m.PluginID)
	if st.FilesTotal != 1 || st.FilesPresent != 0 || st.BytesTotal != fx.decl.Size || st.BytesPresent != 300 || st.RuntimeArrived != 300 {
		t.Fatalf("the archive is not counted as a file with its partial bytes: %+v", st)
	}
	if sum := st.Summary(); !strings.Contains(sum, "runtime archive 300 B of") {
		t.Fatalf("summary: %s", sum)
	}
	if !strings.Contains(st.LastError, "connection reset") {
		t.Fatalf("the archive's failure is not on the card: %+v", st)
	}
	// What the facility is told: material, one file, still arriving — the
	// state that shows the attempt, its cause and its retry, not "starting".
	rt := NewRuntime(RuntimeWires{Acquirer: func() *Acquirer { return q }})
	ms, ok := rt.acquireStatus(m.PluginID)
	if !ok || ms.FilesTotal != 1 || ms.FilesPresent != 0 || ms.BytesPresent != 300 {
		t.Fatalf("a runtime-only download is not material to the facility: %+v %v", ms, ok)
	}
	inst := &pluginfacility.Instance{Activations: []*pluginfacility.Activation{{Role: pluginfacility.RoleStarting, Material: ms}}}
	if got := inst.State(); got != pluginfacility.StateAcquiring {
		t.Fatalf("a runtime downloading reads as %s", got)
	}
	// Its end reaches the page with its remedy.
	q.Retry(m.PluginID)
	waitFor(t, "the archive's download to stop", func() bool { st, _ := q.Status(m.PluginID); return st.Remedy != "" })
	if st, _ := q.Status(m.PluginID); !strings.Contains(st.LastError, "runtime archive") || !strings.Contains(st.Remedy, "Nothing is at the address") {
		t.Fatalf("the archive's end is not said: %+v", st)
	}
}

// G3 IN A SLOT, UNTIL L5: an identity's own material directories from
// before the plugin store keep only what an installed package names — an
// uninstalled plugin's runtime material goes, another's stays — since they
// are only where the store's writer adopts from now. The store's own
// retention is the machine service's (references, its collector).
func TestASlotsOwnMaterialOfAnUninstalledPluginIsRetired(t *testing.T) {
	base := t.TempDir()
	place := func(id string) string {
		t.Helper()
		dir := pluginStorageDir(base, id)
		if err := os.MkdirAll(filepath.Join(dir, archivesDir), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, archivesDir, "x"+archiveSuffix), []byte("archive"), 0o600); err != nil {
			t.Fatal(err)
		}
		return dir
	}
	gone, kept := place("org.example.gone"), place("org.example.kept")
	removed, err := RetireUninstalledSlotMaterial(base, []string{"org.example.kept"})
	if err != nil || len(removed) != 1 || removed[0] != gone {
		t.Fatalf("retired %v, %v", removed, err)
	}
	if _, err := os.Stat(gone); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the uninstalled plugin's material survived: %v", err)
	}
	if _, err := os.Stat(filepath.Join(kept, archivesDir, "x"+archiveSuffix)); err != nil {
		t.Fatalf("an installed package's material was retired: %v", err)
	}
	if removed, err := RetireUninstalledSlotMaterial(filepath.Join(base, "absent"), nil); err != nil || len(removed) != 0 {
		t.Fatalf("an absent directory: %v, %v", removed, err)
	}
}

// G5: THE READY LINE'S DEVICE DOMAINS AGAINST THE SET'S DECLARATION, through
// the one reader of those words (pluginfacility.DeviceDomains): a difference
// is said, and decides nothing.
func TestTheReadyLineIsComparedWithItsSetByDeviceDomain(t *testing.T) {
	differs := Readiness{Accelerator: "cpu_vulkan"}.Against("cuda+vulkan")
	if !strings.Contains(differs, "cpu_vulkan (on vulkan)") || !strings.Contains(differs, "cuda+vulkan (on cuda and vulkan)") || !strings.Contains(differs, "not acted on") {
		t.Fatalf("{vulkan} reported against {cuda, vulkan} declared: %q", differs)
	}
	for _, same := range [][2]string{{"cpu_vulkan", "cpu+vulkan"}, {"vulkan", "vulkan"}, {"cuda+vulkan", "vulkan+cuda"}, {"cpu", "cpu"}} {
		if got := (Readiness{Accelerator: same[0]}).Against(same[1]); got != "" {
			t.Fatalf("%s against %s differs: %q", same[0], same[1], got)
		}
	}
	if got := (Readiness{Accelerator: "cpu"}).Against("metal"); !strings.Contains(got, "the processor alone") {
		t.Fatalf("a set computing on the processor alone: %q", got)
	}
}

// And at an activation: a child whose line names other domains than its set
// declares is admitted all the same, with the difference on its readiness
// and in the log.
func TestAReadyLineThatDiffersFromItsSetStillActivates(t *testing.T) {
	const id = "org.example.readiness-differs"
	rig := newNativeRigWith(t, id, "ready-real", 20000, nil, func(p *AcceleratorProfile) { p.Backend = "vulkan" })
	capture := logsink.CaptureForTest(t)
	ap, err := Activate(context.Background(), rig.pkg, newRegistry(t), rig.opts)
	if err != nil {
		t.Fatalf("a difference decided the activation: %v", err)
	}
	defer func() { _ = ap.Deactivate(context.Background()) }()
	if ap.Readiness == nil || !strings.Contains(ap.Readiness.Differs, "declares vulkan (on vulkan)") {
		t.Fatalf("the difference is not on the readiness: %+v", ap.Readiness)
	}
	if !capture.Contains("plugin " + id + ": the engine reports cpu") {
		t.Fatalf("the difference is not in the log:\n%s", capture.String())
	}
}

// (iv): WHAT A PACKAGE NEEDS, from its declared sets alone.
func TestThePackageSaysWhatItNeedsOfThisMachine(t *testing.T) {
	device := preferenceProfile(6*gib, gib+gib/4, "cuda")
	free := preferenceProfile(gib, 0)
	unified := preferenceProfile(gib, 0, "metal")
	for _, c := range []struct {
		name     string
		order    []string
		profiles map[string]AcceleratorProfile
		want     string
	}{
		{"a device set and a device-free one", []string{"gpu", "cpu-set"}, map[string]AcceleratorProfile{"gpu": device, "cpu-set": free},
			"needs a GPU (cuda) with 1.25 GB of its memory, or runs without one (cpu-set)"},
		{"a device set alone", []string{"gpu"}, map[string]AcceleratorProfile{"gpu": device}, "needs a GPU (cuda) with 1.25 GB of its memory"},
		{"a device that shares the host's memory", []string{"gpu"}, map[string]AcceleratorProfile{"gpu": unified}, "needs a GPU (metal)"},
		{"no set asks for a device", []string{"cpu-set"}, map[string]AcceleratorProfile{"cpu-set": free}, ""},
		{"no native set at all", nil, nil, ""},
	} {
		if got := needsWords(c.order, c.profiles); got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
	// Said by selection, before anything is fetched, for a signed package;
	// a module (no native set) says nothing.
	s := declaredShape(t)
	sel, err := judge(t, s, judgeOpts(s, gpuHost(11*gib), 500*gib))
	if err != nil {
		t.Fatal(err)
	}
	if sel.Needs != "needs a GPU (cuda) with 9.00 GB of its memory, or runs without one (compact)" {
		t.Fatalf("selection's needs line: %q", sel.Needs)
	}
	pkg := writePkg(t, pkgSpec("org.example.module", "1.0.0", []string{"ping"}, fixtureWasm(t, "caller.wasm")))
	res, err := Verify(pkg, nil)
	if err != nil {
		t.Fatal(err)
	}
	if sel, err := SelectContext(t.Context(), pkg, res, &Options{}); err != nil || sel.Needs != "" {
		t.Fatalf("a module's needs: %q, %v", sel.Needs, err)
	}
}
