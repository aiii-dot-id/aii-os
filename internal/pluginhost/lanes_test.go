package pluginhost

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/aiii-dot-id/aii-os/internal/pluginworker"
	"github.com/aiii-dot-id/aii-os/internal/supervisor"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/packagefmt/packagetest"
	"github.com/aiii-dot-id/aii-os/internal/pluginfacility"
	"github.com/aiii-dot-id/aii-os/internal/sections"
)

// The lanes' own tests, moved with them from internal/app (T21): each
// stands a Runtime on the wires its lane reads and nothing else, so a wire
// the lane was not meant to reach is nil and fails loudly. What the
// application adds around Redirect and Stop — adoption into its view,
// release, event delivery — is proven in internal/app, through the
// runtime the facility drives there.

// sectionLanes is a Runtime with a section registry and no plugin options,
// as a host whose options are not built yet.
func sectionLanes(reg *sections.Registry) *Runtime {
	return NewRuntime(RuntimeWires{
		Options:  func() *Options { return nil },
		Sections: func() *sections.Registry { return reg },
	})
}

// sectionPkg builds a section asset package (internal/app's
// buildSectionVer, the same bytes).
func sectionPkg(t *testing.T, dir, id, sectionID, ver, title string) string {
	t.Helper()
	files := map[string][]byte{
		"section.json": []byte(`{"id":"` + sectionID + `","title":"` + title + `","slot":"panel","commands":[],"topics":["status"],"entry":"index.html"}`),
		"index.html":   []byte("<!DOCTYPE html><title>" + title + "</title>"),
	}
	manifest, err := json.Marshal(map[string]any{
		"kind": "asset", "id": id, "version": ver,
		"package_hash": packagetest.ReferencePackageHash(files), "asset_type": "static_template",
	})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, id+"-"+ver+".aiiospkg")
	if err := os.WriteFile(path, packagetest.Build(packagetest.PackageSpec{Root: id + "-" + ver, Manifest: manifest, InstallFiles: files}), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestASectionWithdrawnBeforeItsCommitIsNeverRegistered(t *testing.T) {
	dir := t.TempDir()
	pkg := sectionPkg(t, dir, "org.example.bound", "bound", "0.1.0", "Bound")
	t.Chdir(dir)
	ctx := context.Background()
	reg := sections.NewRegistry()
	h := sectionLanes(reg)
	stand := func(gen pluginfacility.Generation) (pluginfacility.Running, *pluginfacility.Lease) {
		t.Helper()
		ev, err := h.Verify(ctx, pkg)
		if err != nil {
			t.Fatal(err)
		}
		prep, err := h.Prepare(ctx, ev)
		if err != nil {
			t.Fatal(err)
		}
		lease := pluginfacility.NewLease(gen)
		run, err := h.Start(ctx, prep, lease)
		if err != nil {
			t.Fatal(err)
		}
		return run, lease
	}
	// Overtaken while it stood up: the commit is refused and nothing of
	// it can be read.
	run, lease := stand(1)
	lease.Withdraw()
	if err := h.Redirect(nil, run); !errors.Is(err, sections.ErrWithdrawn) {
		t.Fatalf("a withdrawn section was committed: %v", err)
	}
	if _, ok := reg.Get("bound"); ok {
		t.Fatal("a withdrawn section is registered")
	}
	if _, err := h.Stop(ctx, run); err != nil {
		t.Fatal(err)
	}
	// Still wanted: it commits and is read — and stops being read the
	// instant its owner takes it back, before any rollback has run.
	run, lease = stand(2)
	if err := h.Redirect(nil, run); err != nil {
		t.Fatal(err)
	}
	if sec, ok := reg.Get("bound"); !ok || sec.Decl.Title != "Bound" {
		t.Fatal("an authorized section was not registered")
	}
	lease.Withdraw()
	if _, ok := reg.Get("bound"); ok {
		t.Error("a section its owner took back is still served")
	}
	if _, err := h.Stop(ctx, run); err != nil {
		t.Fatal(err)
	}
}

func TestReviewFailedSectionRedirectKeepsPredecessor(t *testing.T) {
	old := &sections.Section{Decl: sections.Decl{ID: "old"}, Dev: true}
	other := &sections.Section{Decl: sections.Decl{ID: "occupied"}, Dev: true}
	next := &sections.Section{Decl: sections.Decl{ID: "occupied"}, Dev: true}
	reg := sections.NewRegistry()
	for _, sec := range []*sections.Section{old, other} {
		if err := reg.Register(sec); err != nil {
			t.Fatal(err)
		}
	}
	err := sectionLanes(reg).Redirect(&Activation{ID: "plugin", Section: old}, &Activation{ID: "plugin", Section: next})
	if err == nil {
		t.Fatal("fixture: conflicting successor unexpectedly accepted")
	}
	if got, ok := reg.Get("old"); !ok || got != old {
		t.Fatal("failed section redirect removed the still-serving predecessor")
	}
}

func TestReviewPredecessorStopPreservesSuccessorSection(t *testing.T) {
	const id = "id.example.section"
	reg := sections.NewRegistry()
	old := &Activation{ID: id, Package: "old.aiiospkg", Section: &sections.Section{Decl: sections.Decl{ID: id}}}
	next := &Activation{ID: id, Package: "new.aiiospkg", Section: &sections.Section{Decl: sections.Decl{ID: id}}}
	if err := reg.Register(old.Section); err != nil {
		t.Fatal(err)
	}
	h := sectionLanes(reg)
	if err := h.Redirect(old, next); err != nil {
		t.Fatal(err)
	}
	if got, ok := reg.Get(id); !ok || got != next.Section {
		t.Fatal("successor not published")
	}
	if _, err := h.Stop(context.Background(), old); err != nil {
		t.Fatal(err)
	}
	if got, ok := reg.Get(id); !ok || got != next.Section {
		t.Fatal("predecessor retirement removed successor section route")
	}
}

func TestLegacyPreparationDoesNotRequireResourceConfiguration(t *testing.T) {
	pkg := buildPkg(t, "org.example.legacy-resources", "0.1.0", []string{"ping"}, "responder.wasm")
	// Legacy packages never opted into resource selection. Their adapter can
	// prepare from verified package/options alone, without a live config
	// owner: no Policy or Serving wire is given, and a module with no native
	// set asks neither.
	h := NewRuntime(RuntimeWires{
		Options:  func() *Options { return &Options{} },
		Acquirer: func() *Acquirer { return nil },
	})
	ev, err := h.Verify(t.Context(), pkg)
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := h.Prepare(t.Context(), ev)
	if err != nil || prepared.HostBytes <= 0 {
		t.Fatalf("legacy preparation: %+v, %v", prepared, err)
	}
}

// A WASM MODULE WITH NO DECLARED ENVELOPE IS PREPARED WHEN THE BROKER WAS
// REFUSED. A refused broker or trust-root configuration leaves the
// app's plugin options nil and every plugin quarantined; Prepare already
// stands in an empty Options for that, but the envelope read went back to
// the wire and dereferenced the nil it had just guarded against. A T0
// module then panicked in Prepare instead of taking the worker's default
// ceiling.
func TestAWasmModulePreparesItsDefaultEnvelopeWhenThePluginOptionsAreRefused(t *testing.T) {
	pkg := buildPkg(t, "org.example.quarantined-wasm", "0.1.0", []string{"ping"}, "responder.wasm")
	h := NewRuntime(RuntimeWires{
		Options:  func() *Options { return nil },
		Acquirer: func() *Acquirer { return nil },
	})
	ev, err := h.Verify(t.Context(), pkg)
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := h.Prepare(t.Context(), ev)
	if err != nil {
		t.Fatal(err)
	}
	if prepared.HostBytes != int64(pluginworker.DefaultMemoryMaxBytes) {
		t.Fatalf("envelope %d, want the worker's default %d", prepared.HostBytes, pluginworker.DefaultMemoryMaxBytes)
	}
}

// The operator's ceiling for a module is its envelope when it declares none.
func TestAWasmModulesEnvelopeIsTheOperatorsCeiling(t *testing.T) {
	pkg := buildPkg(t, "org.example.ceiling-wasm", "0.1.0", []string{"ping"}, "responder.wasm")
	const ceiling = 96 << 20
	h := NewRuntime(RuntimeWires{
		Options: func() *Options {
			return &Options{MemoryMax: map[string]uint64{"org.example.ceiling-wasm": ceiling}}
		},
		Acquirer: func() *Acquirer { return nil },
	})
	ev, err := h.Verify(t.Context(), pkg)
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := h.Prepare(t.Context(), ev)
	if err != nil {
		t.Fatal(err)
	}
	if prepared.HostBytes != ceiling {
		t.Fatalf("envelope %d, want the operator's ceiling %d", prepared.HostBytes, ceiling)
	}
}

// A VOICE ENGINE WITH AN OPEN SESSION IS HANDED TO THE PINNED WATCH, ONCE.
// Its retirement is not established while the session is open (V-D), and
// what releases it later is the app's watch on the session's end
// (watchPinnedPredecessor), reached only through the Pinned wire in the
// middle of Stop. The facility asks again; the watch is armed once.
// Without this, dropping the wire's call passed every test there was (the
// T21 move's report).
func TestStoppingAVoiceEngineWithAnOpenSessionHandsItToThePinnedWatchOnce(t *testing.T) {
	frames := make(chan []byte)
	t.Cleanup(func() { close(frames) })
	v := NewVoiceSession(supervisor.NewSessionClientFrames(frames, io.Discard, nil, 32))
	v.sessionID = "vs-pinned" // open: the engine admitted it, nothing closed it
	ap := pluginWithTestVoice(&ActivePlugin{ID: "id.test.engine", Version: "1.0.0"}, v)
	act := &Activation{ID: ap.ID, Version: ap.Version, Plugin: ap}
	var pinned []*VoiceSession
	h := NewRuntime(RuntimeWires{Pinned: func(a *Activation, s *VoiceSession) {
		if a != act {
			t.Errorf("pinned watch handed %p, want the activation stopped %p", a, act)
		}
		pinned = append(pinned, s)
	}})
	for ask := 1; ask <= 2; ask++ {
		ret, err := h.Stop(t.Context(), act)
		if err != nil || ret.Established || len(ret.Residue) == 0 {
			t.Fatalf("stop %d of an engine holding an open session: %+v, %v — want not established, with the residue said", ask, ret, err)
		}
	}
	if len(pinned) != 1 || pinned[0] != v {
		t.Fatalf("the pinned watch was armed %d time(s), want once with the open session", len(pinned))
	}
}
