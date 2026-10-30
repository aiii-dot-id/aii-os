package store

import (
	"context"
	"strings"
	"testing"
)

// A KIND OF WORK, ITS START AND ITS CALLS. A
// session keeps the family the identity names while it is active,
// its start time from the moment it starts, and at delivery the count of
// the tool calls the record attributes to it; a family outside the slug
// grammar is refused, and a delivered session's family no longer changes.
func TestAWorkSessionKeepsItsFamilyStartAndCallCount(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	if err := s.StartWorkSession(ctx, "ws_kind", "deploy the preview"); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"", "Deploy", "deploy preview", "-x", strings.Repeat("a", 41)} {
		if err := s.SetWorkSessionFamily(ctx, "ws_kind", bad); err == nil {
			t.Fatalf("family %q accepted", bad)
		}
	}
	if err := s.SetWorkSessionFamily(ctx, "ws_kind", "deploy-preview"); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 3; i++ {
		if err := s.RecordToolStart("turn_kind", i, "main", "m", "", "read", "{}"); err != nil {
			t.Fatal(err)
		}
	}
	ws, err := s.WorkSessionByID("ws_kind")
	if err != nil || ws == nil {
		t.Fatal(ws, err)
	}
	if ws.Family != "deploy-preview" || ws.StartedAt == 0 || ws.ToolCallsKnown {
		t.Fatalf("before delivery: family %q, started %d, calls known %v", ws.Family, ws.StartedAt, ws.ToolCallsKnown)
	}
	if err := s.DeliverWorkSession(ctx, "ws_kind", "served: preview up", "", ""); err != nil {
		t.Fatal(err)
	}
	ws, _ = s.WorkSessionByID("ws_kind")
	if !ws.ToolCallsKnown || ws.ToolCalls != 3 {
		t.Fatalf("delivery counted %d calls (known %v), want the 3 the record names", ws.ToolCalls, ws.ToolCallsKnown)
	}
	if err := s.SetWorkSessionFamily(ctx, "ws_kind", "other"); err == nil {
		t.Fatal("a delivered session's family changed")
	}
}

func TestFamilyCountsInterruptedChildApart(t *testing.T) {
	s := testStore(t)
	if err := s.StartWorkSession(t.Context(), "ws_child_restart", SubagentDescription("a bounded child")); err != nil {
		t.Fatal(err)
	}
	if err := s.SetWorkSessionFamily(t.Context(), "ws_child_restart", "restart-probe"); err != nil {
		t.Fatal(err)
	}
	if err := s.DeliverWorkSession(t.Context(), "ws_child_restart", InterruptedLeg(" — leg 1 of 4 had begun"), EvidenceExternalUnknown, ""); err != nil {
		t.Fatal(err)
	}
	r, err := s.FamilyAttempts("restart-probe", 20)
	if err != nil {
		t.Fatal(err)
	}
	if got := r.Tally(); got.Restarted != 1 || got.Unserved != 0 {
		t.Fatalf("host restart counted as the identity's failure: %+v", got)
	}
}
