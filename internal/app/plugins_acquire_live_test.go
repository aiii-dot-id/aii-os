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

	"github.com/aiii-dot-id/aii-os/internal/genesis"
	"github.com/aiii-dot-id/aii-os/internal/genesis/genesistest"
	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
	"github.com/aiii-dot-id/aii-os/internal/packagefmt/packagetest"
	"github.com/aiii-dot-id/aii-os/internal/pluginhost"
)

// .
// .
// .
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

// .
// .
// .
// .
// .
// .
// .
// .
// .
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
	app := New(cfg)
	var fetches atomic.Int32
	app.modelFetch = func(ctx context.Context, url string, offset int64, w io.Writer) (int64, error) {
		fetches.Add(1)
		return 0, errors.New("the model server answered 503")
	}
	if err := startLiveForTest(app); err != nil {
		t.Fatal(err)
	}
	defer app.Stop()

	acq := app.acquirer()
	if acq == nil {
		t.Fatal("the application wired no acquirer")
	}
	// .
	// .
	// .
	voiceWait(t, "the acquisition to be in hand and waiting", 20*time.Second, func() bool {
		st, ok := acq.Status(id)
		return ok && st.Phase == pluginhost.PhaseWaiting
	})
	if fetches.Load() == 0 {
		t.Fatal("the acquirer never tried the download")
	}
	views := app.pluginPendingViews()
	if len(views) != 1 || views[0].ID != id || views[0].FilesTotal != 1 || views[0].FilesPresent != 0 || !strings.Contains(views[0].LastError, "503") {
		t.Fatalf("the page does not name the acquisition: %+v", views)
	}
	app.pluginMu.Lock()
	active := len(app.plugins)
	app.pluginMu.Unlock()
	if active != 0 {
		t.Fatal("a plugin activated without its model")
	}

	// .
	// .
	// .
	acq.Want(pluginhost.Material{PluginID: "org.example.ghost", Version: "1", Models: []pluginhost.ModelDecl{decl}, ModelsDir: filepath.Join(dir, "ghost-models")})
	voiceWait(t, "the ghost to be in hand", 10*time.Second, func() bool { _, ok := acq.Status("org.example.ghost"); return ok })
	// .
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
	if views := app.pluginPendingViews(); len(views) != 1 || views[0].ID != id {
		t.Fatalf("the pass left records for packages it does not want: %+v", views)
	}

	// .
	// .
	// .
	// .
	stopped := make(chan struct{})
	go func() { app.Stop(); close(stopped) }()
	select {
	case <-stopped:
	case <-time.After(20 * time.Second):
		t.Fatal("Stop waited on an acquisition in flight")
	}
}
