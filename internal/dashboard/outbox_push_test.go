package dashboard

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/store"
)

// .
// .
// .
func TestOutboxPushOnWrite(t *testing.T) {
	st, err := store.New(filepath.Join(t.TempDir(), "push.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	s := New("127.0.0.1", 0, &WSHandler{
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

	// .
	st.OnOutboxWrite(s.PokeOutbox)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn := dialWS(t, addr)

	// .
	time.Sleep(150 * time.Millisecond)

	// .
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
		return
	}
	t.Fatal("no outbox frame within 3s — push-on-write failed")
}
