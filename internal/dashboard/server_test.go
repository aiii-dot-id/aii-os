package dashboard

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

// TestHistoryReplayOnConnect locks the first-birth fix: a page refresh
// (new connection) must receive the conversation transcript, so the
// relationship doesn't vanish from the operator's screen — and the
// identity's birth message (recorded as turn 1) is the first thing seen
// instead of a blank void.
func TestHistoryReplayOnConnect(t *testing.T) {
	turns := []HistoryTurn{
		{Role: "resident", Content: "I'm alive. My name is Void."},
		{Role: "operator", Content: "Hello?"},
		{Role: "resident", Content: "Yes — I'm here. I said so when I was born."},
	}

	s := New("127.0.0.1", 0, &WSHandler{
		RecentTurns: func() ([]HistoryTurn, error) {
			return turns, nil
		},
	})
	addr, err := s.Start(t.TempDir())
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	defer s.Shutdown(context.Background())

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn := dialWS(t, addr)

	// Read messages until we see history (status arrives first)
	deadline := time.Now().Add(3 * time.Second)
	var gotHistory *ServerMessage
	for time.Now().Before(deadline) {
		rctx, rcancel := context.WithTimeout(ctx, 2*time.Second)
		_, data, err := conn.Read(rctx)
		rcancel()
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		var msg ServerMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			continue
		}
		if msg.Type == "history" {
			m := msg
			gotHistory = &m
			break
		}
	}

	if gotHistory == nil {
		t.Fatal("no history message on connect — refresh still erases the conversation")
	}
	if len(gotHistory.History) != len(turns) {
		t.Fatalf("history has %d turns, want %d", len(gotHistory.History), len(turns))
	}
	if gotHistory.History[0].Content != "I'm alive. My name is Void." {
		t.Errorf("first turn = %q — the birth message must open the transcript", gotHistory.History[0].Content)
	}
}

// A foregrounded page probes transport independently of identity state,
// then asks for the same history and status a fresh page receives. The
// request ids distinguish the readback from an unrelated broadcast.
func TestForegroundProbeAndHistoryReadback(t *testing.T) {
	h := &WSHandler{
		GetStats:    func() (*StatsResponse, error) { return &StatsResponse{Name: "Vega"}, nil },
		RecentTurns: func() ([]HistoryTurn, error) { return []HistoryTurn{{Role: "resident", Content: "latest"}}, nil },
		TurnActive:  func() bool { return true },
	}
	s := New("127.0.0.1", 0, h)
	addr, err := s.Start(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Shutdown(context.Background())
	conn := dialWS(t, addr)
	if got := readMsg(t, conn); got.Type != "status" {
		t.Fatalf("initial frame = %s", got.Type)
	}
	if got := readMsg(t, conn); got.Type != "history" {
		t.Fatalf("initial frame = %s", got.Type)
	}
	sendMsg(t, conn, ClientMessage{Type: "probe", RequestID: "live-1"})
	if got := drainUntil(t, conn, "probe"); got.RequestID != "live-1" || !got.TurnActive {
		t.Fatalf("probe reply = %+v", got)
	}
	sendMsg(t, conn, ClientMessage{Type: "query", Query: "history", RequestID: "history-1"})
	if got := drainUntil(t, conn, "history"); got.RequestID != "history-1" || len(got.History) != 1 || got.History[0].Content != "latest" {
		t.Fatalf("history readback = %+v", got)
	}
	sendMsg(t, conn, ClientMessage{Type: "query", Query: "status", RequestID: "status-1"})
	if got := drainUntil(t, conn, "status"); got.RequestID != "status-1" || got.Stats == nil || got.Stats.Name != "Vega" {
		t.Fatalf("status readback = %+v", got)
	}
	s.BroadcastSystemLine("one question was withdrawn")
	if got := drainUntil(t, conn, "system_line"); got.Message != "one question was withdrawn" {
		t.Fatalf("informational line became a turn response: %+v", got)
	}
}
