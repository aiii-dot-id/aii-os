package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/genesis"
	"github.com/aiii-dot-id/aii-os/internal/genesis/genesistest"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
	"github.com/aiii-dot-id/aii-os/internal/packagefmt/packagetest"
	"github.com/aiii-dot-id/aii-os/internal/pluginhost"
	"github.com/aiii-dot-id/aii-os/internal/pluginworker/wasmgen"
	"github.com/aiii-dot-id/aii-os/internal/tools"
)

// choicesApp starts an identity with one plugin: its settings declaration,
// its "models" operation declared with the given effects, and a guest that
// answers every operation with the given result.
func choicesApp(t *testing.T, id, settings, effects, result string) *App {
	t.Helper()
	dir := t.TempDir()
	genesistest.NewRoot(t).Birth(t, genesis.BirthConfig{
		Name: "ChoicesTest", KeyPath: filepath.Join(dir, "identity.sec"),
		LedgerPath: filepath.Join(dir, "ledger.jsonl"), DBPath: filepath.Join(dir, "aii.db"),
	}).Ledger.Close()
	files := map[string][]byte{
		"interfaces/models.probe.v1.schema.json": []byte(`[{"id":"models","summary":"List the models","effects":"` + effects + `","capabilities":[]}]`),
		"variants/linux-x86_64-wasm/plugin.wasm": wasmgen.CannedResponder([]byte(`{"jsonrpc":"2.0","id":"h1","result":{"status":"succeeded","operation_result":` + result + `}}`)),
		"settings.json":                          []byte(settings),
	}
	manifest := packagetest.BuildManifestJSON(id, "0.1.0",
		[]packagetest.InterfaceSpec{{ID: "models.probe", Version: 1, SchemaFile: "interfaces/models.probe.v1.schema.json", Methods: []string{"models"}}},
		[]packagetest.VariantSpec{{
			ID: "linux-x86_64-wasm", Platform: packagefmt.HostPlatform(), Arch: packagefmt.HostArch(),
			Topology: packagefmt.HostTopology(), Runtime: "wasm_component", Profile: "wasm_sandbox",
			Entrypoint: "variants/linux-x86_64-wasm/plugin.wasm",
		}},
		files, nil)
	pkg := filepath.Join(dir, id+"-0.1.0.aiiospkg")
	if err := os.WriteFile(pkg, packagetest.Build(packagetest.PackageSpec{Root: id + "-0.1.0", Manifest: manifest, InstallFiles: files}), 0o644); err != nil {
		t.Fatal(err)
	}
	installPluginDir(t, dir, id, pkg)
	t.Chdir(dir)
	app := New(&Config{
		Identity: IdentityConfig{KeyPath: filepath.Join(dir, "identity.sec"),
			LedgerPath: filepath.Join(dir, "ledger.jsonl"), DBPath: filepath.Join(dir, "aii.db")},
		LLM:        withTestProvider(t, dir, "test", "https://127.0.0.1:1", "m", "sk-x"),
		SourcePath: filepath.Join(dir, "config.json"),
		Dashboard:  DashboardConfig{Port: 0},
		Tools:      ToolsConfig{CWD: dir},
		Plugins:    PluginsConfig{Autoload: "T0"},
		Agency:     defaultConfig().Agency,
	})
	if err := startLiveForTest(app); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { stopApp(t, app) })
	return app
}

const modelSetting = `[{"key":"model","type":"string","title":"Model","default":"jev-latest","choices_from":"models"}]`

