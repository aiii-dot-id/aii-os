package identity

import (
	"context"
	"strings"
	"testing"
)

// .
// .
// .
// .

func TestTheOfferedSurfaceIsSixFacades(t *testing.T) {
	want := []string{"note", "recall", "send", "work", "commit", "tools"}
	got := Verbs()
	if len(got) != len(want) {
		t.Fatalf("offered verbs = %d, want six", len(got))
	}
	for i, v := range got {
		if v.Name != want[i] {
			t.Fatalf("offered verb %d = %q, want %q", i, v.Name, want[i])
		}
	}
	for _, absorbed := range []string{"timer", "skill", "project", "curiosity", "measure"} {
		if lookupVerb(absorbed) == nil {
			t.Fatalf("%q must stay reachable as an internal operation", absorbed)
		}
		for _, v := range got {
			if v.Name == absorbed {
				t.Fatalf("%q is absorbed and must not be offered", absorbed)
			}
		}
	}
	if lookupVerb("no_such_verb") != nil {
		t.Fatal("an unknown name must not resolve")
	}
}

func TestWorkAbsorbsAlarmsAndRecallReadsThem(t *testing.T) {
	e, _ := timerEngine(t)
	out, err := e.ExecuteAction(ctxBG(), "verb", "work", map[string]interface{}{
		"action": "alarm.set", "id": "standup", "tag": "ops", "duration": "10m", "message": "the standup",
	})
	if err != nil || !strings.Contains(out, "standup") {
		t.Fatalf("alarm.set: %v %q", err, out)
	}
	list, err := e.ExecuteAction(ctxBG(), "verb", "recall", map[string]interface{}{"source": "alarms"})
	if err != nil || !strings.Contains(list, "standup") || !strings.Contains(list, "the standup") || !strings.Contains(list, "pending") {
		t.Fatalf("recall source=alarms: %v %q", err, list)
	}
	// .
	none, err := e.ExecuteAction(ctxBG(), "verb", "recall", map[string]interface{}{"source": "alarms", "query": "fired"})
	if err != nil || strings.Contains(none, "standup") {
		t.Fatalf("a pending alarm must not match the word fired: %v %q", err, none)
	}
	some, err := e.ExecuteAction(ctxBG(), "verb", "recall", map[string]interface{}{"source": "alarms", "query": "ops pending"})
	if err != nil || !strings.Contains(some, "standup") {
		t.Fatalf("tag and status words must match: %v %q", err, some)
	}
	if _, err := e.ExecuteAction(ctxBG(), "verb", "recall", map[string]interface{}{"source": "alarms", "after_seq": 5}); err == nil || !strings.Contains(err.Error(), "after_seq") {
		t.Fatalf("a standing source takes no cursor: %v", err)
	}
	if _, err := e.ExecuteAction(ctxBG(), "verb", "work", map[string]interface{}{"action": "alarm.list"}); err == nil || !strings.Contains(err.Error(), "recall source=alarms") {
		t.Fatalf("work must point the read at recall: %v", err)
	}
	if out, err := e.ExecuteAction(ctxBG(), "verb", "work", map[string]interface{}{"action": "alarm.cancel", "id": "standup"}); err != nil || !strings.Contains(out, "cancelled") {
		t.Fatalf("alarm.cancel: %v %q", err, out)
	}
	if list, _ := e.ExecuteAction(ctxBG(), "verb", "recall", map[string]interface{}{"source": "alarms"}); strings.Contains(list, "standup") {
		t.Fatalf("cancelled alarm still listed: %q", list)
	}
}

func TestWorkAbsorbsProjectsAndRecallReadsThem(t *testing.T) {
	engine, port := newVerbEngine(t)
	if _, err := engine.ExecuteAction(context.Background(), "verb", "work", map[string]interface{}{
		"action": "project.create", "name": "Harbor", "description": "the harbor survey", "outcome": "a sounded chart",
	}); err != nil {
		t.Fatalf("project.create: %v", err)
	}
	if port.createArgs.name != "Harbor" || port.createArgs.contract == nil || port.createArgs.contract.Outcome != "a sounded chart" {
		t.Fatalf("the facade must carry the project arguments whole: %+v", port.createArgs)
	}
	port.listResult = []ProjectInfo{
		{ID: "harbor", Name: "Harbor", State: "open", Contract: ProjectContract{Outcome: "a sounded chart"}},
		{ID: "quarry", Name: "Quarry", State: "open", Contract: ProjectContract{Outcome: "cut stone"}},
	}
	out, err := engine.ExecuteAction(context.Background(), "verb", "recall", map[string]interface{}{"source": "projects"})
	if err != nil || !strings.Contains(out, "harbor") || !strings.Contains(out, "quarry") {
		t.Fatalf("recall source=projects: %v %q", err, out)
	}
	out, err = engine.ExecuteAction(context.Background(), "verb", "recall", map[string]interface{}{"source": "projects", "query": "chart sounded"})
	if err != nil || !strings.Contains(out, "harbor") || strings.Contains(out, "quarry") {
		t.Fatalf("query words must narrow the projects: %v %q", err, out)
	}
	out, err = engine.ExecuteAction(context.Background(), "verb", "recall", map[string]interface{}{"source": "projects", "query": "granite"})
	if err != nil || !strings.Contains(out, "No project mentions") {
		t.Fatalf("no project carrying the words must say so: %v %q", err, out)
	}
	if _, err := engine.ExecuteAction(context.Background(), "verb", "work", map[string]interface{}{"action": "project.list"}); err == nil || !strings.Contains(err.Error(), "recall source=projects") {
		t.Fatalf("work must point the read at recall: %v", err)
	}
	if _, err := engine.ExecuteAction(context.Background(), "verb", "work", map[string]interface{}{"action": "project.waive", "project": "harbor", "item": "a sounded chart", "reason": "charted by the harbourmaster"}); err != nil {
		t.Fatalf("project.waive: %v", err)
	}
	if port.waiveArgs.id != "harbor" || port.waiveArgs.reason != "charted by the harbourmaster" {
		t.Fatalf("waive arguments not carried: %+v", port.waiveArgs)
	}
	if _, err := engine.ExecuteAction(context.Background(), "verb", "work", map[string]interface{}{"action": "project.bogus"}); err == nil || !strings.Contains(err.Error(), "project.create") {
		t.Fatalf("an unknown project mode must fail closed naming the modes: %v", err)
	}
}

