package app

import (
	"context"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/llm"
	"github.com/aiii-dot-id/aii-os/internal/tools"
)

// A MODEL'S CALL TO AN ORGAN RUNS AS SENT OR NOT AT ALL, as one to a
// builtin does. An argument the organ does not declare was ignored without
// a word; commit's duplicate_ok, offered "(note only)" and refused by the
// organ, is no longer offered, and a model that still sends it is told so,
// typed, before anything runs. A call that names only what the organ
// declares runs as before.
func TestAnUnknownArgumentToAnOrganIsRefusedNamingIt(t *testing.T) {
	app, _ := dispatchApp(t)
	defer stopApp(t, app)
	call := func(name, args string) (obs struct {
		text    string
		refused bool
		code    string
	}) {
		var tc llm.ToolCall
		tc.Function.Name, tc.Function.Arguments = name, args
		o := app.executeToolCall(context.Background(), tc)
		obs.text, obs.refused, obs.code = o.Text, o.Refused, o.ReasonCode
		return obs
	}
	before := app.toolReg.MalformedCallCount()
	got := call("commit", `{"variant":"belief.upsert","statement":"anchors hold","evidence_refs":["exp_x"],"duplicate_ok":true}`)
	if !got.refused || got.code != tools.ReasonUnknownArgument || !strings.Contains(got.text, "commit does not take duplicate_ok; nothing ran.") {
		t.Fatalf("duplicate_ok on commit was not refused, typed and named: %+v", got)
	}
	if app.toolReg.MalformedCallCount() != before+1 {
		t.Error("the refused call is not counted malformed")
	}
	got = call("work", `{"action":"status","timeout":30}`)
	if got.code != tools.ReasonUnknownArgument || !strings.Contains(got.text, "work does not take timeout;") || !strings.Contains(got.text, "It takes: acceptance, action,") {
		t.Fatalf("an unknown argument to work was not refused naming it and what work takes: %+v", got)
	}
	if got := call("recall", `{"query":"probe","exact":false}`); got.refused {
		t.Fatalf("a call of declared arguments was refused: %+v", got)
	}
}

func TestAStartsStepsSetTheBudget(t *testing.T) {
	app, _ := dispatchApp(t)
	defer stopApp(t, app)
	var start llm.ToolCall
	start.Function.Name = "work"
	start.Function.Arguments = `{"action":"start","description":"budgeted at start","steps":7,"falsifier":"the count is off"}`
	start.EmissionOrdinal = 1
	if obs := app.executeToolCall(context.Background(), start); obs.Failed {
		t.Fatalf("the start failed: %s", obs.Text)
	}
	app.turn.mu.Lock()
	predicted := app.turn.turnPredicted
	app.turn.mu.Unlock()
	if predicted != 7 {
		t.Fatalf("steps=7 at start did not set the turn's budget: predicted=%d", predicted)
	}
	ws, err := app.store.ActiveWorkSession()
	if err != nil || ws == nil || ws.Falsifier != "the count is off" {
		t.Fatalf("the start's falsifier was not applied: %+v %v", ws, err)
	}
	var n int
	if err := app.store.DB().QueryRow(`SELECT COUNT(*) FROM conversations WHERE content='Work forecast accepted' AND details LIKE ? AND details LIKE '%"steps":7%'`, "%"+ws.ID+"%").Scan(&n); err != nil || n == 0 {
		t.Fatalf("the forecast was not recorded on the started session %s: %d %v", ws.ID, n, err)
	}
}
