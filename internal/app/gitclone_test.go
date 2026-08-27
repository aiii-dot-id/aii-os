package app

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/genesis"
	"github.com/aiii-dot-id/aii-os/internal/genesis/genesistest"
)

// THE GIT-CLONE INSTALL, end to end. A repository carries the source for
// review and the signed package for execution; cloning it into plugins/
// is the whole install. Nothing is listed in config, and — the part worth
// proving — nothing is restarted: the clone lands while the identity is
// running, and a later `git pull` that updates the package converges the
// same way.
func TestGitCloneIntoPluginsIsTheInstall(t *testing.T) {
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not available")
	}
	dir := t.TempDir()
	result := genesistest.NewRoot(t).Birth(t, genesis.BirthConfig{
		Name:       "Clone",
		KeyPath:    filepath.Join(dir, "identity.sec"),
		LedgerPath: filepath.Join(dir, "ledger.jsonl"),
		DBPath:     filepath.Join(dir, "aii.db"),
	})
	result.Ledger.Close()

	// Build the signed artifact BEFORE chdir (the fixture resolves against
	// the repo), then assemble a repository around it.
	pkg := buildResponderPkg(t, dir, "org.example.cloned")
	// The rebuilt package for the "git pull" half must ALSO be built
	// before the chdir below: the wasm fixture resolves against the repo,
	// not the identity's home.
	rebuilt := buildResponderPkg(t, t.TempDir(), "org.example.cloned")
	succPkg := buildResponderPkg(t, t.TempDir(), "org.example.successor")
	origin := filepath.Join(t.TempDir(), "ring4-memory")
	if err := os.MkdirAll(origin, 0o750); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(pkg)
	if err != nil {
		t.Fatal(err)
	}
	// Source for review, package for execution — both in the repo.
	for name, body := range map[string][]byte{
		"main.go":         []byte("package main // the review surface\n"),
		"plugin.json":     []byte(`{"id":"org.example.cloned"}`),
		"README.md":       []byte("# ring4-memory\n"),
		"cloned.aiiospkg": data,
	} {
		if err := os.WriteFile(filepath.Join(origin, name), body, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	run := func(wd string, args ...string) {
		t.Helper()
		cmd := exec.Command(git, args...)
		cmd.Dir = wd
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@x", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@x")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run(origin, "init", "-q")
	run(origin, "add", "-A")
	run(origin, "commit", "-qm", "signed package alongside its source")

	nd, err := os.ReadFile(rebuilt)
	if err != nil {
		t.Fatal(err)
	}
	succ, err := os.ReadFile(succPkg)
	if err != nil {
		t.Fatal(err)
	}

	t.Chdir(dir)
	cfg := &Config{
		Identity: IdentityConfig{
			Name: "Clone", KeyPath: filepath.Join(dir, "identity.sec"),
			LedgerPath: filepath.Join(dir, "ledger.jsonl"), DBPath: filepath.Join(dir, "aii.db"),
		},
		LLM:        withTestProvider(t, dir, "test", "https://127.0.0.1:1", "m", "sk-x"),
		SourcePath: filepath.Join(dir, "config.json"),
		Dashboard:  DashboardConfig{Port: 0},
		Tools:      ToolsConfig{CWD: dir},
		Plugins:    PluginsConfig{Autoload: "T0"},
		Agency:     defaultConfig().Agency,
	}
	app := New(cfg)
	if err := startLiveForTest(app); err != nil {
		t.Fatal(err)
	}
	defer app.Stop()
	if len(app.plugins) != 0 {
		t.Fatal("nothing installed yet")
	}

	// THE INSTALL: git clone into plugins/. No config edit, no restart.
	if err := os.MkdirAll("plugins", 0o750); err != nil {
		t.Fatal(err)
	}
	run(dir, "clone", "-q", origin, filepath.Join("plugins", "ring4-memory"))
	app.pokePluginSweep()

	const tool = "pl_org_example_cloned_ping"
	waitFor(t, "clone to activate", func() bool {
		_, ok := app.toolReg.Get(tool)
		return ok
	})
	t.Log("clone activated while running — no config entry, no restart")

	// The .git directory must not confuse the scan, and the source files
	// must not be mistaken for packages.
	if _, err := os.Stat(filepath.Join("plugins", "ring4-memory", ".git")); err != nil {
		t.Fatalf("the clone should carry its .git: %v", err)
	}

	// A PULL THAT CHANGES NO PACKAGE FILE must not churn the active plugin.
	if string(nd) != string(data) {
		t.Fatal("expected a deterministic rebuild to be byte-identical")
	}
	run(origin, "commit", "-q", "--allow-empty", "-m", "no-op rebuild")
	run(filepath.Join(dir, "plugins", "ring4-memory"), "pull", "-q")
	app.pluginMu.Lock()
	nactive := len(app.plugins)
	if nactive != 1 {
		app.pluginMu.Unlock()
		t.Fatalf("expected one active plugin before no-op pull sweep, got %d", nactive)
	}
	activeBefore := app.plugins[0]
	// Force one real reconciliation pass instead of waiting for the ticker.
	app.pluginFinger = "force-no-op-sweep"
	app.pluginMu.Unlock()
	app.convergePlugins(t.Context())
	app.pluginMu.Lock()
	unchanged := app.pluginFinger != "force-no-op-sweep" && len(app.plugins) == 1 && app.plugins[0] == activeBefore
	app.pluginMu.Unlock()
	if !unchanged {
		t.Fatal("a pull with no package change churned the running plugin")
	}
	t.Log("no-op pull left the active plugin instance unchanged")

	// A PULL THAT SHIPS A NEW PACKAGE converges live: old out, new in.
	if err := os.Remove(filepath.Join(origin, "cloned.aiiospkg")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(origin, "successor.aiiospkg"), succ, 0o644); err != nil {
		t.Fatal(err)
	}
	run(origin, "add", "-A")
	run(origin, "commit", "-qm", "ship the successor")
	run(filepath.Join(dir, "plugins", "ring4-memory"), "pull", "-q")
	app.pokePluginSweep()
	waitFor(t, "the updated package to take over", func() bool {
		_, oldStill := app.toolReg.Get(tool)
		_, newHere := app.toolReg.Get("pl_org_example_successor_ping")
		return !oldStill && newHere
	})
	t.Log("pull replaced the package live — old deactivated, new activated")

	// THE UNINSTALL: remove the directory.
	if err := os.RemoveAll(filepath.Join("plugins", "ring4-memory")); err != nil {
		t.Fatal(err)
	}
	app.pokePluginSweep()
	waitFor(t, "removal to deactivate", func() bool {
		app.pluginMu.Lock()
		defer app.pluginMu.Unlock()
		return len(app.plugins) == 0
	})
	t.Log("rm -rf uninstalled it, live")
}

func waitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(25 * time.Second)
	for {
		if ok() {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(200 * time.Millisecond)
	}
}
