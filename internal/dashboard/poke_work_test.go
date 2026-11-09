package dashboard

import (
	"context"
	"testing"
	"time"
)

// WHAT IS SCHEDULED IS TOLD TO EVERY PAGE. The host pokes when a turn ends,
// a fresh turn is queued, begins or stands down; the page then asks for the
// work state again, and its "idle" line is drawn from the host's answer.
func TestPokeWorkTellsEveryPageToAskAgain(t *testing.T) {
	s := New("127.0.0.1", 0, &WSHandler{})
	addr, err := s.Start(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Shutdown(context.Background())
	conn := dialWS(t, addr)
	defer conn.CloseNow()
	// The page's socket is in the broadcast set before the poke: at most one
	// passed with none, and a poke sent before the socket joined reached nothing.
	waitForConns(t, s, 1)
	s.PokeWork()
	deadline := time.After(10 * time.Second)
	for {
		select {
		case <-deadline:
			t.Fatal("no work_changed frame reached the page")
		default:
		}
		if m := readMsg(t, conn); m.Type == "work_changed" {
			return
		}
	}
}