func TestWorkAbsorbsCuriosityAndMeasure(t *testing.T) {
	engine, st, _, _, _ := setupEngine(t)
	if _, err := engine.ExecuteAction(context.Background(), "verb", "work", map[string]interface{}{"action": "curiosity"}); err == nil {
		t.Fatal("a cue needs a subject")
	}
	if _, err := engine.ExecuteAction(context.Background(), "verb", "work", map[string]interface{}{
		"action": "curiosity", "subject": "the shape of tail latency", "why": "bimodal under load", "pointer": "ws_probe",
	}); err != nil {
		t.Fatalf("curiosity: %v", err)
	}
	if c, _ := st.CuriosityCue(); c == nil || c.Subject != "the shape of tail latency" || c.Pointer != "ws_probe" {
		t.Fatalf("cue not set through work: %+v", c)
	}
	out, err := engine.ExecuteAction(context.Background(), "verb", "recall", map[string]interface{}{"source": "curiosity"})
	if err != nil || !strings.Contains(out, "tail latency") || !strings.Contains(out, "invitation, not a task") {
		t.Fatalf("recall source=curiosity: %v %q", err, out)
	}
	if _, err := engine.ExecuteAction(context.Background(), "verb", "work", map[string]interface{}{"action": "curiosity.clear"}); err != nil {
		t.Fatalf("curiosity.clear: %v", err)
	}
	if c, _ := st.CuriosityCue(); c != nil {
		t.Fatalf("cue not cleared: %+v", c)
	}
	out, err = engine.ExecuteAction(context.Background(), "verb", "recall", map[string]interface{}{"source": "curiosity"})
	if err != nil || !strings.Contains(out, "No curiosity set") || !strings.Contains(out, "work action=curiosity") {
		t.Fatalf("an empty cue must teach the facade path: %v %q", err, out)
	}
	out, err = engine.ExecuteAction(context.Background(), "verb", "work", map[string]interface{}{"action": "measure", "hours": 24})
	if err != nil || !strings.Contains(out, "Outcome measurement") || !strings.Contains(out, "not success") {
		t.Fatalf("work action=measure: %v %q", err, out)
	}
}

func TestCommitAbsorbsSkillProposalsAndRecallReadsThem(t *testing.T) {
	engine, _, _, _, _ := setupEngine(t)
	if _, err := engine.ExecuteAction(context.Background(), "verb", "commit", map[string]interface{}{
		"variant": "skill.propose", "title": "batch organ reads", "delta": "gather reads before shell",
	}); err == nil {
		t.Fatal("a proposal without evidence is an opinion")
	}
	out, err := engine.ExecuteAction(context.Background(), "verb", "commit", map[string]interface{}{
		"variant": "skill.propose", "title": "batch organ reads", "delta": "gather reads before shell", "evidence": "the 2026-09-10 review of my own legs",
	})
	if err != nil || !strings.Contains(out, "Skill proposal sp_") {
		t.Fatalf("skill.propose: %v %q", err, out)
	}
	list, err := engine.ExecuteAction(context.Background(), "verb", "recall", map[string]interface{}{"source": "skills"})
	if err != nil || !strings.Contains(list, "batch organ reads") {
		t.Fatalf("recall source=skills: %v %q", err, list)
	}
	if none, err := engine.ExecuteAction(context.Background(), "verb", "recall", map[string]interface{}{"source": "skills", "query": "latency"}); err != nil || strings.Contains(none, "batch organ reads") || !strings.Contains(none, "No skill proposal mentions") {
		t.Fatalf("query words must narrow: %v %q", err, none)
	}
	if _, err := engine.ExecuteAction(context.Background(), "verb", "recall", map[string]interface{}{"source": "skill"}); err == nil || !strings.Contains(err.Error(), "skills") {
		t.Fatalf("an unknown source must name the real ones: %v", err)
	}
}
