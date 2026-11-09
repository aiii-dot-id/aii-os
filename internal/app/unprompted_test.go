package app

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/identity"
	"github.com/aiii-dot-id/aii-os/internal/interaction"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

// unprompted_test.go — silence as an audited action (the resident design
// §6): an unprompted word reaches the operator only when the identity chose
// to speak and the words name a promise and a fact newer than the
// operator's last message, or the alarm was theirs; held words come back as
// the identity's own alarm; words not sent stay on the page with the row
// that says why. Every unprompted turn is told its choice in the approved
// words, and the card says what it knows of the operator.

// legSays is cappedUnderPromise with the leg answering by the choice named
// and the words given.
// legSaysAfter is cappedUnderPromise with the leg answering by the choice
// named and the words given, after first moving the intention named (a
// signed state change: the fact its words may cite), "" for none.
func legSaysAfter(t *testing.T, dir, choice, words, moved string) func(n int) string {
	t.Helper()
	base := cappedUnderPromise(t, dir, false, false)
	return func(n int) string {
		switch {
		case n == 9 && choice != "":
			calls := []string{toolCall("c9s", "work", fmt.Sprintf(`{"action":"say","choice":%q}`, choice))}
			if moved != "" {
				calls = append(calls, toolCall("c9m", "commit", fmt.Sprintf(`{"variant":"intention.state_change","id":%q,"state":"completed","outcome":"served: it is written"}`, moved)))
			}
			// The leg sets its own wait: work continues by default (2026-11-06),
			// and these tests judge this leg's words, not a further turn's.
			calls = append(calls, toolCall("c9w", "work", `{"action":"update","resumes_on":"word"}`))
			return callReply("", calls...)
		case n >= 9:
			return textReply(words)
		}
		return base(n)
	}
}

func outboxHolds(t *testing.T, a *App, prefix string) bool {
	t.Helper()
	var n int
	_ = a.store.DB().QueryRow(`SELECT COUNT(*) FROM outbox WHERE id LIKE ?`, prefix+"%").Scan(&n)
	return n > 0
}

func rowBeside(t *testing.T, a *App) string {
	t.Helper()
	var content string
	_ = a.store.DB().QueryRow(`SELECT content FROM conversations WHERE json_extract(details,'$.reason_code')=? ORDER BY turn_seq DESC LIMIT 1`, store.ReasonWordsHeld).Scan(&content)
	return content
}

// waitRowBeside is rowBeside once the row is there: the floor records it
// after the fresh turn's end has settled the condition the tests wait on.
func waitRowBeside(t *testing.T, a *App) string {
	t.Helper()
	waitUntil(t, func() bool { return rowBeside(t, a) != "" }, "the row beside the unsent words")
	return rowBeside(t, a)
}

func runLeg(t *testing.T, app *App, dir, choice, words string) *contScript {
	return runLegAfter(t, app, dir, choice, words, "")
}

func runLegAfter(t *testing.T, app *App, dir, choice, words, moved string) *contScript {
	t.Helper()
	script := legSaysAfter(t, dir, choice, words, moved)
	obs := fixtureModel(t, app, func(n int, w http.ResponseWriter) { _, _ = io.WriteString(w, script(n)) })
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	t.Cleanup(cancel)
	if err := app.acquireTurn(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := app.observeChat(ctx, "go on", func(kind, name, args string) {}); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, replyRecorded(app, words), "the fresh turn's words recorded")
	waitUntil(t, func() bool { return resumesOnOf(t, app) == "word" }, "the fresh turn ending")
	return obs
}

