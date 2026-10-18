package app

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/genesis"
	"github.com/aiii-dot-id/aii-os/internal/genesis/genesistest"
	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
	"github.com/aiii-dot-id/aii-os/internal/tools"
)

func TestAStartLeavesTheStatusFilesInTheTrustDirectory(t *testing.T) {
	dir := t.TempDir()
	result := genesistest.NewRoot(t).Birth(t, genesis.BirthConfig{
		Name:       "TrustPresentTest",
		KeyPath:    filepath.Join(dir, "identity.sec"),
		LedgerPath: filepath.Join(dir, "ledger.jsonl"),
		DBPath:     filepath.Join(dir, "aii.db"),
	})
	result.Ledger.Close()
	t.Chdir(dir)

	trustDir := filepath.Join(dir, "trust")
	if _, err := os.Stat(trustDir); !os.IsNotExist(err) {
		t.Fatalf("the fixture already has a trust directory: %v", err)
	}
	cfg := &Config{
		Identity: IdentityConfig{KeyPath: filepath.Join(dir, "identity.sec"),
			LedgerPath: filepath.Join(dir, "ledger.jsonl"), DBPath: filepath.Join(dir, "aii.db"),
		},
		LLM:        withTestProvider(t, dir, "test", "https://127.0.0.1:1", "m", "sk-x"),
		SourcePath: filepath.Join(dir, "config.json"),
		Dashboard:  DashboardConfig{Port: 0},
		Tools:      ToolsConfig{CWD: dir},
		Agency:     defaultConfig().Agency,
	}
	app := New(cfg)
	if err := startLiveForTest(app); err != nil {
		t.Fatal(err)
	}
	defer stopApp(t, app)

	present := func(when string) {
		t.Helper()
		for _, d := range packagefmt.RevocationDomains() {
			if info, err := os.Stat(filepath.Join(trustDir, d.FileName)); err != nil || info.Size() == 0 {
				t.Fatalf("%s: %s is not in the trust directory: %v", when, d.FileName, err)
			}
			if _, ok := app.pluginOptions().Roots.Revocation.Epoch(d.RootKeyType); !ok {
				t.Fatalf("%s: the %s tier is unavailable: %v", when, d.RootKeyType, app.pluginOptions().Roots.Revocation.Describe())
			}
			if _, _, accepted, err := app.store.TrustEpochHighWater(d.RootKeyType); err != nil || accepted {
				t.Fatalf("%s: the shipped list was added to the identity's accepted epochs for %s (%v)", when, d.RootKeyType, err)
			}
		}
	}
	present("after the start")

	gone := packagefmt.RevocationDomains()[0]
	if err := os.Remove(filepath.Join(trustDir, gone.FileName)); err != nil {
		t.Fatal(err)
	}
	app.pokePluginSweep()
	eventually(t, gone.FileName+" put back by the sweep", func() bool {
		info, err := os.Stat(filepath.Join(trustDir, gone.FileName))
		return err == nil && info.Size() > 0
	})
	present("after a file was taken away and the plugins were scanned")
}

// NOTHING IS WRITTEN WHERE THE PROCESS HAPPENS TO STAND. The trust
// directory is beside the ledger. A configuration that names no ledger
// has no such place, and "trust" was then a path under the working
// directory: the first build of this rule wrote the three files into the
// source tree from every test that built plugin options without an
// identity. With no ledger named nothing is written, and the signed
// tiers are unavailable, which is what a missing file means.
func TestAConfigurationWithNoLedgerWritesNoTrustDirectory(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	cfg := defaultConfig()
	cfg.Identity.LedgerPath = ""
	cfg.Tools.CWD = filepath.Join(root, "home")
	cfg.SourcePath = filepath.Join(root, "config", "config.json")
	a := &App{cfg: cfg}
	opts, err := a.buildPluginOptions(newAdmissionStore(t, t.TempDir()), tools.NewRegistry(t.TempDir(), nil, tools.Timeouts{}), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "trust")); !os.IsNotExist(err) {
		t.Fatalf("building plugin options with no ledger named left a trust directory under the working directory (%v)", err)
	}
	for _, d := range packagefmt.RevocationDomains() {
		if _, ok := opts.Roots.Revocation.Epoch(d.RootKeyType); ok {
			t.Fatalf("the %s tier is available with no status file anywhere", d.RootKeyType)
		}
	}
}
