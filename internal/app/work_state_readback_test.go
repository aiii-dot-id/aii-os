package app

import (
	"context"
	"encoding/json"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/aiii-dot-id/aii-os/internal/conversation"
	"github.com/aiii-dot-id/aii-os/internal/identity"
	"github.com/aiii-dot-id/aii-os/internal/llm"
	"github.com/aiii-dot-id/aii-os/internal/ring"
	"github.com/aiii-dot-id/aii-os/internal/store"
	"github.com/aiii-dot-id/aii-os/internal/tools"
)

func workReadbackApp(t *testing.T) (*App, string) {
	t.Helper()
	a := standingApp(t)
	home := t.TempDir()
	a.cfg.Tools.CWD = home
	a.toolReg = tools.NewRegistry(home, nil, tools.Timeouts{})
	a.toolReg.SetSafeSource(a.SafeMode)
	a.engine = identity.NewEngine(a.store, nil, ring.NewManager(), toolDiscovererAdapter{a.toolReg}, identity.Ports{
		WorkState: a.readWorkState, ToolResultChars: a.cfg.Prompt.MaxToolResultChars,
	})
	return a, home
}

func workReadbackCall(t *testing.T, a *App, ctx context.Context, name string, args map[string]any) conversation.Observation {
	t.Helper()
	raw, err := json.Marshal(args)
	if err != nil {
		t.Fatal(err)
	}
	call := llm.ToolCall{Type: "function"}
	call.Function.Name, call.Function.Arguments = name, string(raw)
	return a.executeToolCall(ctx, call)
}

func readbackSession(t *testing.T, a *App, id string) store.WorkSession {
	t.Helper()
	ws, err := a.store.WorkSessionByID(id)
	if err != nil || ws == nil {
		t.Fatalf("session %q: %+v, %v", id, ws, err)
	}
	return *ws
}

func requireReadbackTargetAbsent(t *testing.T, home string) {
	t.Helper()
	if _, err := os.Lstat(filepath.Join(home, "work")); !os.IsNotExist(err) {
		t.Fatalf("rendering created a notes tree: %v", err)
	}
}

