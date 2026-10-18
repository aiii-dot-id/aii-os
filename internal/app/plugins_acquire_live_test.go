//go:build (linux && !android) || (darwin && !ios) || windows

package app

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/dashboard"
	"github.com/aiii-dot-id/aii-os/internal/genesis"
	"github.com/aiii-dot-id/aii-os/internal/genesis/genesistest"
	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
	"github.com/aiii-dot-id/aii-os/internal/packagefmt/packagetest"
	"github.com/aiii-dot-id/aii-os/internal/pluginhost"
)

// nativePackageNeedingModels signs a native T3 package for this host
// that declares one model file; its child is never started, because the
// model never arrives.
func nativePackageNeedingModels(t *testing.T, dir, id string, root *packagetest.Role, models []byte) string {
	t.Helper()
	vid := packagefmt.HostPlatform() + "-" + packagefmt.HostArch() + "-native"
	files := map[string][]byte{
		"interfaces/quarantine.probe.v1.schema.json": []byte(`{"interface":"quarantine.probe","v":1}`),
		"variants/" + vid + "/child":                 []byte("#!/bin/sh\nexit 0\n"),
		pluginhost.ModelsFile:                        models,
	}
	manifest := packagetest.BuildManifestJSON(id, "0.1.0",
		[]packagetest.InterfaceSpec{{ID: "quarantine.probe", Version: 1, SchemaFile: "interfaces/quarantine.probe.v1.schema.json", Methods: []string{"ping"}}},
		[]packagetest.VariantSpec{{ID: vid, Platform: packagefmt.HostPlatform(), Arch: packagefmt.HostArch(), Topology: packagefmt.HostTopology(),
			Runtime: "native_t3_component", Profile: "platform_reserved", Entrypoint: "variants/" + vid + "/child"}},
		files, nil)
	spec := packagetest.PackageSpec{Root: id + "-0.1.0", Manifest: manifest, InstallFiles: files}
	if err := root.SignT3(&spec); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, id+"-0.1.0.aiiospkg")
	if err := os.WriteFile(path, packagetest.Build(spec), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// THE FIRST NATIVE CATALOG PLUGIN'S JOURNEY, at the application. A
// verified T3 package whose declared model is not on disk is installed.
// The boot pass hands the download to the Acquirer instead of fetching
// inside its own deadline, keeps the acquisition across the pass that
// would otherwise cancel it, and the plugins page names it as
// preparing with why the last attempt stopped. An acquisition for a
// package nobody installed is stopped by the next pass; stopping the
// identity does not wait on the one still in flight. Nothing here
// touches the network: the fetch is the seam's, and it answers 503.
func TestTheSweepKeepsTheWantedAcquisitionAndStopsTheRest(t *testing.T) {
	dir := t.TempDir()
	result := genesistest.NewRoot(t).Birth(t, genesis.BirthConfig{
		Name:       "AcquireTest",
		KeyPath:    filepath.Join(dir, "identity.sec"),
		LedgerPath: filepath.Join(dir, "ledger.jsonl"),
		DBPath:     filepath.Join(dir, "aii.db"),
	})
	result.Ledger.Close()

	root, status, err := packagetest.NewPlatformRelease("aiii_test_platform_release")
	if err != nil {
		t.Fatal(err)
	}
	rootRaw, _ := json.Marshal(root.Env)
	rootPath := filepath.Join(dir, "platform-root.json")
	if err := os.WriteFile(rootPath, rootRaw, 0o644); err != nil {
		t.Fatal(err)
	}
	trust := filepath.Join(dir, "trust")
	if err := os.MkdirAll(trust, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(trust, packagetest.StatusFilePlatform), status, 0o600); err != nil {
		t.Fatal(err)
	}
	const id = "org.example.needs-models"
	decl := pluginhost.ModelDecl{Name: "stt.bin", URL: "https://example.invalid/stt.bin", SHA256: strings.Repeat("a", 64), Size: 1000}
	models, err := json.Marshal([]pluginhost.ModelDecl{decl})
	if err != nil {
		t.Fatal(err)
	}
	installPluginDir(t, dir, id, nativePackageNeedingModels(t, dir, id, root, models))
	t.Chdir(dir)
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cfg := &Config{
		Identity: IdentityConfig{KeyPath: filepath.Join(dir, "identity.sec"),
			LedgerPath: filepath.Join(dir, "ledger.jsonl"), DBPath: filepath.Join(dir, "aii.db"),
		},
		LLM:        withTestProvider(t, dir, "test", "https://127.0.0.1:1", "m", "sk-x"),
		SourcePath: filepath.Join(dir, "config.json"),
		Dashboard:  DashboardConfig{Port: 0},
		Tools:      ToolsConfig{CWD: dir},
		Plugins:    PluginsConfig{Autoload: "T3", PlatformRoot: rootPath, WorkerBinary: exe},
		Agency:     defaultConfig().Agency,
	}
	store := newTestStore(t)
	store.name(cfg)
	app := New(cfg)
	var fetches atomic.Int32
	app.storeMaterialize = store.materialize(func(ctx context.Context, url string, offset int64, w io.Writer) (int64, error) {
		fetches.Add(1)
		return 0, errors.New("the model server answered 503")
	})
	// Its plugin stays acquiring by design (the model server answers
	// 503), so the boot does not wait for the facility to settle.
	if err := startLiveUnsettled(app); err != nil {
		t.Fatal(err)
	}
	defer stopApp(t, app)

	acq := app.acquirer()
	if acq == nil {
		t.Fatal("the application wired no acquirer")
	}
	// The boot pass: the package verifies, the activation hands the
	// model to the Acquirer, the attempt stops at the seam's 503, and
	// the pass KEEPS the job — it is wanted.
	voiceWait(t, "the acquisition to be in hand and waiting", 20*time.Second, func() bool {
		st, ok := acq.Status(id)
		return ok && st.Phase == pluginhost.PhaseWaiting
	})
	if fetches.Load() == 0 {
		t.Fatal("the acquirer never tried the download")
	}

	var views []dashboard.PluginPendingView
	named := func() bool {
		views = app.pluginPendingViews()
		return len(views) == 1 && views[0].ID == id && views[0].Phase == lifeRefused &&
			views[0].FilesTotal == 1 && views[0].FilesPresent == 0 && strings.Contains(views[0].LastError, "503")
	}
	deadline := time.Now().Add(20 * time.Second)
	for !named() {
		if time.Now().After(deadline) {
			t.Fatalf("the page does not name the acquisition: %+v", views)
		}
		time.Sleep(20 * time.Millisecond)
	}
	app.pluginMu.Lock()
	active := len(app.plugins)
	app.pluginMu.Unlock()
	if active != 0 {
		t.Fatal("a plugin activated without its model")
	}

	// A ghost acquisition nobody installed, then another pass (a junk
	// package changes the directory): the pass stops the ghost and keeps
	// the wanted one.
	acq.Want(pluginhost.Material{PluginID: "org.example.ghost", Version: "1", Models: []pluginhost.ModelDecl{decl}, ModelsDir: filepath.Join(dir, "ghost-models")})
	voiceWait(t, "the ghost to be in hand", 10*time.Second, func() bool { _, ok := acq.Status("org.example.ghost"); return ok })
	// A refusal record for a package nobody installed goes the same way.
	app.markPlugin("org.example.ghost2", "1", lifeRefused, "nobody installed this")
	junk := filepath.Join(dir, "plugins", "junk")
	if err := os.MkdirAll(junk, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(junk, "junk.aiiospkg"), []byte("not a package"), 0o644); err != nil {
		t.Fatal(err)
	}
	voiceWait(t, "the pass to stop the ghost", 20*time.Second, func() bool { _, ok := acq.Status("org.example.ghost"); return !ok })
	if _, ok := acq.Status(id); !ok {
		t.Fatal("the pass stopped the acquisition it wants — the plugin would never finish downloading")
	}
	// The acquisition is stopped by the pass itself; the record goes by the
	// page's reader, at the facility's next word, a moment after it. The
	// wait is for the rows to hold the wanted one alone.
	wantedAlone := func() bool {
		views = app.pluginPendingViews()
		return len(views) == 1 && views[0].ID == id
	}
	deadline = time.Now().Add(20 * time.Second)
	for !wantedAlone() {
		if time.Now().After(deadline) {
			t.Fatalf("the pass left records for packages it does not want: %+v", views)
		}
		time.Sleep(20 * time.Millisecond)
	}

	// The identity stops while the boot pass's download is still in
	// flight: Stop returns, because the job runs under the host's
	// context and dies with it — the boot pass activated before that
	// context's plane existed, and the want was held until it did.
	stop := stopAppAside(app)
	select {
	case <-stop.done:
		stop.check(t)
	case <-time.After(20 * time.Second):
		t.Fatal("Stop waited on an acquisition in flight")
	}
}
