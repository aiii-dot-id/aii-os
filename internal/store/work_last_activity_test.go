package store

import (
	"context"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/interaction"
)

// A SESSION'S LAST ACTIVITY IS ITS NEWEST ROW BY EITHER NAME (a review,
// 2026-11-07): a work notice names the session by $.work.session, an
// ordinary call or result by $.work_session_id; a session whose newest
// row is an ordinary call shows that call's time, not its last notice's.
func TestASessionsLastActivityIsItsNewestOrdinaryCall(t *testing.T) {
	s := testStore(t)
	ctx := interaction.WithTurnSource(interaction.WithTurn(context.Background(), "turn-op"), "operator")
	if err := s.StartWorkSession(ctx, "ws_act", "a task"); err != nil {
		t.Fatal(err)
	}
	notice, found, err := s.WorkSessionLastActivity("ws_act")
	if err != nil || !found {
		t.Fatalf("the start's notice is not the session's activity: %v %v", found, err)
	}
	time.Sleep(5 * time.Millisecond)
	if err := s.RecordToolStart("turn-b", 1, "main", "m", "", "ls", `{"path":"a"}`); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordToolDoneMeasured("turn-b", 1, "ls", `{"path":"a"}`, "x", interaction.Succeeded, false, ToolMeasurement{}); err != nil {
		t.Fatal(err)
	}
	var created string
	if err := s.DB().QueryRow(`SELECT MAX(created_at) FROM conversations WHERE json_extract(details,'$.work_session_id') = ? AND json_extract(details,'$.work.session') IS NULL`, "ws_act").Scan(&created); err != nil {
		t.Fatal(err)
	}
	want, err := time.Parse(time.RFC3339Nano, created)
	if err != nil || created == "" {
		t.Fatalf("no ordinary row names the session by work_session_id: %q %v", created, err)
	}
	got, found, err := s.WorkSessionLastActivity("ws_act")
	if err != nil || !found || !got.Equal(want.UTC()) || !got.After(notice) {
		t.Fatalf("the newest ordinary call is not the session's last activity: got %s (found %v, %v), the call %s, the notice %s", got, found, err, want.UTC(), notice)
	}
}
