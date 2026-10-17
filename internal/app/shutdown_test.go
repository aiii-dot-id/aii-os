package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/conversation"
	"github.com/aiii-dot-id/aii-os/internal/ledger"
)

func TestWhoeverStopsATurnSaysWhatStoppedIt(t *testing.T) {
	stop := func(t *testing.T, a *App) {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := a.Shutdown(ctx); err != nil {
			t.Fatal(err)
		}
	}
	for _, c := range []struct {
		name string
		end  func(*testing.T, *App)
		want conversation.TurnStop
	}{
		{"the operator's stop", func(t *testing.T, a *App) {
			if !a.CancelTurn() {
				t.Fatal("rig: there was no turn to stop")
			}
		}, conversation.StoppedByOperator},
		{"a shutdown", stop, conversation.RuntimeShuttingDown},
		{"a restart", func(t *testing.T, a *App) {
			if err := a.Restart(); err != nil {
				t.Fatal(err)
			}
			stop(t, a)
		}, conversation.RuntimeRestarting},
	} {
		for _, rooted := range []string{"the lifetime, as a wake's turn is", "the page's own context"} {
			t.Run(c.name+" of a turn rooted on "+rooted, func(t *testing.T) {
				a := New(defaultConfig())
				t.Cleanup(func() { stopApp(t, a) })
				root := context.Background()
				if rooted != "the page's own context" {
					root = a.lifetime()
				}
				ctx, done := a.beginCancellableTurn(root)
				defer done()
				c.end(t, a)
				select {
				case <-ctx.Done():
				default:
					t.Fatal("the turn's context is still live")
				}
				if got := context.Cause(ctx); got != error(c.want) {
					t.Fatalf("A TURN ENDED BY %s IS TOLD %q; want %q", c.name, got, c.want)
				}
			})
		}
	}
}

func TestShutdownWaitCanExpireWithoutAbandoningTheOwner(t *testing.T) {
	a := New(defaultConfig())
	if err := a.acquireTurn(t.Context()); err != nil {
		t.Fatal(err)
	}
	held := true
	t.Cleanup(func() {
		if held {
			a.releaseTurn()
		}
		if err := a.Shutdown(context.Background()); err != nil {
			t.Error(err)
		}
	})
	ctx, cancel := context.WithCancel(t.Context())
	cancel() // deterministic wait cancellation; the turn remains held
	var pending *ShutdownPending
	if err := a.Shutdown(ctx); !errors.As(err, &pending) || !errors.Is(err, context.Canceled) {
		t.Fatalf("wanted a retryable wait refusal, got %v", err)
	}
	if a.runBackground(func() {}) {
		t.Fatal("shutdown admitted new background work")
	}
	select {
	case <-a.stopDone:
		t.Fatal("teardown finished before the held turn ended")
	default:
	}
	a.releaseTurn()
	held = false
	ctx, finish := context.WithTimeout(t.Context(), 5*time.Second)
	defer finish()
	if err := a.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	if err := a.Shutdown(ctx); err != nil {
		t.Fatal("completion was not reusable", err)
	}
}

func TestATurnThatBeginsDuringAShutdownIsToldOfTheShutdown(t *testing.T) {
	a := New(defaultConfig())
	if err := a.acquireTurn(t.Context()); err != nil {
		t.Fatal(err)
	}
	held := true
	t.Cleanup(func() {
		if held {
			a.releaseTurn()
		}
		stopApp(t, a)
	})
	gone, cancel := context.WithCancel(t.Context())
	cancel()
	var pending *ShutdownPending
	if err := a.Shutdown(gone); !errors.As(err, &pending) {
		t.Fatalf("rig: the shutdown was to be begun and still waiting on the held turn: %v", err)
	}
	for _, rooted := range []string{"the lifetime, as a wake's turn is", "the page's own context"} {
		root := context.Background()
		if rooted != "the page's own context" {
			root = a.lifetime()
		}
		ctx, done := a.beginCancellableTurn(root)
		select {
		case <-ctx.Done():
		default:
			t.Fatalf("a turn rooted on %s began during the shutdown and runs on", rooted)
		}
		if got := context.Cause(ctx); got != error(conversation.RuntimeShuttingDown) {
			t.Fatalf("A TURN ROOTED ON %s THAT BEGAN DURING THE SHUTDOWN IS TOLD %q", rooted, got)
		}
		done()
	}
	select {
	case <-a.stopDone:
		t.Fatal("rig: the teardown finished before the held turn ended")
	default:
	}
	a.releaseTurn()
	held = false
}

func TestShutdownCloseErrorSurvivesEveryLaterWait(t *testing.T) {
	a := New(defaultConfig())
	var err error
	a.ledger, err = ledger.New(filepath.Join(t.TempDir(), "ledger.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	// This owned closed file injects a real final-sync/close error, not prose.
	if err := a.ledger.Close(); err != nil {
		t.Fatal(err)
	}
	first := a.Shutdown(t.Context())
	if !errors.Is(first, os.ErrClosed) {
		t.Fatalf("lost close error: %v", first)
	}
	if again := a.Shutdown(t.Context()); again != first {
		t.Fatalf("close error erased: %v", again)
	}
}

func TestEmbeddedRestartWaiterDistinguishesShutdown(t *testing.T) {
	for _, restart := range []bool{false, true} {
		a := New(defaultConfig())
		result := make(chan bool, 1)
		go func() { result <- a.WaitForRestart() }()
		if restart {
			if err := a.Restart(); err != nil {
				t.Fatal(err)
			}
		} else {
			if err := a.Shutdown(t.Context()); err != nil {
				t.Fatal(err)
			}
		}
		select {
		case got := <-result:
			if got != restart {
				t.Fatalf("restart=%v, got %v", restart, got)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("waiter outlived its owner")
		}
		if err := a.Shutdown(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
}
