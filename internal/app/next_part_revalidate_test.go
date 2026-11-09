package app

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/conversation"
	"github.com/aiii-dot-id/aii-os/internal/interaction"
)

// queuedLeg holds the gate, starts ws_q with a focus, records the capped
// reply and queues its leg behind the gate; change then runs with the
// gate still held, and the gate is released. obs counts the model's
// requests, from the leg alone.
func queuedLeg(t *testing.T, change func(ctx context.Context, app *App)) (*App, *contScript) {
	t.Helper()
	app, _ := dispatchApp(t)
	t.Cleanup(func() { stopApp(t, app) })
	obs := fixtureModel(t, app, func(_ int, w http.ResponseWriter) { _, _ = io.WriteString(w, textReply("resumed")) })
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	if err := app.acquireTurn(ctx); err != nil {
		t.Fatalf("acquire turn: %v", err)
	}
	capped := interaction.WithTurn(ctx, "turn_capped")
	if err := app.store.StartWorkSession(ctx, "ws_q", "queued work"); err != nil {
		t.Fatal(err)
	}
	focus := "the queued focus"
	if err := app.store.UpdateWorkPlan(ctx, "ws_q", &focus, nil, nil, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := app.recordReply(capped, "capped here, the rest follows", conversation.Result{}); err != nil {
		t.Fatal(err)
	}
	if scheduled, why := app.scheduledOrWhy(capDeclared, "turn_capped"); !scheduled {
		t.Fatalf("rig: the leg was not scheduled: %s", why)
	}
	change(ctx, app)
	app.releaseTurn()
	return app, obs
}

func TestAQueuedNextPartStandsDownWhenWhatQueuedItChanged(t *testing.T) {
	for _, c := range []struct {
		name, why string
		work      string // the work's name in the identity's line; "queued work" where empty
		// change returns what puts it back before the checks, or nil.
		change func(t *testing.T, ctx context.Context, app *App) (undo func())
	}{{
		name: "continuation switched off",
		why:  "continuation was switched off (agency.turn_continuation) before it began",
		change: func(_ *testing.T, _ context.Context, app *App) func() {
			off := false
			app.cfgMu.Lock()
			app.cfg.Agency.TurnContinuation = &off
			app.cfgMu.Unlock()
			return nil
		},
	}, {
		name: "the session cannot be read",
		why:  "the host could not read its work session before it began",
		work: "ws_q", // its description cannot be read either: it is named by its id
		change: func(t *testing.T, ctx context.Context, app *App) func() {
			conn, err := app.store.DB().Conn(ctx)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = conn.Close() })
			if _, err := conn.ExecContext(ctx, `ALTER TABLE work_sessions RENAME COLUMN status TO unreadable`); err != nil {
				t.Fatal(err)
			}
			return func() {
				if _, err := conn.ExecContext(context.Background(), `ALTER TABLE work_sessions RENAME COLUMN unreadable TO status`); err != nil {
					t.Fatal(err)
				}
			}
		},
	}} {
		t.Run(c.name, func(t *testing.T) {
			var undo func()
			app, obs := queuedLeg(t, func(ctx context.Context, app *App) { undo = c.change(t, ctx, app) })
			var notice string
			waitUntil(t, func() bool {
				notice = outboxReply(t, app, "continuation_")
				return notice != ""
			}, "the operator's notice that the leg stood down")
			// The leg holds the gate until it returns, its line written before.
			if err := app.acquireTurn(context.Background()); err != nil {
				t.Fatal(err)
			}
			app.releaseTurn()
			if undo != nil {
				undo()
			}
			if !strings.HasPrefix(notice, "The next part of ") || !strings.HasSuffix(notice, " did not run: "+c.why+".") {
				t.Fatalf("the operator is told %q, want the next part not run because %s", notice, c.why)
			}
			if n := len(obs.snapshot()); n != 0 {
				t.Fatalf("a leg that stood down asked the model %d time(s)", n)
			}
			work := c.work
			if work == "" {
				work = "queued work"
			}
			want := `[The next part of "` + work + `" did not run: ` + c.why + `. Nothing continues it by itself.]`
			if got := lineAfter(t, app, "capped here, the rest follows"); got != want {
				t.Fatalf("after the reply that promised the next part the identity reads %q, want %q", got, want)
			}
		})
	}
}

// A LEG RUNS ON ITS OWN SESSION, active or not (a review, 2026-11-05, round
// 5; every continuing leg since 2026-11-06): another session becoming the
// active one does not stand it down, and its fact names its own session.
func TestAQueuedNextPartRunsOnItsOwnSessionWhenAnotherBecameActive(t *testing.T) {
	app, obs := queuedLeg(t, func(ctx context.Context, app *App) {
		if err := app.store.StartWorkSession(ctx, "ws_other", "other work"); err != nil {
			t.Fatal(err)
		}
	})
	waitUntil(t, sawRequest(obs, "Work session ws_q is still active"), "the leg's request on its own session")
	if err := app.acquireTurn(context.Background()); err != nil {
		t.Fatal(err)
	}
	app.releaseTurn()
	if n := len(obs.snapshot()); n != 1 {
		t.Fatalf("the leg asked the model %d time(s), want once", n)
	}
}

// Unchanged, the leg runs, and the card it carries is its own session's.
func TestAnUnchangedQueuedNextPartResumesWithItsOwnCard(t *testing.T) {
	app, obs := queuedLeg(t, func(context.Context, *App) {})
	waitUntil(t, func() bool { return len(obs.snapshot()) > 0 }, "the leg's request to the model")
	if err := app.acquireTurn(context.Background()); err != nil {
		t.Fatal(err)
	}
	app.releaseTurn()
	sent := requestText(t, obs.snapshot()[0])
	for _, want := range []string{"Work session ws_q is still active", "Focus: the queued focus"} {
		if !strings.Contains(sent, want) {
			t.Fatalf("the leg's request does not carry %q:\n%s", want, sent)
		}
	}
}
