package app

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/store"
)

// .
// .
// .
func countSuccessfulCall(a *App, name, args string) {
	ordinal := a.countToolCall(name, args)
	if name == "work" {
		a.countSuccessfulWorkCall(args, ordinal)
	}
}

// .
// .
// .
// .
// .
// .
func TestReadOnlyShellCommand(t *testing.T) {
	cases := []struct {
		name    string
		command string
		want    bool
	}{
		{"plain grep", "grep -n foo bar.txt", true},
		{"cd passthrough to grep", "cd /home/user && grep -n foo internal/x.go", true},
		{"cd passthrough to rg", "cd /home/user && rg pattern", true},
		{"cd then ls", "cd /tmp && ls -la", true},
		{"bare cd only", "cd /home/user", false},
		{"cd then mutation", "cd /home/user && go build ./...", false},
		{"cd chained to sed write", "cd /home/user && sed -i s/a/b/ f.go", false},
		{"git status", "git status --short", true},
		{"git log", "git log --oneline -5", true},
		{"git diff", "git diff HEAD", true},
		{"git commit is mutation", "git commit -m x", false},
		{"go build is mutation", "go build ./...", false},
		{"rm is mutation", "rm -rf /tmp/x", false},
		{"echo pipe to file", "echo x > /tmp/f", false},
		{"cat plain", "cat main.go", true},
		{"head", "head -20 f.go", true},
		{"tail", "tail -5 f.go", true},
		{"wc", "wc -l f.go", true},
		{"file", "file bin", true},
		{"find", "find . -name x", true},
		{"empty", "", false},
	}
	for _, c := range cases {
		if got := readOnlyShellCommand(c.command); got != c.want {
			t.Errorf("%s: readOnlyShellCommand(%q) = %v, want %v", c.name, c.command, got, c.want)
		}
	}
}

func TestReadOnlyToolCall(t *testing.T) {
	cases := []struct {
		name     string
		tool     string
		argsJSON string
		want     bool
	}{
		{"read organ", "read", `{"file_path":"/tmp/x"}`, true},
		{"grep organ", "grep", `{"pattern":"x"}`, true},
		{"shell read-only", "shell", `{"command":"grep -n x y"}`, true},
		{"shell read-only with cd", "shell", `{"command":"cd /home/user && grep -n x y.go"}`, true},

		{"shell malformed json", "shell", `{command`, false},
		{"write is mutation", "write", `{"file_path":"/tmp/x"}`, false},
		{"edit is mutation", "edit", `{"file_path":"/tmp/x"}`, false},
	}
	for _, c := range cases {
		if got := readOnlyToolCall(c.tool, c.argsJSON); got != c.want {
			t.Errorf("%s: readOnlyToolCall(%s, %q) = %v, want %v", c.name, c.tool, c.argsJSON, got, c.want)
		}
	}
}

// .
// .
// .
func TestCountToolCall(t *testing.T) {
	a := &App{}
	countSuccessfulCall(a, "grep", `{"pattern":"x"}`)
	countSuccessfulCall(a, "shell", `{"command":"cd /r && grep x y"}`)
	countSuccessfulCall(a, "write", `{"file_path":"/tmp/x","content":"y"}`)
	countSuccessfulCall(a, "work", `{"action":"spawn","goal":"g"}`)
	countSuccessfulCall(a, "work", `{"action":  "start"}`)
	if a.turnCalls != 5 {
		t.Errorf("turnCalls = %d, want 5", a.turnCalls)
	}
	if a.turnReadOnly != 2 {
		t.Errorf("turnReadOnly = %d, want 2", a.turnReadOnly)
	}
	if a.turnSpawned != 1 {
		t.Errorf("turnSpawned = %d, want 1", a.turnSpawned)
	}
}

