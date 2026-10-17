package app

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/interaction"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

// decodeArgs is a call's arguments decoded as dispatch decodes them.
// Text that is not a JSON object is no arguments.
func decodeArgs(argsJSON string) map[string]any {
	var args map[string]any
	if json.Unmarshal([]byte(argsJSON), &args) != nil {
		return nil
	}
	return args
}

// countSuccessfulCall is the unit-test seam for a call whose dispatch result
// is already known to be successful. Production records the attempt before
// dispatch and commits work facts only after the verb returns nil.
func countSuccessfulCall(a *App, name, argsJSON string) {
	args := decodeArgs(argsJSON)
	ordinal := a.countToolCall(name, args)
	if name == "work" {
		a.countSuccessfulWorkCall(args, ordinal)
	}
}

// CS-7: the read-only classifier decides what the baseline counts as
// breadth-shaped work. The leading-`cd` passthrough is load-bearing —
// the resident's dominant shell idiom is `cd /work/… && grep …`, and a
// classifier that judged on the leading token alone would call those
// turns non-read-only and undercount exactly the market the harvest
// path addresses.
func TestReadOnlyShellCommand(t *testing.T) {
	cases := []struct {
		name    string
		command string
		want    bool
	}{
		{"plain grep", "grep -n foo bar.txt", true},
		{"cd passthrough to grep", "cd /work/repo && grep -n foo internal/x.go", true},
		{"cd passthrough to rg", "cd /work/repo && rg pattern", true},
		{"cd then ls", "cd /tmp && ls -la", true},
		{"bare cd only", "cd /work/repo", false},
		{"cd then mutation", "cd /work/repo && go build ./...", false},
		{"cd chained to sed write", "cd /work/repo && sed -i s/a/b/ f.go", false},
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
		{"shell read-only with cd", "shell", `{"command":"cd /work/repo && grep -n x y.go"}`, true},

		{"shell malformed json", "shell", `{command`, false},
		{"write is mutation", "write", `{"file_path":"/tmp/x"}`, false},
		{"edit is mutation", "edit", `{"file_path":"/tmp/x"}`, false},
	}
	for _, c := range cases {
		if got := readOnlyToolCall(c.tool, decodeArgs(c.argsJSON)); got != c.want {
			t.Errorf("%s: readOnlyToolCall(%s, %q) = %v, want %v", c.name, c.tool, c.argsJSON, got, c.want)
		}
	}
}

// The meter counts calls, read-only share, and work spawns; the spawn
// count parses the action rather than string-matching (emitted JSON
// spacing is not guaranteed).
func TestCountToolCall(t *testing.T) {
	a := &App{}
	countSuccessfulCall(a, "grep", `{"pattern":"x"}`)
	countSuccessfulCall(a, "shell", `{"command":"cd /r && grep x y"}`)
	countSuccessfulCall(a, "write", `{"file_path":"/tmp/x","content":"y"}`)
	countSuccessfulCall(a, "work", `{"action":"spawn","goal":"g"}`)
	countSuccessfulCall(a, "work", `{"action":  "start"}`)
	if a.turn.turnCalls != 5 {
		t.Errorf("turnCalls = %d, want 5", a.turn.turnCalls)
	}
	if a.turn.turnReadOnly != 2 {
		t.Errorf("turnReadOnly = %d, want 2", a.turn.turnReadOnly)
	}
	if a.turn.turnSpawned != 1 {
		t.Errorf("turnSpawned = %d, want 1", a.turn.turnSpawned)
	}
}

