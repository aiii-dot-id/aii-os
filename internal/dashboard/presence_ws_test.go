package dashboard

import (
	"context"
	"testing"
	"time"
)

// .
// .
// .
func TestOperatorActsCountAsPresenceQueriesDoNot(t *testing.T) {
	h := &WSHandler{GetStats: func() (*StatsResponse, error) { return &StatsResponse{}, nil }}
	s := New("127.0.0.1", 0, h)
	addr, err := s.Start(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Shutdown(context.Background())
	conn := dialWS(t, addr)
	_ = readMsg(t, conn)
	if s.OperatorActiveWithin(time.Minute) {
		t.Fatal("connecting is not an act of the operator's")
	}
	if !s.SessionLive() {
		t.Fatal("a connection is a live session, though — presence and interaction are different facts")
	}
	sendMsg(t, conn, ClientMessage{Type: "query", Query: "status", RequestID: "q1"})
	_ = readMsg(t, conn)
	if s.OperatorActiveWithin(time.Minute) {
		t.Fatal("a query the page sends on its own must not count as the operator acting")
	}
	sendMsg(t, conn, ClientMessage{Type: "presence"})
	deadline := time.Now().Add(2 * time.Second)
	for !s.OperatorActiveWithin(time.Minute) && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if !s.OperatorActiveWithin(time.Minute) {
		t.Fatal("a gesture reported by the page is the operator acting")
	}
	if !s.OperatorActiveWithin(time.Second) {
		t.Fatal("the stamp is recent")
	}
}