func TestAnUnpromptedWordIsSentOnlyWhenGrounded(t *testing.T) {
	t.Run("speak, naming the promise beside a newer fact", func(t *testing.T) {
		app, dir := dispatchApp(t)
		defer stopApp(t, app)
		pressStage(t, app, store.StageTrusted)
		obs := runLegAfter(t, app, dir, "speak", "in_1 is completed: the report is written; a word on it when you are back.", "in_1")
		// The words reach the outbox after the fresh turn's end has settled
		// the condition runLeg waits on.
		waitUntil(t, func() bool { return outboxHolds(t, app, "wake_cont_") }, "the grounded words on the outbox")
		if r := rowBeside(t, app); r != "" {
			t.Fatalf("a row beside sent words: %q", r)
		}
		var fact string
		for _, b := range obs.snapshot() {
			if strings.Contains(b, "budget checkpoint") {
				fact = requestText(t, b)
			}
		}
		if !strings.Contains(fact, "Say which with work action=say choice=speak|hold|go_on; a held word is delivered at that time as your own alarm. Whatever you say unprompted names the promise it answers.") {
			t.Fatalf("the fresh turn was not told its choice:\n%s", fact)
		}
		if !strings.Contains(fact, "### Your operator\nHeard from at ") || !strings.Contains(fact, "Their page is not open.") {
			t.Fatalf("the card does not say what it knows of the operator:\n%s", fact)
		}
	})
	t.Run("speak, naming the promise but not the state it moved to", func(t *testing.T) {
		app, dir := dispatchApp(t)
		defer stopApp(t, app)
		pressStage(t, app, store.StageTrusted)
		runLegAfter(t, app, dir, "speak", "in_1: nothing new to report.", "in_1")
		if r := waitRowBeside(t, app); r != wordsHeldRow {
			t.Fatalf("the row beside the unsent words: %q", r)
		}
		if outboxHolds(t, app, "wake_cont_") {
			t.Fatal("words naming the promise without its new state were sent")
		}
	})
	t.Run("speak, naming the promise beside no fact of its own", func(t *testing.T) {
		app, dir := dispatchApp(t)
		defer stopApp(t, app)
		pressStage(t, app, store.StageTrusted)
		// in_2 moved, not in_1: the words name in_1 and cite nothing newer of it.
		ctx := interaction.WithTurnSource(interaction.WithTurn(context.Background(), "turn_other"), turnSourceOperator)
		if _, err := app.engine.ExecuteAction(ctx, "verb", "commit", map[string]any{"variant": "intention.create", "id": "in_2", "statement": "another", "why": "because"}); err != nil {
			t.Fatal(err)
		}
		runLegAfter(t, app, dir, "speak", "in_1: nothing new, but in_10 moved.", "in_2")
		if r := waitRowBeside(t, app); r != wordsHeldRow {
			t.Fatalf("the row beside the unsent words: %q", r)
		}
		if outboxHolds(t, app, "wake_cont_") {
			t.Fatal("words citing an unrelated fact were sent")
		}
	})
	t.Run("speak, naming no promise", func(t *testing.T) {
		app, dir := dispatchApp(t)
		defer stopApp(t, app)
		pressStage(t, app, store.StageTrusted)
		runLeg(t, app, dir, "speak", "Just checking in with nothing new.")
		if r := waitRowBeside(t, app); r != wordsHeldRow {
			t.Fatalf("the row beside the unsent words: %q", r)
		}
		if outboxHolds(t, app, "wake_cont_") {
			t.Fatal("ungrounded words were sent")
		}
	})
	t.Run("no choice at all", func(t *testing.T) {
		app, dir := dispatchApp(t)
		defer stopApp(t, app)
		pressStage(t, app, store.StageTrusted)
		runLeg(t, app, dir, "", "in_1: done, by the way.")
		if r := waitRowBeside(t, app); r != wordsHeldRow {
			t.Fatalf("the row beside the unsent words: %q", r)
		}
		if outboxHolds(t, app, "wake_cont_") {
			t.Fatal("words with no choice were sent")
		}
	})
	t.Run("hold", func(t *testing.T) {
		app, dir := dispatchApp(t)
		defer stopApp(t, app)
		pressStage(t, app, store.StageTrusted)
		runLeg(t, app, dir, "hold", "in_1: the report is written.")
		if r := waitRowBeside(t, app); !strings.HasPrefix(r, "[Your words are held until ") {
			t.Fatalf("the row beside the held words: %q", r)
		}
		if outboxHolds(t, app, "wake_cont_") {
			t.Fatal("held words were sent")
		}
		alarms, err := app.store.DueAlarms("wall", int64(1)<<62, 20)
		if err != nil {
			t.Fatal(err)
		}
		held := false
		for _, al := range alarms {
			if strings.HasPrefix(al.AlarmID, "held_wake_cont_") {
				tag, msg := identity.DecodeTimerPayload(al.Payload)
				if tag == identity.HeldWordsTag && msg == "in_1: the report is written." && al.Deadline > time.Now().UnixMilli() {
					held = true
				}
			}
		}
		if !held {
			t.Fatalf("the held words are not the identity's own alarm: %+v", alarms)
		}
	})
	t.Run("go on", func(t *testing.T) {
		app, dir := dispatchApp(t)
		defer stopApp(t, app)
		pressStage(t, app, store.StageTrusted)
		runLeg(t, app, dir, "go_on", "in_1: carrying on.")
		if outboxHolds(t, app, "wake_cont_") {
			t.Fatal("words after go_on were sent")
		}
		if r := rowBeside(t, app); r != "" {
			t.Fatalf("a row beside words the identity chose to keep: %q", r)
		}
	})
	t.Run("an alarm the operator's turn set is answered regardless", func(t *testing.T) {
		// An ordinary alarm (no promise's tag), set by the identity's timer
		// verb in the operator's turn: the floor does not apply to the
		// answer to their own ask (round 4: a promise's alarm with no kept
		// condition wakes nothing, so this alarm is the ordinary kind).
		app, _ := dispatchApp(t)
		defer stopApp(t, app)
		pressStage(t, app, store.StageTrusted)
		fixtureModel(t, app, func(n int, w http.ResponseWriter) { _, _ = io.WriteString(w, textReply("tea is ready, as you asked")) })
		app.executor.Stop()
		firing := identity.TimerWake{AlarmID: "t_tea", Deadline: 42, Message: "tea", SetIn: turnSourceOperator}
		raw, _ := json.Marshal(firing)
		w := &store.WorkItem{ID: "wake_" + firing.DeliveryID(), Kind: identity.TimerWakeWorkKind, Payload: string(raw), DedupKey: "t_tea@42"}
		if _, err := app.store.AddOutboxNoticeWithWork(firing.DeliveryID(), "[timer t_tea fired] tea", w); err != nil {
			t.Fatal(err)
		}
		if err := (&timerWakeHandler{app}).RunWork(goldenTurnCtx(t), w); err != nil {
			t.Fatal(err)
		}
		if !outboxHolds(t, app, "wake_t_tea_") {
			t.Fatal("the answer to the operator's own alarm was not sent")
		}
	})
}
