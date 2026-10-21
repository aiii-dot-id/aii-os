package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/genesis"
	"github.com/aiii-dot-id/aii-os/internal/genesis/genesistest"
	"github.com/aiii-dot-id/aii-os/internal/llm"
	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
	"github.com/aiii-dot-id/aii-os/internal/packagefmt/packagetest"
	"github.com/aiii-dot-id/aii-os/internal/tools"
)

// buildResponderPkgWithPrimaries is buildResponderPkg with a chosen
// operation list and a real descriptor file, so the tools organ has
// summaries and families to show, whose descriptors mark the operations
// named in primary as their author's primary operations.
func buildResponderPkgWithPrimaries(t *testing.T, dir, id string, ops []string, primary ...string) string {
	t.Helper()
	wasm, err := os.ReadFile(filepath.Join("..", "pluginworker", "testdata", "responder.wasm"))
	if err != nil {
		t.Fatalf("read responder fixture: %v (run `go generate ./internal/pluginworker`)", err)
	}
	var descs []string
	for _, op := range ops {
		descs = append(descs, fmt.Sprintf(`{"id":%q,"summary":"Operation %s of %s","effects":"read.internal","capabilities":[],"primary":%t}`, op, op, id, slices.Contains(primary, op)))
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

// THE TOOL LIST HAS A CEILING; ACTIVATION HAS NONE. Ten
// plugins, forty operations, each marked primary by its author: every
// plugin activates, the prompt's tool definitions stop at the ceiling,
// the primaries are listed in tool-name order until it, the prompt's hint
// counts the rest, and one past the ceiling is called through tools
// action=call by the same path a listed one runs by its tool name.
func TestPromptToolDefinitionsHoldUnderTheCeilingWithTenPlugins(t *testing.T) {
	dir := t.TempDir()
	result := genesistest.NewRoot(t).Birth(t, genesis.BirthConfig{
		Name:       "CeilingTest",
		KeyPath:    filepath.Join(dir, "identity.sec"),
		LedgerPath: filepath.Join(dir, "ledger.jsonl"),
		DBPath:     filepath.Join(dir, "aii.db"),
	})
	result.Ledger.Close()

	ops := []string{"probe.ping", "probe.status", "probe.list", "probe.explain"}
	for i := 0; i < 10; i++ {
		id := fmt.Sprintf("org.example.p%02d", i)
		installPluginDir(t, dir, id, buildResponderPkgWithPrimaries(t, dir, id, ops, ops...))
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
	defer stopApp(t, app)
	if len(app.plugins) != 10 {
		t.Fatalf("ten plugins must be active, got %d", len(app.plugins))
	}

	defs := app.buildToolDefinitions()
	if len(defs) != PromptToolDefinitionCeiling {
		t.Fatalf("definitions = %d, want the ceiling %d with forty primaries waiting", len(defs), PromptToolDefinitionCeiling)
	}
	var listed []string
	for _, d := range defs {
		if strings.HasPrefix(d.Function.Name, "pl_") {
			listed = append(listed, d.Function.Name)
		}
	}
	_, unlisted := app.toolReg.PromptNames(registryRoom())
	if len(listed) == 0 || len(listed)+unlisted != 40 {
		t.Fatalf("%d primaries listed and %d counted unlisted, want forty in all", len(listed), unlisted)
	}
	if !slices.IsSorted(listed) || listed[0] != "pl_org_example_p00_probe_explain" {
		t.Fatalf("the primaries are listed in tool-name order: %v", listed)
	}
	if b := app.toolReg.Brief(); b.Total != 40 || len(b.Families) != 1 || b.Families[0].Name != "probe" || b.Families[0].Count != 40 || b.Families[0].More != 40-tools.MaxFamilyNames {
		t.Fatalf("brief over ten plugins: %+v", b)
	}
	// The prompt NAMES what is installed, in the tool guidance, and counts
	// the primaries the list left out.
	p, err := app.composer.Compose("", 0)
	if err != nil || !strings.Contains(p.Text, "Installed plugin families; primary operations already appear in your tool list:") ||
		!strings.Contains(p.Text, "  probe — 40: probe.explain, probe.list, probe.ping, probe.status") ||
		!strings.Contains(p.Text, fmt.Sprintf(" %d primary operation(s) did not fit in your tool list; call them the same way.", unlisted)) {
		t.Fatalf("the prompt must name the installed operations and count the unlisted: %v", err)
	}

	var listedDef *llm.ToolDefinition
	for i := range defs {
		if defs[i].Function.Name == "pl_org_example_p00_probe_ping" {
			listedDef = &defs[i]
		}
	}
	if listedDef == nil || !strings.HasPrefix(listedDef.Function.Description, `plugin org.example.p00 says: "Operation probe.ping of org.example.p00" — Operation "probe.ping" of interface probe.ops@1, plugin org.example.p00 (`) {
		t.Fatalf("the listed definition leads with the descriptor's summary, quoted under its plugin's id: %+v", listedDef)
	}

	// A listed primary runs by its tool name; the last primary, past the
	// ceiling, runs through the route.
	obs := app.executeToolCall(context.Background(), toolCallFor("pl_org_example_p00_probe_ping"))
	if obs.Failed || !strings.Contains(obs.Text, `"echoed":true`) {
		t.Fatalf("a listed primary must run: %+v", obs)
	}
	last := "pl_org_example_p09_probe_status"
	if slices.Contains(listed, last) {
		t.Fatalf("%s should be past the ceiling", last)
	}
	routed := toolCallFor("tools")
	routed.Function.Arguments = `{"action":"call","name":"` + last + `","args":{}}`
	if obs := app.executeToolCall(context.Background(), routed); obs.Failed || !strings.Contains(obs.Text, `"echoed":true`) {
		t.Fatalf("a primary past the ceiling must run through tools action=call: %+v", obs)
	}
	card, err := app.toolReg.Show(last)
	if err != nil || card.Plugin != "org.example.p09" || card.Version != "0.1.0" || card.Tier != "T0" || card.Family != "probe" || card.Reason != "" || !card.Primary || card.Effects != "read.internal" {
		t.Fatalf("show over a real activation: %+v %v", card, err)
	}
}
