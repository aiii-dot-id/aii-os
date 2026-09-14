package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/audio"
	"github.com/aiii-dot-id/aii-os/internal/genesis"
	"github.com/aiii-dot-id/aii-os/internal/genesis/genesistest"
	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
	"github.com/aiii-dot-id/aii-os/internal/packagefmt/packagetest"
	"github.com/aiii-dot-id/aii-os/internal/pluginworker/wasmgen"
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
func TestAnOperatorActIsConfirmedOnceAndBoundToItsArguments(t *testing.T) {
	dir := t.TempDir()
	result := genesistest.NewRoot(t).Birth(t, genesis.BirthConfig{
		Name:       "ActTest",
		KeyPath:    filepath.Join(dir, "identity.sec"),
		LedgerPath: filepath.Join(dir, "ledger.jsonl"),
		DBPath:     filepath.Join(dir, "aii.db"),
	})
	result.Ledger.Close()
	const id = "org.example.uid"
	files := map[string][]byte{
		"interfaces/speaker.uid.v1.schema.json":  []byte(`[{"id":"enroll","summary":"Enroll a speaker from three finals","effects":"write.local","capabilities":[],"operator_confirms":true}]`),
		"variants/linux-x86_64-wasm/plugin.wasm": wasmgen.Responder(),
	}
	manifest := packagetest.BuildManifestJSON(id, "0.1.0",
		[]packagetest.InterfaceSpec{{ID: "speaker.uid", Version: 1, SchemaFile: "interfaces/speaker.uid.v1.schema.json", Methods: []string{"enroll"}}},
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
	if len(app.plugins) != 1 {
		t.Fatalf("the plugin must be active: %+v", app.plugins)
	}
	tool := app.plugins[0].ToolNames[0]
	ctx := context.Background()

	// .
	done := make(chan struct{})
	app.voiceSessions.Store("vs-1", &voiceHandle{id: "vs-1", v: &fakeEngineSession{}, b: &audio.Binding{InputHandle: "in-vs-1", Contained: true}, done: done, drained: make(chan struct{})})
	app.rememberFinal("vs-1", 3, "my name is Sam")
	app.rememberFinal("vs-1", 5, "and this is my voice")
	app.rememberFinal("vs-1", 8, "enroll me")

	args := map[string]interface{}{"session_id": "vs-1", "finals": []interface{}{3.0, 5.0, 8.0}, "label": "Sam"}
	res, err := app.toolReg.Execute(ctx, tool, args)
	if err != nil || !strings.Contains(res.Output, `"proposed":true`) {
		t.Fatalf("the call proposes: %v %+v", err, res)
	}
	again, _ := app.toolReg.Execute(ctx, tool, args)
	if again.Output != res.Output {
		t.Fatalf("re-asking the same thing answers with the same act: %s vs %s", again.Output, res.Output)
	}
	state, err := app.applyConfigChange(map[string]interface{}{})
	if err != nil {
		t.Fatal(err)
	}
	acts := state.Plugins.Installed[0].Acts
	if len(acts) != 1 || acts[0].Operation != "enroll" || acts[0].Args["label"] != "Sam" || acts[0].Session != "vs-1" || len(acts[0].Finals) != 3 || acts[0].Finals[1].Text != "and this is my voice" || !acts[0].Finals[1].Heard {
		t.Fatalf("the view shows the act, its arguments and what was heard: %+v", acts)
	}
	actID := acts[0].ID
	if !strings.Contains(res.Output, actID) {
		t.Fatalf("the identity was told the act's id: %s", res.Output)
	}

	// .
	other := map[string]interface{}{"session_id": "vs-1", "finals": []interface{}{3.0, 5.0, 8.0}, "label": "Jim"}
	if r, _ := app.toolReg.Execute(ctx, tool, other); strings.Contains(r.Output, actID) {
		t.Fatal("changed arguments are a different act")
	}
	// .
	if r, _ := app.toolReg.Execute(ctx, tool, map[string]interface{}{"session_id": "vs-1", "finals": []interface{}{99.0}, "label": "x"}); !strings.Contains(r.Error, "never heard") {
		t.Fatalf("an unheard final: %+v", r)
	}
	if r, _ := app.toolReg.Execute(ctx, tool, map[string]interface{}{"session_id": "vs-9", "label": "x"}); !strings.Contains(r.Error, "not open") {
		t.Fatalf("an unknown session: %+v", r)
	}

	// .
	state, _ = app.applyConfigChange(map[string]interface{}{})
	var otherID string
	for _, a := range state.Plugins.Installed[0].Acts {
		if a.Args["label"] == "Jim" {
			otherID = a.ID
		}
	}
	if err := app.decideAct(ctx, id, otherID, false); err != nil {
		t.Fatal(err)
	}
	if err := app.decideAct(ctx, id, otherID, true); err == nil {
		t.Fatal("a denied act is gone")
	}

	// .
	if err := app.decideAct(ctx, "org.example.other", actID, true); err == nil {
		t.Fatal("an act belongs to its plugin")
	}
	if err := app.decideAct(ctx, id, actID, true); err != nil {
		t.Fatalf("confirm: %v", err)
	}
	if err := app.decideAct(ctx, id, actID, true); err == nil {
		t.Fatal("the same act cannot be confirmed twice")
	}
	turns, err := app.store.RecentTurns(4)
	if err != nil {
		t.Fatal(err)
	}
	var recorded string
	for _, tr := range turns {
		if strings.Contains(tr.Content, "[act "+actID+"] confirmed enroll") {
			recorded = tr.Content
		}
	}
	if recorded == "" || !strings.Contains(recorded, `"echoed":true`) || !strings.Contains(recorded, `"label":"Sam"`) {
		t.Fatalf("the outcome is recorded as the operator's, with the arguments and the guest's answer: %q (%d turns)", recorded, len(turns))
	}
	var denied string
	for _, tr := range turns {
		if strings.Contains(tr.Content, "[act "+otherID+"] denied enroll") {
			denied = tr.Content
		}
	}
	if denied == "" {
		t.Fatal("a denial is recorded too")
	}
	state, _ = app.applyConfigChange(map[string]interface{}{})
	if len(state.Plugins.Installed[0].Acts) != 0 {
		t.Fatalf("nothing awaits the operator: %+v", state.Plugins.Installed[0].Acts)
	}

	// .
	r, _ := app.toolReg.Execute(ctx, tool, map[string]interface{}{"session_id": "vs-1", "finals": []interface{}{3.0}, "label": "Late"})
	state, _ = app.applyConfigChange(map[string]interface{}{})
	lateID := state.Plugins.Installed[0].Acts[0].ID
	if !strings.Contains(r.Output, lateID) {
		t.Fatal("the late act")
	}
	close(done)
	app.voiceSessions.Delete("vs-1")
	if err := app.decideAct(ctx, id, lateID, true); err == nil || !strings.Contains(err.Error(), "has ended") {
		t.Fatalf("a session that ended refuses the act: %v", err)
	}

	// .
	r, _ = app.toolReg.Execute(ctx, tool, map[string]interface{}{"label": "Old"})
	app.acts.mu.Lock()
	for _, act := range app.acts.acts {
		act.Expires = time.Now().Add(-time.Second)
	}
	app.acts.mu.Unlock()
	state, _ = app.applyConfigChange(map[string]interface{}{})
	if len(state.Plugins.Installed[0].Acts) != 0 {
		t.Fatal("an expired act is not shown")
	}
	_ = r
}
