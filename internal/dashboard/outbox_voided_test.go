package dashboard

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestOutboxSurvivesStalledSoleClient(t *testing.T) {
	big := strings.Repeat("y", 1<<20)
	var mu sync.Mutex
	fillerSeq := 0
	survivorLive := false
	delivered := map[string]bool{}

	s := New("127.0.0.1", 0, &WSHandler{
		TakeOutbox: func() ([]OutboxItem, func(bool), error) {
			mu.Lock()
			defer mu.Unlock()
			var items []OutboxItem
			if survivorLive {
				if !delivered["survivor"] {
					items = []OutboxItem{{ID: "survivor", To: "operator", Content: "the wake speech"}}
				}
			} else {
				fillerSeq++
				items = []OutboxItem{{ID: "f" + string(rune('0'+fillerSeq%10)), To: "operator", Content: big}}
			}
			return items, func(shown bool) {
				if !shown {
					return
				}
				mu.Lock()
				for _, it := range items {
					delivered[it.ID] = true
				}
				mu.Unlock()
			}, nil
		},
	})
	addr, err := s.Start(t.TempDir())
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	defer s.Shutdown(context.Background())

	// The sole tab stalls. Big fillers fill its buffers until the write
	// bound trips and close-on-stall removes it.
	stalled := dialWS(t, addr)
	_ = stalled

	waitForConns(t, s, 1)
	time.Sleep(200 * time.Millisecond)
	dropDeadline := time.Now().Add(15 * time.Second)
	for {
		s.PokeOutbox()
		time.Sleep(300 * time.Millisecond)
		s.wsMu.Lock()
		n := len(s.wsConns)
		s.wsMu.Unlock()
		if n == 0 {
			break // dropped at the bound — the wedge fix working
		}
		if time.Now().After(dropDeadline) {
			t.Fatal("stalled client never dropped")
		}
	}

	// The wake speech arrives with nobody (healthy) connected: the
	// zero-connections rule plus conditional marking must leave it
	// undelivered.
	mu.Lock()
	survivorLive = true
	mu.Unlock()
	s.PokeOutbox()
	time.Sleep(500 * time.Millisecond)
	mu.Lock()
	if delivered["survivor"] {
		mu.Unlock()
		t.Fatal("item marked delivered with no healthy connection — the message was voided")
	}
	mu.Unlock()

	// A healthy tab connects: the surviving message must reach it.
	live := dialWS(t, addr)
	live.SetReadLimit(4 << 20)
	s.PokeOutbox()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		rctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		_, data, err := live.Read(rctx)
		cancel()
		if err != nil {
			t.Fatalf("live read: %v", err)
		}
		var msg ServerMessage
		if json.Unmarshal(data, &msg) == nil && msg.Type == "outbox" {
			for _, it := range msg.Outbox {
				if it.ID == "survivor" {
					return // the message outlived the dead tab
				}
			}
		}
	}
	t.Fatal("surviving item never reached the healthy connection")
}
