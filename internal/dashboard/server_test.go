package dashboard

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

// .
// .
// .
// .
// .
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

	// .
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
