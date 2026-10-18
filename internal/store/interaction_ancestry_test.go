package store

import (
	"encoding/json"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/interaction"
)

func TestInteractionDescendantsUseRetainedParentage(t *testing.T) {
	s, err := NewMemory()
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for _, id := range []string{"root", "other"} {
		if err := s.StartWorkSession(t.Context(), id, id); err != nil {
			t.Fatal(err)
		}
	}
	enqueue := func(child, parent, key string) {
		t.Helper()
		raw, _ := json.Marshal(map[string]any{"ws_id": child, "parent_work_session_id": parent, "depth": 1})
		item := &WorkItem{Kind: SubagentWorkKind, Payload: string(raw), DedupKey: key, Source: "identity"}
		if key == child {
			if _, ok, err := s.EnqueueWorkWithSessionBelowLimit(item, 50, child, SubagentDescription(child)); err != nil || !ok {
				t.Fatalf("enqueue %s: %v %v", child, ok, err)
			}
		} else if _, err := s.EnqueueWork(item); err != nil {
			t.Fatal(err)
		}
	}
	enqueue("child", "root", "child")
	enqueue("grandchild", "child", "grandchild")
	enqueue("child", "root", "child#2")
	enqueue("sibling", "other", "sibling")
	for _, id := range []string{"root", "other", "child", "grandchild", "sibling"} {
		if err := s.RecordToolStart("turn-"+id, 1, id, "test", "provider-call", "read", `{}`); err != nil {
			t.Fatal(err)
		}
	}
	query := func(desc bool, wantAvailability string, want ...string) {
		t.Helper()
		q := interaction.Query{Version: 1, Limit: 1, Filter: interaction.Filter{WorkSession: "root", IncludeDescendants: desc, Kinds: []interaction.Kind{interaction.ToolCall}}}
		got := map[string]bool{}
		for {
			p, e := s.QueryInteractions(t.Context(), q)
			if e != nil {
				t.Fatal(e)
			}
			if p.Availability != wantAvailability || p.Lost != 0 {
				t.Fatalf("availability/lost: %+v", p)
			}
			for _, r := range p.Rows {
				if got[r.Details.WorkSession] {
					t.Fatal("duplicate", r.ID)
				}
				got[r.Details.WorkSession] = true
			}
			if p.NextCursor == "" {
				break
			}
			q.Cursor = p.NextCursor
			changed := q
			changed.Filter.IncludeDescendants = !desc
			if _, e = s.QueryInteractions(t.Context(), changed); e == nil {
				t.Fatal("cursor crossed ancestry scope")
			}
		}
		if len(got) != len(want) {
			t.Fatalf("got %v want %v", got, want)
		}
		for _, id := range want {
			if !got[id] {
				t.Fatalf("missing %s in %v", id, got)
			}
		}
	}
	query(false, "complete", "root")
	query(true, "complete", "root", "child", "grandchild")
	// Same project and depth do not establish any missing parent. Old queue
	// requests remain readable, but make ancestry completeness unknowable.
	enqueue("legacy", "", "legacy")
	query(false, "complete", "root")
	query(true, "ancestry_incomplete", "root", "child", "grandchild")
	// A conflicting continuation is never resolved by picking the latest row.
	enqueue("child", "other", "child#3")
	query(true, "ancestry_incomplete", "root")
	// Historical corrupt parentage remains readable. Fresh atomic admission
	// refuses a missing parent, so construct the retained rows directly.
	for child, parent := range map[string]string{"cycle-a": "cycle-b", "cycle-b": "cycle-a"} {
		if err := s.StartWorkSession(t.Context(), child, SubagentDescription(child)); err != nil {
			t.Fatal(err)
		}
		raw, _ := json.Marshal(map[string]any{"ws_id": child, "parent_work_session_id": parent, "depth": 1})
		if _, err := s.EnqueueWork(&WorkItem{Kind: SubagentWorkKind, Payload: string(raw), DedupKey: child, Source: "identity"}); err != nil {
			t.Fatal(err)
		}
	}
	page, err := s.QueryInteractions(t.Context(), interaction.Query{Version: 1, Filter: interaction.Filter{WorkSession: "cycle-a", IncludeDescendants: true}})
	if err != nil {
		t.Fatal(err)
	}
	if page.Availability != "ancestry_incomplete" {
		t.Fatal("cyclic ancestry claimed complete")
	}
	for _, r := range page.Rows {
		if r.Details.WorkSession != "cycle-a" {
			t.Fatal("cycle invented descendant", r)
		}
	}
	s.SetFrozen(true, "read only")
	query(true, "ancestry_incomplete", "root")
}
