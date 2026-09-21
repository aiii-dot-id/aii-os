package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/genesis"
	"github.com/aiii-dot-id/aii-os/internal/genesis/genesistest"
	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
	"github.com/aiii-dot-id/aii-os/internal/packagefmt/packagetest"
)

// .
// .
// .
// .
func TestAnOfferedOperationIsOfferedAfterARestart(t *testing.T) {
	dir := t.TempDir()
	result := genesistest.NewRoot(t).Birth(t, genesis.BirthConfig{
		Name:       "StandingOfferTest",
		KeyPath:    filepath.Join(dir, "identity.sec"),
		LedgerPath: filepath.Join(dir, "ledger.jsonl"),
		DBPath:     filepath.Join(dir, "aii.db"),
	})
	result.Ledger.Close()
	const id = "org.example.mem"
	installPluginDir(t, dir, id, buildResponderPkgWithOps(t, dir, id, []string{"mem.store", "mem.recall"}))
	t.Chdir(dir)
	cfg := func() *Config {
		return &Config{
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
	}

	first := New(cfg())
	if err := startLiveForTest(first); err != nil {
		t.Fatal(err)
	}
	name, err := first.toolReg.Offer("mem.store")
	if err != nil {
		first.Stop()
		t.Fatal(err)
	}
	first.Stop()

	second := New(cfg())
	if err := startLiveForTest(second); err != nil {
		t.Fatal(err)
	}
	defer second.Stop()
	if len(second.plugins) != 1 {
		t.Fatalf("the plugin did not activate after the restart: %d active", len(second.plugins))
	}
	offered := false
	for _, d := range second.buildToolDefinitions() {
		if d.Function.Name == name {
			offered = true
		}
	}
	if !offered {
		t.Fatalf("%s was offered before the restart and is not in the tool list after it (offer now %v)", name, second.toolReg.Offered())
	}
	for _, d := range second.buildToolDefinitions() {
		if d.Function.Name == "pl_org_example_mem_mem_recall" {
			t.Fatal("an operation never offered reached the tool list")
		}
	}
}

// .
// .
func buildMemPkg(t *testing.T, dir, id, version, effects string, ops []string) string {
	t.Helper()
	wasm, err := os.ReadFile(filepath.Join("..", "pluginworker", "testdata", "responder.wasm"))
	if err != nil {
		t.Fatalf("read responder fixture: %v", err)
	}
	var descs []string
	for _, op := range ops {
		descs = append(descs, fmt.Sprintf(`{"id":%q,"summary":"Operation %s of %s","effects":%q,"capabilities":[]}`, op, op, id, effects))
	}
	files := map[string][]byte{
		"interfaces/probe.ops.v1.schema.json":    []byte("[" + strings.Join(descs, ",") + "]"),
		"variants/linux-x86_64-wasm/plugin.wasm": wasm,
	}
	manifest := packagetest.BuildManifestJSON(id, version,
		[]packagetest.InterfaceSpec{{ID: "probe.ops", Version: 1, SchemaFile: "interfaces/probe.ops.v1.schema.json", Methods: ops}},
		[]packagetest.VariantSpec{{
			ID: "linux-x86_64-wasm", Platform: packagefmt.HostPlatform(), Arch: packagefmt.HostArch(),
			Topology: packagefmt.HostTopology(), Runtime: "wasm_component", Profile: "wasm_sandbox",
			Entrypoint: "variants/linux-x86_64-wasm/plugin.wasm",
		}},
		files, nil)
	path := filepath.Join(dir, id+"-"+version+".aiiospkg")
	if err := os.WriteFile(path, packagetest.Build(packagetest.PackageSpec{Root: id + "-" + version, Manifest: manifest, InstallFiles: files}), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// .
// .
// .
// .
// .
func TestAnOperationThatChangedIsWithheldAndNamedUntilATurnCompletes(t *testing.T) {
	dir := t.TempDir()
	result := genesistest.NewRoot(t).Birth(t, genesis.BirthConfig{
		Name:       "WithheldOfferTest",
		KeyPath:    filepath.Join(dir, "identity.sec"),
		LedgerPath: filepath.Join(dir, "ledger.jsonl"),
		DBPath:     filepath.Join(dir, "aii.db"),
	})
	result.Ledger.Close()
	const id = "org.example.mem"
	// .
	// .
	release := map[string]string{
		"0.1.0": buildMemPkg(t, dir, id, "0.1.0", "read.internal", []string{"mem.store", "mem.recall"}),
		"0.2.0": buildMemPkg(t, dir, id, "0.2.0", "write.local", []string{"mem.store", "mem.recall"}),
	}
	install := func(version string) {
		if err := os.RemoveAll(filepath.Join(dir, "plugins", id)); err != nil {
			t.Fatal(err)
		}
		installPluginDir(t, dir, id, release[version])
	}
	install("0.1.0")
	t.Chdir(dir)
	cfg := func() *Config {
		return &Config{
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
	}

	first := New(cfg())
	if err := startLiveForTest(first); err != nil {
		t.Fatal(err)
	}
	name, err := first.toolReg.Offer("mem.store")
	if err != nil {
		first.Stop()
		t.Fatal(err)
	}
	first.Stop()

	install("0.2.0")
	second := New(cfg())
	if err := startLiveForTest(second); err != nil {
		t.Fatal(err)
	}
	defer second.Stop()
	if len(second.plugins) != 1 {
		t.Fatalf("the updated plugin did not activate: %d active", len(second.plugins))
	}
	for _, d := range second.buildToolDefinitions() {
		if d.Function.Name == name {
			t.Fatalf("%s declares write.local now and reached the tool list on a read.internal offer", name)
		}
	}
	for i := 0; i < 2; i++ {
		facts, err := second.buildTurnFacts(false)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(facts, "### Offers withheld") || !strings.Contains(facts, name) || !strings.Contains(facts, "mem.store in org.example.mem 0.2.0") {
			t.Fatalf("compose %d did not name the withheld offer:\n%s", i+1, facts)
		}
	}
	second.markComposedHarvests()
	facts, err := second.buildTurnFacts(false)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(facts, "Offers withheld") {
		t.Fatalf("a turn completed and the withheld offer was named again:\n%s", facts)
	}
	seats, err := second.store.StandingOffer()
	if err != nil || len(seats) != 1 || seats[0].Name != name {
		t.Fatalf("the record no longer holds the identity's seat: %+v, %v", seats, err)
	}
}
