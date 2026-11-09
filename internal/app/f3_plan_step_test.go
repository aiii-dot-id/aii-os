package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/conversation"
	"github.com/aiii-dot-id/aii-os/internal/identity"
	"github.com/aiii-dot-id/aii-os/internal/interaction"
	"github.com/aiii-dot-id/aii-os/internal/llm"
	"github.com/aiii-dot-id/aii-os/internal/store"
	"github.com/aiii-dot-id/aii-os/internal/workrecord"
)

// THE PLAN STEP A CALL SERVED (F3, the future-learning design's F3). The
// identity names a line of its plan with work update plan_step=; the calls
// that begin after it keep that line, copied, until it names another,
// writes none, or the plan is replaced. The selection is held to the plan
// in the plan update's own transaction, and each call's start reads it in
// its own; recall source=work says "step: <line>" beside a call that has
// one, and the export gives each call its plan_step.

const stairPlan = "# The stair\n1. read the stair\n  2. measure the stair  \n3. cut the treads"

// stepApp is the seam app with a main session ws_f3 holding plan.
func stepApp(t *testing.T, plan string) *App {
	t.Helper()
	a := requestApp(t)
	with := store.WorkStart{}
	if plan != "" {
		with.Plan = &plan
	}
	if err := a.store.StartWorkSessionWith(t.Context(), "ws_f3", "the stair", with); err != nil {
		t.Fatal(err)
	}
	return a
}

func workAsked(id, args string) llm.ToolCall { return askedCall(id, "work", args) }

func probeAsked(id string) llm.ToolCall { return askedCall(id, "f2_probe", "{}") }

// stepsServed is each call row of turn, by its ordinal: the plan line kept
// beside its start, nil where none is.
func stepsServed(t *testing.T, a *App, turn string) map[int]*string {
	t.Helper()
	rows, err := a.store.DB().Query(`SELECT json_extract(c.details,'$.emission_ordinal'), ps.payload FROM conversations c
		LEFT JOIN turn_annotations ps ON ps.turn_seq=c.turn_seq AND ps.kind=? WHERE c.turn_id=? AND c.kind='tool_call'`, store.AnnotationPlanStepServed, turn)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := map[int]*string{}
	for rows.Next() {
		var ordinal int
		var payload sql.NullString
		if err := rows.Scan(&ordinal, &payload); err != nil {
			t.Fatal(err)
		}
		out[ordinal] = nil
		if payload.Valid {
			var step struct {
				Line string `json:"line"`
			}
			if err := json.Unmarshal([]byte(payload.String), &step); err != nil {
				t.Fatal(err)
			}
			out[ordinal] = &step.Line
		}
	}
	return out
}

// wantSteps holds turn's calls to the lines wanted, by ordinal ("" none).
func wantSteps(t *testing.T, a *App, what, turn string, want ...string) {
	t.Helper()
	got := stepsServed(t, a, turn)
	if len(got) != len(want) {
		t.Fatalf("%s: %d calls started, want %d", what, len(got), len(want))
	}
	for i, w := range want {
		g := got[i+1]
		switch {
		case w == "" && g != nil:
			t.Errorf("%s: call %d served %q, want none", what, i+1, *g)
		case w != "" && (g == nil || *g != w):
			t.Errorf("%s: call %d served %v, want %q", what, i+1, g, w)
		}
	}
}

// resultOf is the result row of turn's call at ordinal: its content and
// its reason code.
func resultOf(t *testing.T, a *App, turn string, ordinal int) (string, string) {
	t.Helper()
	var content, code string
	if err := a.store.DB().QueryRow(`SELECT content, COALESCE(json_extract(details,'$.reason_code'),'') FROM conversations
		WHERE turn_id=? AND kind='tool_result' AND json_extract(details,'$.emission_ordinal')=?`, turn, ordinal).Scan(&content, &code); err != nil {
		t.Fatalf("the result of %s #%d: %v", turn, ordinal, err)
	}
	return content, code
}

func session(t *testing.T, a *App, id string) *store.WorkSession {
	t.Helper()
	ws, err := a.store.WorkSessionByID(id)
	if err != nil || ws == nil {
		t.Fatalf("session %s: %v", id, err)
	}
	return ws
}

