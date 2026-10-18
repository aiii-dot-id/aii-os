package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/genesis"
	"github.com/aiii-dot-id/aii-os/internal/genesis/genesistest"
	"github.com/aiii-dot-id/aii-os/internal/packagefmt/packagetest"
	"github.com/aiii-dot-id/aii-os/internal/pluginfacility"
	"github.com/aiii-dot-id/aii-os/internal/pluginhost"
	"github.com/aiii-dot-id/aii-os/internal/pluginworker"
	"github.com/aiii-dot-id/aii-os/internal/tools"
)

func buildSectionVer(t *testing.T, dir, id, sectionID, ver, title string) string {
	t.Helper()
	files := map[string][]byte{
		"section.json": []byte(`{"id":"` + sectionID + `","title":"` + title + `","slot":"panel","commands":[],"topics":["status"],"entry":"index.html"}`),
		"index.html":   []byte("<!DOCTYPE html><title>" + title + "</title>"),
	}
	manifest, err := json.Marshal(map[string]any{
		"kind": "asset", "id": id, "version": ver,
		"package_hash": packagetest.ReferencePackageHash(files), "asset_type": "static_template",
	})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, id+"-"+ver+".aiiospkg")
	if err := os.WriteFile(path, packagetest.Build(packagetest.PackageSpec{Root: id + "-" + ver, Manifest: manifest, InstallFiles: files}), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// A route can stay continuously registered while a selection made before the
// commit is refused before dispatch. Never retry or ignore an execution error.
type journeyPreDispatchRefusal struct{ code string }

func (e *journeyPreDispatchRefusal) Error() string { return e.code }

func TestTheConsumerJourneyThroughTheProduct(t *testing.T) {
	const (
		plug     = "org.example.journey"
		panel    = "org.example.panel"
		late     = "org.example.late"
		waits    = "org.example.waits"
		tool     = "pl_org_example_journey_ping"
		envelope = int64(pluginworker.DefaultMemoryMaxBytes) // what a WASM plugin reserves
	)
	dir := t.TempDir()
	result := genesistest.NewRoot(t).Birth(t, genesis.BirthConfig{
		Name: "Journey", KeyPath: filepath.Join(dir, "identity.sec"),
		LedgerPath: filepath.Join(dir, "ledger.jsonl"), DBPath: filepath.Join(dir, "aii.db"),
	})
	result.Ledger.Close()
	plugV1, plugV2 := buildResponderVer(t, dir, plug, "0.1.0", ""), buildResponderVer(t, dir, plug, "0.2.0", "")
	panelV1, panelV2 := buildSectionVer(t, dir, panel, "journey", "0.1.0", "Before"), buildSectionVer(t, dir, panel, "journey", "0.2.0", "After")
	// Built before the working directory moves: the fixtures are read
	// relative to the package.
	latePkg, waitsPkg := buildResponderPkg(t, dir, late), buildResponderPkg(t, dir, waits)
	installPluginDir(t, dir, plug, plugV1)
	installPluginDir(t, dir, panel, panelV1)
	t.Chdir(dir)

	config := func(budget int64) *Config {
		c := &Config{
			Identity: IdentityConfig{KeyPath: filepath.Join(dir, "identity.sec"),
				LedgerPath: filepath.Join(dir, "ledger.jsonl"), DBPath: filepath.Join(dir, "aii.db")},
			LLM:        withTestProvider(t, dir, "test", "https://127.0.0.1:1", "m", "sk-x"),
			SourcePath: filepath.Join(dir, "config.json"),
			Dashboard:  DashboardConfig{Port: 0},
			Tools:      ToolsConfig{CWD: dir},
			Plugins:    PluginsConfig{Autoload: "T0"},
			Agency:     defaultConfig().Agency,
		}
		c.Plugins.Runtime.AdmissionMemoryBudgetBytes = budget
		return c
	}
	app := New(config(0))
	if err := startLiveForTest(app); err != nil {
		t.Fatal(err)
	}
	stopped := false
	defer func() {
		if !stopped {
			stopApp(t, app)
		}
	}()

	view := func(a *App, id string) pluginfacility.InstanceView {
		for _, v := range a.pluginFacility().Snapshot().Instances {
			if v.ID == id {
				return v
			}
		}
		return pluginfacility.InstanceView{}
	}
	await := func(what string, ok func() bool) {
		t.Helper()
		deadline := time.Now().Add(45 * time.Second)
		for !ok() {
			if time.Now().After(deadline) {
				var states []string
				for _, v := range app.pluginFacility().Snapshot().Instances {
					states = append(states, fmt.Sprintf("%s=%s gens=%v residue=%v %s", v.ID, v.State, len(v.Activations), v.Residue, refusalReason(v.Refusal)))
				}
				t.Fatalf("timed out waiting for %s; facility: %v", what, states)
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	// alone: exactly one activation exists for the id, it serves, and
	// nothing it replaced still holds anything.
	alone := func(a *App, id string) bool {
		v := view(a, id)
		return v.State == pluginfacility.StateActive && len(v.Activations) == 1 && len(v.Residue) == 0
	}
	call := func(a *App) error {
		res, err := a.toolReg.Execute(context.Background(), tool, map[string]any{})
		if err != nil {
			return err
		}
		if res.Error != "" && (res.ReasonCode == tools.ReasonDispatchTargetChanged || res.ReasonCode == pluginhost.ReasonActivationWithdrawn) {
			if _, exists := a.toolReg.Get(tool); !exists {
				return fmt.Errorf("registry gap during pre-dispatch refusal: %+v", res)
			}
			return &journeyPreDispatchRefusal{res.ReasonCode}
		}
		if res.Error != "" || !strings.Contains(res.Output, `"echoed":true`) {
			return fmt.Errorf("%+v", res)
		}
		return nil
	}
	setBudget := func(b int64) {
		app.cfgMu.Lock()
		app.cfg.Plugins.Runtime.AdmissionMemoryBudgetBytes = b
		app.cfgMu.Unlock()
		app.rescanPlugins(context.Background())
	}

	// ── both serve ────────────────────────────────────────────────────
	if err := call(app); err != nil {
		t.Fatalf("the plugin does not answer: %v", err)
	}
	if sec, ok := app.sections.Get("journey"); !ok || sec.Decl.Title != "Before" {
		t.Fatalf("the section is not registered: %+v", sec)
	}

	// ── both are updated, the plugin WHILE IT IS BEING CALLED ─────────
	var calls, failed, refused int
	var firstFailure error
	var mu sync.Mutex
	stopCalls, callsDone := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(callsDone)
		for {
			select {
			case <-stopCalls:
				return
			default:
			}
			err := call(app)
			mu.Lock()
			calls++
			var beforeDispatch *journeyPreDispatchRefusal
			if errors.As(err, &beforeDispatch) {
				refused++
			} else if err != nil {
				failed++
				if firstFailure == nil {
					firstFailure = err
				}
			}
			mu.Unlock()
		}
	}()
	swapPackage(t, dir, plug, plugV2)
	swapPackage(t, dir, panel, panelV2)
	app.rescanPlugins(context.Background())
	awaitPluginVersion(t, app, plug, "0.2.0")
	await("the section's successor to answer", func() bool {
		sec, ok := app.sections.Get("journey")
		return ok && sec.Decl.Title == "After"
	})
	// ── their predecessors retire ─────────────────────────────────────
	await("both predecessors' retirements to establish", func() bool { return alone(app, plug) && alone(app, panel) })
	close(stopCalls)
	<-callsDone
	mu.Lock()
	if calls-refused == 0 || failed != 0 {
		t.Errorf("%d of %d calls failed across update (%d explicit pre-dispatch refusals): %v", failed, calls, refused, firstFailure)
	}
	t.Logf("update: %d calls, %d successful, %d typed pre-dispatch refusals; no retries", calls, calls-failed-refused, refused)
	mu.Unlock()
	// ── and the routes are still the successors' ──────────────────────
	if err := call(app); err != nil {
		t.Fatalf("after its predecessor retired, the updated plugin does not answer: %v", err)
	}
	if sec, ok := app.sections.Get("journey"); !ok || sec.Decl.Title != "After" {
		t.Fatalf("after its predecessor retired, the section's route is gone: ok=%v %+v", ok, sec)
	}
	servingGen := view(app, plug).Activations[0].Gen

	// ── a budget too small refuses a new plugin, for good ─────────────
	setBudget(envelope / 2)
	installPluginDir(t, dir, late, latePkg)
	app.rescanPlugins(context.Background())
	refusedForGood := func() bool {
		v := view(app, late)
		return v.State == pluginfacility.StateRefused && v.Refusal != nil && v.Refusal.Stage == pluginfacility.StageAdmit && v.Refusal.Class == pluginfacility.ClassPermanent
	}
	await("the refusal under a budget it can never fit", refusedForGood)
	if r := view(app, late).Refusal; !strings.Contains(r.Remedy, "admission_memory_budget_bytes") {
		t.Errorf("the refusal does not tell the operator what to change: %q", r.Remedy)
	}
	// ── Try again really asks again ───────────────────────────────────
	before := view(app, late).Activations
	if err := app.RetryPlugin(late); err != nil {
		t.Fatal(err)
	}
	await("a NEW attempt after Try again, refused for the same real reason", func() bool {
		now := view(app, late).Activations
		return refusedForGood() && len(now) > 0 && len(before) > 0 && now[len(now)-1].Gen > before[len(before)-1].Gen
	})
	// ── raising the budget is what lets it in ─────────────────────────
	setBudget(2*envelope + envelope/2)
	await("it to serve once the budget allows it — with nobody pressing Try again", func() bool { return alone(app, late) })
	// …and restarted nothing that was healthy.
	if got := view(app, plug); !alone(app, plug) || got.Activations[0].Gen != servingGen {
		t.Errorf("a budget change restarted a healthy engine: generation %d became %+v", servingGen, got.Activations)
	}

	// ── a start held waiting for room is uninstalled ──────────────────
	installPluginDir(t, dir, waits, waitsPkg)
	app.rescanPlugins(context.Background())
	await("the third plugin to wait for room", func() bool { return view(app, waits).State == pluginfacility.StateAdmitting })
	// THE PAGE FOLLOWS THE FACILITY A MOMENT BEHIND IT, through the one
	// reader that keeps its rows, so the wait is for the row. Applying a
	// snapshot here by hand made this test a second writer beside that
	// reader: the reader, still holding the snapshot it took while the
	// start was on its way to admission, wrote its older sentence over
	// the newer one, and the row was read in between.
	said := func() bool {
		for _, row := range app.pluginPendingViews() {
			if row.ID == waits && strings.Contains(row.Summary, "waiting for") {
				return true
			}
		}
		return false
	}
	for deadline := time.Now().Add(20 * time.Second); !said(); {
		if time.Now().After(deadline) {
			t.Errorf("the page does not say what the held start waits for: %+v", app.pluginPendingViews())
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err := app.UninstallPlugin(waits); err != nil {
		t.Fatal(err)
	}
	app.rescanPlugins(context.Background())
	await("the held start to be withdrawn and forgotten", func() bool { return view(app, waits).ID == "" })
	if v, _ := activeRelease(app, waits); v != "" {
		t.Errorf("a plugin uninstalled while it waited was started anyway: %s", v)
	}

	// ── and after a restart all of it is still true ───────────────────
	stopApp(t, app)
	stopped = true
	app = New(config(2*envelope + envelope/2))
	if err := startLiveForTest(app); err != nil {
		t.Fatal(err)
	}
	stopped = false
	if v, _ := activeRelease(app, plug); v != "0.2.0" {
		t.Errorf("after the restart the plugin serves %q", v)
	}
	if err := call(app); err != nil {
		t.Errorf("after the restart the plugin does not answer: %v", err)
	}
	if sec, ok := app.sections.Get("journey"); !ok || sec.Decl.Title != "After" {
		t.Errorf("after the restart the section is not registered: ok=%v", ok)
	}
	if !alone(app, late) {
		t.Errorf("after the restart the recovered plugin does not serve: %+v", view(app, late))
	}
}
