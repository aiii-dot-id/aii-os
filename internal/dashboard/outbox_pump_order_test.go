package dashboard

import (
	"context"
	"encoding/json"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func TestAPushNeverCarriesAReadOlderThanThePage(t *testing.T) {
	var mu sync.Mutex
	queued := []string{"first"}
	marked, claimed := map[string]bool{}, map[string]bool{}
	var hold chan struct{}
	reading := make(chan struct{}, 1)
	s := New("127.0.0.1", 0, &WSHandler{
		TakeOutbox: func() ([]OutboxItem, func(bool), error) {
			mu.Lock()
			var items []OutboxItem
			for _, id := range queued {
				if !marked[id] && !claimed[id] {
					items = append(items, OutboxItem{ID: id, To: "operator", Content: "notice " + id})
				}
			}
			held := hold
			hold = nil
			mu.Unlock()
			if held != nil { // what was read is what returns, however late
				reading <- struct{}{}
				<-held
			}
			return items, func(shown bool) {
				mu.Lock()
				defer mu.Unlock()
				for _, it := range items {
					if shown && !claimed[it.ID] {
						marked[it.ID] = true
					}
				}
			}, nil
		},
	})
	addr, err := s.Start(t.TempDir())
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	defer s.Shutdown(context.Background())

	one := dialWS(t, addr)
	outboxUntil(t, one, "first") // its connect has read, pushed and marked

	// A notice lands, and the pump's read for the open page is held.
	mu.Lock()
	queued = append(queued, "flying")
	hold = make(chan struct{})
	release := hold
	mu.Unlock()
	s.PokeOutbox()
	select {
	case <-reading:
	case <-time.After(30 * time.Second):
		t.Fatal("the pump never read the outbox for the open page")
	}

	// While the read is in hand the last page goes, the route claims the
	// notice — no page is open — and another page opens and connects.
	one.Close(websocket.StatusNormalClosure, "")
	waitForConnsAtMost(t, s, 0)
	mu.Lock()
	claimed["flying"] = true
	queued = append(queued, "second")
	mu.Unlock()
	two := dialWS(t, addr)
	seen := outboxUntil(t, two, "second")

	// The held read returns; the next notice is the new page's, and the
	// pump's passes reach the socket in order, so what came before it is
	// everything the held read sent there.
	close(release)
	mu.Lock()
	queued = append(queued, "third")
	mu.Unlock()
	s.PokeOutbox()
	seen = append(seen, outboxUntil(t, two, "third")...)
	if slices.Contains(seen, "flying") {
		t.Fatalf("a page was pushed a read older than itself: %v", seen)
	}
}

// outboxUntil reads a page's frames until an outbox frame holds want and
// returns every id the outbox frames held on the way.
func outboxUntil(t *testing.T, conn *websocket.Conn, want string) []string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var seen []string
	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			t.Fatalf("waiting for %q, after %v: %v", want, seen, err)
		}
		var msg ServerMessage
		if json.Unmarshal(data, &msg) != nil || msg.Type != "outbox" {
			continue
		}
		done := false
		for _, it := range msg.Outbox {
			seen = append(seen, it.ID)
			done = done || it.ID == want
		}
		if done {
			return seen
		}
	}
}
