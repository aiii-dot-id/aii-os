package identity

import (
	"context"
	"slices"
	"strings"
	"testing"
)

// .
// .
// .
// .

type fakeContinuityPort struct{ calls []string }

func (p *fakeContinuityPort) Read(_ context.Context, query string) (string, error) {
	p.calls = append(p.calls, "read:"+query)
	return "six lines", nil
}
func (p *fakeContinuityPort) Take(context.Context) (string, error) {
	p.calls = append(p.calls, "take")
	return "taken", nil
}
func (p *fakeContinuityPort) Verify(_ context.Context, name string) (string, error) {
	p.calls = append(p.calls, "verify:"+name)
	return "proved", nil
}

func continuityEngine(t *testing.T) (*Engine, *fakeContinuityPort) {
	t.Helper()
	e, _, _, _, _ := setupEngine(t)
	port := &fakeContinuityPort{}
	e.SetContinuity(port)
	return e, port
}

// .
// .
func TestTheContinuityModesAreRoutedByRecallAndWork(t *testing.T) {
	e, port := continuityEngine(t)
	for _, call := range []struct {
		verb string
		args map[string]interface{}
		want string
	}{
		{"recall", map[string]interface{}{"query": "", "source": "continuity"}, "read:"},
		{"recall", map[string]interface{}{"query": "snapshots", "source": "continuity"}, "read:snapshots"},
		{"work", map[string]interface{}{"action": "backup.take"}, "take"},
		{"work", map[string]interface{}{"action": "backup.verify"}, "verify:"},
		{"work", map[string]interface{}{"action": "backup.verify", "id": " ledger-20260920T080005Z-seq1402 "}, "verify:ledger-20260920T080005Z-seq1402"},
	} {
		port.calls = nil
		if _, err := e.ExecuteAction(ctxBG(), "verb", call.verb, call.args); err != nil {
			t.Errorf("%s %v: %v", call.verb, call.args, err)
			continue
		}
		if len(port.calls) != 1 || port.calls[0] != call.want {
			t.Errorf("%s %v reached the host as %v, want %q", call.verb, call.args, port.calls, call.want)
		}
	}
	// .
	if _, err := e.ExecuteAction(ctxBG(), "verb", "recall", map[string]interface{}{"query": "", "source": "continuity", "after_seq": float64(3)}); err == nil {
		t.Error("continuity was paged like a store of the record")
	}
}

// .
// .
// .
// .
func TestAWorkerCannotTakeOrProveASnapshot(t *testing.T) {
	e, port := continuityEngine(t)
	worker := context.WithValue(ctxBG(), SubagentDepth{}, 1)
	for _, action := range []string{"backup.take", "backup.verify"} {
		_, err := e.ExecuteAction(worker, "verb", "work", map[string]interface{}{"action": action, "_subagent_depth": float64(0)})
		if err == nil || !strings.Contains(err.Error(), "not a worker's") {
			t.Errorf("A WORKER'S %s REACHED THE HOST: %v", action, err)
		}
	}
	if len(port.calls) != 0 {
		t.Fatalf("the host was reached from a worker's seat: %v", port.calls)
	}
	if _, err := e.ExecuteAction(worker, "verb", "recall", map[string]interface{}{"query": "", "source": "continuity"}); err != nil || len(port.calls) != 1 {
		t.Errorf("the read is open to every seat: %v %v", err, port.calls)
	}
}

// .
// .
// .
// .
func TestTheOperationRefusesEverythingItDoesNotName(t *testing.T) {
	e, port := continuityEngine(t)
	for _, action := range []string{"restore", "escrow.create", "escrow.check", "prune", "disable", "delete", "open", ""} {
		if _, err := e.ExecuteAction(ctxBG(), "verb", "continuity", map[string]interface{}{"action": action, "id": "ledger-20260920T080005Z-seq1402"}); err == nil {
			t.Errorf("the operation performed %q", action)
		}
	}
	for _, action := range []string{"backup.restore", "backup.delete", "backup.prune", "backup.open", "backup."} {
		_, err := e.ExecuteAction(ctxBG(), "verb", "work", map[string]interface{}{"action": action})
		if err == nil || !strings.Contains(err.Error(), "your operator's to do") {
			t.Errorf("work action=%s: %v", action, err)
		}
	}
	if len(port.calls) != 0 {
		t.Fatalf("a refused action reached the host: %v", port.calls)
	}
	// .
	bare, _, _, _, _ := setupEngine(t)
	if _, err := bare.ExecuteAction(ctxBG(), "verb", "work", map[string]interface{}{"action": "backup.take"}); err == nil || !strings.Contains(err.Error(), "keeps no snapshots") {
		t.Errorf("a runtime with no maintenance: %v", err)
	}
}

// .
// .
// .
func TestTheContinuitySurfaceIsAdvertisedAndIsNotATool(t *testing.T) {
	if n := len(Verbs()); n != 6 {
		t.Fatalf("the model is offered %d facades; the rule is six", n)
	}
	if lookupOffered("continuity") != nil {
		t.Fatal("continuity is offered to the model as a tool of its own")
	}
	if v := lookupVerb("continuity"); v == nil || v.Params != nil {
		t.Fatalf("continuity must be an absorbed operation with no schema of its own: %+v", v)
	}
	enumOf := func(verb, param string) []string {
		props, _ := lookupVerb(verb).Params["properties"].(map[string]interface{})
		p, _ := props[param].(map[string]interface{})
		vals, _ := p["enum"].([]string)
		return vals
	}
	for _, action := range []string{"backup.take", "backup.verify"} {
		if !slices.Contains(enumOf("work", "action"), action) {
			t.Errorf("work does not advertise %s: a mode that is routed and not advertised cannot be discovered", action)
		}
		if !strings.Contains(lookupVerb("work").Description, action) {
			t.Errorf("the work charter prose does not name %s", action)
		}
	}
	if !slices.Contains(enumOf("recall", "source"), "continuity") {
		t.Error("recall does not advertise source=continuity")
	}
	props, _ := lookupVerb("work").Params["properties"].(map[string]interface{})
	for name := range props {
		if strings.Contains(name, "backup") || strings.Contains(name, "snapshot") || strings.Contains(name, "continuity") {
			t.Errorf("work gained a parameter for this surface (%s); id is the one it reads, and work already had it", name)
		}
	}
	id, _ := props["id"].(map[string]interface{})
	if desc, _ := id["description"].(string); !strings.Contains(desc, "(backup.verify)") {
		t.Errorf("id does not name the mode that reads it: %q", desc)
	}
}
