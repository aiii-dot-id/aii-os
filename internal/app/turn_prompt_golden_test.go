package app

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/interaction"
)

var updateTurnGolden = flag.Bool("update-turn-golden", false, "rewrite testdata/turn_prompt from the requests the turn paths send")

var (
	turnGoldenNow      = regexp.MustCompile(`### Now\n[^\n]*`)
	turnGoldenDate     = regexp.MustCompile(`\b\d{4}-\d{2}-\d{2}\b`)
	turnGoldenPlatform = regexp.MustCompile(`You are running on [^.]*\.`)
)

func TestEachTurnPathSendsItsPinnedPrompt(t *testing.T) {
	paths := []struct {
		name string
		run  func(t *testing.T, a *App)
	}{
		{"operator", func(t *testing.T, a *App) {
			ctx := goldenTurnCtx(t)
			if err := a.acquireTurn(ctx); err != nil {
				t.Fatal(err)
			}
			if reply, err := a.observeChat(ctx, "GOLDEN_OPERATOR the typed words", func(kind, name, args string) {}); err != nil || reply != "noted" {
				t.Fatalf("operator turn = %q, %v", reply, err)
			}
		}},
		{"spoken", func(t *testing.T, a *App) {
			ctx := goldenTurnCtx(t)
			if err := a.acquireTurn(ctx); err != nil {
				t.Fatal(err)
			}
			defer a.releaseTurn()
			if reply, err := a.wake(ctx, roleOperator, voiceMarker+"GOLDEN_SPOKEN the spoken words"); err != nil || reply != "noted" {
				t.Fatalf("spoken operator turn = %q, %v", reply, err)
			}
		}},
		{"leftover", func(t *testing.T, a *App) {
			// The words' turn is given the token by the release that kept
			// it for them (releaseTurn) and never takes it itself: held
			// here as that release leaves it.
			<-a.turnGate
			a.runLeftoverSteerTurn([]steerEntry{
				{role: roleOperator, content: "GOLDEN_LEFTOVER_ONE the operator's queued words", recordCtx: context.Background()},
				{role: roleParticipant, content: "GOLDEN_LEFTOVER_TWO a participant's queued words", recordCtx: context.Background()},
			})
		}},
		{"system", func(t *testing.T, a *App) {
			ctx := goldenTurnCtx(t)
			if err := a.acquireTurn(ctx); err != nil {
				t.Fatal(err)
			}
			defer a.releaseTurn()
			if reply, err := a.wake(withTurnSource(ctx, turnSourceHarvest), string(interaction.System), "[GOLDEN_SYSTEM a fact the substrate states]"); err != nil || reply != "noted" {
				t.Fatalf("system wake = %q, %v", reply, err)
			}
		}},
		{"timer", func(t *testing.T, a *App) {
			ctx := goldenTurnCtx(t)
			if err := a.acquireTurn(ctx); err != nil {
				t.Fatal(err)
			}
			defer a.releaseTurn()
			// The firing that woke this turn is a pending timer event of
			// its own; the turn's facts leave it out.
			if err := a.store.AddOutboxMessage("timer_golden_1", "operator", "", "GOLDEN_TIMER_NOTICE the firing itself", nil); err != nil {
				t.Fatal(err)
			}
			ctx = context.WithValue(withTurnSource(ctx, turnSourceTimer), timerRecordKey{}, "timer_golden_1")
			if reply, err := a.wake(ctx, string(interaction.System), "[GOLDEN_TIMER the alarm] — your own alarm woke you. Respond to your operator in your own words."); err != nil || reply != "noted" {
				t.Fatalf("timer wake = %q, %v", reply, err)
			}
		}},
		{"participant", func(t *testing.T, a *App) {
			ctx := goldenTurnCtx(t)
			if err := a.acquireTurn(ctx); err != nil {
				t.Fatal(err)
			}
			defer a.releaseTurn()
			if reply, err := a.wake(withTurnSource(ctx, turnSourceArrival), string(interaction.Participant), "[messages] GOLDEN_PARTICIPANT a contact wrote"); err != nil || reply != "noted" {
				t.Fatalf("participant wake = %q, %v", reply, err)
			}
		}},
	}
	for _, p := range paths {
		t.Run(p.name, func(t *testing.T) {
			a, dir := goldenTurnApp(t)
			obs := fixtureModel(t, a, func(_ int, w http.ResponseWriter) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(textReply("noted")))
			})
			p.run(t, a)
			waitUntil(t, func() bool { return !a.TurnActive() }, "the turn to end")
			reqs := obs.snapshot()
			if len(reqs) != 1 {
				t.Fatalf("the %s path sent %d requests, want 1", p.name, len(reqs))
			}
			got := goldenRequest(t, reqs[0], dir)
			file := filepath.Join("testdata", "turn_prompt", p.name+".golden")
			if *updateTurnGolden {
				if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(file, []byte(got), 0o644); err != nil {
					t.Fatal(err)
				}
				return
			}
			want, err := os.ReadFile(file)
			if err != nil {
				t.Fatalf("%v (generate it with -update-turn-golden)", err)
			}
			if got != string(want) {
				t.Fatalf("the %s path's request moved from %s.\n%s", p.name, file, goldenDiff(string(want), got))
			}
		})
	}
}