func readbackQueueRows(t *testing.T, a *App) [][3]string {
	t.Helper()
	rows, err := a.store.DB().Query(`SELECT id, state, retry_count FROM work_queue ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out [][3]string
	for rows.Next() {
		var row [3]string
		if err := rows.Scan(&row[0], &row[1], &row[2]); err != nil {
			t.Fatal(err)
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func enqueueReadbackChild(t *testing.T, a *App, id string) {
	t.Helper()
	if _, added, err := a.store.EnqueueWorkWithSessionBelowLimit(&store.WorkItem{
		Kind: identity.SubagentWorkKind, DedupKey: id, Payload: "{}", Source: "identity",
	}, 8, id, store.SubagentDescription("bounded child task")); err != nil || !added {
		t.Fatalf("enqueue child %q: added=%v, %v", id, added, err)
	}
}

func TestCoreWorkStatusReadsTheWholeAuthoredContractWithoutChangingIt(t *testing.T) {
	a, home := workReadbackApp(t)
	const id = "legacy resident session"
	if err := a.store.StartWorkSession(t.Context(), id, "complete the recorded contract"); err != nil {
		t.Fatal(err)
	}
	plan := strings.Repeat("界", 14000) + "\nUNICODE_PLAN_TAIL_終"
	fields := map[string]any{
		"action": "update", "state": "ESTABLISHED_STATE", "focus": "CURRENT_FOCUS",
		"next_move": "EXACT_NEXT_MOVE", "plan": plan, "expected_evidence": "EXPECTED_READBACK",
		"falsifier": "SPECIFIC_FALSIFIER", "decision_needed": "EXACT_OPERATOR_CHOICE",
	}
	if obs := workReadbackCall(t, a, t.Context(), "work", fields); obs.Failed {
		t.Fatal(obs.Text)
	}
	before := readbackSession(t, a, id)
	standingBefore, authoredBefore, err := a.store.StandingStateAuthored()
	if err != nil {
		t.Fatal(err)
	}
	text, failed := workCall(t, a, `{"action":"status"}`)
	if failed {
		t.Fatal(text)
	}
	for _, want := range []string{"ESTABLISHED_STATE", "CURRENT_FOCUS", "EXACT_NEXT_MOVE", plan, "EXPECTED_READBACK", "SPECIFIC_FALSIFIER", "EXACT_OPERATOR_CHOICE", sessionNotesLine(id)} {
		if strings.Count(text, want) != 1 {
			t.Fatalf("whole readback lost, duplicated or clipped a field ending %q", compactText(want, 80))
		}
	}
	if !utf8.ValidString(text) || utf8.RuneCountInString(text) > a.engine.ToolResultLimit() || len(text) <= a.engine.ToolResultLimit() {
		t.Fatal("fixture must fit the character limit while exceeding that limit in bytes")
	}
	if after := readbackSession(t, a, id); !reflect.DeepEqual(before, after) {
		t.Fatalf("status changed its session: before=%+v after=%+v", before, after)
	}
	standingAfter, authoredAfter, err := a.store.StandingStateAuthored()
	if err != nil || standingBefore != standingAfter || authoredBefore != authoredAfter {
		t.Fatalf("status changed standing state or its authored time: %q/%d -> %q/%d, %v", standingBefore, authoredBefore, standingAfter, authoredAfter, err)
	}
	requireReadbackTargetAbsent(t, home)
}

func TestCoreWorkStatusReadsStandingStateWithoutCreatingASession(t *testing.T) {
	a, home := workReadbackApp(t)
	const standing = "STANDALONE_STANDING_STATE: wait for the operator's answer"
	if err := a.store.SetStandingState(standing); err != nil {
		t.Fatal(err)
	}
	before, authoredBefore, err := a.store.StandingStateAuthored()
	if err != nil {
		t.Fatal(err)
	}
	text, failed := workCall(t, a, `{"action":"status"}`)
	if failed || !strings.Contains(text, standing) || strings.Contains(text, "Session notes target:") {
		t.Fatalf("standing-only status invented a session or lost standing state: failed=%v, %q", failed, text)
	}
	if ws, err := a.store.ActiveWorkSession(); err != nil || ws != nil {
		t.Fatalf("standing read created a session: %+v, %v", ws, err)
	}
	after, authoredAfter, err := a.store.StandingStateAuthored()
	if err != nil || before != after || authoredBefore != authoredAfter {
		t.Fatalf("standing read changed its source: %q/%d -> %q/%d, %v", before, authoredBefore, after, authoredAfter, err)
	}
	requireReadbackTargetAbsent(t, home)
}

func TestCoreWorkStatusChildReadsOnlyItsBoundContractAndNotesTarget(t *testing.T) {
	a, home := workReadbackApp(t)
	const parent, child, sibling, old = "resident-current", "child/current", "sibling-current", "resident-old"
	oldPlan := "OLD_STANDING_PLAN_MUST_NOT_REACH_CHILD"
	if err := a.store.StartWorkSession(t.Context(), old, "previous resident task"); err != nil {
		t.Fatal(err)
	}
	if err := a.store.UpdateWorkPlan(t.Context(), old, nil, nil, &oldPlan, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	if err := a.store.DeliverWorkSession(t.Context(), old, "done", "", ""); err != nil {
		t.Fatal(err)
	}
	if err := a.store.StartWorkSession(t.Context(), parent, "resident task"); err != nil {
		t.Fatal(err)
	}
	parentPlan, siblingPlan := "PARENT_CONTRACT_MUST_NOT_REACH_CHILD", "SIBLING_CONTRACT_MUST_NOT_REACH_CHILD"
	if err := a.store.UpdateWorkPlan(t.Context(), parent, nil, nil, &parentPlan, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	if err := a.store.SetStandingState("RESIDENT_STANDING_MUST_NOT_REACH_CHILD"); err != nil {
		t.Fatal(err)
	}
	enqueueReadbackChild(t, a, child)
	enqueueReadbackChild(t, a, sibling)
	if err := a.store.UpdateWorkPlan(t.Context(), sibling, nil, nil, &siblingPlan, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	ctx := context.WithValue(t.Context(), identity.SubagentWorkSession{}, child)
	for _, authored := range []bool{true, false} {
		fields := map[string]any{"action": "update", "focus": "", "next_move": "", "plan": "", "state": "", "expected_evidence": "", "falsifier": "", "decision_needed": ""}
		if authored {
			for _, key := range []string{"focus", "next_move", "plan", "state", "expected_evidence", "falsifier", "decision_needed"} {
				fields[key] = "CHILD_OWN_" + key
			}
		}
		if obs := workReadbackCall(t, a, ctx, "work", fields); obs.Failed {
			t.Fatal(obs.Text)
		}
		before := []store.WorkSession{readbackSession(t, a, parent), readbackSession(t, a, child), readbackSession(t, a, sibling), readbackSession(t, a, old)}
		queueBefore := readbackQueueRows(t, a)
		obs := workReadbackCall(t, a, ctx, "work", map[string]any{"action": "status"})
		if obs.Failed || !strings.Contains(obs.Text, "### Your child work session: "+child) || !strings.Contains(obs.Text, sessionNotesLine(child)) {
			t.Fatalf("child lost its own readback: failed=%v, %s", obs.Failed, obs.Text)
		}
		for _, forbidden := range []string{parentPlan, siblingPlan, oldPlan, "RESIDENT_STANDING_MUST_NOT_REACH_CHILD", sessionNotesTarget(parent), sessionNotesTarget(sibling), "Standing plan", "Standing state"} {
			if strings.Contains(obs.Text, forbidden) {
				t.Fatalf("child substituted another seat's state %q: %s", forbidden, obs.Text)
			}
		}
		for _, key := range []string{"focus", "next_move", "plan", "state", "expected_evidence", "falsifier", "decision_needed"} {
			if strings.Contains(obs.Text, "CHILD_OWN_"+key) != authored {
				t.Fatalf("child readback did not follow its own authored=%v %s", authored, key)
			}
		}
		after := []store.WorkSession{readbackSession(t, a, parent), readbackSession(t, a, child), readbackSession(t, a, sibling), readbackSession(t, a, old)}
		if !reflect.DeepEqual(before, after) {
			t.Fatal("status changed a session or harvested a delivery")
		}
		if !reflect.DeepEqual(queueBefore, readbackQueueRows(t, a)) {
			t.Fatal("status advanced or retried queued work")
		}
	}
	requireReadbackTargetAbsent(t, home)
}

func TestCoreWorkStatusRefusesInvalidChildBindingsWithoutResidentFallback(t *testing.T) {
	a, home := workReadbackApp(t)
	if err := a.store.StartWorkSession(t.Context(), "resident", "resident task"); err != nil {
		t.Fatal(err)
	}
	if err := a.store.UpdateWorkState(t.Context(), "resident", "RESIDENT_SECRET_STATE"); err != nil {
		t.Fatal(err)
	}
	for name, marker := range map[string]any{"empty": "", "wrong type": 17, "missing child": "not-in-the-store", "nonchild": "resident"} {
		t.Run(name, func(t *testing.T) {
			ctx := context.WithValue(t.Context(), identity.SubagentWorkSession{}, marker)
			obs := workReadbackCall(t, a, ctx, "work", map[string]any{"action": "status"})
			if !obs.Failed || !strings.Contains(obs.Text, "status.session") || strings.Contains(obs.Text, "RESIDENT_SECRET_STATE") || strings.Contains(obs.Text, sessionNotesTarget("resident")) {
				t.Fatalf("invalid child binding read resident state: failed=%v, %s", obs.Failed, obs.Text)
			}
		})
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if out, err := a.readWorkState(ctx); err == nil || out != "" {
		t.Fatalf("cancelled read returned state: %q, %v", out, err)
	}
	requireReadbackTargetAbsent(t, home)
}

func TestCoreWorkStatusPreservesReadFailuresInsteadOfInventingAbsence(t *testing.T) {
	t.Run("closed projection", func(t *testing.T) {
		a, home := workReadbackApp(t)
		if err := a.store.Close(); err != nil {
			t.Fatal(err)
		}
		text, failed := workCall(t, a, `{"action":"status"}`)
		if !failed || !strings.Contains(text, "load active work") || strings.Contains(text, "No authored work state") {
			t.Fatalf("unreadable work became empty work: failed=%v, %q", failed, text)
		}
		ctx := context.WithValue(t.Context(), identity.SubagentWorkSession{}, "retained-child")
		obs := workReadbackCall(t, a, ctx, "work", map[string]any{"action": "status"})
		if !obs.Failed || !strings.Contains(obs.Text, "child session") || strings.Contains(obs.Text, "No authored work state") {
			t.Fatalf("unreadable child became absent: failed=%v, %q", obs.Failed, obs.Text)
		}
		requireReadbackTargetAbsent(t, home)
	})
	t.Run("standing read", func(t *testing.T) {
		a, home := workReadbackApp(t)
		plan := "FALLBACK_PLAN_MUST_NOT_REPLACE_UNREADABLE_STANDING"
		if err := a.store.StartWorkSession(t.Context(), "earlier", "previous task"); err != nil {
			t.Fatal(err)
		}
		if err := a.store.UpdateWorkPlan(t.Context(), "earlier", nil, nil, &plan, nil, nil, nil); err != nil {
			t.Fatal(err)
		}
		if err := a.store.DeliverWorkSession(t.Context(), "earlier", "done", "", ""); err != nil {
			t.Fatal(err)
		}
		if _, err := a.store.DB().Exec(`DROP TABLE standing_state`); err != nil {
			t.Fatal(err)
		}
		text, failed := workCall(t, a, `{"action":"status"}`)
		if failed || !strings.Contains(text, "Standing state") || !strings.Contains(text, "unavailable") || strings.Contains(text, plan) || strings.Contains(text, "No authored work state") {
			t.Fatalf("failed standing read was hidden or substituted: failed=%v, %s", failed, text)
		}
		requireReadbackTargetAbsent(t, home)
	})
}

func TestSessionNotesTargetIsBoundedPortableAndPureForHistoricalIDs(t *testing.T) {
	home := t.TempDir()
	t.Chdir(home)
	if got := sessionNotesTarget(""); got != "" || sessionNotesLine("") != "" {
		t.Fatalf("absence invented a notes target: %q", got)
	}
	if got := sessionNotesTarget("abc"); got != "work/ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad/notes.md" {
		t.Fatalf("notes target does not bind the exact historical ID: %q", got)
	}
	shape := regexp.MustCompile(`^work/[0-9a-f]{64}/notes\.md$`)
	seen := map[string]string{}
	for _, id := range []string{"main", "ws_uuid", "../escape", "/absolute", `C:\outside\notes`, "CON", "a\nb", "界/../終", "Case", "case", "id", " id", "é", "e\u0301", strings.Repeat("long", 10000)} {
		got := sessionNotesTarget(id)
		if !shape.MatchString(got) || path.IsAbs(got) || path.Clean(got) != got || sessionNotesTarget(id) != got {
			t.Fatalf("ID produced an unstable or nonportable path: %q -> %q", compactText(id, 80), got)
		}
		if previous, exists := seen[got]; exists {
			t.Fatalf("distinct exact IDs shared a target: %q and %q", previous, compactText(id, 80))
		}
		seen[got] = compactText(id, 80)
	}
	if entries, err := os.ReadDir(home); err != nil || len(entries) != 0 {
		t.Fatalf("deriving notes targets performed filesystem writes: %v, %v", entries, err)
	}
	if got := resumeCardFor(&store.WorkSession{ID: "session-with-no-authored-card"}); got != "" {
		t.Fatalf("notes metadata made the authored resume card noisy: %q", got)
	}
}

func TestSessionNotesUseOrdinaryFileToolsAndTheirSafeAndPathBoundaries(t *testing.T) {
	t.Run("ordinary files and SAFE", func(t *testing.T) {
		a, home := workReadbackApp(t)
		const id = "session for notes"
		if err := a.store.StartWorkSession(t.Context(), id, "file notes"); err != nil {
			t.Fatal(err)
		}
		if _, err := a.buildWorkState(); err != nil {
			t.Fatal(err)
		}
		requireReadbackTargetAbsent(t, home)
		target := sessionNotesTarget(id)
		obs := workReadbackCall(t, a, t.Context(), "write", map[string]any{"file_path": target, "content": "WRITTEN_NOTE_界"})
		if obs.Failed {
			t.Fatalf("ordinary write could not use the advertised target: %s", obs.Text)
		}
		read := workReadbackCall(t, a, t.Context(), "read", map[string]any{"file_path": target})
		if read.Failed || !strings.Contains(read.Text, "WRITTEN_NOTE_界") {
			t.Fatalf("ordinary read lost the note: failed=%v, %s", read.Failed, read.Text)
		}
		before, err := os.ReadFile(filepath.Join(home, filepath.FromSlash(target)))
		if err != nil {
			t.Fatal(err)
		}
		state, err := a.buildWorkState()
		if err != nil || !strings.Contains(state, sessionNotesLine(id)) || strings.Contains(state, "WRITTEN_NOTE_界") {
			t.Fatalf("rendering read scratch into authored state or lost its target: %q, %v", state, err)
		}
		if after, err := os.ReadFile(filepath.Join(home, filepath.FromSlash(target))); err != nil || string(before) != string(after) {
			t.Fatalf("rendering changed existing scratch: %q -> %q, %v", before, after, err)
		}
		a.mode.mode, a.mode.reason = ModeSafe, "test integrity boundary"
		a.engine.SetSafeMode("test integrity boundary")
		blocked := workReadbackCall(t, a, t.Context(), "write", map[string]any{"file_path": target, "content": "MUST_NOT_WRITE"})
		if !blocked.Failed || !blocked.Refused || blocked.ReasonCode != tools.ReasonSafeSuspended {
			t.Fatalf("SAFE let a notes write through: %+v", blocked)
		}
		read = workReadbackCall(t, a, t.Context(), "read", map[string]any{"file_path": target})
		if read.Failed || !strings.Contains(read.Text, "WRITTEN_NOTE_界") {
			t.Fatalf("SAFE lost the existing read-only note: failed=%v, %s", read.Failed, read.Text)
		}
		after, err := os.ReadFile(filepath.Join(home, filepath.FromSlash(target)))
		if err != nil || string(before) != string(after) {
			t.Fatalf("SAFE changed the note: %q -> %q, %v", before, after, err)
		}
	})
	t.Run("symlink escape", func(t *testing.T) {
		a, home := workReadbackApp(t)
		outside := t.TempDir()
		if err := os.Symlink(outside, filepath.Join(home, "work")); err != nil {
			t.Skipf("this host cannot create the symlink required by the probe: %v", err)
		}
		target := sessionNotesTarget("linked notes")
		obs := workReadbackCall(t, a, t.Context(), "write", map[string]any{"file_path": target, "content": "MUST_NOT_ESCAPE"})
		if !obs.Failed {
			t.Fatalf("advertised notes location bypassed file-tool confinement: %s", obs.Text)
		}
		if entries, err := os.ReadDir(outside); err != nil || len(entries) != 0 {
			t.Fatalf("refused notes write changed the outside directory: %v, %v", entries, err)
		}
	})
}
