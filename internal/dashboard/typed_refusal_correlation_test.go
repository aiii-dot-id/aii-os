package dashboard

import (
	"context"
	"errors"
	"testing"
)

// A TYPED MESSAGE REFUSED WHILE A TURN RUNS IS ANSWERED UNDER ITS OWN
// REQUEST. Every frame its refusal sends the page that typed it (the
// receipt, the refusal's reason, the admission's answer) carries the
// message's request id, so the page shows it beside the message; a frame
// with no id is read as the running turn's own error, and ends the
// thinking state of a turn that is still working.
func TestARefusedTypedMessageIsAnsweredUnderItsRequest(t *testing.T) {
	h := &WSHandler{
		AdmitChat: func(context.Context, string) (bool, error) {
			return false, errors.New("fixture: eight messages already wait in the turn")
		},
		TurnActive: func() bool { return true },
	}
	s := New("127.0.0.1", 0, h)
	addr, err := s.Start(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Shutdown(context.Background()) })
	conn := dialWS(t, addr)
	sendMsg(t, conn, ClientMessage{Type: "chat", RequestID: "typed-1", Message: "and also this"})
	sendMsg(t, conn, ClientMessage{Type: "probe", RequestID: "after"})
	seen := map[string]bool{}
	for m := readMsg(t, conn); m.Type != "probe"; m = readMsg(t, conn) {
		switch m.Type {
		case "error", "chat_ack", "chat_receipt", "request_error":
			if m.RequestID != "typed-1" {
				t.Fatalf("A REFUSED TYPED MESSAGE WAS ANSWERED WITH NO REQUEST: type=%q id=%q %q; the page reads it as the running turn's error", m.Type, m.RequestID, m.Message)
			}
			seen[m.Type] = true
		case "response":
			if m.Done {
				t.Fatalf("a refused typed message ended the running turn on the page: %+v", m)
			}
		}
	}
	for _, want := range []string{"error", "chat_ack", "chat_receipt"} {
		if !seen[want] {
			t.Fatalf("the refused message was not answered with %s under its request: %v", want, seen)
		}
	}
}