func TestAPluginLooksUpASettingsChoices(t *testing.T) {
	const id = "org.example.models"
	app := choicesApp(t, id, modelSetting, "read.external",
		`{"choices":[{"value":"jev-latest","label":"The latest Jev"},{"value":"jev-preview"}]}`)
	state, err := app.applyConfigChange(map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	if s := state.Plugins.Installed[0].Settings[0]; !s.ChoicesFrom || s.Key != "model" {
		t.Fatalf("the card is told the setting looks up its choices: %+v", s)
	}
	choices, err := app.PluginSettingChoices(id, "model")
	if err != nil || len(choices) != 2 || choices[0].Value != "jev-latest" || choices[0].Label != "The latest Jev" || choices[1].Value != "jev-preview" {
		t.Fatalf("the plugin's choices come back whole: %v %+v", err, choices)
	}
	if _, err := app.applyConfigChange(map[string]any{"plugins.settings." + id + ".model": "jev-1.13.0"}); err != nil {
		t.Fatalf("a value the list does not name is still a string the setting holds: %v", err)
	}
	if _, err := app.PluginSettingChoices(id, "absent"); err == nil {
		t.Fatal("a setting that looks up nothing is refused by name")
	}
}

// Rendering a page must never be what makes a plugin act: an operation
// whose signed descriptor is not a read is refused before it is called.
func TestSettingChoicesComeOnlyFromARead(t *testing.T) {
	app := choicesApp(t, "org.example.writes", modelSetting, "write.external", `{"choices":[{"value":"x"}]}`)
	if _, err := app.PluginSettingChoices("org.example.writes", "model"); err == nil || !strings.Contains(err.Error(), "not a read") {
		t.Fatalf("a write is refused by name: %v", err)
	}
}

// An answer outside the bounds is refused whole, with the reason.
func TestSettingChoicesOutsideTheirBoundsAreRefused(t *testing.T) {
	app := choicesApp(t, "org.example.twice", modelSetting, "read.internal", `{"choices":[{"value":"a"},{"value":"a"}]}`)
	if _, err := app.PluginSettingChoices("org.example.twice", "model"); err == nil || !strings.Contains(err.Error(), "offered twice") {
		t.Fatalf("a malformed answer is refused with its reason: %v", err)
	}
}

// A setting that names an operation the package does not declare is a
// declaration the host does not honor: the activation refuses.
func TestChoicesFromMustNameADeclaredOperation(t *testing.T) {
	logs := logsink.CaptureForTest(t)
	app := choicesApp(t, "org.example.undeclared", `[{"key":"model","type":"string","title":"Model","choices_from":"list_models"}]`, "read.internal", `{"choices":[]}`)
	if len(app.plugins) != 0 {
		t.Fatalf("a package whose setting names no declared operation is not activated: %+v", app.plugins)
	}
	// Refusal state is published before its log is written. Settled startup
	// proves the activation ended, not that this later observation is ready.
	waitUntil(t, func() bool {
		return logs.Contains(`is not a method this package declares`) && logs.Contains("list_models")
	}, "the refusal log names the undeclared choices operation")
	if !logs.Contains(`is not a method this package declares`) || !logs.Contains("list_models") {
		t.Fatalf("the activation is refused for that reason, by name:\n%s", logs.String())
	}
}

// heldRead is a plugin's read operation that answers only when its call's
// context ends.
type heldRead struct {
	name    string
	owner   *pluginhost.ActivePlugin
	entered chan struct{}
}

func (h *heldRead) DispatchOwner() any  { return h.owner }
func (h *heldRead) Name() string        { return h.name }
func (h *heldRead) Description() string { return "a read that waits for its context" }
func (h *heldRead) Parameters() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{}}
}
func (h *heldRead) ReplaySafe() bool { return true }
func (h *heldRead) Execute(ctx context.Context, _ map[string]any) (tools.Result, error) {
	close(h.entered)
	<-ctx.Done()
	return tools.Result{}, ctx.Err()
}

// A PAGE'S LOOKUP ENDS WITH THE IDENTITY (T19). A choices lookup waits on
// the plugin for up to choicesTimeout. Rooted on Background it went on
// for that whole bound after the identity had stopped, its page waiting
// on it; rooted on the lifetime, Shutdown's first act ends it.
func TestAChoicesLookupInFlightEndsWhenTheIdentityStops(t *testing.T) {
	a := liveApp(t)
	const id = "org.example.held"
	ap := &pluginhost.ActivePlugin{ID: id, Settings: []pluginhost.SettingDecl{{Key: "model", Type: "string", Title: "Model", ChoicesFrom: "models"}}}
	read := &heldRead{name: pluginhost.ToolNameFor(id, "models"), owner: ap, entered: make(chan struct{})}
	if err := a.toolReg.RegisterHostOp(read, id); err != nil {
		t.Fatal(err)
	}
	addPlugin(a, ap)
	done := make(chan error, 1)
	go func() { _, err := a.PluginSettingChoices(id, "model"); done <- err }()
	select {
	case <-read.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the lookup never reached the plugin")
	}
	stopApp(t, a)
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("the lookup ends because the identity stopped: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the lookup outlived the identity's stop: it waits out its own bound")
	}
}

// ownedOp is one activation's operation: a read whose check can be held
// open (ReplaySafe waits for release once checked is closed), or a write.
// Each counts its calls.
type ownedOp struct {
	name    string
	owner   *pluginhost.ActivePlugin
	read    bool
	checked chan struct{}
	release chan struct{}
	once    sync.Once
	calls   atomic.Int32
}

func (o *ownedOp) DispatchOwner() any  { return o.owner }
func (o *ownedOp) Name() string        { return o.name }
func (o *ownedOp) Description() string { return "one activation's operation" }
func (o *ownedOp) Parameters() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{}}
}
func (o *ownedOp) ReplaySafe() bool {
	if o.checked != nil {
		o.once.Do(func() { close(o.checked) })
		<-o.release
	}
	return o.read
}
func (o *ownedOp) Execute(context.Context, map[string]any) (tools.Result, error) {
	o.calls.Add(1)
	return tools.Result{Output: `{"choices":[{"value":"a"}]}`}, nil
}