// A SELECTED LINE IS WHAT THE CALLS AFTER IT SERVED, AND STILL READS AFTER
// THE PLAN CHANGES. The update that names it began before it and served
// none; the call after it in the same response, and the next response's,
// keep the line (trimmed, as the plan's lines are compared). Replacing the
// plan later leaves those calls' line as it was. recall source=work says
// "step: <line>" beside each, the export gives plan_step, and neither kind
// reaches a plugin's read. Controls: the line read at execution; the
// recall's words changed; the kinds served to plugins.
func TestTheCallsAfterASelectionRecordItsLine(t *testing.T) {
	a := stepApp(t, stairPlan)
	script := &requestScript{replies: []*llm.Response{
		answerCalls("m", usageOf(100, 5), workAsked("c1", `{"action":"update","plan_step":" 2. measure the stair"}`), probeAsked("c2")),
		answerCalls("m", usageOf(110, 5), probeAsked("c3")),
	}}
	if _, err := runRequests(t, a, script, "t_f3_select", conversation.Config{}); err != nil {
		t.Fatal(err)
	}
	if content, _ := resultOf(t, a, "t_f3_select", 1); !strings.HasSuffix(content, "← Work session updated.") {
		t.Fatalf("the selecting update answered %q", content)
	}
	const line = "2. measure the stair"
	wantSteps(t, a, "after a selection", "t_f3_select", "", line, line)

	v2 := "1. buy the timber\n2. measure the stair again"
	if err := a.store.UpdateWorkPlan(t.Context(), "ws_f3", nil, nil, &v2, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	wantSteps(t, a, "after the plan changed", "t_f3_select", "", line, line)

	lines := exportLines(t, a, workrecord.Filter{Session: "ws_f3"})["ws_f3"]
	if len(lines.Calls) != 3 || lines.Calls[0].PlanStep != nil || lines.Calls[1].PlanStep == nil || *lines.Calls[1].PlanStep != line || lines.Calls[2].PlanStep == nil || *lines.Calls[2].PlanStep != line {
		raw, _ := json.Marshal(lines.Calls)
		t.Fatalf("the export's plan_step: %s", raw)
	}
	if raw, _ := json.Marshal(lines.Calls[0]); !strings.Contains(string(raw), `"plan_step":null`) {
		t.Errorf("a call that served no step does not say null: %s", raw)
	}

	recall := a.executeToolCall(interaction.WithTurn(t.Context(), "t_f3_recall"), askedCall("r", "recall", `{"source":"work","id":"ws_f3"}`))
	if recall.Failed {
		t.Fatalf("recall source=work: %s", recall.Text)
	}
	var stepped, plain int
	for _, l := range strings.Split(recall.Text, "\n") {
		bracket, call, ok := strings.Cut(strings.TrimPrefix(l, "  [seq "), "] ")
		if !ok || !strings.HasPrefix(l, "  [seq ") {
			continue
		}
		switch {
		case strings.HasSuffix(bracket, ", step: "+line) && strings.HasPrefix(call, "f2_probe("):
			stepped++
		case strings.HasPrefix(call, "work(") && !strings.Contains(bracket, "step"):
			plain++
		}
	}
	if stepped != 2 || plain != 1 {
		t.Errorf("recall source=work shows %d stepped probes and %d unstepped updates, want 2 and 1:\n%s", stepped, plain, recall.Text)
	}

	page, err := a.QueryInteractions(t.Context(), interaction.Query{Version: 1, Source: "recorded", Filter: interaction.Filter{Kinds: []interaction.Kind{interaction.ToolCall, interaction.Notice}}})
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range page.Rows {
		for _, kind := range []string{store.AnnotationPlanStep, store.AnnotationPlanStepServed} {
			if _, has := r.Annotations[kind]; has {
				t.Errorf("a plugin's read of %s %s carries %s", r.Kind, r.ID, kind)
			}
		}
	}
}

// A CHANGED PLAN CLEARS THE SELECTION; THE SAME PLAN WRITTEN AGAIN DOES
// NOT. A line selected in v1; plan= with v1's text, byte for byte, is no
// change, and the next call still serves the line; the plan replaced by v2
// with no step: the next call serves none, even where v2 holds the same
// line. Never v1's line on v2's calls, never a v2 line inferred. Controls:
// a replacement that does not clear; the identical text clearing.
func TestAReplacedPlanClearsTheSelection(t *testing.T) {
	a := stepApp(t, stairPlan)
	same, err := json.Marshal(map[string]string{"action": "update", "plan": stairPlan})
	if err != nil {
		t.Fatal(err)
	}
	script := &requestScript{replies: []*llm.Response{
		answerCalls("m", usageOf(100, 5), workAsked("c1", `{"action":"update","plan_step":"2. measure the stair"}`), probeAsked("c2")),
		answerCalls("m", usageOf(105, 5), workAsked("c3", string(same)), probeAsked("c4")),
		answerCalls("m", usageOf(110, 5), workAsked("c5", `{"action":"update","plan":"1. buy the timber\n2. measure the stair"}`), probeAsked("c6")),
	}}
	if _, err := runRequests(t, a, script, "t_f3_replaced", conversation.Config{}); err != nil {
		t.Fatal(err)
	}
	const line = "2. measure the stair"
	wantSteps(t, a, "the same plan, then a changed one", "t_f3_replaced", "", line, line, line, line, "")
}

// A START TAKES PLAN_STEP AS AN UPDATE DOES. Held to the start's own plan,
// written with the start: the first call after it serves the line. A start
// whose step has no plan, or is no line of its plan, is refused in the
// update's words and starts nothing. Control: a start ignoring plan_step.
func TestAStartTakesPlanStep(t *testing.T) {
	a := requestApp(t)
	script := &requestScript{replies: []*llm.Response{
		answerCalls("m", usageOf(100, 5), workAsked("c1", `{"action":"start","description":"the rail","plan":"1. find the rail\n2. fit the rail","plan_step":"2. fit the rail"}`), probeAsked("c2")),
		answerCalls("m", usageOf(110, 5), workAsked("c3", `{"action":"start","description":"a step with no plan","plan_step":"1. find the rail"}`)),
		answerCalls("m", usageOf(120, 5), workAsked("c4", `{"action":"start","description":"a step not in its plan","plan":"1. sand","plan_step":"2. oil"}`)),
	}}
	if _, err := runRequests(t, a, script, "t_f3_start", conversation.Config{}); err != nil {
		t.Fatal(err)
	}
	if content, code := resultOf(t, a, "t_f3_start", 1); code != "" || !strings.Contains(content, "← Started work session ") {
		t.Fatalf("the start with plan and plan_step: %q %q", content, code)
	}
	wantSteps(t, a, "after a start that selects", "t_f3_start", "", "2. fit the rail", "2. fit the rail", "2. fit the rail")
	for _, c := range []struct {
		ordinal     int
		description string
		words       string
	}{
		{3, "a step with no plan", "plan_step needs a plan: set plan= first"},
		{4, "a step not in its plan", "plan_step is not a line of your plan; copy one exactly, or write none"},
	} {
		if content, code := resultOf(t, a, "t_f3_start", c.ordinal); code != "update.plan_step" || !strings.HasSuffix(content, "← Error: "+c.words) {
			t.Errorf("%s: %q %q", c.description, content, code)
		}
		var n int
		if err := a.store.DB().QueryRow(`SELECT COUNT(*) FROM work_sessions WHERE description=?`, c.description).Scan(&n); err != nil || n != 0 {
			t.Errorf("%s: a refused start started %d session(s) (%v)", c.description, n, err)
		}
	}
}

// PLAN AND PLAN_STEP TOGETHER HOLD THE LINE TO THE NEW PLAN. The step is a
// line of the plan the same update sets, and only of it: a line of the old
// plan alone is refused, and the refused update writes neither the plan
// nor the step. Control: validation against the old plan.
func TestPlanAndPlanStepTogetherHoldToTheNewPlan(t *testing.T) {
	a := stepApp(t, stairPlan)
	script := &requestScript{replies: []*llm.Response{
		answerCalls("m", usageOf(100, 5), workAsked("c1", `{"action":"update","plan":"1. buy the timber\n2. glue the treads","plan_step":"2. glue the treads"}`), probeAsked("c2")),
		answerCalls("m", usageOf(110, 5), workAsked("c3", `{"action":"update","plan":"1. sand\n2. oil","plan_step":"1. buy the timber"}`), probeAsked("c4")),
	}}
	if _, err := runRequests(t, a, script, "t_f3_together", conversation.Config{}); err != nil {
		t.Fatal(err)
	}
	wantSteps(t, a, "plan and plan_step together", "t_f3_together", "", "2. glue the treads", "2. glue the treads", "2. glue the treads")
	if content, code := resultOf(t, a, "t_f3_together", 3); code != "update.plan_step" || !strings.HasSuffix(content, "← Error: plan_step is not a line of your plan; copy one exactly, or write none") {
		t.Errorf("a line of the old plan alone was not refused: %q %q", content, code)
	}
	if ws := session(t, a, "ws_f3"); ws.Plan != "1. buy the timber\n2. glue the treads" {
		t.Errorf("the refused update wrote its plan: %q", ws.Plan)
	}
}

// NONE CLEARS, AND NEEDS NO PLAN. A session with no plan takes
// plan_step=none; a session with a selection is cleared by it.
func TestNoneClearsAndNeedsNoPlan(t *testing.T) {
	a := stepApp(t, "")
	script := &requestScript{replies: []*llm.Response{
		answerCalls("m", usageOf(100, 5), workAsked("c1", `{"action":"update","plan_step":"none"}`), probeAsked("c2")),
		answerCalls("m", usageOf(110, 5), workAsked("c3", `{"action":"update","plan":"1. sand\n2. oil","plan_step":"2. oil"}`), probeAsked("c4")),
		answerCalls("m", usageOf(120, 5), workAsked("c5", `{"action":"update","plan_step":" none "}`), probeAsked("c6")),
	}}
	if _, err := runRequests(t, a, script, "t_f3_none", conversation.Config{}); err != nil {
		t.Fatal(err)
	}
	if content, code := resultOf(t, a, "t_f3_none", 1); code != "" || !strings.HasSuffix(content, "← Work session updated.") {
		t.Fatalf("none with no plan: %q %q", content, code)
	}
	wantSteps(t, a, "none", "t_f3_none", "", "", "", "2. oil", "2. oil", "")
}

// A REFUSED PLAN_STEP CHANGES NOTHING. Each refusal in its words, typed:
// a line that is not the plan's, and a step with no plan. The refusal is
// the plan update's transaction's, which runs before any other write of
// the update (the verb has no check of its own;
// TestAStepRefusedAfterThePlanChangedChangesNothingElse proves the order),
// so the update's other fields, the standing state among them, are not
// written, and the selection stands. Control: the plan update moved back
// after the other writes.
func TestARefusedPlanStepChangesNothing(t *testing.T) {
	a := stepApp(t, stairPlan)
	script := &requestScript{replies: []*llm.Response{
		answerCalls("m", usageOf(100, 5), workAsked("c1", `{"action":"update","plan_step":"3. cut the treads"}`), probeAsked("c2")),
		answerCalls("m", usageOf(110, 5), workAsked("c3", `{"action":"update","state":"moved on","focus":"the rail","standing":"waiting on the saw","plan_step":"3. cut the rail"}`), probeAsked("c4")),
	}}
	if _, err := runRequests(t, a, script, "t_f3_refused", conversation.Config{}); err != nil {
		t.Fatal(err)
	}
	if content, code := resultOf(t, a, "t_f3_refused", 3); code != "update.plan_step" || !strings.HasSuffix(content, "← Error: plan_step is not a line of your plan; copy one exactly, or write none") {
		t.Errorf("not a line: %q %q", content, code)
	}
	wantSteps(t, a, "after a refused line", "t_f3_refused", "", "3. cut the treads", "3. cut the treads", "3. cut the treads")
	if ws := session(t, a, "ws_f3"); ws.State != "" || ws.Focus != "" || ws.Plan != stairPlan {
		t.Errorf("the refused update wrote state %q, focus %q, plan %q", ws.State, ws.Focus, ws.Plan)
	}
	if standing, err := a.store.StandingState(); err != nil || standing != "" {
		t.Errorf("the refused update wrote the standing state %q (%v)", standing, err)
	}

	b := stepApp(t, "")
	script = &requestScript{replies: []*llm.Response{
		answerCalls("m", usageOf(100, 5), workAsked("c1", `{"action":"update","state":"started","plan_step":"1. read the stair"}`), probeAsked("c2")),
	}}
	if _, err := runRequests(t, b, script, "t_f3_noplan", conversation.Config{}); err != nil {
		t.Fatal(err)
	}
	if content, code := resultOf(t, b, "t_f3_noplan", 1); code != "update.plan_step" || !strings.HasSuffix(content, "← Error: plan_step needs a plan: set plan= first") {
		t.Errorf("no plan: %q %q", content, code)
	}
	if ws := session(t, b, "ws_f3"); ws.State != "" {
		t.Errorf("the refused update wrote its state: %q", ws.State)
	}
	var notices int
	if err := b.store.DB().QueryRow(`SELECT COUNT(*) FROM turn_annotations WHERE kind=?`, store.AnnotationPlanStep).Scan(&notices); err != nil || notices != 0 {
		t.Errorf("a refused step left %d selections (%v)", notices, err)
	}
	wantSteps(t, b, "with no plan", "t_f3_noplan", "", "")
}

// A CHILD'S SELECTION STAYS ITS OWN. A child's update acts on its own
// session; its calls serve its line, the resident's serve the resident's,
// and neither moves the other.
func TestAChildsSelectionStaysItsOwn(t *testing.T) {
	a := stepApp(t, stairPlan)
	ctx := t.Context()
	if _, err := a.store.DB().Exec(`INSERT INTO work_queue (id, kind, payload, dedup_key, source, state) VALUES ('wq_kid', ?, '{}', 'ws_kid', 'identity', 'DONE')`, identity.SubagentWorkKind); err != nil {
		t.Fatal(err)
	}
	kidPlan := "1. find the rail\n2. fit the rail"
	if err := a.store.StartWorkSessionWith(ctx, "ws_kid", store.SubagentDescription("the rail"), store.WorkStart{Plan: &kidPlan}); err != nil {
		t.Fatal(err)
	}
	child := context.WithValue(ctx, identity.SubagentWorkSession{}, "ws_kid")
	kid := appTranscript{st: a.store, safe: a.safeTools, actor: "ws_kid"}
	main := appTranscript{st: a.store, safe: a.safeTools}
	update := func(c context.Context, tr appTranscript, turn, args string) {
		t.Helper()
		tc := workAsked("u", args)
		tc.EmissionOrdinal = 1
		if err := tr.RecordToolStart(turn, 1, "u", "work", args, "m"); err != nil {
			t.Fatal(err)
		}
		obs := appToolExecutor{a}.Execute(interaction.WithTurn(c, turn), tc)
		if obs.Failed {
			t.Fatalf("%s: %s", turn, obs.Text)
		}
		if err := tr.RecordToolDone(turn, 1, "work", args, obs); err != nil {
			t.Fatal(err)
		}
	}
	start := func(tr appTranscript, turn string) {
		t.Helper()
		if err := tr.RecordToolStart(turn, 1, "p", "f2_probe", "{}", "m"); err != nil {
			t.Fatal(err)
		}
	}
	update(child, kid, "t_kid_select", `{"action":"update","plan_step":"2. fit the rail"}`)
	start(kid, "t_kid_call")
	start(main, "t_main_call")
	wantSteps(t, a, "the child's call", "t_kid_call", "2. fit the rail")
	wantSteps(t, a, "the resident's call", "t_main_call", "")
	if ws := session(t, a, "ws_f3"); ws.Plan != stairPlan {
		t.Fatalf("the child's update reached the resident's session: %q", ws.Plan)
	}

	update(ctx, main, "t_main_select", `{"action":"update","plan_step":"1. read the stair"}`)
	start(kid, "t_kid_call2")
	start(main, "t_main_call2")
	wantSteps(t, a, "the child's next call", "t_kid_call2", "2. fit the rail")
	wantSteps(t, a, "the resident's next call", "t_main_call2", "1. read the stair")
}
