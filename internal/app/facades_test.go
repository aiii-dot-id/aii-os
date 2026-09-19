package app

import (
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/identity"
	"github.com/aiii-dot-id/aii-os/internal/tools"
)

// .
// .
// .
func TestThePromptOffersSixFacades(t *testing.T) {
	a := &App{toolReg: tools.NewRegistry(t.TempDir(), nil, tools.Timeouts{})}
	offered := map[string]bool{}
	for _, v := range identity.Verbs() {
		offered[v.Name] = true
	}
	var verbs []string
	byName := map[string]map[string]interface{}{}
	for _, def := range a.buildToolDefinitions() {
		byName[def.Function.Name] = def.Function.Parameters
		if offered[def.Function.Name] {
			verbs = append(verbs, def.Function.Name)
		}
	}
	if got, want := strings.Join(verbs, " "), "note recall send work commit tools"; got != want {
		t.Fatalf("offered verbs = %q, want %q", got, want)
	}
	enum := func(verb, param string) []string {
		props := byName[verb]["properties"].(map[string]interface{})
		p, ok := props[param].(map[string]interface{})
		if !ok {
			t.Fatalf("%s has no %s", verb, param)
		}
		vals, _ := p["enum"].([]string)
		return vals
	}
	has := func(vals []string, want string) bool {
		for _, v := range vals {
			if v == want {
				return true
			}
		}
		return false
	}
	for _, mode := range []string{"measure", "alarm.set", "alarm.cancel", "project.create", "project.update", "project.close", "project.select", "project.deselect", "project.evidence", "project.waive", "curiosity", "curiosity.clear", "voice.mode"} {
		if !has(enum("work", "action"), mode) {
			t.Errorf("work action enum must advertise %s", mode)
		}
	}
	for _, param := range []string{"hours", "when", "duration", "message", "project", "outcome", "acceptance", "item", "class", "subject", "pointer", "mode", "listen", "speak"} {
		if _, ok := byName["work"]["properties"].(map[string]interface{})[param]; !ok {
			t.Errorf("work schema must carry %s for its absorbed modes", param)
		}
	}
	if !has(enum("commit", "variant"), "skill.propose") {
		t.Error("commit variant enum must advertise skill.propose")
	}
	for _, source := range []string{"alarms", "projects", "skills", "curiosity"} {
		if !has(enum("recall", "source"), source) {
			t.Errorf("recall source enum must advertise %s", source)
		}
	}
	for _, absorbed := range []string{"timer", "skill", "project", "curiosity", "measure"} {
		if _, ok := byName[absorbed]; ok {
			t.Errorf("%s is absorbed and must not be a tool", absorbed)
		}
	}
}

func TestWorkReadsAreReadOnlyCalls(t *testing.T) {
	cases := []struct {
		args string
		want bool
	}{
		{`{"action":"status"}`, true},
		{`{"action":"measure","hours":1}`, true},
		{`{"action":"update","state":"x"}`, false},
		{`{"action":"alarm.set","duration":"1m"}`, false},
		{`{"action":"project.create","name":"x"}`, false},
		{`{"action":"curiosity","subject":"x"}`, false},
		{`{"action":"spawn","goal":"x"}`, false},
		{`{action`, false},
	}
	for _, c := range cases {
		if got := readOnlyToolCall("work", c.args); got != c.want {
			t.Errorf("readOnlyToolCall(work, %s) = %v, want %v", c.args, got, c.want)
		}
	}
	if !readOnlyToolCall("recall", `{"source":"alarms"}`) {
		t.Error("recall is read-only whatever the source")
	}
	if readOnlyToolCall("measure", `{}`) || readOnlyToolCall("curiosity", `{"action":"show"}`) {
		t.Error("the absorbed names are not tools and classify as nothing")
	}
}
