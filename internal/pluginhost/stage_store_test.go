//go:build (linux && !android) || (darwin && !ios) || windows

package pluginhost

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/broker"
	"github.com/aiii-dot-id/aii-os/internal/pluginfacility"
	"github.com/aiii-dot-id/aii-os/internal/plugstore"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

// AND SO DOES MATERIAL. This is the ordering that actually moved: the
// models and the runtime tree a native engine declares were resolved
// AFTER the binding, so a plugin whose material was absent or still
// arriving was bound — wiping its temp-scoped working set — and then
// refused and closed again, on every sweep. A voice engine downloading
// gigabytes is in exactly that state for minutes at a time.
func TestMaterialIsSettledBeforeTheBrokerIsTouched(t *testing.T) {
	skipWhereNativeIsRefused(t)
	roots, role := platformRootsForTest(t)
	child, err := os.ReadFile(fakechildBin)
	if err != nil {
		t.Fatal(err)
	}
	const id = "org.example.rtbind"
	st := newBrokerStore(t)
	h, berr := broker.New(broker.Config{Store: st, Grants: map[string]broker.Grant{id: {KV: true}}})
	if berr != nil {
		t.Fatal(berr)
	}
	if err := st.PluginKVPut(store.PluginScope{PluginID: id}, "working-set", "what the running release holds", true, 64, 1<<20); err != nil {
		t.Fatal(err)
	}

	fx := newRuntimeFixture(t)
	pkg := nativePackage(t, id, child, role, runtimesJSON(t, fx))
	// This host keeps no runtime directory, so the declared material can
	// never be met here — the refusal the plugin would get on every pass
	// while its tree was absent.
	_, aerr := Activate(context.Background(), pkg, newRegistry(t), supervisedOpts(Options{Roots: roots, Broker: h}))
	var missing *RuntimeMissingError
	if !errors.As(aerr, &missing) {
		t.Fatalf("an unmeetable runtime declaration must refuse typed, got %v", aerr)
	}
	assertWorkingSetSurvived(t, st, id, "what the running release holds")
}

