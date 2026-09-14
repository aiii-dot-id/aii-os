package app

import (
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/store"
)

// .
// .
// .
// .

func TestPlanningBriefRendersOnlyUnderAnActiveSession(t *testing.T) {
	a, _ := focusFixture(t)
	a.planningBrief = true

	before, err := a.buildWorkState()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(before, "### Before you act") {
		t.Fatalf("no session, yet the brief rendered — conversation must cost nothing:\n%s", before)
	}

	if err := a.store.StartWorkSession("ws_brief", "brief render"); err != nil {
		t.Fatal(err)
	}
	focus, next, plan := "land the brief", "negative-control it", "- [ ] one step"
	if err := a.store.UpdateWorkPlan("ws_brief", &focus, &next, &plan, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	state, err := a.buildWorkState()
	if err != nil {
		t.Fatal(err)
	}
	bi, pi := strings.Index(state, "### Before you act"), strings.Index(state, "### Resume")
	if bi < 0 {
		t.Fatalf("active session, yet no brief:\n%s", state)
	}
	if pi < 0 || bi > pi {
		t.Fatalf("the brief must be its own section before the resident's plan, not inside it (brief %d, plan %d)", bi, pi)
	}
	if strings.Contains(state[pi:], "Before you act") {
		t.Fatal("substrate text inside the resident's plan section (CS-4)")
	}

	// .
	// .
	// .
	if err := a.store.InsertTurnMetric(store.TurnMetric{TsMs: time.Now().UTC().UnixMilli(), Calls: 4, ReadOnly: 2, Rounds: 2, Predicted: 3, DeclaredOrdinal: 1}); err != nil {
		t.Fatal(err)
	}
	withRhythm, err := a.buildWorkState()
	if err != nil {
		t.Fatal(err)
	}
	ri, bi2 := strings.Index(withRhythm, "### Rhythm"), strings.Index(withRhythm, "### Before you act")
	if ri < 0 || bi2 < 0 || ri > bi2 {
		t.Fatalf("the rhythm line must render above the brief (rhythm %d, brief %d):\n%s", ri, bi2, withRhythm)
	}

	a.planningBrief = false
	off, err := a.buildWorkState()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(off, "### Before you act") {
		t.Fatal("disarmed, yet the brief rendered")
	}
}

// .
// .
func TestPlanningBriefStaysShortAndNamesTheKeys(t *testing.T) {
	if n := len(planningBriefText); n > 800 {
		t.Fatalf("brief is %d bytes; long injected doctrine reduces effectiveness — keep it under 800", n)
	}
	for _, want := range []string{"steps=", "independent=", "falsifier", "done, with its scope", "continue, with the exact resume point", "stop, with why", "METHOD.md", "Reads first", "once, before the first call that changes anything", "work update plan=", "Then act: call the tool rather than describe it"} {
		if !strings.Contains(planningBriefText, want) {
			t.Errorf("brief is missing %q", want)
		}
	}
	if strings.Contains(planningBriefText, "three times") {
		t.Error("the brief hard-codes a calibration number; the rhythm line and the mirror render the live one")
	}
}

// .
func TestPlanningBriefEnabledGate(t *testing.T) {
	f, tr := false, true
	cfg := func(family, plan, brief *bool) Config {
		var c Config
		c.Agency.HeuristicNudges, c.Agency.PlanNudge, c.Agency.PlanningBrief = family, plan, brief
		return c
	}
	for _, c := range []struct {
		name string
		cfg  Config
		want bool
	}{
		{"family absent is off", cfg(nil, nil, nil), false},
		{"family on, rest absent, is on", cfg(&tr, nil, nil), true},
		{"plan_nudge false disarms", cfg(&tr, &f, nil), false},
		{"own key false disarms", cfg(&tr, nil, &f), false},
		{"all explicit on", cfg(&tr, &tr, &tr), true},
	} {
		if got := planningBriefEnabled(c.cfg); got != c.want {
			t.Errorf("%s: planningBriefEnabled = %v, want %v", c.name, got, c.want)
		}
		hook := (&App{}).firstActHook(c.cfg)
		if (hook != nil) != c.want {
			t.Errorf("%s: firstActHook armed=%v, want %v — the two surfaces must agree", c.name, hook != nil, c.want)
		}
	}
}

// .
// .
func TestActToolCallJudgesTheWholeChain(t *testing.T) {
	sh := func(cmd string) string { return `{"command":` + jsonQuote(cmd) + `}` }
	cases := []struct {
		name, tool, args string
		act              bool
	}{
		{"read organ", "read", `{}`, false},
		{"grep organ", "grep", `{}`, false},
		{"recall", "recall", `{}`, false},
		{"work is bookkeeping", "work", `{"action":"update"}`, false},
		{"tools is discovery", "tools", `{}`, false},
		{"edit", "edit", `{}`, true},
		{"note", "note", `{}`, true},
		{"commit", "commit", `{}`, true},
		{"send", "send", `{}`, true},
		{"plugin search reads", "pl_org_example_memory_search", `{}`, false},
		{"plugin store acts", "pl_org_example_memory_store", `{}`, true},
		{"cd then grep", "shell", sh("cd /home/user && grep -n foo x.go"), false},
		{"read chained to rm", "shell", sh("cat x && rm y"), true},
		{"pipe of reads", "shell", sh("grep a f | head -5"), false},
		{"reads separated by semicolon", "shell", sh("git status; git diff"), false},
		{"ls then build", "shell", sh("ls; go build ./..."), true},
		{"echo into file", "shell", sh("echo x > /tmp/f"), true},
		{"cat into file", "shell", sh("cat x > y"), true},
		{"append into file", "shell", sh("cat x >> log"), true},
		{"stderr to null stays a read", "shell", sh("grep a f 2>/dev/null"), false},
		{"stdout to null stays a read", "shell", sh("ls > /dev/null"), false},
		{"descriptor dup stays a read", "shell", sh("grep a f 2>&1 | head"), false},
		{"bare cd decides nothing", "shell", sh("cd /home/user"), true},
		{"empty command", "shell", sh(""), true},
		{"malformed args", "shell", `{`, true},
	}
	for _, c := range cases {
		if got := actToolCall(c.tool, c.args); got != c.act {
			t.Errorf("%s: actToolCall(%s, %s) = %v, want %v", c.name, c.tool, c.args, got, c.act)
		}
	}
}

func jsonQuote(s string) string {
	b := []byte{'"'}
	for _, r := range s {
		switch r {
		case '"':
			b = append(b, '\\', '"')
		case '\\':
			b = append(b, '\\', '\\')
		default:
			b = append(b, string(r)...)
		}
	}
	return string(append(b, '"'))
}
