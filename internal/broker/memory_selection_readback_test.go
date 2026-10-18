package broker

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

func selectionReadback(t *testing.T, b *Binding, args string, missingID bool) (recallResult, memorySelectionContext) {
	t.Helper()
	m := dispatch(t, b, memParams(opMemoryRecall, args))
	if missingID {
		wantResult(t, m, statusFailed, reasonMemoryNotFound)
	} else {
		wantResult(t, m, statusSucceeded, "")
	}
	var r recallResult
	var scoped struct {
		SelectionContext *memorySelectionContext `json:"selection_context"`
	}
	if err := json.Unmarshal(m["operation_result"], &r); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(m["operation_result"], &scoped); err != nil {
		t.Fatal(err)
	}
	if scoped.SelectionContext == nil {
		t.Fatal("host omitted resolved selection")
	}
	if strings.Contains(string(m["operation_result"]), "private-query-marker") {
		t.Fatal("query reflected in selection readback")
	}
	return r, *scoped.SelectionContext
}

func TestMemorySelectionReadbackUsesTheResolvedPredicate(t *testing.T) {
	h, b := memHost(t, Config{})
	memoryTask(t, b, "work-a", "project-a")
	if err := h.cfg.Store.SetActiveProject("unrelated-ui-project"); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []memorySelectionContext{
		{Selection: ""}, {Selection: "legacy"}, {Selection: "reusable"},
		{Selection: "project", ProjectID: "project-a"},
		{Selection: "task", ProjectID: "project-a", WorkSessionID: "work-a"},
		{Selection: "relevant", ProjectID: "project-a", WorkSessionID: "work-a"},
	} {
		for _, selector := range []string{`"query":"private-query-marker"`, `"recent":true`, `"id":"missing-note"`} {
			args := fmt.Sprintf(`{%s,"selection":%q}`, selector, tc.Selection)
			_, got := selectionReadback(t, b, args, strings.HasPrefix(selector, `"id"`))
			if got != tc {
				t.Fatalf("selection %s: got %+v want %+v", args, got, tc)
			}
		}
	}

	wantResult(t, dispatch(t, b, memParams(opMemoryRecall, `{"recent":true,"selection":"relevant","work_session_id":"forged"}`)), statusDenied, reasonArgumentInvalid)
	for _, reason := range []string{"no_session", "session_lookup_failed"} {
		b.BeginOperation(OperationScope{Operation: "search", SessionReason: reason, Declared: true, Capabilities: []string{"ring4.memory"}})
		if reason == "session_lookup_failed" {
			wantResult(t, dispatch(t, b, memParams(opMemoryRecall, `{"recent":true,"selection":"relevant"}`)), statusDenied, reasonArgumentInvalid)
			continue
		}
		for _, selector := range []string{`"query":"missing"`, `"recent":true`, `"id":"missing-note"`} {
			r, got := selectionReadback(t, b, `{`+selector+`,"selection":"relevant"}`, strings.HasPrefix(selector, `"id"`))
			if got != (memorySelectionContext{Selection: "relevant", Reason: "no_session"}) || len(r.Hits) != 0 {
				t.Fatal(got, r)
			}
		}
		for _, selection := range []string{"task", "project"} {
			wantResult(t, dispatch(t, b, memParams(opMemoryRecall, fmt.Sprintf(`{"recent":true,"selection":%q}`, selection))), statusDenied, reasonArgumentInvalid)
		}
	}
}

