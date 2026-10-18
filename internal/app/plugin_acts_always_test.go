package app

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/broker"
	"github.com/aiii-dot-id/aii-os/internal/dashboard"
	"github.com/aiii-dot-id/aii-os/internal/genesis"
	"github.com/aiii-dot-id/aii-os/internal/genesis/genesistest"
	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
	"github.com/aiii-dot-id/aii-os/internal/packagefmt/packagetest"
	"github.com/aiii-dot-id/aii-os/internal/pluginworker/wasmgen"
)

// actsApp is a live app with one T0 plugin whose enroll declares
// operator_confirms (write.local) and whose list is a read; the guest
// echoes its arguments back. Returns the app, the plugin id, and the
// two tool names.
func actsApp(t *testing.T) (*App, string, string, string) {
	t.Helper()
	dir := t.TempDir()
	result := genesistest.NewRoot(t).Birth(t, genesis.BirthConfig{
		Name:       "AlwaysTest",
		KeyPath:    filepath.Join(dir, "identity.sec"),
		LedgerPath: filepath.Join(dir, "ledger.jsonl"),
		DBPath:     filepath.Join(dir, "aii.db"),
	})
	result.Ledger.Close()
	const id = "org.example.uid"
	files := map[string][]byte{
		"interfaces/speaker.uid.v1.schema.json":  []byte(`[{"id":"enroll","summary":"Enroll a speaker","effects":"write.local","capabilities":[],"operator_confirms":true},{"id":"list","summary":"List speakers","effects":"read.internal","capabilities":[]}]`),
		"variants/linux-x86_64-wasm/plugin.wasm": wasmgen.Responder(),
	}
	manifest := packagetest.BuildManifestJSON(id, "0.1.0",
		[]packagetest.InterfaceSpec{{ID: "speaker.uid", Version: 1, SchemaFile: "interfaces/speaker.uid.v1.schema.json", Methods: []string{"enroll", "list"}}},
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
	// The default configuration, so a change the operator's Always
	// writes validates whole (prompt, agency, every section present).
	cfg := defaultConfig()
	cfg.Identity = IdentityConfig{KeyPath: filepath.Join(dir, "identity.sec"),
		LedgerPath: filepath.Join(dir, "ledger.jsonl"), DBPath: filepath.Join(dir, "aii.db")}
	cfg.LLM = withTestProvider(t, dir, "test", "https://127.0.0.1:1", "m", "sk-x")
	cfg.SourcePath = filepath.Join(dir, "config.json")
	cfg.Dashboard = DashboardConfig{Port: 0}
	cfg.Tools = ToolsConfig{CWD: dir}
	cfg.Plugins = PluginsConfig{Autoload: "T0"}
	app := New(cfg)
	t.Cleanup(func() { stopApp(t, app) })
	// This fixture proves act/grant semantics, not concurrent file reload.
	// Park the governed watchers BEFORE they start; a self-save must not race
	// the next explicit act with the watcher's normalization pass. Dedicated
	// reload/supersession tests exercise that owner separately.
	app.gate.Pause()
	if err := startLiveForTest(app); err != nil {
		t.Fatal(err)
	}
	if !app.gate.Paused() {
		t.Fatal("act fixture started concurrent periodic watchers")
	}
	if len(app.plugins) != 1 || len(app.plugins[0].ToolNames) != 2 {
		t.Fatalf("the plugin must be active with two operations: %+v", app.plugins)
	}
	var enroll, list string
	for _, name := range app.plugins[0].ToolNames {
		if strings.HasSuffix(name, "enroll") {
			enroll = name
		} else {
			list = name
		}
	}
	return app, id, enroll, list
}

func pendingActIDs(t *testing.T, app *App) []string {
	t.Helper()
	state, err := app.applyConfigChange(map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, a := range state.Plugins.Installed[0].Acts {
		ids = append(ids, a.ID)
	}
	return ids
}

// "ALWAYS" IS THE OPERATOR'S STANDING WORD:
// it runs the act once and writes the operation into auto_confirm, both
// recorded; the next call of that operation runs at once, stamped auto,
// with nothing proposed; revoked, the call proposes again; under SAFE
// the call proposes and the standing word is ignored.
func TestAlwaysRunsTheActAndStandsUntilRevoked(t *testing.T) {
	app, id, enroll, _ := actsApp(t)
	ctx := context.Background()
	args := map[string]any{"label": "Quinn"}
	res, err := app.toolReg.Execute(ctx, enroll, args)
	if err != nil || !strings.Contains(res.Output, `"proposed":true`) {
		t.Fatalf("the first call proposes: %v %+v", err, res)
	}
	ids := pendingActIDs(t, app)
	if len(ids) != 1 {
		t.Fatalf("one act awaits: %v", ids)
	}
	if err := app.alwaysAct(ctx, "org.example.other", ids[0]); err == nil {
		t.Fatal("an act belongs to its plugin")
	}
	if err := app.alwaysAct(ctx, id, ids[0]); err != nil {
		t.Fatalf("always: %v", err)
	}
	if g := app.configSnapshot().Plugins.Grants[id]; len(g.AutoConfirm) != 1 || g.AutoConfirm[0] != "enroll" {
		t.Fatalf("the standing word is written: %+v", g)
	}
	onDisk, err := LoadConfig(app.configSnapshot().SourcePath)
	if err != nil {
		t.Fatal(err)
	}
	if g := onDisk.Plugins.Grants[id]; len(g.AutoConfirm) != 1 || g.AutoConfirm[0] != "enroll" {
		t.Fatalf("and published: %+v", g)
	}
	turns, _ := app.store.RecentTurns(4)
	var recorded string
	for _, tr := range turns {
		if strings.Contains(tr.Content, "[act "+ids[0]+"] confirmed, and always from now on") {
			recorded = tr.Content
		}
	}
	if recorded == "" || strings.Contains(recorded, `"echoed":true`) || !strings.Contains(recorded, "revoked on the Plugins page") {
		t.Fatalf("the outcome turn says the act ran and the word stands: %q", recorded)
	}
	var resultExcerpt string
	if err := app.store.DB().QueryRow(`SELECT content FROM conversations WHERE kind='tool_result' AND turn_id=?`, "operator_act_"+ids[0]).Scan(&resultExcerpt); err != nil || !strings.Contains(resultExcerpt, `"echoed":true`) {
		t.Fatalf("Always lost the linked result: %q %v", resultExcerpt, err)
	}
	if ids := pendingActIDs(t, app); len(ids) != 0 {
		t.Fatalf("the act is consumed: %v", ids)
	}

	// The next call runs at once, stamped auto, nothing proposed.
	res, err = app.toolReg.Execute(ctx, enroll, map[string]any{"label": "Jim"})
	// The guest answers (the auto stamp itself is proven at the seam,
	// pluginhost/acts_test.go); nothing is proposed.
	if err != nil || res.Error != "" || strings.Contains(res.Output, "proposed") || !strings.Contains(res.Output, `"echoed":true`) {
		t.Fatalf("a standing confirmation runs the call at once: %v %+v", err, res)
	}
	if ids := pendingActIDs(t, app); len(ids) != 0 {
		t.Fatalf("nothing proposed under a standing word: %v", ids)
	}

	// Revoked on the card (the list without it), the call proposes again.
	if _, err := app.applyConfigChange(map[string]any{"plugins.grants." + id + ".auto_confirm": []any{}}); err != nil {
		t.Fatal(err)
	}
	if _, ok := app.configSnapshot().Plugins.Grants[id]; ok {
		t.Fatal("an entry granting nothing is removed")
	}
	res, _ = app.toolReg.Execute(ctx, enroll, map[string]any{"label": "Jo"})
	if !strings.Contains(res.Output, `"proposed":true`) {
		t.Fatalf("revoked, the call proposes: %+v", res)
	}
	ids = pendingActIDs(t, app)

	// Under SAFE the standing word is ignored: the registry refuses a
	// non-read tool before the host is reached, the seam answers no
	// standing word, and a confirm of what was pending refuses too.
	if err := app.alwaysAct(ctx, id, ids[0]); err != nil {
		t.Fatal(err)
	}
	res, _ = app.toolReg.Execute(ctx, enroll, map[string]any{"label": "Pending"})
	if !strings.Contains(res.Output, `"echoed":true`) {
		t.Fatalf("the standing word runs the call before SAFE: %+v", res)
	}
	app.enterSafe("test: the record is frozen")
	if _, ok := app.standingConfirmation(id, "enroll"); ok {
		t.Fatal("SAFE ignores auto_confirm at the seam")
	}
	res, _ = app.toolReg.Execute(ctx, enroll, map[string]any{"label": "Safe"})
	if res.Error == "" || !strings.Contains(res.Error, "safe mode") || strings.Contains(res.Output, "echoed") {
		t.Fatalf("under SAFE nothing runs: %+v", res)
	}
	if ids := pendingActIDs(t, app); len(ids) != 0 {
		t.Fatalf("nothing proposed under SAFE: %v", ids)
	}
}

// READ ONLY IS THE CONNECT SCOPE: the operator's one box on the plugin's
// card refuses, before any proposal, an operation that writes or
// executes — the identity reads why — and lets a read operation run;
// cleared, the write operation is proposed as before.
func TestReadOnlyRefusesWritesBeforeAnyProposal(t *testing.T) {
	app, id, enroll, list := actsApp(t)
	ctx := context.Background()
	if _, err := app.applyConfigChange(map[string]any{"plugins.grants." + id + ".read_only": true}); err != nil {
		t.Fatal(err)
	}
	res, err := app.toolReg.Execute(ctx, enroll, map[string]any{"label": "Quinn"})
	if err != nil || !strings.Contains(res.Error, "read only") || !strings.Contains(res.Error, "write.local") || strings.Contains(res.Output, "proposed") {
		t.Fatalf("a write operation is refused by name under read only: %v %+v", err, res)
	}
	if ids := pendingActIDs(t, app); len(ids) != 0 {
		t.Fatalf("nothing is proposed under read only: %v", ids)
	}
	if res, err := app.toolReg.Execute(ctx, list, map[string]any{"q": "x"}); err != nil || res.Error != "" || !strings.Contains(res.Output, `"echoed":true`) {
		t.Fatalf("a read operation runs under read only: %v %+v", err, res)
	}
	state, _ := app.applyConfigChange(map[string]any{})
	if g := state.Plugins.Installed[0].Grants; !g.ReadOnly || !g.Listed {
		t.Fatalf("the view says read only: %+v", g)
	}
	if _, err := app.applyConfigChange(map[string]any{"plugins.grants." + id + ".read_only": false}); err != nil {
		t.Fatal(err)
	}
	if res, _ := app.toolReg.Execute(ctx, enroll, map[string]any{"label": "Quinn"}); !strings.Contains(res.Output, `"proposed":true`) {
		t.Fatalf("cleared, the write operation is proposed: %+v", res)
	}
}

func TestReadOnlyAndStandingConfirmationGrants(t *testing.T) {
	const id = "org.example.uid"
	cfg := defaultConfig()
	cfg.SourcePath = filepath.Join(t.TempDir(), "config.json")
	cfg.Plugins.Grants = map[string]broker.Grant{id: {KV: true, Local: []string{"192.168.1.0/24:*"}}}
	if _, err := saveConfig(cfg); err != nil {
		t.Fatal(err)
	}
	a := New(cfg)
	for _, v := range []any{"enroll", true, []any{"enroll", 3}} {
		if _, err := a.applyConfigChange(map[string]any{"plugins.grants." + id + ".auto_confirm": v}); err == nil {
			t.Fatalf("auto_confirm=%v must be refused", v)
		}
	}
	if _, err := a.applyConfigChange(map[string]any{
		"plugins.grants." + id + ".auto_confirm": []any{" enroll ", "enroll", "", "list"},
		"plugins.grants." + id + ".read_only":    true,
	}); err != nil {
		t.Fatal(err)
	}
	g := a.configSnapshot().Plugins.Grants[id]
	if !g.ReadOnly || len(g.AutoConfirm) != 2 || g.AutoConfirm[0] != "enroll" || g.AutoConfirm[1] != "list" || !g.KV || len(g.Local) != 1 {
		t.Fatalf("live grant = %+v", g)
	}
	onDisk, err := LoadConfig(cfg.SourcePath)
	if err != nil {
		t.Fatal(err)
	}
	if d := onDisk.Plugins.Grants[id]; !d.ReadOnly || len(d.AutoConfirm) != 2 || len(d.Local) != 1 {
		t.Fatalf("published grant = %+v", d)
	}
	// Withdrawing the booleans keeps an entry that still carries a list …
	if _, err := a.applyConfigChange(map[string]any{
		"plugins.grants." + id + ".kv":           false,
		"plugins.grants." + id + ".read_only":    false,
		"plugins.grants." + id + ".auto_confirm": []string{},
	}); err != nil {
		t.Fatal(err)
	}
	if g, ok := a.configSnapshot().Plugins.Grants[id]; !ok || len(g.Local) != 1 || g.ReadOnly || len(g.AutoConfirm) != 0 {
		t.Fatalf("a grant with a local list stays listed: %+v (%v)", g, ok)
	}
	// … and one that carries only read_only stays too.
	const other = "org.example.other"
	if _, err := a.applyConfigChange(map[string]any{"plugins.grants." + other + ".read_only": true}); err != nil {
		t.Fatal(err)
	}
	if g, ok := a.configSnapshot().Plugins.Grants[other]; !ok || !g.ReadOnly {
		t.Fatalf("read only alone is an entry: %+v", g)
	}
	if _, err := a.applyConfigChange(map[string]any{"plugins.grants." + other + ".read_only": false}); err != nil {
		t.Fatal(err)
	}
	if _, ok := a.configSnapshot().Plugins.Grants[other]; ok {
		t.Fatal("an entry granting nothing is removed")
	}
	if v := pluginGrantView(broker.Grant{ReadOnly: true, AutoConfirm: []string{"enroll"}}); !v.ReadOnly || len(v.AutoConfirm) != 1 {
		t.Fatalf("the view carries both: %+v", v)
	}
}

// THE SCOPE IS THE OPERATOR'S WORD, SAID TWICE: a connect answer that names an
// installed plugin sets its read_only grant from the chosen scope — read
// only narrows, read and modify widens — while a connector that is not an
// installed plugin changes no grant.
func TestAConnectAnswerSetsTheInstalledPluginsReadOnlyGrant(t *testing.T) {
	app, id, _, _ := actsApp(t)
	if err := app.store.StartWorkSession(t.Context(), "ws_c", "read the issues"); err != nil {
		t.Fatal(err)
	}
	if err := app.proposeAsk("ws_c", "I need your GitHub to read the issues.", nil, id); err != nil {
		t.Fatal(err)
	}
	v := app.askViews()[0]
	if _, _, err := app.answerAsk(dashboard.AskAnswer{ID: v.ID, Answer: "connect", Scope: "read"}); err != nil {
		t.Fatal(err)
	}
	if g := app.configSnapshot().Plugins.Grants[id]; !g.ReadOnly {
		t.Fatalf("read only follows the answer: %+v", g)
	}
	if err := app.proposeAsk("ws_c", "And to file them too.", nil, id); err != nil {
		t.Fatal(err)
	}
	v = app.askViews()[0]
	if _, _, err := app.answerAsk(dashboard.AskAnswer{ID: v.ID, Answer: "connect", Scope: "modify"}); err != nil {
		t.Fatal(err)
	}
	if g, ok := app.configSnapshot().Plugins.Grants[id]; ok && g.ReadOnly {
		t.Fatalf("read and modify lifts it: %+v", g)
	}
	if err := app.proposeAsk("ws_c", "I need your email.", nil, "email"); err != nil {
		t.Fatal(err)
	}
	v = app.askViews()[0]
	before := len(app.configSnapshot().Plugins.Grants)
	if _, _, err := app.answerAsk(dashboard.AskAnswer{ID: v.ID, Answer: "connect", Scope: "read"}); err != nil {
		t.Fatal(err)
	}
	if len(app.configSnapshot().Plugins.Grants) != before {
		t.Fatal("a connector that is not an installed plugin changes no grant")
	}
}

// A GRANT NOT WRITTEN IS NO ANSWER: when the installed plugin's read_only
// grant cannot be persisted, the connect answer returns the failure (the
// persistence error itself, not a retelling) instead of claiming the
// scope; the grant is unchanged, nothing is said to the identity, and the
// question is kept, so pressing again once the write can land answers it.
func TestAConnectAnswerCarriesAFailedGrantWrite(t *testing.T) {
	app, id, _, _ := actsApp(t)
	if err := app.store.StartWorkSession(t.Context(), "ws_g", "read the issues"); err != nil {
		t.Fatal(err)
	}
	if err := app.proposeAsk("ws_g", "I need your GitHub to read the issues.", nil, id); err != nil {
		t.Fatal(err)
	}
	v := app.askViews()[0]
	// The configuration cannot be persisted: its directory is a file.
	blocker := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(blocker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	app.cfgMu.Lock()
	source := app.cfg.SourcePath
	app.cfg.SourcePath = filepath.Join(blocker, "config.json")
	app.cfgMu.Unlock()
	text, _, err := app.answerAsk(dashboard.AskAnswer{ID: v.ID, Answer: "connect", Scope: "read"})
	var pathErr *fs.PathError
	if err == nil || text != "" || !errors.As(err, &pathErr) {
		t.Fatalf("the failed grant write is the answer: %q, %v", text, err)
	}
	if g := app.configSnapshot().Plugins.Grants[id]; g.ReadOnly {
		t.Fatalf("the scope that was not written is not claimed: %+v", g)
	}
	if asks := app.askViews(); len(asks) != 1 || asks[0].ID != v.ID {
		t.Fatalf("the question is kept: %+v", asks)
	}
	app.cfgMu.Lock()
	app.cfg.SourcePath = source
	app.cfgMu.Unlock()
	text, _, err = app.answerAsk(dashboard.AskAnswer{ID: v.ID, Answer: "connect", Scope: "read"})
	if err != nil || !strings.Contains(text, "(read only)") {
		t.Fatalf("answered once the write lands: %q, %v", text, err)
	}
	if g := app.configSnapshot().Plugins.Grants[id]; !g.ReadOnly {
		t.Fatalf("read only follows the answer: %+v", g)
	}
}
