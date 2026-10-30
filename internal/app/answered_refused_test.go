package app

import (
	"context"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/interaction"
	"github.com/aiii-dot-id/aii-os/internal/llm"
	"github.com/aiii-dot-id/aii-os/internal/tools"
)

func TestAStorePluginRefusedIsAFailedCallThatBringsItsLessons(t *testing.T) {
	a, _ := promptCacheApp(t)
	addContextProbe(t, a, `{"text":"lesson: a lesson needs its own words","detail_route":"search id=lesson-store"}`)
	const refused, kept = `{"stored":false,"refused":true,"reasonCode":"OPERATION_ARGUMENT_INVALID"}`, `{"stored":true}`
	for name, out := range map[string]tools.Result{
		"pl_example_store_refused": {Output: refused, AnsweredRefused: true},
		"pl_example_store_kept":    {Output: kept},
	} {
		if err := a.toolReg.RegisterDynamic(&fixtureTool{name: name, run: func(context.Context) (tools.Result, error) { return out, nil }}, "org.example.memory"); err != nil {
			t.Fatal(err)
		}
	}
	call := func(name string) string {
		t.Helper()
		var tc llm.ToolCall
		tc.ID, tc.Type, tc.Function.Name, tc.Function.Arguments = "c1", "function", name, "{}"
		obs := appToolExecutor{a}.Execute(context.Background(), tc)
		want := interaction.Succeeded
		if name == "pl_example_store_refused" {
			want = interaction.Failed
		}
		if got := observedOutcome(obs); got != want {
			t.Fatalf("%s is recorded %q, want %q (%+v)", name, got, want, obs)
		}
		return obs.Text
	}

	first := call("pl_example_store_refused")
	if !strings.Contains(first, refused) || strings.Contains(first, "Error:") {
		t.Fatalf("the identity does not read the plugin's own answer:\n%s", first)
	}
	if !strings.Contains(first, "Your notes about failures like this one:") || !strings.Contains(first, "lesson: a lesson needs its own words") {
		t.Fatalf("a refused store's lessons did not follow it:\n%s", first)
	}
	if again := call("pl_example_store_refused"); strings.Contains(again, "Your notes about failures like this one:") {
		t.Fatalf("the same refusal brought its lessons twice:\n%s", again)
	}
	if control := call("pl_example_store_kept"); !strings.Contains(control, kept) || strings.Contains(control, "Your notes about failures") {
		t.Fatalf("a kept store reads as a failure:\n%s", control)
	}
}

// AN OPERATOR'S ACT IS HELD TO THE SAME WORD. An act the operator
// confirmed whose operation answers, as a success, that it refused is not
// "confirmed" and done: its tool result is recorded failed, the operator's
// record says the operation refused, and the operator is told it was
// refused. The plugin's words stay in the tool result, never the
// operator's record (TestOperatorActFailureTextHasToolAuthorship).
func TestAnOperatorActWhoseAnswerSaysRefusedIsRecordedRefused(t *testing.T) {
	a := standingApp(t)
	defer a.bgCancel()
	a.toolReg.Register(&fixtureTool{name: "pl_fixture_store", run: func(context.Context) (tools.Result, error) {
		return tools.Result{Output: `{"stored":false,"refused":true}`, AnsweredRefused: true}, nil
	}})
	act := &pendingAct{ID: "refused-act", Plugin: "fixture", Tool: "pl_fixture_store", Operation: "store", Args: map[string]any{}}
	err := a.runAct(t.Context(), act, "confirmed")
	if err == nil || !strings.Contains(err.Error(), "ran and was refused: "+actAnsweredRefused) {
		t.Fatalf("the operator is told %v, want that the act was refused", err)
	}
	var outcome string
	if err := a.store.DB().QueryRow(`SELECT outcome FROM conversations WHERE kind='tool_result' AND json_extract(details, '$.tool')='pl_fixture_store'`).Scan(&outcome); err != nil || outcome != string(interaction.Failed) {
		t.Fatalf("the act's tool result is recorded %q (%v), want failed", outcome, err)
	}
	var said int
	if err := a.store.DB().QueryRow(`SELECT COUNT(*) FROM conversations WHERE role='operator' AND content LIKE '%confirmed, and the operation refused%'`).Scan(&said); err != nil || said != 1 {
		t.Fatalf("the operator's record of the act does not say the operation refused: %d %v", said, err)
	}
}
