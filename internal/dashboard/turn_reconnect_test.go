package dashboard

import (
	"context"
	"github.com/coder/websocket"
	"sync"
	"testing"
)

/* The live defect (operator report, 2026-08-24): a page reload mid-turn
   stopped the thinking dots and the pulsing orb, and the page never
   caught up with turns or tool calls. Root cause: the turn's frames
   (stream-start, tool events, the final reply) were sent to the
   OWNING connection only — a reloaded page connects fresh, never hears
   any of them, and renders a frozen conversation while the turn runs
   on invisibly. Turn state is broadcast state: every open screen and
   any fresh page mid-turn must see the same honest live view. */

func TestTurnFramesReachReloadedPage(t *testing.T) {
	block := make(chan struct{})
	var mu sync.Mutex
	turnLive := false
	active := func() bool { mu.Lock(); defer mu.Unlock(); return turnLive }
	s := New("127.0.0.1", 0, &WSHandler{
		IdentityName: "Reload",
		Speaker:      "identity",
		TurnActive:   active,
		HandleMessage: func(ctx context.Context, msg string) (string, error) {
			mu.Lock()
			turnLive = true
			mu.Unlock()
			<-block
			mu.Lock()
			turnLive = false
			mu.Unlock()
			return "done after reload", nil
		},
	})
	addr, err := s.Start(t.TempDir())
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	defer s.Shutdown(context.Background())

	// First page: the one that speaks. It must see the turn's
	// stream-start frame (broadcast now, not owner mail).
	pageOne := dialWS(t, addr)
	sendMsg(t, pageOne, map[string]interface{}{"type": "chat", "message": "begin the turn"})
	m := drainUntil(t, pageOne, "response")
	if !m.Stream {
		t.Fatalf("page one: expected stream:true response, got %+v", m)
	}

	// THE RELOAD: a fresh connection mid-turn.
	pageTwo := dialWS(t, addr)

	// The fresh page must receive the connect burst (status etc.) AND
	// the stream:true frame — it lands mid-turn and must know.
	m2 := drainUntil(t, pageTwo, "response")
	if !m2.Stream {
		t.Fatalf("reloaded page: expected stream:true response, got %+v", m2)
	}

	// Release the turn. The final reply must reach BOTH pages.
	close(block)
	done := 0
	for _, page := range []*websocket.Conn{pageOne, pageTwo} {
		for i := 0; i < 20; i++ {
			m := readMsg(t, page)
			if m.Type == "response" && m.Done {
				if m.Message != "done after reload" {
					t.Fatalf("reply %q", m.Message)
				}
				done++
				break
			}
		}
	}
	if done != 2 {
		t.Fatalf("final reply reached %d of 2 pages", done)
	}
}
