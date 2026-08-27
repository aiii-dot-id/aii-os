package dashboard

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/store"
)

// Push-on-write (James's design question, 2026-08-17): a store outbox
// write must reach a CONNECTED dashboard within moments — event-driven,
// no poller. Store.OnOutboxWrite -> server.PokeOutbox -> pump -> frame.
func TestOutboxPushOnWrite(t *testing.T) {
	st, err := store.New(filepath.Join(t.TempDir(), "push.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	s := New("127.0.0.1", 0, &WSHandler{
		IdentityName: "Push",
		GetOutbox: func() ([]OutboxItem, error) {
			msgs, err := st.UndeliveredMessages()
			if err != nil {
				return nil, err
			}
			items := make([]OutboxItem, len(msgs))
			for i, m := range msgs {
				items[i] = OutboxItem{ID: m.ID, To: m.ToRole, Content: m.Content}
			}
			return items, nil
		},
		MarkDelivered: func(id string) error {
			return st.MarkDelivered(id, "dashboard")
		},
	})
	addr, err := s.Start(t.TempDir())
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	defer s.Shutdown(context.Background())

	// The app wiring under test: store write -> pump signal.
	st.OnOutboxWrite(s.PokeOutbox)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn := dialWS(t, addr)

	// Let the connection settle past connect-time delivery (empty outbox).
	time.Sleep(150 * time.Millisecond)

	// The event: an outbox write after connect.
	if err := st.AddOutboxMessage("wake_test_1", "operator", "", "I woke. Here is what I noticed.", nil); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		rctx, rcancel := context.WithTimeout(ctx, 2*time.Second)
		_, data, err := conn.Read(rctx)
		rcancel()
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		var msg ServerMessage
		if json.Unmarshal(data, &msg) != nil || msg.Type != "outbox" {
			continue
		}
		if len(msg.Outbox) == 0 || msg.Outbox[0].Content != "I woke. Here is what I noticed." {
			t.Fatalf("wrong outbox frame: %+v", msg.Outbox)
		}
		return // pushed to a live connection, without a poller window
	}
	t.Fatal("no outbox frame within 3s — push-on-write failed")
}