func TestMemorySelectionReadbackRetainsProjectLessonsAndIsolation(t *testing.T) {
	h, b := memHost(t, Config{Grants: map[string]Grant{"p": {Memory: true}, "other-plugin": {Memory: true}}})
	memoryTask(t, b, "first-work", "project-a")
	lesson := remember(t, b, `{"text":"Validate generated fixtures before publishing the workflow output","applicability":"project"}`)
	private := remember(t, b, `{"text":"Validate this task only","applicability":"task"}`)
	portable := remember(t, b, `{"text":"Validate a returned result before treating it as evidence","applicability":"reusable"}`)
	other := h.Bind("other-plugin", packagefmt.TierT1, []string{"ring4.memory"})
	setMemoryTask(other, "first-work")
	foreign := remember(t, other, `{"text":"Validate foreign plugin note","applicability":"project"}`)
	memoryTask(t, b, "later-work", "project-a")
	r, context := selectionReadback(t, b, `{"query":"Validate","selection":"relevant"}`, false)
	ids := map[string]bool{}
	for _, hit := range r.Hits {
		ids[hit.ID] = true
	}
	if !ids[lesson.ID] || !ids[portable.ID] || ids[private.ID] || ids[foreign.ID] {
		t.Fatal("wrong learned-note boundary", ids)
	}
	if context.WorkSessionID != "later-work" || context.ProjectID != "project-a" {
		t.Fatal(context)
	}
	_, detail := selectionReadback(t, b, fmt.Sprintf(`{"id":%q,"selection":"project"}`, lesson.ID), false)
	if detail != (memorySelectionContext{Selection: "project", ProjectID: "project-a"}) {
		t.Fatal(detail)
	}
	memoryTask(t, b, "foreign-work", "project-b")
	r, _ = selectionReadback(t, b, `{"recent":true,"selection":"relevant"}`, false)
	if len(r.Hits) != 1 || r.Hits[0].ID != portable.ID {
		t.Fatal("cross-project note exposed", r)
	}
	_, absent := selectionReadback(t, b, fmt.Sprintf(`{"id":%q,"selection":"relevant"}`, lesson.ID), true)
	if absent.ProjectID != "project-b" || absent.WorkSessionID != "foreign-work" {
		t.Fatal("missing detail leaked note scope", absent)
	}
	b.BeginOperation(OperationScope{Operation: "search", SessionReason: "no_session", Declared: true, Capabilities: []string{"ring4.memory"}})
	r, context = selectionReadback(t, b, `{"recent":true,"selection":"relevant"}`, false)
	if len(r.Hits) != 1 || r.Hits[0].ID != portable.ID || context.Reason != "no_session" {
		t.Fatal(r, context)
	}
	selectionReadback(t, b, fmt.Sprintf(`{"id":%q,"selection":"relevant"}`, lesson.ID), true)
}

func TestMemorySelectionReadbackPreservesAutomaticReadOnlyScope(t *testing.T) {
	h, b := memHost(t, Config{})
	memoryTask(t, b, "automatic-work", "automatic-project")
	note := remember(t, b, `{"text":"compiler fixture validation","applicability":"project"}`)
	ref := store.MemoryRef{Store: "plugin_memories", ID: note.ID}
	before, found, err := h.cfg.Store.MemoryAccessOf(ref)
	if err != nil {
		t.Fatal(err)
	}
	b.BeginOperation(OperationScope{Operation: "context", ContextRead: true, WorkSession: "automatic-work", Declared: true, Capabilities: []string{"ring4.memory"}, Effects: EffectsReadInternal})
	for _, args := range []string{`{"query":"compiler fixture","any_words":true,"selection":"relevant"}`, `{"recent":true,"selection":"relevant"}`, fmt.Sprintf(`{"id":%q,"selection":"relevant"}`, note.ID)} {
		r, c := selectionReadback(t, b, args, false)
		if len(r.Hits) != 1 || r.Hits[0].ID != note.ID || c.ProjectID != "automatic-project" {
			t.Fatal(r, c)
		}
	}
	after, afterFound, err := h.cfg.Store.MemoryAccessOf(ref)
	if err != nil || found != afterFound || !reflect.DeepEqual(before, after) {
		t.Fatal("automatic read reinforced", before, after, err)
	}
	wantErrorReason(t, dispatch(t, b, memParams(opMemoryRemember, `{"text":"must not write"}`)), "CONTEXT_READ_ONLY")
}
