package app

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/identity"
	"github.com/aiii-dot-id/aii-os/internal/interaction"
	"github.com/aiii-dot-id/aii-os/internal/limits"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

// ceiling_test.go — the day's token ceiling (the resident design §5):
// a resume past it waits for tomorrow or the operator's word, the yield
// says so in the approved words, and the card's numbers are the store's.

func spendToday(t *testing.T, a *App, tokens int) {
	t.Helper()
	if err := a.store.InsertTurnMetric(store.TurnMetric{TsMs: time.Now().UTC().UnixMilli(), Calls: 2, Predicted: 1, Source: turnSourceOperator, Provider: "fake", Model: "fake",
		Usage: &store.TurnUsage{PromptTokens: tokens - 1, CompletionTokens: 1, TotalTokens: tokens}}); err != nil {
		t.Fatal(err)
	}
}

// setCeiling writes agency.daily_token_ceiling as a save would: under the
// configuration's lock.
func setCeiling(a *App, c limits.Ceiling) {
	a.cfgMu.Lock()
	a.cfg.Agency.DailyTokenCeiling = c
	a.cfgMu.Unlock()
}

func tomorrowOf(a *App) int64 {
	zone := a.speechZone()
	now := time.Now().In(zone)
	return time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, zone).AddDate(0, 0, 1).UnixMilli()
}

func TestAResumePastTheDaysCeilingWaitsForTomorrow(t *testing.T) {
	t.Run("a checkpoint under a promise, and the card", func(t *testing.T) {
		app, dir := dispatchApp(t)
		defer stopApp(t, app)
		pressStage(t, app, store.StageTrusted)
		setCeiling(app, limits.Of(10))
		script := cappedUnderPromise(t, dir, false, false)
		obs := fixtureModel(t, app, func(n int, w http.ResponseWriter) { _, _ = io.WriteString(w, script(n)) })
		spendToday(t, app, 20)
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()
		if err := app.acquireTurn(ctx); err != nil {
			t.Fatal(err)
		}
		reply, err := app.observeChat(ctx, "go on", func(kind, name, args string) {})
		if err != nil {
			t.Fatal(err)
		}
		if got := recordedReply(t, app, reply); !strings.Contains(got, "Nothing continues by itself: today's ceiling is reached; this work resumes tomorrow or on your operator's word.") {
			t.Fatalf("the reply does not say the ceiling is reached: %q", got)
		}
		if on := resumesOnOf(t, app); on != "time:"+strconv.FormatInt(tomorrowOf(app), 10) {
			t.Fatalf("the condition past the ceiling is %q, want tomorrow", on)
		}
		ws, _ := app.store.ActiveWorkSession()
		if alarm, found, _ := app.store.AlarmByID(identity.PromiseAlarmID(ws.ID)); !found || alarm.Deadline != tomorrowOf(app) {
			t.Fatalf("no alarm keeps tomorrow: %+v %v", alarm, found)
		}
		// The card's numbers are the store's: read before the next turn
		// composes them.
		declared, used, _, _ := app.store.LastTurnDeclaration()
		want := fmt.Sprintf("### Your spend\nToday %d tokens, of a ceiling of 10. Last turn you declared %d steps and used %d.", app.spentToday(), declared, used)
		if err := app.acquireTurn(ctx); err != nil {
			t.Fatal(err)
		}
		if _, err := app.observeChat(ctx, "where are we?", func(kind, name, args string) {}); err != nil {
			t.Fatal(err)
		}
		bodies := obs.snapshot()
		if got := requestText(t, bodies[len(bodies)-1]); !strings.Contains(got, want) {
			t.Fatalf("the card's spend is not the store's: want %q in\n%s", want, got)
		}
		time.Sleep(300 * time.Millisecond)
		if sawRequest(obs, "budget checkpoint")() {
			t.Fatal("a fresh turn followed past the ceiling")
		}
	})
	t.Run("the yield", func(t *testing.T) {
		app, _ := dispatchApp(t)
		defer stopApp(t, app)
		pressStage(t, app, store.StageTrusted)
		setCeiling(app, limits.Of(10))
		spendToday(t, app, 20)
		ctx := interaction.WithTurnSource(interaction.WithTurn(context.Background(), "turn_y"), turnSourceOperator)
		if _, err := app.engine.ExecuteAction(ctx, "verb", "work", map[string]any{"action": "start", "description": "keep a promise"}); err != nil {
			t.Fatal(err)
		}
		recordPromise(t, app, "in_1")
		out, err := app.engine.ExecuteAction(ctx, "verb", "work", map[string]any{"action": "yield", "promise": "in_1", "resumes_on": "word"})
		if err != nil {
			t.Fatal(err)
		}
		if out != "Turn yielding — your gate frees when this round ends. Today's ceiling is reached; this work resumes tomorrow or on your operator's word." {
			t.Fatalf("the yield's words past the ceiling: %q", out)
		}
		if on := resumesOnOf(t, app); on != "time:"+strconv.FormatInt(tomorrowOf(app), 10) {
			t.Fatalf("the yield's condition past the ceiling is %q, want tomorrow", on)
		}
	})
	t.Run("under the ceiling nothing changes", func(t *testing.T) {
		app, _ := dispatchApp(t)
		defer stopApp(t, app)
		pressStage(t, app, store.StageTrusted)
		setCeiling(app, limits.Of(100))
		spendToday(t, app, 20)
		if past, _ := app.pastCeiling(); past {
			t.Fatal("under the ceiling read as past it")
		}
		setCeiling(app, limits.Ceiling{})
		if past, _ := app.pastCeiling(); past {
			t.Fatal("no ceiling read as past it")
		}
	})
}
