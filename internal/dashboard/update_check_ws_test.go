package dashboard

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/coder/websocket"
)

// .
// .
// .
// .
func TestUpdateCheckOverWS(t *testing.T) {
	calls := 0
	h := &WSHandler{
		UpdateCheck: func() (*UpdateState, error) {
			calls++
			if calls == 2 {
				return nil, errors.New("already checking")
			}
			return &UpdateState{CurrentVersion: "1.0.0", Checking: true, Enabled: true}, nil
		},
		GetStats: func() (*StatsResponse, error) {
			return &StatsResponse{Update: &UpdateState{CurrentVersion: "1.0.0", AvailableVersion: "1.1.0", Enabled: true}}, nil
		},
	}
	s := New("127.0.0.1", 0, h)
	addr, err := s.Start(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Shutdown(context.Background())
	conn := dialWS(t, addr)
	other := dialWS(t, addr)
	// .
	_ = readMsg(t, conn)
	_ = readMsg(t, other)

	sendMsg(t, conn, ClientMessage{Type: "update_check", RequestID: "u1"})
	msg := readMsg(t, conn)
	if msg.Type != "update_check" || msg.RequestID != "u1" || msg.Update == nil || !msg.Update.Checking {
		t.Fatalf("the reply must carry the checker's state as the run starts: %+v", msg)
	}
	sendMsg(t, conn, ClientMessage{Type: "update_check", RequestID: "u2"})
	msg = readMsg(t, conn)
	if msg.Type != "error" || msg.RequestID != "u2" || !strings.Contains(msg.Message, "already checking") {
		t.Fatalf("a refused run must come back as an error naming the situation: %+v", msg)
	}
	if calls != 2 {
		t.Fatalf("the handler was invoked %d times for two messages", calls)
	}

	s.BroadcastStatus()
	m := readMsg(t, other)
	if m.Type != "status" || m.Stats == nil || m.Stats.Update == nil || m.Stats.Update.AvailableVersion != "1.1.0" {
		t.Fatalf("the finished state must reach another screen through the status broadcast: %+v", m)
	}
}

// .
// .
// .
func TestUpdateCheckIsBehindTheExistingOriginWall(t *testing.T) {
	invoked := false
	h := &WSHandler{UpdateCheck: func() (*UpdateState, error) { invoked = true; return &UpdateState{}, nil }}
	s := New("127.0.0.1", 0, h)
	addr, err := s.Start(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Shutdown(context.Background())
	ctx, cancel := context.WithTimeout(context.Background(), 5e9)
	defer cancel()
	c, _, err := websocket.Dial(ctx, "ws://"+addr+"/ws", &websocket.DialOptions{
		HTTPHeader: http.Header{"Origin": []string{"http://evil.example"}},
	})
	if err == nil {
		c.Close(websocket.StatusNormalClosure, "")
		t.Fatal("a foreign Origin was admitted to the socket")
	}
	if invoked {
		t.Fatal("the handler ran for a connection the wall refused")
	}
}
