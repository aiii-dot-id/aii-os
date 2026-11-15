package dashboard

import (
	"context"
	"testing"
)

// A READ THE HOST REFUSES NAMES THE REQUEST IT REFUSES. The page takes a refusal
// that names no request as the turn's own, and ends the turn it shows (ws.js,
// onError). A refused read that named none ended a turn the identity was still
// answering: Stop became Send, and Status stopped saying "answering". Each read
// here is refused because its handler is not set, and each refusal carries the
// request_id the read was sent with.
func TestAReadTheHostRefusesNamesTheRequestItRefuses(t *testing.T) {
	s := New("127.0.0.1", 0, &WSHandler{})
	addr, err := s.Start(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Shutdown(context.Background())
	conn := dialWS(t, addr)

	for _, read := range []ClientMessage{
		{Type: "query", Query: "spend", Days: 32},
		{Type: "query", Query: "work"},
		{Type: "query", Query: "tools"},
		{Type: "query", Query: "identity"},
		{Type: "query", Query: "config"},
		{Type: "query", Query: "continuity"},
		{Type: "query", Query: "recall", Q: "a word"},
		{Type: "query", Query: "workspace", Name: "p1"},
	} {
		read.RequestID = "refused-" + read.Query
		sendMsg(t, conn, read)
		got := drainUntil(t, conn, "error")
		if got.RequestID != read.RequestID {
			t.Errorf("the %s read's refusal (%q) named request %q, not %q", read.Query, got.Message, got.RequestID, read.RequestID)
		}
	}
}