func goldenTurnApp(t *testing.T) (*App, string) {
	t.Helper()
	a, dir := dispatchApp(t)
	t.Cleanup(func() { stopApp(t, a) })
	if err := a.store.SetStandingState("GOLDEN_STANDING waiting on the review"); err != nil {
		t.Fatal(err)
	}
	ctx := t.Context()
	if err := a.recordConversation(ctx, string(interaction.Operator), "GOLDEN_EARLIER the operator asked before"); err != nil {
		t.Fatal(err)
	}
	if err := a.recordConversation(ctx, string(interaction.Resident), "GOLDEN_EARLIER_REPLY the identity answered"); err != nil {
		t.Fatal(err)
	}
	// One pending event of each kind, a few milliseconds apart: they are
	// shown in the order they happened, and a tie would be decided by id.
	if _, err := a.store.RecordInbound("in_mail_golden", "mail", "friend", "GOLDEN_ARRIVAL a message waiting"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(3 * time.Millisecond)
	if err := a.store.AddOutboxMessage("timer_golden_other", "operator", "", "GOLDEN_OTHER_TIMER a firing no turn took", nil); err != nil {
		t.Fatal(err)
	}
	time.Sleep(3 * time.Millisecond)
	if err := a.store.AddOutboxMessage("msg_golden_outcome", "peer", "friend", "sent earlier", nil); err != nil {
		t.Fatal(err)
	}
	if err := a.store.MarkEffectUnknown(claimRow(t, a, "msg_golden_outcome"), "mail", "fixture response lost"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(3 * time.Millisecond)
	return a, dir
}

func goldenTurnCtx(t *testing.T) context.Context {
	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	t.Cleanup(cancel)
	return ctx
}

// goldenRequest is one request's messages, role by role, with the clock,
// the platform and the fixture's directory normalised.
func goldenRequest(t *testing.T, body, dir string) string {
	t.Helper()
	var b strings.Builder
	for i, m := range wireMessages(t, body) {
		fmt.Fprintf(&b, "=== message %d (%s) ===\n", i, m.Role)
		b.WriteString(m.Content)
		b.WriteString("\n")
	}
	out := strings.ReplaceAll(b.String(), dir, "<dir>")
	out = turnGoldenNow.ReplaceAllString(out, "### Now\n<now>")
	out = turnGoldenPlatform.ReplaceAllString(out, "You are running on <platform>.")
	return turnGoldenDate.ReplaceAllString(out, "<date>")
}

// goldenDiff names the first line where two goldens part.
func goldenDiff(want, got string) string {
	w, g := strings.Split(want, "\n"), strings.Split(got, "\n")
	for i := 0; i < len(w) || i < len(g); i++ {
		var wl, gl string
		if i < len(w) {
			wl = w[i]
		}
		if i < len(g) {
			gl = g[i]
		}
		if wl != gl {
			return fmt.Sprintf("first difference at line %d:\nwant: %s\ngot:  %s", i+1, wl, gl)
		}
	}
	return "(no line differs)"
}
