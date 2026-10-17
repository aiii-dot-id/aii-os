package dashboard

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestUninstallOverWSReachesTheOneHook(t *testing.T) {
	calls := 0
	stats := func() (*StatsResponse, error) { return &StatsResponse{UninstallOffered: true}, nil }
	h := &WSHandler{GetStats: stats, Uninstall: func() error {
		calls++
		if calls == 2 {
			return errors.New("this identity does not run from the app's bundle")
		}
		return nil
	}}
	s := New("127.0.0.1", 0, h)
	addr, err := s.Start(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Shutdown(context.Background())
	conn := dialWS(t, addr)
	if m := readMsg(t, conn); m.Type != "status" || m.Stats == nil || !m.Stats.UninstallOffered {
		t.Fatalf("the status does not carry the offer: %+v", m)
	}
	sendMsg(t, conn, ClientMessage{Type: "uninstall", RequestID: "u1"})
	if msg := readMsg(t, conn); msg.Type != "uninstall" || msg.RequestID != "u1" {
		t.Fatalf("the reply: %+v", msg)
	}
	sendMsg(t, conn, ClientMessage{Type: "uninstall", RequestID: "u2"})
	if msg := readMsg(t, conn); msg.Type != "error" || !strings.Contains(msg.Message, "bundle") {
		t.Fatalf("a refusal must name itself: %+v", msg)
	}
	if calls != 2 {
		t.Fatalf("the hook ran %d times for two messages", calls)
	}

	none := New("127.0.0.1", 0, &WSHandler{GetStats: stats})
	addr2, err := none.Start(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer none.Shutdown(context.Background())
	c2 := dialWS(t, addr2)
	_ = readMsg(t, c2)
	sendMsg(t, c2, ClientMessage{Type: "uninstall", RequestID: "u3"})
	if msg := readMsg(t, c2); msg.Type != "error" || !strings.Contains(msg.Message, "not offered") {
		t.Fatalf("no hook: %+v", msg)
	}
}
