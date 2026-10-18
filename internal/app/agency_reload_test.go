package app

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/identity"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

// A RELOAD APPLIES THE AGENCY CONFIG A BOOT APPLIES (T12.d). The running
// identity holds copies of it — the engine's spawn ceilings, queue switch
// and child budget, the store's claim limit on sub-agent work, the
// resident loop's round and token bounds, the rhythm's alarm — and the
// reload said every agency key applied live while it refreshed only the
// ceilings and the local wall.

// heldAgency is what the running identity holds of the agency config,
// read by reflection as enginePorts reads the engine's ports.
type heldAgency struct {
	Depth, Parallel, Mints, Wall, LocalWall int64
	QueueOff                                bool
	Rounds, Calls, Legs                     int64
	TurnRounds, TurnTokens                  int64
}

func agencyHeld(a *App) heldAgency {

	a.cfgMu.RLock()
	defer a.cfgMu.RUnlock()
	e := reflect.ValueOf(a.engine).Elem()
	l := reflect.ValueOf(a.conv).Elem().FieldByName("cfg")
	n := func(v reflect.Value, name string) int64 { return v.FieldByName(name).Int() }
	return heldAgency{
		Depth: n(e, "maxSubagentDepth"), Parallel: n(e, "maxParallelSubagents"), Mints: n(e, "maxSubagentMints"),
		Wall: n(e, "subagentWallSeconds"), LocalWall: n(e, "subagentWallSecondsLocal"),
		QueueOff: e.FieldByName("spawnQueueOff").Bool(),
		Rounds:   n(e, "spawnRounds"), Calls: n(e, "spawnCalls"), Legs: n(e, "spawnLegs"),
		TurnRounds: n(l, "MaxIterations"), TurnTokens: n(l, "TurnTokenBudget"),
	}
}

// heldFrom is what a running identity should hold of ag.
func heldFrom(ag AgencyConfig) heldAgency {
	return heldAgency{
		Depth: int64(ag.MaxSubagentDepth), Parallel: int64(ag.MaxParallelSubagents), Mints: int64(ag.SubagentMaxMints),
		Wall: int64(ag.SubagentWallSeconds), LocalWall: int64(ag.SubagentWallSecondsLocal),
		QueueOff: !agencyOn(ag.SpawnQueue),
		Rounds:   int64(ag.SubagentMaxToolRounds), Calls: int64(ag.SubagentMaxToolCalls), Legs: int64(ag.SubagentMaxLegs),
		TurnRounds: int64(ag.MaxToolRounds), TurnTokens: int64(ag.TurnTokenBudget),
	}
}

// claimSubagents claims sub-agent work until the store holds back, and
// says how many it took: what the claim limit admits beside the claims
// already held.
func claimSubagents(t *testing.T, st *store.Store) int {
	t.Helper()
	n := 0
	for {
		item, err := st.ClaimWork([]string{identity.SubagentWorkKind}, time.Now().UnixMilli())
		if err != nil {
			t.Fatal(err)
		}
		if item == nil {
			return n
		}
		n++
	}
}

// rhythmEvery is the metabolism alarm's period and next deadline, in
// milliseconds, as TIME holds them durably.
func rhythmEvery(t *testing.T, st *store.Store) (every, deadline int64) {
	t.Helper()
	if err := st.DB().QueryRow(`SELECT repeat_every, deadline FROM alarms WHERE alarm_id = 'rhythm'`).Scan(&every, &deadline); err != nil {
		t.Fatal(err)
	}
	return every, deadline
}

func silentYield(a *App) (refused bool) {
	return a.executeToolCall(a.lifetime(), yieldCall(`{"action":"yield"}`)).Failed
}

