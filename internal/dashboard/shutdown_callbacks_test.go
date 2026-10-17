package dashboard

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestShutdownWaitsForWebSocketConfigAndCleanup(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	var stats atomic.Int32
	var cleaned atomic.Bool
	s := New("127.0.0.1", 0, &WSHandler{
		GetStats: func() (*StatsResponse, error) {
			stats.Add(1)
			return &StatsResponse{}, nil
		},
		SetConfig: func(map[string]any) (*ConfigState, error) {
			close(entered)
			<-release
			return &ConfigState{}, nil
		},
		PagesClosed: func() { cleaned.Store(true) },
	})
	addr, err := s.Start(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { unblock(); _ = s.Shutdown(context.Background()) })
	conn := dialWS(t, addr)
	drainUntil(t, conn, "status")
	sendMsg(t, conn, ClientMessage{Type: "config_set", Config: map[string]any{"cognition.consolidate_batch": 12}})
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("config callback did not begin")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	err = s.Shutdown(ctx)
	cancel()
	var cut *DrainCut
	if !errors.As(err, &cut) {
		t.Fatalf("shutdown finished while a WebSocket callback was held: %v", err)
	}
	if cleaned.Load() {
		t.Fatal("connection cleanup ran before the callback returned")
	}
	unblock()
	ctx, cancel = context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	if stats.Load() != 2 || !cleaned.Load() {
		t.Fatalf("shutdown did not join the config status read and deferred cleanup: stats=%d cleaned=%v", stats.Load(), cleaned.Load())
	}
}

func TestShutdownRefusesWebSocketRegistrationAfterItsSnapshot(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	var stats atomic.Int32
	s := New("127.0.0.1", 0, &WSHandler{GetStats: func() (*StatsResponse, error) {
		stats.Add(1)
		return &StatsResponse{}, nil
	}})
	s.wsAdmitHook = func() { close(entered); <-release }
	addr, err := s.Start(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { unblock(); _ = s.Shutdown(context.Background()) })
	dialWS(t, addr)
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("WebSocket admission did not begin")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	err = s.Shutdown(ctx)
	cancel()
	var cut *DrainCut
	if !errors.As(err, &cut) {
		t.Fatalf("shutdown did not count an accepted WebSocket: %v", err)
	}
	unblock()
	ctx, cancel = context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.Settle(ctx); err != nil {
		t.Fatal(err)
	}
	if s.PageOpen() || stats.Load() != 0 {
		t.Fatalf("a WebSocket entered after shutdown's connection snapshot: open=%v stats=%d", s.PageOpen(), stats.Load())
	}
}

func TestShutdownJoinsTheOutboxReaderBeforeResourcesCanClose(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	var hold, ended atomic.Bool
	s := New("127.0.0.1", 0, &WSHandler{
		GetStats: func() (*StatsResponse, error) { return &StatsResponse{}, nil },
		TakeOutbox: func() ([]OutboxItem, func(bool), error) {
			if hold.Load() {
				close(entered)
				<-release
				ended.Store(true)
			}
			return nil, func(bool) {}, nil
		},
	})
	addr, err := s.Start(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { unblock(); _ = s.Shutdown(context.Background()) })
	conn := dialWS(t, addr)
	drainUntil(t, conn, "status")
	hold.Store(true)
	s.PokeOutbox()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("outbox reader did not begin")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	began := time.Now()
	err = s.Shutdown(ctx)
	cancel()
	var cut *DrainCut
	if !errors.As(err, &cut) {
		t.Fatalf("shutdown finished while the outbox reader was held: %v", err)
	}
	if time.Since(began) > time.Second {
		t.Error("shutdown ignored its caller's bound while joining the reader")
	}
	settle, done := context.WithTimeout(context.Background(), 100*time.Millisecond)
	if err := s.Settle(settle); err == nil {
		t.Error("settle omitted the background reader still inside its callback")
	}
	done()
	unblock()
	ctx, cancel = context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.Settle(ctx); err != nil {
		t.Fatal(err)
	}
	if !ended.Load() {
		t.Fatal("settle returned while the reader still owned its callback")
	}
}
