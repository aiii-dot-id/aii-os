package app

import (
	"context"
	"errors"
	"github.com/aiii-dot-id/aii-os/internal/conversation"
	"github.com/aiii-dot-id/aii-os/internal/identity"
	"github.com/aiii-dot-id/aii-os/internal/interaction"
	"github.com/aiii-dot-id/aii-os/internal/project"
	"github.com/aiii-dot-id/aii-os/internal/store"
	"testing"
)

func TestInteractionTransientOrderRetentionAndPromptBoundary(t *testing.T) {
	st, err := store.NewMemory()
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	e := identity.NewEngine(st, nil, nil, nil, identity.Ports{})
	a := &App{store: st, engine: e}
	a.safeTools = &safeToolRecord{owner: e}
	// A normal write failure is visible, but it never becomes prompt history.
	st.SetFrozen(true, "test freeze")
	ref, err := e.RecordConversationRef(context.Background(), "resident", "failed output", interaction.Details{Reason: "original fact"})
	if err == nil || ref.ID == "" {
		t.Fatal("write failure became success", ref, err)
	}
	if len(e.SafeTranscript()) != 0 {
		t.Fatal("unrecorded output entered prompt")
	}
	e.SetSafeMode("test")
	if _, err = e.RecordConversationRef(context.Background(), "operator", "question", interaction.Details{}); err != nil {
		t.Fatal(err)
	}
	if err = a.safeTools.RecordToolStart("turn", 1, "provider", "read", "private args", "model"); err != nil {
		t.Fatal(err)
	}
	if _, err = e.RecordConversationRef(context.Background(), "resident", "answer", interaction.Details{}); err != nil {
		t.Fatal(err)
	}
	if err = a.safeTools.RecordToolDone("turn", 1, "read", "private args", conversation.Observation{Text: "private result"}); err != nil {
		t.Fatal(err)
	}
	p, err := a.QueryInteractions(t.Context(), interaction.Query{Version: 1, Source: "transient"})
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Rows) != 5 || p.Rows[0].Details.RecordingError == "" || p.Rows[0].Details.Reason != "original fact" {
		t.Fatalf("view: %+v", p)
	}
	want := []interaction.Kind{interaction.Message, interaction.Message, interaction.ToolCall, interaction.Message, interaction.ToolResult}
	for i, r := range p.Rows {
		if r.Sequence != uint64(i+1) || r.Kind != want[i] {
			t.Fatalf("order: %+v", p.Rows)
		}
		if r.Kind == interaction.ToolCall || r.Kind == interaction.ToolResult {
			if r.Content != "" {
				t.Fatal("SAFE retained tool body")
			}
		}
	}
	if len(e.SafeTranscript()) != 2 {
		t.Fatal("prompt selection changed")
	}
	for i := 0; i < 205; i++ {
		if _, err = e.RecordConversationRef(context.Background(), "operator", "repeat", interaction.Details{}); err != nil {
			t.Fatal(err)
		}
	}
	_, rows, lost := a.transientInteractions()
	if len(rows) != 202 || lost != 8 {
		t.Fatalf("separate owner bounds: rows=%d lost=%d", len(rows), lost)
	}
}

func TestInteractionProjectCriteriaPreservePriorAndReportPartialWrite(t *testing.T) {
	st, err := store.NewMemory()
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	a := &App{store: st, projects: project.NewManager(t.TempDir())}
	a.wireProjectInteractionRecorder()
	first := &project.Contract{Outcome: "outcome", Acceptance: []string{"before"}}
	p, err := a.projects.Create("Criteria", "", "operator", nil, first, nil)
	if err != nil {
		t.Fatal(err)
	}
	after := &project.Contract{Outcome: "outcome", Acceptance: []string{"after"}}
	if _, err = a.projects.ApplyPatch(p.ID, nil, nil, nil, nil, after, nil); err != nil {
		t.Fatal(err)
	}
	page, err := st.QueryInteractions(t.Context(), interaction.Query{Version: 1})
	if err != nil || len(page.Rows) != 2 {
		t.Fatal(page, err)
	}
	change := page.Rows[1].Details.Project
	if change == nil || change.Before.Acceptance[0] != "before" || change.After.Acceptance[0] != "after" {
		t.Fatalf("criteria history %+v", change)
	}
	st.SetFrozen(true, "injected failure")
	_, err = a.projects.ApplyPatch(p.ID, nil, nil, nil, nil, first, nil)
	var partial *project.ContractRecordError
	if !errors.As(err, &partial) {
		t.Fatalf("partial result hidden: %v", err)
	}
	saved, err := a.projects.Load(p.ID)
	if err != nil || saved.Contract.Acceptance[0] != "before" {
		t.Fatal("actual saved manifest not reported", err)
	}
}
