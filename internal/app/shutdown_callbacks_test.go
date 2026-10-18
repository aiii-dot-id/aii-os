package app

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/dashboard"
	"github.com/aiii-dot-id/aii-os/internal/store"
	"github.com/coder/websocket"
)

func TestShutdownKeepsTheStoreUntilWebSocketCallbacksReturn(t *testing.T) {
	a, _ := stoppable(t, nil)
	projection, err := store.New(filepath.Join(t.TempDir(), "store.db"))
	if err != nil {
		t.Fatal(err)
	}
	a.store = projection
	entered, release := make(chan struct{}), make(chan struct{})
	readback := make(chan error, 1)
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	t.Cleanup(func() { unblock(); stopApp(t, a) })
	a.dashboard.SwapHandler(&dashboard.WSHandler{
		GetStats: func() (*dashboard.StatsResponse, error) {
			_, err := projection.GetStats()
			return &dashboard.StatsResponse{}, err
		},
		SetConfig: func(map[string]any) (*dashboard.ConfigState, error) {
			close(entered)
			<-release
			_, err := projection.GetStats()
			readback <- err
			return &dashboard.ConfigState{}, err
		},
	})
	conn := dialWSS(t, a.dashboard.Origin())
	readUntil(t, conn, func(m *dashboard.ServerMessage) bool { return m.Type == "status" })
	body, err := json.Marshal(dashboard.ClientMessage{Type: "config_set", Config: map[string]any{"cognition.consolidate_batch": 12}})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	err = conn.Write(ctx, websocket.MessageText, body)
	cancel()
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("config callback did not begin")
	}
	ctx, cancel = context.WithTimeout(context.Background(), 1200*time.Millisecond)
	err = a.Shutdown(ctx)
	cancel()
	var pending *ShutdownPending
	if !errors.As(err, &pending) || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("a callback still owns the store; shutdown must remain pending: %v", err)
	}
	if a.store != projection || a.ledger == nil {
		t.Fatal("pending shutdown released identity resources")
	}
	if _, err := projection.GetStats(); err != nil {
		t.Fatalf("store closed while callback was held: %v", err)
	}
	unblock()
	select {
	case err := <-readback:
		if err != nil {
			t.Fatalf("callback read a closed store: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("released callback did not finish")
	}
	ctx, cancel = context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := a.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	if a.store != nil || a.ledger != nil {
		t.Fatal("completed shutdown retained identity resources")
	}
	if _, err := projection.GetStats(); err == nil {
		t.Fatal("completed shutdown did not close the store")
	}
}