// ACQUISITION DOES NOT ONLY FETCH. It verifies what is on disk against
// what was declared, and it PUBLISHES the runtime tree the child runs
// from. A staged package that called itself present because its bytes
// were already downloaded would skip that, and the child would be
// started with no root to run from.
func TestAStagedPackageWithMaterialIsNeverAlreadyPresent(t *testing.T) {
	// A wasm package declares no material at all: nothing to acquire.
	plain := writePkg(t, pkgSpec("org.example.nomaterial", "0.1.0", []string{"ping"}, fixtureWasm(t, "caller.wasm")))
	st, err := Stage(context.Background(), plain, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !st.Present() {
		t.Fatal("a lane that declares no material has nothing to acquire")
	}
	if st.Material().PluginID != "" {
		t.Fatalf("and declares none: %+v", st.Material())
	}

	// A native engine with a runtime declaration does, and must be
	// acquired however much of it is already on disk.
	skipWhereNativeIsRefused(t)
	roots, role := platformRootsForTest(t)
	child, rerr := os.ReadFile(fakechildBin)
	if rerr != nil {
		t.Fatal(rerr)
	}
	fx := newRuntimeFixture(t)
	pkg := nativePackage(t, "org.example.hasmaterial", child, role, runtimesJSON(t, fx))
	res, verr := Verify(pkg, supervisedOpts(Options{Roots: roots}))
	if verr != nil {
		t.Fatal(verr)
	}
	prepared, perr := StagePrepared(pkg, res, supervisedOpts(Options{Roots: roots, StoreDir: t.TempDir()}))
	if perr != nil {
		t.Fatalf("preparing a native package: %v", perr)
	}
	if prepared.Material().PluginID == "" {
		t.Fatalf("precondition: this package must declare material")
	}
	if prepared.Present() {
		t.Fatal("a lane with declared material must be acquired: acquisition is what publishes its runtime root")
	}
}

// A START THAT CANNOT SUCCEED MUST NOT COST THE PLUGIN ITS WORKING SET.
// The stages are separately callable so a caller can sequence them, and
// a caller that skipped acquisition used to get as far as BINDING the
// plugin — which clears its temp-scoped rows and publishes it to the
// capability plane — before failing to open a child from an empty
// runtime root. The work was destroyed on the way to a certain failure.
func TestStartingUnacquiredMaterialRefusesBeforeAnythingIsBound(t *testing.T) {
	skipWhereNativeIsRefused(t)
	roots, role := platformRootsForTest(t)
	child, err := os.ReadFile(fakechildBin)
	if err != nil {
		t.Fatal(err)
	}
	const id = "org.example.unacquired"
	st := newBrokerStore(t)
	h, berr := broker.New(broker.Config{Store: st, Grants: map[string]broker.Grant{id: {KV: true}}})
	if berr != nil {
		t.Fatal(berr)
	}
	if err := st.PluginKVPut(store.PluginScope{PluginID: id}, "working-set", "what the running release holds", true, 64, 1<<20); err != nil {
		t.Fatal(err)
	}

	fx := newRuntimeFixture(t)
	pkg := nativePackage(t, id, child, role, runtimesJSON(t, fx))
	opts := supervisedOpts(Options{Roots: roots, Broker: h, StoreDir: t.TempDir()})
	res, verr := Verify(pkg, opts)
	if verr != nil {
		t.Fatal(verr)
	}
	staged, perr := StagePrepared(pkg, res, opts)
	if perr != nil {
		t.Fatalf("preparing: %v", perr)
	}
	if staged.Present() {
		t.Fatal("precondition: this package declares material")
	}

	// Start WITHOUT Acquire.
	var missing *NotAcquiredError
	if _, serr := staged.Start(context.Background(), newRegistry(t), true); !errors.As(serr, &missing) {
		t.Fatalf("a start with unacquired material must refuse typed, got %v", serr)
	}
	assertWorkingSetSurvived(t, st, id, "what the running release holds")
}

// Selection and preparation read in one walk: the accelerator profiles,
// the models and runtime declarations, and the entrypoints of both
// component sets this host could run, since which one is chosen is known
// only once the declarations are read.
func TestSelectionAndPreparationWalkThePackageOnce(t *testing.T) {
	skipWhereNativeIsRefused(t)
	c := componentSetsFor(t, "1.0.0")
	room := pluginfacility.Availability{HostKnown: true, HostTotal: 32 << 30, HostAvailable: 16 << 30,
		Devices: map[string]pluginfacility.DeviceAvailability{"cuda": {Known: true, Total: 8 << 30, Available: 8 << 30}}}
	opts := &Options{Roots: c.roots, WorkerBinary: "unused-before-start", StoreDir: t.TempDir(),
		ResourceFacts: func(context.Context) (pluginfacility.Availability, error) { return room, nil }}
	res, err := Verify(c.pkg, opts)
	if err != nil {
		t.Fatal(err)
	}
	walks := countWalks(t)
	staged, err := StagePreparedContext(t.Context(), c.pkg, res, opts)
	if err != nil {
		t.Fatal(err)
	}
	if n := walks.Load(); n != 1 {
		t.Fatalf("selection and preparation walk the package once, not %d times", n)
	}
	mat := staged.Material()
	if staged.Selection().VariantID != "full" || mat.Runtime == nil || mat.Runtime.VariantID != "full" || len(mat.Models) != 1 || mat.Models[0].Name != "full-model" {
		t.Fatalf("what the walk read chose and prepared the full set: %s, %+v", staged.Selection().VariantID, mat)
	}
}

// NOTHING IS HELD BETWEEN ACQUIRE AND START, AND START RUNS ONLY FROM WHAT
// WAS VERIFIED. Acquire verifies and holds no pin (a candidate the facility
// never starts holds nothing of the store); Start pins the very records
// Acquire verified, and refuses, typed, when one was replaced meanwhile.
func TestStartPinsWhatAcquireVerifiedOrRefuses(t *testing.T) {
	skipWhereNativeIsRefused(t)
	roots, role := platformRootsForTest(t)
	child, err := os.ReadFile(fakechildBin)
	if err != nil {
		t.Fatal(err)
	}
	fx := newRuntimeFixture(t)
	pkg := nativePackage(t, "org.example.repinned", child, role, runtimesJSON(t, fx))
	opts := supervisedOpts(Options{Roots: roots})
	w := useStore(t, opts, fx.fetch)
	staged, err := Stage(context.Background(), pkg, opts)
	if err != nil {
		t.Fatal(err)
	}
	if held := plugstore.HeldForTest(w.Dir); len(held) != 0 {
		t.Fatalf("an acquired, unstarted candidate holds the store: %v", held)
	}
	record := staged.runtimeRoot + plugstore.RecordSuffix
	raw, err := os.ReadFile(record)
	if err != nil {
		t.Fatal(err)
	}
	// The writer rebuilt the root between the two: a record that is
	// another file, with the same bytes.
	if err := os.Remove(record); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(record, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, serr := staged.Start(context.Background(), newRegistry(t), true); !errors.Is(serr, ErrMaterialReplaced) {
		t.Fatalf("a start after its root's record was replaced: want ErrMaterialReplaced, got %v", serr)
	}
	if held := plugstore.HeldForTest(w.Dir); len(held) != 0 {
		t.Fatalf("a refused start holds the store: %v", held)
	}
}

// WHAT A NEED CARRIES IS ABSOLUTE, HOWEVER THE IDENTITY NAMES IT. The sweep
// names a package by its path under the slot (plugins/<id>/<pkg>), and a
// configuration may name the data directory relative to the slot too; the
// machine service, another process with another working directory, refuses
// a root's carrier not named by an absolute path and passes over adoption
// candidates that are not. Prepare names them absolute.
func TestANeedNamesItsPackageAndItsOwnCopiesAbsolute(t *testing.T) {
	skipWhereNativeIsRefused(t)
	roots, role := platformRootsForTest(t)
	child, err := os.ReadFile(fakechildBin)
	if err != nil {
		t.Fatal(err)
	}
	fx := newRuntimeFixture(t)
	pkg := nativePackage(t, "org.example.relative", child, role, runtimesJSON(t, fx))
	slot := filepath.Dir(pkg)
	t.Chdir(slot)
	opts := supervisedOpts(Options{Roots: roots, StoreDir: filepath.Join(t.TempDir(), "plugins"),
		AdoptModelsDir: filepath.Join("data", "plugins-models"), AdoptRuntimeDir: filepath.Join("data", "plugins-runtime")})
	rel := filepath.Base(pkg)
	res, err := Verify(rel, opts)
	if err != nil {
		t.Fatal(err)
	}
	staged, err := StagePrepared(rel, res, opts)
	if err != nil {
		t.Fatal(err)
	}
	m := staged.material
	for _, w := range m.Works() {
		if _, err := w.Check(); err != nil {
			t.Fatalf("the service would refuse %s: %v", w.Entry, err)
		}
		if w.Root != nil && w.Root.Package != filepath.Join(slot, rel) {
			t.Fatalf("the root's carrier is named %q, want %q", w.Root.Package, filepath.Join(slot, rel))
		}
	}
	a := m.Adoption()
	for _, d := range append(append([]string(nil), a.ModelDirs...), a.ArchiveDirs...) {
		if !filepath.IsAbs(d) || !strings.HasPrefix(d, slot) {
			t.Fatalf("an adoption candidate %q is not absolute under the slot %s", d, slot)
		}
	}
}
