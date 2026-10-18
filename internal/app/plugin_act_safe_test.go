package app

import (
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/broker"
)

func TestAConfirmedActRunsInSAFEOnlyAsADeclaredRead(t *testing.T) {
	classes := []struct {
		effects string
		runs    bool
	}{
		{broker.EffectsReadInternal, true},
		{broker.EffectsReadExternal, true},
		{"", false},
		{broker.EffectsWriteLocal, false},
		{broker.EffectsWriteExternal, false},
		{broker.EffectsExec, false},
	}
	for _, c := range classes {
		name := c.effects
		if name == "" {
			name = "undeclared"
		}
		t.Run(name, func(t *testing.T) {
			a := standingApp(t)
			defer a.bgCancel()
			a.safeTools = &safeToolRecord{}
			tool := &authorshipTool{text: "OBSERVED"}
			a.toolReg.Register(tool)
			newAct := func(id string) *pendingAct {
				return &pendingAct{ID: id + "-" + name, Plugin: "fixture", Tool: tool.Name(), Operation: "run", Effects: c.effects, Args: map[string]any{}}
			}

			// Control: outside SAFE every class runs, so the refusal below is
			// SAFE's and nothing else's.
			if err := a.runAct(t.Context(), newAct("open"), "confirmed"); err != nil || tool.calls != 1 {
				t.Fatalf("outside SAFE a %q act runs once: %v (calls %d)", c.effects, err, tool.calls)
			}

			a.enterSafe("test: the record is frozen")
			act := newAct("safe")
			err := a.runAct(t.Context(), act, "confirmed")
			outcome := safeActOutcome(a, act.ID)
			if c.runs {
				if err != nil || tool.calls != 2 {
					t.Fatalf("a declared read runs in SAFE: %v (calls %d)", err, tool.calls)
				}
				if !strings.Contains(outcome, "confirmed run of fixture") {
					t.Fatalf("a read that ran is recorded confirmed: %q", outcome)
				}
				return
			}
			if tool.calls != 1 {
				t.Fatalf("a %q act ran in SAFE (calls %d)", c.effects, tool.calls)
			}
			if err == nil || !strings.Contains(err.Error(), "SAFE (test: the record is frozen)") || !strings.Contains(err.Error(), "nothing ran") {
				t.Fatalf("the refusal names SAFE and its reason, and says nothing ran: %v", err)
			}
			want := c.effects + " operation"
			if c.effects == "" {
				want = "an operation of no declared effect class"
			}
			if !strings.Contains(err.Error(), want) {
				t.Fatalf("the refusal says what the act is, %q: %v", want, err)
			}
			if !strings.Contains(outcome, "refused run of fixture") || !strings.Contains(outcome, "SAFE holds (test: the record is frozen)") {
				t.Fatalf("the act is recorded refused with SAFE's reason: %q", outcome)
			}
		})
	}
}

// safeActOutcome is the last outcome the host retained for the act. SAFE
// freezes the store, so the engine keeps it in memory, as it keeps every
// word SAFE says.
func safeActOutcome(a *App, actID string) string {
	var found string
	for _, turn := range a.engine.SafeTranscript() {
		if strings.Contains(turn.Content, "[act "+actID+"]") {
			found = turn.Content
		}
	}
	return found
}

// THE TWO GATES THAT LET A READ THROUGH WHILE A WRITE WAITS ASK ONE QUESTION.
// The operator's read-only grant (admitOperation) and SAFE (runAct) each
// decided for themselves whether a class is a read, and differed on the
// undeclared class: the grant refused it, SAFE let it run. They share one
// predicate now, so they cannot differ again.
func TestTheReadOnlyGrantAndSAFEAskTheSameQuestionOfAnEffectClass(t *testing.T) {
	a := standingApp(t)
	defer a.bgCancel()
	a.safeTools = &safeToolRecord{}
	tool := &authorshipTool{text: "OBSERVED"}
	a.toolReg.Register(tool)
	a.cfg.Plugins.Grants = map[string]broker.Grant{"fixture": {ReadOnly: true}}
	a.enterSafe("test: the record is frozen")
	for _, effects := range []string{broker.EffectsReadInternal, broker.EffectsReadExternal, "", broker.EffectsWriteLocal, broker.EffectsWriteExternal, broker.EffectsExec} {
		before := tool.calls
		act := &pendingAct{ID: "same-" + effects, Plugin: "fixture", Tool: tool.Name(), Operation: "run", Effects: effects, Args: map[string]any{}}
		_ = a.runAct(t.Context(), act, "confirmed")
		admitted := a.admitOperation("fixture", "run", effects) == nil
		ranInSAFE := tool.calls > before
		wantRead := effects == broker.EffectsReadInternal || effects == broker.EffectsReadExternal
		if admitted != wantRead || ranInSAFE != wantRead {
			t.Errorf("effect class %q: the read-only grant admits it = %v, SAFE runs it = %v; only a declared read does both", effects, admitted, ranInSAFE)
		}
	}
}
