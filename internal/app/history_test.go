package app

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/store"
)

func TestBuildHistoryKeepsCurrentOnceAndReportsOlderTurns(t *testing.T) {
	s, err := store.New(filepath.Join(t.TempDir(), "aii.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	for _, turn := range []struct{ role, content string }{
		{"operator", "old question"},
		{"resident", "old answer"},
		{"operator", "current question"},
	} {
		if err := s.AddConversationTurn(turn.role, turn.content); err != nil {
			t.Fatal(err)
		}
	}

	a := &App{store: s, cfg: &Config{Prompt: PromptConfig{RecentTurns: 2}}}
	history, omitted, err := a.buildHistory()
	if err != nil {
		t.Fatal(err)
	}
	if omitted != 1 {
		t.Fatalf("omitted = %d, want 1", omitted)
	}
	if len(history) != 2 || history[0].Role != "assistant" || history[1].Role != "user" {
		t.Fatalf("history roles = %+v", history)
	}
	if history[1].Content != "current question" {
		t.Fatalf("current turn = %q", history[1].Content)
	}
}

func TestTurnGateWaitHonorsCancellation(t *testing.T) {
	a := New(&Config{})
	if err := a.acquireTurn(t.Context()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- a.acquireTurn(ctx) }()
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("waiting turn returned %v, want cancellation", err)
	}
	a.releaseTurn()
}

func TestTurnGateRefusesCanceledTurnWhenFree(t *testing.T) {
	a := New(&Config{})
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := a.acquireTurn(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("free turn returned %v, want cancellation", err)
	}
	if err := a.acquireTurn(t.Context()); err != nil {
		t.Fatalf("canceled turn consumed the gate: %v", err)
	}
	a.releaseTurn()
}

func TestTurnGateRequiresInitializedApp(t *testing.T) {
	if err := (&App{}).acquireTurn(t.Context()); err == nil {
		t.Fatal("uninitialized turn gate must refuse instead of blocking")
	}
}

func TestNewOwnsLifecycleInEveryMode(t *testing.T) {
	a := New(&Config{})
	if a.bgCtx == nil || a.bgCancel == nil {
		t.Fatal("application lifecycle must exist before LIVE/SAFE mode is selected")
	}
	a.Stop()
	select {
	case <-a.bgCtx.Done():
	default:
		t.Fatal("Stop did not cancel the application lifecycle")
	}
}

func TestStopJoinsApplicationBackground(t *testing.T) {
	a := New(&Config{})
	done := make(chan struct{})
	if !a.runBackground(func() {
		<-a.bgCtx.Done()
		close(done)
	}) {
		t.Fatal("background owner was refused before shutdown")
	}
	a.Stop()
	select {
	case <-done:
	default:
		t.Fatal("Stop returned before its background owner exited")
	}
}

func TestRunBackgroundRefusesAfterStop(t *testing.T) {
	a := New(&Config{})
	a.Stop()
	if a.runBackground(func() { t.Error("background owner ran after shutdown") }) {
		t.Fatal("background owner was admitted after shutdown")
	}
}

func TestStopWaitsForResidentTurn(t *testing.T) {
	a := New(&Config{})
	if err := a.acquireTurn(t.Context()); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		a.Stop()
		close(done)
	}()
	select {
	case <-done:
		t.Fatal("Stop closed live resources during a resident turn")
	case <-time.After(20 * time.Millisecond):
	}
	a.releaseTurn()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Stop did not finish after the resident turn ended")
	}
}

func TestStartLiveFailureLeavesApplicationAlive(t *testing.T) {
	dir := t.TempDir()
	a := New(&Config{Identity: IdentityConfig{
		KeyPath: dir + "/missing-key", LedgerPath: dir + "/ledger.jsonl", DBPath: dir + "/aii.db",
	}})
	if err := startLiveForTest(a); err == nil {
		t.Fatal("missing identity key was accepted")
	}
	if err := a.bgCtx.Err(); err != nil {
		t.Fatalf("live transition failure stopped the Firstboot application: %v", err)
	}
	a.Stop()
}
