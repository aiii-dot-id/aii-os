package dashboard

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestAReplyReturnedWithAFailureIsShownBesideIt(t *testing.T) {
	h := &WSHandler{
		Speaker: "identity",
		HandleMessage: func(ctx context.Context, msg string) (string, error) {
			return "the words", errors.New("the host could not record this reply: disk full")
		},
	}
	s := New("127.0.0.1", 0, h)
	addr, err := s.Start(t.TempDir())
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	defer s.Shutdown(context.Background())
	conn := dialWS(t, addr)
	waitForConns(t, s, 1)
	sendMsg(t, conn, ClientMessage{Type: "chat", Message: "are you there?"})
	shown := false
	for i := 0; i < 20; i++ {
		m := readMsg(t, conn)
		switch {
		case m.Type == "response" && m.Done:
			if m.Message != "the words" || m.Role != "identity" {
				t.Fatalf("the reply frame = %+v, want the identity's words", m)
			}
			shown = true
		case m.Type == "error":
			if !shown {
				t.Fatalf("the failure %q reached the page and the words it came with did not", m.Message)
			}
			if !strings.Contains(m.Message, "could not record this reply") {
				t.Fatalf("error frame = %q, want the host's failure", m.Message)
			}
			return
		}
	}
	t.Fatal("no error frame reached the page")
}
