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

	"github.com/aiii-dot-id/aii-os/internal/genesis"
	"github.com/aiii-dot-id/aii-os/internal/genesis/genesistest"
	"github.com/aiii-dot-id/aii-os/internal/machine"
	"github.com/aiii-dot-id/aii-os/internal/packagefmt/packagetest"
	"github.com/aiii-dot-id/aii-os/internal/pluginhost"
	"github.com/aiii-dot-id/aii-os/internal/plugstore"
)

func TestBootDoesNotWaitOnTheLinkAndPresentMaterialNeedsNoService(t *testing.T) {
	dir := t.TempDir()
	result := genesistest.NewRoot(t).Birth(t, genesis.BirthConfig{
		Name:       "StoreBoot",
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

	// The model, in the store already: put there by the store's writer
	// before the identity starts, and the writer let go — no service runs.
	const id = "org.example.present"
	body := []byte("a model another identity brought")
	sum := sha256.Sum256(body)
	decl := pluginhost.ModelDecl{Name: "present.bin", URL: "https://example.invalid/present.bin", SHA256: hex.EncodeToString(sum[:]), Size: int64(len(body))}
	models, err := json.Marshal([]pluginhost.ModelDecl{decl})
	if err != nil {
		t.Fatal(err)
	}
	mdir := filepath.Join(t.TempDir(), "machine")
	storeDir, err := machine.StoreDir(mdir)
	if err != nil {
		t.Fatal(err)
	}
	w, err := plugstore.OpenWriter(storeDir)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(models)
	view := &pluginhost.ViewWork{ModelsSHA256: "sha256:" + hex.EncodeToString(digest[:]), Names: []string{decl.Name}, Models: []pluginhost.ModelDecl{decl}}
	work := pluginhost.StoreWork{Entry: "v/" + plugstore.ViewKey(view.ModelsSHA256, view.Names), View: view}
	serve := func(_ context.Context, _ string, offset int64, out io.Writer) (int64, error) {
		n, err := out.Write(body[offset:])
		return int64(n), err
	}
	if err := pluginhost.EnsureEntry(t.Context(), w, work, serve, pluginhost.Adoption{}, nil); err != nil {
		t.Fatal(err)
	}
	w.Close()

	installPluginDir(t, dir, id, nativePackageNeedingModels(t, dir, id, root, models))
	t.Chdir(dir)
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cfg := &Config{
		Identity: IdentityConfig{KeyPath: filepath.Join(dir, "identity.sec"),
			LedgerPath: filepath.Join(dir, "ledger.jsonl"), DBPath: filepath.Join(dir, "aii.db")},
		LLM:        withTestProvider(t, dir, "test", "https://127.0.0.1:1", "m", "sk-x"),
		SourcePath: filepath.Join(dir, "config.json"),
		Dashboard:  DashboardConfig{Port: 0},
		Tools:      ToolsConfig{CWD: dir},
		Plugins:    PluginsConfig{Autoload: "T3", PlatformRoot: rootPath, WorkerBinary: exe},
		Agency:     defaultConfig().Agency,
		Machine:    MachineConfig{Dir: mdir},
	}

	baseline := func() time.Duration {
		was := accountMachineDir
		accountMachineDir = func() (string, error) { return "", errors.New("the baseline boots with no machine service") }
		defer func() { accountMachineDir = was }()
		bare := *cfg
		bare.Machine = MachineConfig{}
		app := New(&bare)
		began := time.Now()
		if err := startLiveUnsettled(app); err != nil {
			t.Fatalf("the baseline boot: %v", err)
		}
		took := time.Since(began)
		if app.machine.client.Load() != nil {
			t.Fatal("rig: the baseline boot started a link")
		}
		stopApp(t, app)
		return took
	}()
	app := New(cfg)
	began := time.Now()
	booted := make(chan error, 1)
	go func() { booted <- startLiveUnsettled(app) }()
	select {
	case err := <-booted:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Minute):
		t.Fatalf("THE BOOT HAS NOT RETURNED after %v with the service down: it waits on the link", time.Since(began))
	}
	took, limit := time.Since(began), max(15*time.Second, 3*baseline)
	if took > limit {
		stopApp(t, app)
		t.Fatalf("THE BOOT WAITED ON THE LINK: it took %v with the service down; the same boot with no link took %v (the limit is %v)", took, baseline, limit)
	}
	defer stopApp(t, app)
	t.Logf("the boot returned after %v with the service down; with no link it took %v", took, baseline)
	c := app.machine.client.Load()
	if c == nil {
		t.Fatal("the live boot started no link")
	}
	if s := c.Snapshot(); s.State == machine.StateLinked {
		t.Fatalf("a link with no service: %+v", s)
	}
	if got := app.pluginOptions().StoreDir; got != storeDir {
		t.Fatalf("the identity reads the store at %s, want %s", got, storeDir)
	}
	// The facility's pass over the package: its material is verified in
	// the store, and what follows is the child's (which exits at once, in
	// this test), never a wait for the service. What is held is where the
	// activation gets to and what it never asks, not how soon: the wait is
	// a condition's, and its bound catches only an activation that never
	// moves; thirty seconds was a guess at how soon, as the boot's fifteen
	// were.
	acq := app.acquirer()
	voiceWait(t, "the package's activation to be past its material", 2*time.Minute, func() bool {
		for _, v := range app.pluginPendingViews() {
			if v.ID == id && (v.Refusal != nil || v.Phase == "refused" || v.Phase == "starting") {
				return true
			}
		}
		app.pluginMu.Lock()
		active := len(app.plugins)
		app.pluginMu.Unlock()
		return active == 1
	})
	if st, ok := acq.Status(id); ok {
		t.Fatalf("material in the store was asked for: %+v", st)
	}
	for _, v := range app.pluginPendingViews() {
		if strings.Contains(v.LastError+v.Summary, "machine service") {
			t.Fatalf("a present plugin waits on the service: %+v", v)
		}
	}
	if s := c.Snapshot(); s.State == machine.StateLinked {
		t.Fatalf("a link with no service: %+v", s)
	}
}