// THE SEAM, NOT ITS TWO ENDS. Store tests proved InsertTurnMetric
// writes and RhythmStats reads; the meter test proved countToolCall
// counts. Both passed for weeks while production never called
// InsertTurnMetric at all, so turn_metrics held zero rows on a live
// identity and the rhythm line rendered nothing — a whole
// self-observation feature dark, with a green suite. This test crosses
// the seam the unit tests each stopped short of: meter, summary, store,
// reader.
func TestTurnSummaryWritesTheRowTheRhythmLineReads(t *testing.T) {
	dir := t.TempDir()
	st, err := store.NewForTest(filepath.Join(dir, "aii.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })

	a := &App{store: st}
	countSuccessfulCall(a, "grep", `{"pattern":"x"}`)               // read-only
	countSuccessfulCall(a, "write", `{"file_path":"/tmp/x"}`)       // mutation
	countSuccessfulCall(a, "work", `{"action":"spawn","goal":"g"}`) // spawn

	a.logTurnSummary(turnLoop{})

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

// A turn meter that cannot reach a store must not take the turn down
// with it: the rhythm row is a rebuildable runtime aggregate, never
// identity truth. Pre-birth there is no store at all.
func TestTurnSummarySurvivesWithoutAStore(t *testing.T) {
	a := &App{}
	countSuccessfulCall(a, "grep", `{"pattern":"x"}`)
	a.logTurnSummary(turnLoop{}) // must not panic
	if a.turn.turnCalls != 0 {
		t.Fatalf("the meter must still reset: turnCalls = %d", a.turn.turnCalls)
	}
}

func TestHarvestedCountsThisTurnNotTheStandingBacklog(t *testing.T) {
	dir := t.TempDir()
	st, err := store.NewForTest(filepath.Join(dir, "aii.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })

	a := &App{store: st}
	for _, id := range []string{"ws_a", "ws_b", "ws_c"} {
		if err := st.StartWorkSession(t.Context(), id, "delegated"); err != nil {
			t.Fatal(err)
		}
	}

	// Turns ending inside one millisecond are separate rows: a taken
	// millisecond sends the next row to the next free one, so the three
	// read back in the order they were written.
	turn := func(harvest ...string) {
		a.composedUnharvested = nil
		for _, id := range harvest {
			a.composedUnharvested = append(a.composedUnharvested, composedOutcome{id: id, text: "- [" + id + "]"})
		}
		var carried []string
		for _, o := range a.composedUnharvested {
			carried = append(carried, o.text)
		}
		a.markComposedHarvests(strings.Join(carried, "\n")) // carried whole
		a.logTurnSummary(turnLoop{})
	}

	turn("ws_a", "ws_b") // 2 harvests
	turn("ws_c")         // 1 harvest
	turn()               // none — backlog is now 3, this turn reaped 0

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
		t.Fatalf("wrote %d turn rows, want 3", len(rows))
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

// A mark the store refuses is not a harvest. swept= and harvested= must
// both count what succeeded, or they report work that never landed.
func TestFailedHarvestMarkIsNotCounted(t *testing.T) {
	dir := t.TempDir()
	st, err := store.NewForTest(filepath.Join(dir, "aii.db"))
	if err != nil {
		t.Fatal(err)
	}
	st.Close() // every write from here on fails

	a := &App{store: st, composedUnharvested: []composedOutcome{{id: "ws_a", text: "- [ws_a]"}, {id: "ws_b", text: "- [ws_b]"}}}
	a.markComposedHarvests("- [ws_a] - [ws_b]")
	if a.turn.turnHarvested != 0 {
		t.Errorf("turnHarvested = %d after every mark failed, want 0", a.turn.turnHarvested)
	}
}

// The prediction is observed from the tool call, exactly as spawned is —
// no session storage, no plumbing through the identity layer. What it
// must NOT do is accept a number it would then have to truncate: the
// ring bug in commit.go was a truncating read that checked 6 and stored
// 6 while the caller said 6.5, and this reads the same JSON shape.
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
		if a.turn.turnPredicted != c.want {
			t.Errorf("%s: turnPredicted = %d, want %d", c.name, a.turn.turnPredicted, c.want)
		}
	}
}

// independent= rides the same shape as steps= and obeys the same rules:
// integral or refused, never truncated, update-only, last wins.
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
		if a.turn.turnIndependent != c.want {
			t.Errorf("%s: turnIndependent = %d, want %d", c.name, a.turn.turnIndependent, c.want)
		}
	}
	// last statement wins
	a := &App{}
	countSuccessfulCall(a, "work", `{"action":"update","independent":4}`)
	countSuccessfulCall(a, "work", `{"action":"update","independent":2}`)
	if a.turn.turnIndependent != 2 {
		t.Errorf("last-wins: turnIndependent = %d, want 2", a.turn.turnIndependent)
	}
}

// Only stated estimates with complete windows contribute; an estimate made
// late in a turn still describes the requests remaining after that point.
func TestPlanCalibrationCountsOnlyMeasuredEstimates(t *testing.T) {
	st, err := store.NewForTest(filepath.Join(t.TempDir(), "aii.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	now := time.Now().UTC().UnixMilli()
	rows := []store.TurnMetric{
		{TsMs: now - 5000, Calls: 47, Predicted: 6, DeclaredOrdinal: 2, Emissions: &store.ToolCallEmissions{Count: 47, Complete: true}},
		{TsMs: now - 4000, Calls: 12, Predicted: 10, DeclaredOrdinal: 4, Emissions: &store.ToolCallEmissions{Count: 12, Complete: true}},
		{TsMs: now - 3000, Calls: 90},
		{TsMs: now - 2000, Calls: 80, Predicted: 70, DeclaredOrdinal: 61, Emissions: &store.ToolCallEmissions{Count: 80, Complete: true}},
		{TsMs: now - 1000, Calls: 30, Predicted: 30},
	}
	for _, row := range rows {
		if err := st.InsertTurnMetric(row); err != nil {
			t.Fatal(err)
		}
	}
	got, err := st.PlanCalibration(48 * time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	want := store.PlanCalibrationStats{Plans: 3, Predicted: 86, Actual: 72, Unmeasured: 1}
	if got != want {
		t.Fatalf("calibration = %+v, want %+v", got, want)
	}
}

func TestPlanCalibrationSilentWithNoPredictions(t *testing.T) {
	st, err := store.NewForTest(filepath.Join(t.TempDir(), "aii.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.InsertTurnMetric(store.TurnMetric{TsMs: time.Now().UTC().UnixMilli(), Calls: 30}); err != nil {
		t.Fatal(err)
	}
	got, err := st.PlanCalibration(48 * time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if got != (store.PlanCalibrationStats{}) {
		t.Fatalf("unstated estimate appeared: %+v", got)
	}
	facts, err := (&App{store: st}).buildTurnFacts(t.Context(), false)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(facts, "estimates") || strings.Contains(facts, "stated estimate") {
		t.Fatalf("calibration rendered without an estimate: %s", facts)
	}
}

func TestRhythmNamesRemainingRequestsAndUnknownWindows(t *testing.T) {
	st, err := store.NewForTest(filepath.Join(t.TempDir(), "aii.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	now := time.Now().UTC().UnixMilli()
	for _, row := range []store.TurnMetric{
		{TsMs: now - 1000, Calls: 4, Predicted: 2, DeclaredOrdinal: 6, Emissions: &store.ToolCallEmissions{Count: 8, Complete: true}},
		{TsMs: now, Calls: 10, Predicted: 3, DeclaredOrdinal: 2},
	} {
		if err := st.InsertTurnMetric(row); err != nil {
			t.Fatal(err)
		}
	}
	facts, err := (&App{store: st}).buildTurnFacts(t.Context(), false)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"Across 1 fully measured turn(s), first estimates totaled 2 remaining tool-call requests; 2 were emitted after those declarations, including refused requests.",
		"Excluded 1 stated estimate(s) without a valid, complete request window.",
		"New work update steps= estimates can be compared when their turn's request count is complete.",
	} {
		if !strings.Contains(facts, want) {
			t.Fatalf("rhythm omitted %q: %s", want, facts)
		}
	}
}

func TestCrashDisclosureSurvivesFailedTurns(t *testing.T) {
	st, err := store.NewForTest(filepath.Join(t.TempDir(), "aii.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })

	a := &App{store: st, bootInterrupted: []string{"write", "shell"}}
	first, err := a.buildTurnFacts(t.Context(), false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(first, "Interrupted at last shutdown") || !strings.Contains(first, "write") {
		t.Fatalf("first compose does not disclose the interruption: %q", first)
	}
	// The turn that used this compose FAILED. The next compose must
	// still carry the warning — this is the assertion the shipped
	// clear-at-compose version cannot pass.
	second, err := a.buildTurnFacts(t.Context(), false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(second, "Interrupted at last shutdown") {
		t.Fatal("a failed turn consumed the crash disclosure — it must survive until a turn succeeds")
	}
	// A turn SUCCEEDS without carrying it — its facts were folded or
	// omitted for the budget: it was not shown, and it stays.
	a.markComposedHarvests("")
	again, err := a.buildTurnFacts(t.Context(), false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(again, "Interrupted at last shutdown") {
		t.Fatal("a turn that did not carry the crash disclosure consumed it")
	}
	// A turn SUCCEEDS and carried it: the success seam consumes it.
	a.markComposedHarvests(again)
	third, err := a.buildTurnFacts(t.Context(), false)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(third, "Interrupted at last shutdown") {
		t.Fatal("the disclosure outlived its delivery — it must clear once a turn succeeds")
	}
}

// Failure made ambient (watch finding F1): the rhythm block reports
// failed/truncated calls when they exist and stays silent when clean.
func TestRhythmReportsToolFailuresOnlyWhenTheyExist(t *testing.T) {
	st, err := store.NewForTest(filepath.Join(t.TempDir(), "aii.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	a := &App{store: st}
	countSuccessfulCall(a, "grep", `{"pattern":"x"}`)
	a.logTurnSummary(turnLoop{}) // a turn row so the rhythm block renders at all
	// A SUCCESSFUL call on the record: the clean case must have done>0,
	// or an always-render mutation slips past on an empty table (it did —
	// the first version of this test proved nothing about the gate).
	if err := st.RecordToolStart("t", 0, "main", "m", "c0", "grep", `{}`); err != nil {
		t.Fatal(err)
	}
	if err := st.RecordToolDoneMeasured("t", 0, "grep", `{}`, "match", interaction.Succeeded, false, store.ToolMeasurement{}); err != nil {
		t.Fatal(err)
	}

	clean, err := a.buildTurnFacts(t.Context(), false)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(clean, "failed and") {
		t.Fatal("failure clause rendered with zero failures — silence is the honest zero")
	}

	if err := st.RecordToolStart("t", 1, "main", "m", "c1", "shell", `{}`); err != nil {
		t.Fatal(err)
	}
	if err := st.RecordToolDoneMeasured("t", 1, "shell", `{}`, "Error: denied", interaction.Failed, false, store.ToolMeasurement{}); err != nil {
		t.Fatal(err)
	}
	dirty, err := a.buildTurnFacts(t.Context(), false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(dirty, "1 failed") {
		t.Fatalf("a failed call did not reach the rhythm block: %q", dirty)
	}
}

func TestDeclaredOrdinalRecordsTheFirstDeclaration(t *testing.T) {
	a := &App{}
	countSuccessfulCall(a, "grep", `{"pattern":"x"}`)               // call 1
	countSuccessfulCall(a, "grep", `{"pattern":"y"}`)               // call 2
	countSuccessfulCall(a, "work", `{"action":"update","steps":6}`) // call 3: first declaration
	countSuccessfulCall(a, "shell", `{"command":"ls"}`)             // call 4
	countSuccessfulCall(a, "work", `{"action":"update","steps":9}`) // call 5: revision
	if a.turn.turnPredicted != 9 {
		t.Errorf("value last-wins: predicted = %d, want 9", a.turn.turnPredicted)
	}
	if a.turn.turnDeclaredOrdinal != 3 {
		t.Errorf("declared ordinal = %d, want 3 — the FIRST declaration is the advance-ness fact", a.turn.turnDeclaredOrdinal)
	}
	if remaining, ordinal := a.predictedThisTurn(); remaining != 9 || ordinal != 5 {
		t.Fatalf("live budget pair = %d at %d, want 9 at 5", remaining, ordinal)
	}
	a.resetTurnMeter()
	if a.turn.turnDeclaredOrdinal != 0 {
		t.Error("ordinal survived the reset")
	}
	if remaining, ordinal := a.predictedThisTurn(); remaining != 0 || ordinal != 0 {
		t.Fatalf("previous turn's budget survived reset: %d at %d", remaining, ordinal)
	}
}

func TestSuccessfulWorkFactsKeepCallOrderWhenCompletionReorders(t *testing.T) {
	a := &App{}
	early, late := decodeArgs(`{"action":"update","steps":3,"independent":2}`), decodeArgs(`{"action":"update","steps":8,"independent":5}`)
	first := a.countToolCall("work", early)
	second := a.countToolCall("work", late)
	// The later call completes first.
	a.countSuccessfulWorkCall(late, second)
	a.countSuccessfulWorkCall(early, first)
	if a.turn.turnPredicted != 8 || a.turn.turnIndependent != 5 {
		t.Fatalf("completion order replaced call order: predicted=%d independent=%d", a.turn.turnPredicted, a.turn.turnIndependent)
	}
	if remaining, ordinal := a.predictedThisTurn(); remaining != 8 || ordinal != second {
		t.Fatalf("completion order replaced the live budget pair: %d at %d", remaining, ordinal)
	}
	if a.turn.turnDeclaredOrdinal != first || a.turn.turnFirstPredicted != 3 {
		t.Fatalf("first declaration pair = (%d,%d), want (%d,3)", a.turn.turnDeclaredOrdinal, a.turn.turnFirstPredicted, first)
	}
}

func TestBetaUnplannedDeepCountRejectsLegacyLateAndAbsent(t *testing.T) {
	st, err := store.NewForTest(filepath.Join(t.TempDir(), "aii.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	base := time.Now().UTC().UnixMilli()
	rows := []store.TurnMetric{
		{TsMs: base + 1, Calls: 120, Predicted: 0},                       // absent -> counts
		{TsMs: base + 2, Calls: 130, Predicted: 25, DeclaredOrdinal: 0},  // legacy unknown -> counts
		{TsMs: base + 3, Calls: 140, Predicted: 25, DeclaredOrdinal: 87}, // late -> counts
		{TsMs: base + 4, Calls: 150, Predicted: 25, DeclaredOrdinal: 3},  // timely -> passes
		{TsMs: base + 5, Calls: 40, Predicted: 0},                        // shallow -> ignored
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

// The tool-event sink a chat attaches is called after the turn lock is let
// go. One lock guards the whole turn state, so a sink that reads or writes
// any of it — the call meter, the cost gauge, the declared budget, the
// open ask — would wait on itself for ever if it were called under that lock.
// Both emitters are fired: the resident's (emitToolEvent) and the one a
// tool's executor holds (appEmitter).
func TestTheToolSinkRunsOutsideTheTurnLock(t *testing.T) {
	a := &App{}
	a.turn.toolEmit = func(kind, name, args string) {
		a.countToolCall("read", decodeArgs(`{"path":"notes.md"}`))
		_ = a.lastTurnCost()
		_, _ = a.predictedThisTurn()
		a.resetAsk()
	}
	for _, c := range []struct {
		emitter string
		fire    func()
	}{
		{"emitToolEvent", func() { a.emitToolEvent("call", "read", `{"path":"notes.md"}`) }},
		{"appEmitter.EmitToolEvent", func() { appEmitter{a: a}.EmitToolEvent("call", "read", `{"path":"notes.md"}`) }},
	} {
		done := make(chan struct{})
		go func() {
			defer close(done)
			c.fire()
		}()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatalf("%s did not return within 5 s: the sink was called under the turn lock and waits on it", c.emitter)
		}
	}
	// The sink ran once through each emitter, so the two returns above are
	// the sink's, not an emitter that never called it.
	a.turn.mu.Lock()
	calls := a.turn.turnCalls
	a.turn.mu.Unlock()
	if calls != 2 {
		t.Fatalf("the sink ran %d time(s), want 2 (once through each emitter)", calls)
	}
}

func TestTheMeterReadsTheArgumentsTheCallRanWith(t *testing.T) {
	a := &App{}
	countSuccessfulCall(a, "shell", `{"COMMAND":"cat notes.md"}`)
	if a.turn.turnCalls != 1 || a.turn.turnReadOnly != 0 {
		t.Errorf("a shell call with no command the shell reads was metered as a read: calls=%d read_only=%d", a.turn.turnCalls, a.turn.turnReadOnly)
	}
	countSuccessfulCall(a, "work", `{"action":"spawn","goal":"count the jars","steps":"three"}`)
	if a.turn.turnSpawned != 1 {
		t.Errorf("an accepted spawn with a mistyped extra field was not counted: spawned=%d", a.turn.turnSpawned)
	}
	countSuccessfulCall(a, "work", `{"action":"update","steps":4,"independent":"two"}`)
	if a.turn.turnPredicted != 4 || a.turn.turnIndependent != 0 {
		t.Errorf("steps=4 beside a mistyped independent= = predicted %d, independent %d; want 4, 0", a.turn.turnPredicted, a.turn.turnIndependent)
	}
}
