package dashboard

import (
	"context"
	"testing"
)

func TestAServerGivenNoHandlerAnswersItsSocketAsAZeroHandler(t *testing.T) {
	s := New("127.0.0.1", 0, nil)
	addr, err := s.Start(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Shutdown(context.Background())

	conn := dialWS(t, addr)
	sendMsg(t, conn, ClientMessage{Type: "probe", RequestID: "built-nil"})
	if got := drainUntil(t, conn, "probe"); got.RequestID != "built-nil" || got.TurnActive {
		t.Fatalf("probe on a server built with no handler = %+v", got)
	}

	// Swapped to nil under an open socket: the next arm that reads a hook
	// without a nil check (cancel reads CancelTurn) answers, not panics.
	s.SwapHandler(nil)
	sendMsg(t, conn, ClientMessage{Type: "cancel"})
	if got := drainUntil(t, conn, "error"); got.Message != "no turn is running to cancel" {
		t.Fatalf("cancel after a swap to no handler = %+v", got)
	}
	sendMsg(t, conn, ClientMessage{Type: "probe", RequestID: "swapped-nil"})
	if got := drainUntil(t, conn, "probe"); got.RequestID != "swapped-nil" || got.TurnActive {
		t.Fatalf("probe after a swap to no handler = %+v", got)
	}
}