// A booted identity, its config edited and reloaded: everything the boot
// applied follows, raised and lowered, and the queue switch both ways;
// an unchanged reload moves nothing. The executor is stopped first, so
// the claims counted are the test's own.
func TestAReloadAppliesTheAgencyConfigABootApplies(t *testing.T) {
	dir := t.TempDir()
	keyPath, ledgerPath, dbPath := birthFixture(t, dir, "AgencyReload")
	cfg := safebootConfig(t, dir, "AgencyReload", keyPath, ledgerPath, dbPath)
	off := false
	cfg.Agency.MaxParallelSubagents = 1
	cfg.Agency.YieldAnswer = &off
	if _, err := saveConfig(cfg); err != nil {
		t.Fatal(err)
	}
	app := New(cfg)
	if err := startLiveForTest(app); err != nil {
		t.Fatalf("live boot: %v", err)
	}
	defer stopApp(t, app)
	if _, safe := app.SafeMode(); safe {
		t.Fatal("rig: this boot was meant to be NORMAL")
	}
	app.executor.Stop()
	for i := 0; i < 4; i++ {
		if _, err := app.store.EnqueueWork(&store.WorkItem{Kind: identity.SubagentWorkKind, Payload: `{}`,
			DedupKey: fmt.Sprintf("probe-%d", i), Source: "identity", LeaseMs: int64(time.Hour / time.Millisecond)}); err != nil {
			t.Fatal(err)
		}
	}
	reload := func(edit func(*AgencyConfig)) {
		t.Helper()
		edited := app.configSnapshot()
		edit(&edited.Agency)
		if _, err := saveConfig(&edited); err != nil {
			t.Fatal(err)
		}
		app.reloadConfig()
	}

	// The boot.
	booted := agencyHeld(app)
	if want := heldFrom(app.configSnapshot().Agency); booted != want {
		t.Fatalf("the boot holds %+v, want %+v", booted, want)
	}
	if n := claimSubagents(t, app.store); n != 1 {
		t.Fatalf("the boot's claim limit admitted %d, want 1 (max_parallel_subagents)", n)
	}
	every, deadline := rhythmEvery(t, app.store)
	if every != int64(cfg.Agency.RhythmSeconds)*1000 {
		t.Fatalf("rhythm every %dms, want %ds", every, cfg.Agency.RhythmSeconds)
	}
	app.markFleetSpent()
	if silentYield(app) {
		t.Fatal("yield_answer false: a silent yield is admitted")
	}

	// THE CONTROL: the same file again moves nothing.
	reload(func(*AgencyConfig) {})
	if got := agencyHeld(app); got != booted {
		t.Errorf("an unchanged reload moved the held agency: %+v -> %+v", booted, got)
	}
	if n := claimSubagents(t, app.store); n != 0 {
		t.Errorf("an unchanged reload admitted %d more claims", n)
	}
	if e, d := rhythmEvery(t, app.store); e != every || d != deadline {
		t.Errorf("an unchanged reload re-armed the rhythm: every %d at %d -> every %d at %d", every, deadline, e, d)
	}

	// RAISED, and every switch turned.
	on := true
	reload(func(ag *AgencyConfig) {
		ag.MaxParallelSubagents = 2
		ag.MaxSubagentDepth = 2
		ag.SubagentMaxMints = 9
		ag.SubagentWallSeconds = 700
		ag.SubagentWallSecondsLocal = 900
		ag.SubagentMaxToolRounds = 11
		ag.SubagentMaxToolCalls = 33
		ag.SubagentMaxLegs = 2
		ag.MaxToolRounds = 20
		ag.TurnTokenBudget = 400_000
		ag.RhythmSeconds = 900
		ag.TurnContinuation = &off
		ag.YieldAnswer = &on
	})
	want := heldAgency{Depth: 2, Parallel: 2, Mints: 9, Wall: 700, LocalWall: 900, Rounds: 11, Calls: 33, Legs: 2, TurnRounds: 20, TurnTokens: 400_000}
	if got := agencyHeld(app); got != want {
		t.Errorf("after the reload the identity holds %+v, want %+v", got, want)
	}
	if n := claimSubagents(t, app.store); n != 1 {
		t.Errorf("max_parallel_subagents raised to 2: the store admitted %d more claims, want 1", n)
	}
	if e, _ := rhythmEvery(t, app.store); e != 900_000 {
		t.Errorf("rhythm_seconds 900: the alarm repeats every %dms", e)
	}
	if !silentYield(app) {
		t.Error("yield_answer true: a silent yield with a spent fleet is still admitted")
	}
	if err := app.store.StartWorkSession(t.Context(), "ws_capped", "a capped task"); err != nil {
		t.Fatal(err)
	}
	if scheduled, why := app.scheduleContinuation(capDeclared, ""); scheduled || !strings.Contains(why, "agency.turn_continuation") {
		t.Errorf("turn_continuation false: a capped turn was continued (%v), or was not told the switch is why nothing continues it (%q)", scheduled, why)
	}

	// LOWERED: the claims already held are over the new limit.
	reload(func(ag *AgencyConfig) { ag.MaxParallelSubagents = 1 })
	if got := agencyHeld(app).Parallel; got != 1 {
		t.Errorf("max_parallel_subagents lowered to 1: the engine holds %d", got)
	}
	if n := claimSubagents(t, app.store); n != 0 {
		t.Errorf("max_parallel_subagents lowered to 1 with two claimed: the store admitted %d more", n)
	}

	// THE QUEUE OFF: the claim limit goes with it, as at a boot with it off.
	reload(func(ag *AgencyConfig) { ag.SpawnQueue = &off })
	if !agencyHeld(app).QueueOff {
		t.Error("spawn_queue false: the engine still queues")
	}
	if n := claimSubagents(t, app.store); n != 2 {
		t.Errorf("spawn_queue false: the store admitted %d more claims, want the 2 left (no claim limit)", n)
	}
}

