package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/genesis"
	"github.com/aiii-dot-id/aii-os/internal/genesis/genesistest"
)

// A RUNNING IDENTITY'S FACILITIES ARE HANDED THE CONTRADICTION VIEW. Each
// facility takes it by a setter, and every unit test of the facility sets
// it by hand — so nothing but a boot can show that the boot does. A setter
// nobody called is a view that renders in tests and never in life.
func TestABootHandsBothFacilitiesTheContradictionView(t *testing.T) {
	dir := t.TempDir()
	result := genesistest.NewRoot(t).Birth(t, genesis.BirthConfig{
		Name:       "TensionsWiring",
		KeyPath:    filepath.Join(dir, "identity.sec"),
		LedgerPath: filepath.Join(dir, "ledger.jsonl"),
		DBPath:     filepath.Join(dir, "aii.db"),
	})
	result.Ledger.Close()

	cfgPath := filepath.Join(dir, "config.json")
	cfg := defaultConfig()
	cfg.Identity = IdentityConfig{KeyPath: filepath.Join(dir, "identity.sec"),
		LedgerPath: filepath.Join(dir, "ledger.jsonl"), DBPath: filepath.Join(dir, "aii.db"),
	}
	cfg.LLM = withTestProvider(t, dir, "test", "https://x", "m", "sk-x")
	cfg.Dashboard.Port = 0
	cfg.Tools.CWD = dir
	cfg.SourcePath = cfgPath
	b, _ := json.Marshal(cfg)
	if err := os.WriteFile(cfgPath, b, 0o644); err != nil {
		t.Fatal(err)
	}
	app := New(cfg)
	if err := startLiveForTest(app); err != nil {
		t.Fatalf("startLive: %v", err)
	}
	defer stopApp(t, app)

	for _, name := range []string{"dream", "consolidate"} {
		owner, ok := app.timeFac.OwnerFor(name)
		if !ok {
			t.Fatalf("%s is not registered with TIME", name)
		}
		wired, ok := owner.(interface{ TensionsWired() bool })
		if !ok {
			t.Fatalf("%s cannot say whether it was handed the view", name)
		}
		if !wired.TensionsWired() {
			t.Errorf("THE BOOT NEVER HANDED %s THE CONTRADICTION VIEW: CONTESTED is a label again, in a running identity only", name)
		}
	}
}
