package dashboard

import (
	"context"
	"strings"
	"testing"
)

// .
// .
// .
// .
// .
// .
func TestRecallQueryReachesTheRecord(t *testing.T) {
	var asked string
	h := &WSHandler{
		GetStats: func() (*StatsResponse, error) { return &StatsResponse{}, nil },
		Recall:   func(q string) (string, error) { asked = q; return "Recall for \"" + q + "\": one match", nil },
	}
	s := New("127.0.0.1", 0, h)
	addr, err := s.Start(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Shutdown(context.Background())
	conn := dialWS(t, addr)
	_ = readMsg(t, conn)

	sendMsg(t, conn, ClientMessage{Type: "query", Query: "recall", Q: "  anchor ", RequestID: "r1"})
	reply := readMsg(t, conn)
	if reply.Type != "recall" || reply.Query != "anchor" || reply.RequestID != "r1" || !strings.Contains(reply.Message, "one match") {
		t.Fatalf("recall reply = %+v", reply)
	}
	if asked != "anchor" {
		t.Fatalf("the handler was asked %q, want the trimmed text", asked)
	}

	sendMsg(t, conn, ClientMessage{Type: "query", Query: "recall", Q: "   ", RequestID: "r2"})
	if reply := readMsg(t, conn); reply.Type != "error" || !strings.Contains(reply.Message, "needs a word") {
		t.Fatalf("an empty recall must be refused with the reason, got %+v", reply)
	}
}

func TestRecallQueryWithoutAHandlerSaysSo(t *testing.T) {
	h := &WSHandler{GetStats: func() (*StatsResponse, error) { return &StatsResponse{}, nil }}
	s := New("127.0.0.1", 0, h)
	addr, err := s.Start(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Shutdown(context.Background())
	conn := dialWS(t, addr)
	_ = readMsg(t, conn)
	sendMsg(t, conn, ClientMessage{Type: "query", Query: "recall", Q: "anchor"})
	if reply := readMsg(t, conn); reply.Type != "error" || !strings.Contains(reply.Message, "not available") {
		t.Fatalf("want a 'not available' error, got %+v", reply)
	}
}