// swapDuringCheck runs one choices lookup of id's "model" setting whose read
// check (read.ReplaySafe) is held open while replacement takes the read's
// name for a new activation of id, and returns the lookup's error.
func swapDuringCheck(t *testing.T, a *App, id string, read *ownedOp, replacement tools.Tool, next *pluginhost.ActivePlugin) error {
	t.Helper()
	read.checked, read.release = make(chan struct{}), make(chan struct{})
	done := make(chan error, 1)
	go func() { _, err := a.PluginSettingChoices(id, "model"); done <- err }()
	select {
	case <-read.checked:
	case <-time.After(5 * time.Second):
		t.Fatal("the lookup never asked whether the operation is a read")
	}
	if err := a.toolReg.SupersedeOrigin(id, []tools.Tool{replacement}, map[string]bool{replacement.Name(): true}); err != nil {
		t.Fatal(err)
	}
	setPlugins(a, []*pluginhost.ActivePlugin{next})
	close(read.release)
	select {
	case err := <-done:
		return err
	case <-time.After(5 * time.Second):
		t.Fatal("the lookup did not end")
	}
	return nil
}

// choicesSwapApp is a live identity with one plugin whose "models" read
// answers the "model" setting's choices.
func choicesSwapApp(t *testing.T, id string) (*App, []pluginhost.SettingDecl, *ownedOp) {
	t.Helper()
	a := liveApp(t)
	settings := []pluginhost.SettingDecl{{Key: "model", Type: "string", Title: "Model", ChoicesFrom: "models"}}
	old := &pluginhost.ActivePlugin{ID: id, Settings: settings}
	read := &ownedOp{name: pluginhost.ToolNameFor(id, "models"), owner: old, read: true}
	if err := a.toolReg.RegisterHostOp(read, id); err != nil {
		t.Fatal(err)
	}
	addPlugin(a, old)
	return a, settings, read
}

// THE READ A LOOKUP CHECKED IS THE READ IT CALLS. The lookup asks whether the operation is a read, and only then calls
// it by name. A plugin replaced in between, its new release offering a
// write under the same name, must not have that write run because a page
// opened: the call is bound to the activation whose setting and read were
// checked, and the replacement is refused with no call made. With nothing
// replaced, the same read is called once and its choices come back.
func TestTheReadAChoicesLookupCheckedIsTheReadItCalls(t *testing.T) {
	const id = "org.example.swapped"
	a, settings, read := choicesSwapApp(t, id)

	// Unchanged: the read is called once and answers.
	choices, err := a.PluginSettingChoices(id, "model")
	if err != nil || len(choices) != 1 || choices[0].Value != "a" || read.calls.Load() != 1 {
		t.Fatalf("an unchanged read is called once and answers: %v %+v, %d calls", err, choices, read.calls.Load())
	}

	// Replaced between the check and the call.
	next := &pluginhost.ActivePlugin{ID: id, Settings: settings}
	write := &ownedOp{name: read.name, owner: next, read: false}
	err = swapDuringCheck(t, a, id, read, write, next)
	if err == nil || write.calls.Load() != 0 {
		t.Fatalf("a write that replaced the checked read was called by a page's lookup: err %v, %d calls", err, write.calls.Load())
	}
	if read.calls.Load() != 1 {
		t.Fatalf("the replaced read was called again: %d calls", read.calls.Load())
	}
}

// confirmOp is an activation's operation declared operator_confirms. As the
// plugin host's does (internal/pluginhost/optool.go, t.acts.Propose), a call
// it is given proposes it for the operator's confirmation through the
// host's proposer, and runs nothing.
type confirmOp struct {
	ownedOp
	a *App
}

func (o *confirmOp) Execute(ctx context.Context, args map[string]any) (tools.Result, error) {
	o.calls.Add(1)
	id, err := actProposer{o.a}.Propose(ctx, pluginhost.ActProposal{Plugin: o.owner.ID, Tool: o.name, Operation: "models",
		Summary: "a write that needs the operator", Effects: "write.external", Args: args})
	if err != nil {
		return tools.Result{Error: err.Error()}, nil
	}
	return tools.Result{Output: `{"proposed":true,"act":"` + id + `"}`}, nil
}

// A PAGE'S LOOKUP PROPOSES NOTHING FOR THE OPERATOR EITHER. Where the name
// the lookup checked as a read resolves, by the time it is called, to another
// activation's operation that needs the operator's confirmation, calling it
// would put a pending act on the Plugins page that no one asked for. The
// call is refused before the operation is given it: no act is pending.
func TestAChoicesLookupProposesNothingForWhatReplacedItsRead(t *testing.T) {
	const id = "org.example.proposes"
	a, settings, read := choicesSwapApp(t, id)
	next := &pluginhost.ActivePlugin{ID: id, Settings: settings}
	confirm := &confirmOp{ownedOp: ownedOp{name: read.name, owner: next, read: false}, a: a}
	err := swapDuringCheck(t, a, id, read, confirm, next)
	a.acts.mu.Lock()
	pending := len(a.acts.acts)
	a.acts.mu.Unlock()
	if err == nil || confirm.calls.Load() != 0 || pending != 0 {
		t.Fatalf("a page's lookup proposed an act for the operator: err %v, %d calls, %d acts pending", err, confirm.calls.Load(), pending)
	}
}