func TestSafeTakesNoAgencyFromAReload(t *testing.T) {
	raise := func(t *testing.T, a *App) {
		t.Helper()
		edited := a.configSnapshot()
		edited.Agency.MaxSubagentDepth, edited.Agency.MaxParallelSubagents = 5, 5
		if _, err := saveConfig(&edited); err != nil {
			t.Fatal(err)
		}
		a.reloadConfig()
	}
	spawn := func(t *testing.T, a *App) {
		t.Helper()
		var before, after int
		count := `SELECT COUNT(*) FROM work_queue WHERE kind = '` + identity.SubagentWorkKind + `'`
		if err := a.store.DB().QueryRow(count).Scan(&before); err != nil {
			t.Fatal(err)
		}
		obs := a.executeToolCall(a.lifetime(), yieldCall(`{"action":"spawn","goal":"look around"}`)) // any work call
		if !obs.Failed {
			t.Fatalf("a spawn in SAFE was admitted: %s", obs.Text)
		}
		if err := a.store.DB().QueryRow(count).Scan(&after); err != nil {
			t.Fatal(err)
		}
		if after != before {
			t.Fatalf("a refused spawn left %d sub-agent rows, had %d", after, before)
		}
	}

	t.Run("boot SAFE", func(t *testing.T) {
		dir := t.TempDir()
		keyPath, ledgerPath, dbPath := birthFixture(t, dir, "SafeAgency")
		buildPriorProjection(t, ledgerPath, dbPath)
		tamperChain(t, keyPath, ledgerPath)
		cfg := safebootConfig(t, dir, "SafeAgency", keyPath, ledgerPath, dbPath)
		if _, err := saveConfig(cfg); err != nil {
			t.Fatal(err)
		}
		a := New(cfg)
		if err := startLiveForTest(a); err != nil {
			t.Fatalf("SAFE boot: %v", err)
		}
		defer stopApp(t, a)
		if _, safe := a.SafeMode(); !safe {
			t.Fatal("rig: a tampered chain did not boot SAFE")
		}
		held := agencyHeld(a)
		raise(t, a)
		if got := agencyHeld(a); got != held {
			t.Fatalf("a reload moved SAFE's agency: %+v -> %+v", held, got)
		}
		if held.Depth != 0 || held.Parallel != 0 || held.Rounds != 0 {
			t.Fatalf("SAFE's engine holds agency it was never given: %+v", held)
		}
		spawn(t, a)
	})
	t.Run("live, then SAFE", func(t *testing.T) {
		a, _ := liveThenSafe(t, t.TempDir(), "https://127.0.0.1:1")
		defer stopApp(t, a)
		raise(t, a)
		if got := agencyHeld(a); got.Depth != 5 || got.Parallel != 5 {
			t.Fatalf("rig: a live identity in SAFE takes a reload's agency, and holds %+v", got)
		}
		spawn(t, a)
		var frozen *store.FrozenError
		_, err := a.engine.ExecuteAction(a.lifetime(), "verb", "work", map[string]any{"action": "spawn", "goal": "look around"})
		if !errors.As(err, &frozen) {
			t.Fatalf("the spawn was not refused by the frozen queue: %v", err)
		}
	})
}

// The reload's own words: an edit of a key the running identity takes
// only at boot is stated as saved for next boot; an edit of a key it
// applies is stated as applied, and as nothing else.
func TestAReloadStatesTheAgencyKeysABootAloneTakes(t *testing.T) {
	for name, edit := range map[string]func(*AgencyConfig){
		"queue_workers": func(ag *AgencyConfig) { ag.QueueWorkers += 2 },
	} {
		out := reloadLog(t, func(c *Config) { edit(&c.Agency) })
		if !strings.Contains(out, "SAVED FOR NEXT BOOT") {
			t.Errorf("agency.%s is taken at boot, and a reload of it said nothing of the sort:\n%s", name, out)
		}
	}
	for name, edit := range map[string]func(*AgencyConfig){
		"spawn_queue":       func(ag *AgencyConfig) { off := false; ag.SpawnQueue = &off },
		"subagent_max_legs": func(ag *AgencyConfig) { ag.SubagentMaxLegs++ },
		"max_tool_rounds":   func(ag *AgencyConfig) { ag.MaxToolRounds++ },
		"turn_token_budget": func(ag *AgencyConfig) { ag.TurnTokenBudget++ },
		"rhythm_seconds":    func(ag *AgencyConfig) { ag.RhythmSeconds++ },
		"outcome_window":    func(ag *AgencyConfig) { ag.OutcomeWindow += 64 },
		"turn_continuation": func(ag *AgencyConfig) { off := false; ag.TurnContinuation = &off },
		"yield_answer":      func(ag *AgencyConfig) { off := false; ag.YieldAnswer = &off },
	} {
		out := reloadLog(t, func(c *Config) { edit(&c.Agency) })
		if strings.Contains(out, "SAVED FOR NEXT BOOT") || !strings.Contains(out, "agency ceilings applied live") {
			t.Errorf("agency.%s applies live, and a reload did not say so:\n%s", name, out)
		}
	}
}
