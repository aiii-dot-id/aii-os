package app

import (
	"bytes"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/genesis"
	"github.com/aiii-dot-id/aii-os/internal/genesis/genesistest"
)

// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
func TestTouchingAPackageDoesNotTearItDown(t *testing.T) {
	dir := t.TempDir()
	result := genesistest.NewRoot(t).Birth(t, genesis.BirthConfig{
		Name:       "TouchTest",
		KeyPath:    filepath.Join(dir, "identity.sec"),
		LedgerPath: filepath.Join(dir, "ledger.jsonl"),
		DBPath:     filepath.Join(dir, "aii.db"),
	})
	result.Ledger.Close()
	pkg := buildResponderPkg(t, dir, "org.example.touched")
	t.Chdir(dir)

	cfg := &Config{
		Identity: IdentityConfig{KeyPath: filepath.Join(dir, "identity.sec"),
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

	installPluginDir(t, dir, "org.example.touched", pkg)
	const tool = "pl_org_example_touched_ping"
	deadline := time.Now().Add(20 * time.Second)
	for {
		if _, ok := app.toolReg.Get(tool); ok {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("setup: plugin never activated; tools: %v", app.toolReg.Names())
		}
		time.Sleep(100 * time.Millisecond)
	}

	// .
	// .
	installed := filepath.Join(dir, "plugins", "org.example.touched", filepath.Base(pkg))
	stamp := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(installed, stamp, stamp); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	log.SetOutput(&buf)
	defer log.SetOutput(os.Stderr)
	app.convergePlugins(t.Context())

	if out := buf.String(); strings.Contains(out, "deactivated") {
		t.Fatalf("a restamp with identical bytes tore the activation down — plugin KV is activation-scoped, so this wipes the plugin's working set:\n%s", out)
	}
	if _, ok := app.toolReg.Get(tool); !ok {
		t.Fatalf("the plugin left the registry after a mere restamp; tools: %v", app.toolReg.Names())
	}
}
