package dashboard

import (
	"context"
	"testing"
)

// .
// .
// .
// .
// .
// .
func TestBroadcastWorkOverWebSocket(t *testing.T) {
	s := New("127.0.0.1", 0, &WSHandler{
		GetStats: func() (*StatsResponse, error) {
			return &StatsResponse{Name: "X"}, nil
		},
		GetWork: func() (*WorkState, error) {
			return &WorkState{
				Live:   []WorkSessionItem{{ID: "ws_live1", Description: "survey the logs", Status: "active"}},
				Queued: 2,
			}, nil
		},
	})
	addr, _ := s.Start(t.TempDir())
	defer s.Shutdown(context.Background())

	conn := dialWS(t, addr)
	// .
	// .
	waitForConns(t, s, 1)

	s.BroadcastWork()

	m := drainUntil(t, conn, "work")
	if m.Work == nil || len(m.Work.Live) != 1 || m.Work.Live[0].ID != "ws_live1" {
		t.Fatalf("push arrived without the live worker: %+v", m.Work)
	}
	if m.Work.Queued != 2 {
		t.Fatalf("queued = %d, want 2 — the push must carry current state whole", m.Work.Queued)
	}
}