// .
// .
// .
// .
// .
// .
// .
// .
func TestTurnSummaryWritesTheRowTheRhythmLineReads(t *testing.T) {
	dir := t.TempDir()
	st, err := store.New(filepath.Join(dir, "aii.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })

	a := &App{store: st}
	countSuccessfulCall(a, "grep", `{"pattern":"x"}`)
	countSuccessfulCall(a, "write", `{"file_path":"/tmp/x"}`)
	countSuccessfulCall(a, "work", `{"action":"spawn","goal":"g"}`)

	a.logTurnSummary()

	turns, calls, roPct, spawns, _, _, err := st.RhythmStats(48 * time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if turns != 1 {
		t.Fatalf("turns = %d, want 1 — the summary seam must mint a row", turns)
	}
	if calls != 3 {
		t.Fatalf("calls = %d, want 3", calls)
	}
	if roPct != 33 {
		t.Fatalf("read-only = %d%%, want 33 (1 of 3)", roPct)
	}
	if spawns != 1 {
		t.Fatalf("spawns = %d, want 1", spawns)
	}
}

// .
// .
// .
func TestTurnSummarySurvivesWithoutAStore(t *testing.T) {
	a := &App{}
	countSuccessfulCall(a, "grep", `{"pattern":"x"}`)
	a.logTurnSummary()
	if a.turnCalls != 0 {
		t.Fatalf("the meter must still reset: turnCalls = %d", a.turnCalls)
	}
}

// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
func TestHarvestedCountsThisTurnNotTheStandingBacklog(t *testing.T) {
	dir := t.TempDir()
	st, err := store.New(filepath.Join(dir, "aii.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })

	a := &App{store: st}
	for _, id := range []string{"ws_a", "ws_b", "ws_c"} {
		if err := st.StartWorkSession(id, "delegated"); err != nil {
			t.Fatal(err)
		}
	}

	// .
	// .
	// .
	// .
	turn := func(harvest ...string) {
		a.composedUnharvested = harvest
		a.markComposedHarvests()
		a.logTurnSummary()
		time.Sleep(2 * time.Millisecond)
	}

	turn("ws_a", "ws_b")
	turn("ws_c")
	turn()

	var rows []int
	q, err := st.DB().Query(`SELECT harvested FROM turn_metrics ORDER BY ts_ms`)
	if err != nil {
		t.Fatal(err)
	}
	defer q.Close()
	for q.Next() {
		var h int
		if err := q.Scan(&h); err != nil {
			t.Fatal(err)
		}
		rows = append(rows, h)
	}
	if len(rows) != 3 {
		t.Fatalf("wrote %d turn rows, want 3 (did two turns share a millisecond?)", len(rows))
	}
	want := []int{2, 1, 0}
	for i := range want {
		if rows[i] != want[i] {
			t.Errorf("turn %d reported harvested=%d, want %d", i+1, rows[i], want[i])
		}
	}

	_, _, _, _, harvests, _, err := st.RhythmStats(48 * time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if harvests != 3 {
		t.Errorf("RhythmStats summed harvested to %d, want 3 — one per session actually reaped", harvests)
	}
}

// .
// .
func TestFailedHarvestMarkIsNotCounted(t *testing.T) {
	dir := t.TempDir()
	st, err := store.New(filepath.Join(dir, "aii.db"))
	if err != nil {
		t.Fatal(err)
	}
	st.Close()

	a := &App{store: st, composedUnharvested: []string{"ws_a", "ws_b"}}
	a.markComposedHarvests()
	if a.turnHarvested != 0 {
		t.Errorf("turnHarvested = %d after every mark failed, want 0", a.turnHarvested)
	}
}

// .
// .
// .
// .
// .
func TestPredictedStepsObservedFromTheToolCall(t *testing.T) {
	cases := []struct {
		name string
		args string
		want int
	}{
		{"stated on update", `{"action":"update","steps":6}`, 6},
		{"last statement wins", `{"action":"update","steps":9}`, 9},
		{"absent", `{"action":"update"}`, 0},
		{"zero is no prediction", `{"action":"update","steps":0}`, 0},
		{"negative refused", `{"action":"update","steps":-3}`, 0},
		{"non-integral refused, never truncated", `{"action":"update","steps":6.5}`, 0},
		{"not on spawn", `{"action":"spawn","steps":6}`, 0},
		{"malformed json", `{"action":`, 0},
	}
	for _, c := range cases {
		a := &App{}
		countSuccessfulCall(a, "work", c.args)
		if a.turnPredicted != c.want {
			t.Errorf("%s: turnPredicted = %d, want %d", c.name, a.turnPredicted, c.want)
		}
	}
}

// .
// .
func TestIndependentDeclarationObservedFromTheToolCall(t *testing.T) {
	cases := []struct {
		name string
		args string
		want int
	}{
		{"declared on update", `{"action":"update","independent":3}`, 3},
		{"beside steps", `{"action":"update","steps":6,"independent":2}`, 2},
		{"absent", `{"action":"update"}`, 0},
		{"zero is nothing", `{"action":"update","independent":0}`, 0},
		{"negative refused", `{"action":"update","independent":-2}`, 0},
		{"non-integral refused, never truncated", `{"action":"update","independent":2.5}`, 0},
		{"not on spawn", `{"action":"spawn","independent":3}`, 0},
	}
	for _, c := range cases {
		a := &App{}
		countSuccessfulCall(a, "work", c.args)
		if a.turnIndependent != c.want {
			t.Errorf("%s: turnIndependent = %d, want %d", c.name, a.turnIndependent, c.want)
		}
	}
	// .
	a := &App{}
	countSuccessfulCall(a, "work", `{"action":"update","independent":4}`)
	countSuccessfulCall(a, "work", `{"action":"update","independent":2}`)
	if a.turnIndependent != 2 {
		t.Errorf("last-wins: turnIndependent = %d, want 2", a.turnIndependent)
	}
	// .
	countSuccessfulCall(a, "work", `{"action":"spawn","goal":"g"}`)
	declared, spawned := a.fanoutState()
	if declared != 2 || spawned != 1 {
		t.Errorf("fanoutState = (%d,%d), want (2,1)", declared, spawned)
	}
}

// .
// .
// .
func TestPlanCalibrationCountsOnlyStatedPlans(t *testing.T) {
	dir := t.TempDir()
	st, err := store.New(filepath.Join(dir, "aii.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })

	now := time.Now().UTC().UnixMilli()
	rows := []store.TurnMetric{
		{TsMs: now - 5000, Calls: 47, Predicted: 6, DeclaredOrdinal: 2},
		{TsMs: now - 4000, Calls: 12, Predicted: 10, DeclaredOrdinal: 4},
		{TsMs: now - 3000, Calls: 90, Predicted: 0},
		{TsMs: now - 2000, Calls: 80, Predicted: 70, DeclaredOrdinal: 61},
		{TsMs: now - 1000, Calls: 30, Predicted: 30},
	}
	for _, r := range rows {
		if err := st.InsertTurnMetric(r); err != nil {
			t.Fatal(err)
		}
	}

	plans, predicted, actual, err := st.PlanCalibration(48 * time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if plans != 2 {
		t.Errorf("plans = %d, want 2 — silent, LATE and unknown-timing rows must not feed the mirror (review 6: that would teach calibration from narration)", plans)
	}
	if predicted != 16 {
		t.Errorf("predicted = %d, want 16", predicted)
	}
	if actual != 59 {
		t.Errorf("actual = %d, want 59", actual)
	}
}

// .
// .
func TestPlanCalibrationSilentWithNoPredictions(t *testing.T) {
	dir := t.TempDir()
	st, err := store.New(filepath.Join(dir, "aii.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })

	if err := st.InsertTurnMetric(store.TurnMetric{TsMs: time.Now().UTC().UnixMilli(), Calls: 30}); err != nil {
		t.Fatal(err)
	}
	plans, _, _, err := st.PlanCalibration(48 * time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if plans != 0 {
		t.Errorf("plans = %d, want 0 — a turn that stated nothing is not a plan", plans)
	}
}

// .
// .
// .
// .
// .
// .
func TestPlanStateAgainstARealStore(t *testing.T) {
	newApp := func(t *testing.T) (*App, *store.Store) {
		t.Helper()
		st, err := store.New(filepath.Join(t.TempDir(), "aii.db"))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { st.Close() })
		return &App{store: st}, st
	}

	t.Run("no session: unplanned and inactive", func(t *testing.T) {
		a, _ := newApp(t)
		planned, active := a.planState()
		if planned || active {
			t.Fatalf("planState() = (%v,%v), want (false,false) — the case that silenced the nudge in production", planned, active)
		}
	})

	t.Run("active session without a plan", func(t *testing.T) {
		a, st := newApp(t)
		if err := st.StartWorkSession("ws_a", "doing something"); err != nil {
			t.Fatal(err)
		}
		planned, active := a.planState()
		if planned || !active {
			t.Errorf("planState() = (%v,%v), want (false,true) — this pair selects the update-not-start wording", planned, active)
		}
	})

	t.Run("whitespace is not a plan", func(t *testing.T) {
		a, st := newApp(t)
		if err := st.StartWorkSession("ws_b", "doing something"); err != nil {
			t.Fatal(err)
		}
		blank := "   \n\t "
		if err := st.UpdateWorkPlan("ws_b", nil, nil, &blank, nil, nil, nil); err != nil {
			t.Fatal(err)
		}
		if planned, _ := a.planState(); planned {
			t.Error("whitespace counted as a plan")
		}
	})

	t.Run("a real plan silences the ask", func(t *testing.T) {
		a, st := newApp(t)
		if err := st.StartWorkSession("ws_c", "doing something"); err != nil {
			t.Fatal(err)
		}
		plan := "1. read the store\n2. fix the accessor"
		if err := st.UpdateWorkPlan("ws_c", nil, nil, &plan, nil, nil, nil); err != nil {
			t.Fatal(err)
		}
		if planned, _ := a.planState(); !planned {
			t.Error("a session carrying a plan should silence the nudge")
		}
	})

	t.Run("a broken store stays silent", func(t *testing.T) {
		st, err := store.New(filepath.Join(t.TempDir(), "aii.db"))
		if err != nil {
			t.Fatal(err)
		}
		st.Close()
		a := &App{store: st}
		if planned, _ := a.planState(); !planned {
			t.Error("a store error must report planned — the nudge is an ask, not a guard")
		}
	})
}

// .
// .
func TestPlanNudgeEnabledGate(t *testing.T) {
	f, tr := false, true
	for _, c := range []struct {
		name string
		v    *bool
		want bool
	}{
		{"absent is on", nil, true},
		{"explicit true is on", &tr, true},
		{"explicit false disarms", &f, false},
	} {
		if got := planNudgeEnabled(c.v); got != c.want {
			t.Errorf("%s: planNudgeEnabled = %v, want %v", c.name, got, c.want)
		}
	}
}

// .
// .
// .
// .
func TestCrashDisclosureSurvivesFailedTurns(t *testing.T) {
	st, err := store.New(filepath.Join(t.TempDir(), "aii.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })

	a := &App{store: st, bootInterrupted: []string{"write", "shell"}}
	first, err := a.buildTurnFacts(false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(first, "Interrupted at last shutdown") || !strings.Contains(first, "write") {
		t.Fatalf("first compose does not disclose the interruption: %q", first)
	}
	// .
	// .
	// .
	second, err := a.buildTurnFacts(false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(second, "Interrupted at last shutdown") {
		t.Fatal("a failed turn consumed the crash disclosure — it must survive until a turn succeeds")
	}
	// .
	a.markComposedHarvests()
	third, err := a.buildTurnFacts(false)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(third, "Interrupted at last shutdown") {
		t.Fatal("the disclosure outlived its delivery — it must clear once a turn succeeds")
	}
}

// .
// .
func TestRhythmReportsToolFailuresOnlyWhenTheyExist(t *testing.T) {
	st, err := store.New(filepath.Join(t.TempDir(), "aii.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	a := &App{store: st}
	countSuccessfulCall(a, "grep", `{"pattern":"x"}`)
	a.logTurnSummary()
	// .
	// .
	// .
	if err := st.RecordToolStart("t", 0, "main", "m", "c0", "grep", `{}`); err != nil {
		t.Fatal(err)
	}
	if err := st.RecordToolDone("t", 0, "grep", `{}`, "match", false, false); err != nil {
		t.Fatal(err)
	}

	clean, err := a.buildTurnFacts(false)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(clean, "failed and") {
		t.Fatal("failure clause rendered with zero failures — silence is the honest zero")
	}

	if err := st.RecordToolStart("t", 1, "main", "m", "c1", "shell", `{}`); err != nil {
		t.Fatal(err)
	}
	if err := st.RecordToolDone("t", 1, "shell", `{}`, "Error: denied", true, false); err != nil {
		t.Fatal(err)
	}
	dirty, err := a.buildTurnFacts(false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(dirty, "1 failed") {
		t.Fatalf("a failed call did not reach the rhythm block: %q", dirty)
	}
}

// .
// .
// .
// .
func TestDeclaredOrdinalRecordsTheFirstDeclaration(t *testing.T) {
	a := &App{}
	countSuccessfulCall(a, "grep", `{"pattern":"x"}`)
	countSuccessfulCall(a, "grep", `{"pattern":"y"}`)
	countSuccessfulCall(a, "work", `{"action":"update","steps":6}`)
	countSuccessfulCall(a, "shell", `{"command":"ls"}`)
	countSuccessfulCall(a, "work", `{"action":"update","steps":9}`)
	if a.turnPredicted != 9 {
		t.Errorf("value last-wins: predicted = %d, want 9", a.turnPredicted)
	}
	if a.turnDeclaredOrdinal != 3 {
		t.Errorf("declared ordinal = %d, want 3 — the FIRST declaration is the advance-ness fact", a.turnDeclaredOrdinal)
	}
	a.resetTurnMeter()
	if a.turnDeclaredOrdinal != 0 {
		t.Error("ordinal survived the reset")
	}
}

func TestSuccessfulWorkFactsKeepCallOrderWhenCompletionReorders(t *testing.T) {
	a := &App{}
	first := a.countToolCall("work", `{"action":"update","steps":3,"independent":2}`)
	second := a.countToolCall("work", `{"action":"update","steps":8,"independent":5}`)
	// .
	a.countSuccessfulWorkCall(`{"action":"update","steps":8,"independent":5}`, second)
	a.countSuccessfulWorkCall(`{"action":"update","steps":3,"independent":2}`, first)
	if a.turnPredicted != 8 || a.turnIndependent != 5 {
		t.Fatalf("completion order replaced call order: predicted=%d independent=%d", a.turnPredicted, a.turnIndependent)
	}
	if a.turnDeclaredOrdinal != first || a.turnFirstPredicted != 3 {
		t.Fatalf("first declaration pair = (%d,%d), want (%d,3)", a.turnDeclaredOrdinal, a.turnFirstPredicted, first)
	}
}

// .
// .
// .
// .
func TestBetaUnplannedDeepCountRejectsLegacyLateAndAbsent(t *testing.T) {
	st, err := store.New(filepath.Join(t.TempDir(), "aii.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	base := time.Now().UTC().UnixMilli()
	rows := []store.TurnMetric{
		{TsMs: base + 1, Calls: 120, Predicted: 0},
		{TsMs: base + 2, Calls: 130, Predicted: 25, DeclaredOrdinal: 0},
		{TsMs: base + 3, Calls: 140, Predicted: 25, DeclaredOrdinal: 87},
		{TsMs: base + 4, Calls: 150, Predicted: 25, DeclaredOrdinal: 3},
		{TsMs: base + 5, Calls: 40, Predicted: 0},
	}
	for _, r := range rows {
		if err := st.InsertTurnMetric(r); err != nil {
			t.Fatal(err)
		}
	}
	n, err := st.BetaUnplannedDeepCount(base)
	if err != nil {
		t.Fatal(err)
	}
	if n != 3 {
		t.Fatalf("unplanned deep count = %d, want 3 (absent + legacy-unknown + late; the timely deep turn passes)", n)
	}
}
