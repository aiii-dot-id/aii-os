package broker

import (
	"fmt"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/store"
)

func TestMemoryChildKeepsItsParentProjectAfterFocusSwitch(t *testing.T) {
	h, b := memHost(t, Config{})
	memoryTask(t, b, "parent-a", "project-a")
	inA := remember(t, b, `{"text":"project A method","applicability":"project"}`)
	memoryTask(t, b, "parent-b", "project-b")
	inB := remember(t, b, `{"text":"project B method","applicability":"project"}`)
	item := &store.WorkItem{Kind: store.SubagentWorkKind, Payload: `{"ws_id":"child-a","parent_work_session_id":"parent-a"}`, DedupKey: "child-a", Source: "identity"}
	if _, added, err := h.cfg.Store.EnqueueWorkWithSessionBelowLimit(item, 4, "child-a", store.SubagentDescription("child of A")); err != nil || !added {
		t.Fatalf("enqueue: %v %v", added, err)
	}
	setMemoryTask(b, "child-a")
	selection, err := b.memorySelection("project")
	if err != nil || selection.Project != "project-a" || selection.WorkSession != "child-a" {
		t.Fatalf("selection=%+v err=%v", selection, err)
	}
	note := remember(t, b, `{"text":"child A working note","applicability":"task"}`)
	stored, found, err := h.cfg.Store.PluginMemoryGet(b.storageScope(), note.ID)
	if err != nil || !found || stored.Project != "project-a" || stored.WorkSession != "child-a" {
		t.Fatalf("stored=%+v found=%v err=%v", stored, found, err)
	}
	got := recall(t, b, `{"recent":true,"selection":"relevant"}`)
	seen := map[string]bool{}
	for _, hit := range got.Hits {
		seen[hit.ID] = true
	}
	if !seen[inA.ID] || !seen[note.ID] || seen[inB.ID] || len(seen) != 2 {
		t.Fatalf("child retrieval crossed project: %v", seen)
	}
	wantResult(t, dispatch(t, b, memParams(opMemoryRecall, fmt.Sprintf(`{"id":%q,"selection":"project"}`, inB.ID))), statusFailed, reasonMemoryNotFound)
	if h.cfg.Store.ActiveProjectID() != "project-b" {
		t.Fatal("memory changed operator focus")
	}
}
