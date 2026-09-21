package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/genesis"
	"github.com/aiii-dot-id/aii-os/internal/genesis/genesistest"
	"github.com/aiii-dot-id/aii-os/internal/llm"
	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
	"github.com/aiii-dot-id/aii-os/internal/packagefmt/packagetest"
	"github.com/aiii-dot-id/aii-os/internal/tools"
)

// .
// .
// .
func buildResponderPkgWithOps(t *testing.T, dir, id string, ops []string) string {
	t.Helper()
	wasm, err := os.ReadFile(filepath.Join("..", "pluginworker", "testdata", "responder.wasm"))
	if err != nil {
		t.Fatalf("read responder fixture: %v (run `go generate ./internal/pluginworker`)", err)
	}
	var descs []string
	for _, op := range ops {
		descs = append(descs, fmt.Sprintf(`{"id":%q,"summary":"Operation %s of %s","effects":"read.internal","capabilities":[]}`, op, op, id))
	}
	files := map[string][]byte{
		"interfaces/probe.ops.v1.schema.json":    []byte("[" + strings.Join(descs, ",") + "]"),
		"variants/linux-x86_64-wasm/plugin.wasm": wasm,
	}
	manifest := packagetest.BuildManifestJSON(id, "0.1.0",
		[]packagetest.InterfaceSpec{{
			ID: "probe.ops", Version: 1,
			SchemaFile: "interfaces/probe.ops.v1.schema.json",
			Methods:    ops,
		}},
		[]packagetest.VariantSpec{{
			ID: "linux-x86_64-wasm", Platform: packagefmt.HostPlatform(), Arch: packagefmt.HostArch(),
			Topology: packagefmt.HostTopology(), Runtime: "wasm_component", Profile: "wasm_sandbox",
			Entrypoint: "variants/linux-x86_64-wasm/plugin.wasm",
		}},
		files, nil)
	path := filepath.Join(dir, id+"-0.1.0.aiiospkg")
	if err := os.WriteFile(path, packagetest.Build(packagetest.PackageSpec{
		Root: id + "-0.1.0", Manifest: manifest, InstallFiles: files,
	}), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func toolCallFor(name string) llm.ToolCall {
	tc := llm.ToolCall{ID: "t-" + name, Type: "function"}
	tc.Function.Name = name
	tc.Function.Arguments = "{}"
	return tc
}

// .
// .
// .
// .
// .
func TestPromptToolDefinitionsHoldUnderTheCeilingWithTenPlugins(t *testing.T) {
	dir := t.TempDir()
	result := genesistest.NewRoot(t).Birth(t, genesis.BirthConfig{
		Name:       "OfferTest",
		KeyPath:    filepath.Join(dir, "identity.sec"),
		LedgerPath: filepath.Join(dir, "ledger.jsonl"),
		DBPath:     filepath.Join(dir, "aii.db"),
	})
	result.Ledger.Close()

	ops := []string{"probe.ping", "probe.status", "probe.list", "probe.explain"}
	for i := 0; i < 10; i++ {
		id := fmt.Sprintf("org.example.p%02d", i)
		installPluginDir(t, dir, id, buildResponderPkgWithOps(t, dir, id, ops))
	}
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
	if len(app.plugins) != 10 {
		t.Fatalf("ten plugins must be active, got %d", len(app.plugins))
	}

	base := len(app.buildToolDefinitions())
	for _, d := range app.buildToolDefinitions() {
		if strings.HasPrefix(d.Function.Name, "pl_") {
			t.Fatalf("a plugin operation reached the prompt before any offer: %s", d.Function.Name)
		}
	}
	if b := app.toolReg.Brief(); b.Total != 40 || len(b.Families) != 1 || b.Families[0].Name != "probe" || b.Families[0].Count != 40 || b.Families[0].More != 40-tools.MaxFamilyNames {
		t.Fatalf("brief over ten plugins: %+v", b)
	}
	if !app.toolReg.HasDynamic() {
		t.Fatal("the posture condition must hold with plugins installed")
	}
	// .
	// .
	if p, err := app.composer.Compose("", 0); err != nil || !strings.Contains(p.Text, "Plugin operations installed beside you") || !strings.Contains(p.Text, "  probe — 40: probe.explain, probe.list, probe.ping, probe.status") {
		t.Fatalf("the prompt must name the installed operations: %v", err)
	}

	// .
	unoffered := "pl_org_example_p03_probe_status"
	obs := app.executeToolCall(context.Background(), toolCallFor(unoffered))
	if !obs.Failed || !strings.Contains(obs.Text, "not in your active offer") || !strings.Contains(obs.Text, "tools action=offer name="+unoffered) {
		t.Fatalf("unoffered dispatch must refuse with the path: %+v", obs)
	}

	// .
	// .
	if _, err := app.toolReg.Offer("probe.ping"); err == nil || !strings.Contains(err.Error(), "more than one") {
		t.Fatalf("an operation id ten plugins declare must not resolve: %v", err)
	}
	for i := 0; i < tools.MaxOffered; i++ {
		name := fmt.Sprintf("pl_org_example_p%02d_probe_ping", i)
		if _, err := app.toolReg.Offer(name); err != nil {
			t.Fatalf("offer %s: %v", name, err)
		}
	}
	if _, err := app.toolReg.Offer("pl_org_example_p08_probe_ping"); err == nil {
		t.Fatal("the ninth offer must refuse")
	}
	defs := app.buildToolDefinitions()
	if len(defs) != base+tools.MaxOffered {
		t.Fatalf("definitions = %d, want base %d + %d offered", len(defs), base, tools.MaxOffered)
	}
	if len(defs) > PromptToolDefinitionCeiling {
		t.Fatalf("definitions = %d exceed the ceiling %d", len(defs), PromptToolDefinitionCeiling)
	}
	var offeredDef *llm.ToolDefinition
	for i := range defs {
		if defs[i].Function.Name == "pl_org_example_p00_probe_ping" {
			offeredDef = &defs[i]
		}
	}
	if offeredDef == nil || !strings.HasPrefix(offeredDef.Function.Description, "Operation probe.ping of org.example.p00 — ") {
		t.Fatalf("the offered definition leads with the descriptor's summary: %+v", offeredDef)
	}

	// .
	obs = app.executeToolCall(context.Background(), toolCallFor("pl_org_example_p00_probe_ping"))
	if obs.Failed || !strings.Contains(obs.Text, `"echoed":true`) {
		t.Fatalf("offered dispatch must run: %+v", obs)
	}
	card, err := app.toolReg.Show("pl_org_example_p00_probe_ping")
	if err != nil || card.Plugin != "org.example.p00" || card.Version != "0.1.0" || card.Tier != "T0" || card.Family != "probe" || card.State != tools.StateOffered || card.Effects != "read.internal" {
		t.Fatalf("show over a real activation: %+v %v", card, err)
	}
}
