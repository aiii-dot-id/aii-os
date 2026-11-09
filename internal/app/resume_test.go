package app

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/interaction"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

// resume_test.go — the host's one function for a promise's `now` (the
// resident design, draft 8, approved 2026-11-05): a turn that ends at a
// checkpoint under a promise is carried on by a fresh turn, once, while the
// promise moves; an arrival's turn is never continued unattended; a `now`
// the process did not take up is taken up once across a restart.

// cappedUnderPromise is cappedOnce with the first turn's update carrying a
// promise and its condition. The leg that follows caps again when legCaps
// is set, after moving the promise when progress is set; every later
// request is words alone.
func cappedUnderPromise(t *testing.T, dir string, legCaps, progress bool) func(n int) string {
	t.Helper()
	if err := writeFileForTest(filepath.Join(dir, "probe.txt"), "leg probe"); err != nil {
		t.Fatal(err)
	}
	read := func(n int) string { return callReply("", toolCall(fmt.Sprintf("c%d", n), "read", probeArgs(dir, n))) }
	return func(n int) string {
		switch {
		case n == 1:
			return callReply("",
				toolCall("c1a", "work", `{"action":"start","description":"keep a promise"}`),
				toolCall("c1b", "commit", `{"variant":"intention.create","id":"in_1","statement":"the report is written by morning","why":"I said I would"}`),
				toolCall("c1c", "work", `{"action":"update","plan":"read the probe","steps":1,"promise":"in_1"}`))
		case n <= 7:
			return read(n)
		case n == 8:
			return textReply("capped here")
		case !legCaps:
			return textReply("leg done")
		case n == 9 && progress:
			return callReply("",
				toolCall("c9a", "commit", `{"variant":"intention.state_change","id":"in_1","state":"completed","outcome":"served: the report is written"}`),
				toolCall("c9b", "work", `{"action":"update","plan":"read it again","steps":1}`))
		case n == 9:
			return callReply("", toolCall("c9", "work", `{"action":"update","plan":"read it again","steps":1}`))
		case n <= 15:
			return read(n)
		case n == 16:
			return textReply("capped again")
		}
		return textReply("leg done")
	}
}

// resumesOnOf reads what the active session says resumes its work.
func resumesOnOf(t *testing.T, a *App) string {
	t.Helper()
	ws, err := a.store.ActiveWorkSession()
	if err != nil {
		t.Fatal(err)
	}
	if ws == nil {
		return "<no session>"
	}
	return ws.ResumesOn
}

// promiseNotice is the newest host's notice of a change of the condition,
// its reason.
func promiseNotice(t *testing.T, a *App) string {
	t.Helper()
	var content string
	if err := a.store.DB().QueryRow(`SELECT content FROM conversations WHERE kind='notice' AND json_extract(details,'$.work.resumes_on') IS NOT NULL ORDER BY turn_seq DESC LIMIT 1`).Scan(&content); err != nil {
		t.Fatal(err)
	}
	return content
}

// replyRecorded reads whether a reply beginning so is in the record.
func replyRecorded(a *App, said string) func() bool {
	return func() bool {
		var n int
		_ = a.store.DB().QueryRow(`SELECT COUNT(*) FROM conversations WHERE role='resident' AND substr(content,1,?)=?`, len(said), said).Scan(&n)
		return n > 0
	}
}

func sawRequest(obs *contScript, marker string) func() bool {
	return func() bool {
		for _, b := range obs.snapshot() {
			if strings.Contains(b, marker) {
				return true
			}
		}
		return false
	}
}

// A TURN THAT ENDS AT A CHECKPOINT UNDER A PROMISE IS CARRIED ON BY A FRESH
// TURN, ONCE: the condition becomes `now` (Ring 4, the host's own write,
// in the record), the chain's count does not apply, the fresh turn's card
// says a fresh turn of yours follows under the commitment, and when that
// turn ends by itself without saying what resumes the work the condition
// becomes `word`, with the reason in the record.
func TestACheckpointUnderAPromiseIsCarriedOnByAFreshTurnOnce(t *testing.T) {
	app, dir := dispatchApp(t)
	defer stopApp(t, app)
	pressStage(t, app, store.StageTrusted)
	script := cappedUnderPromise(t, dir, false, false)
	obs := fixtureModel(t, app, func(n int, w http.ResponseWriter) { _, _ = io.WriteString(w, script(n)) })
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	if err := app.acquireTurn(ctx); err != nil {
		t.Fatal(err)
	}
	standDown(app) // the chain is spent: under a promise that is no bound
	reply, err := app.observeChat(ctx, "go on", func(kind, name, args string) {})
	if err != nil {
		t.Fatal(err)
	}
	if got := recordedReply(t, app, reply); got != "capped here\n\n[This turn ended at its declared budget after 9 tool calls; the work continues automatically in a fresh turn.]" {
		t.Fatalf("the reply does not say a fresh turn follows: %q", got)
	}
	if on := resumesOnOf(t, app); on != "now" {
		t.Fatalf("the condition after the checkpoint is %q, want now", on)
	}
	waitUntil(t, sawRequest(obs, "budget checkpoint — continuation 1]"), "the fresh turn reaching the model")
	waitUntil(t, func() bool { return resumesOnOf(t, app) == "word" }, "the condition becoming word after the fresh turn ended by itself with no progress")
	if n := promiseNotice(t, app); !strings.Contains(n, "No progress on in_1") {
		t.Fatalf("the record does not say why the condition changed: %q", n)
	}
	// The fresh turn's card said what resumes the work, and under what.
	var card string
	for _, b := range obs.snapshot() {
		if strings.Contains(b, "budget checkpoint — continuation 1]") {
			card = requestText(t, b)
		}
	}
	if !strings.Contains(card, "This work resumes on a fresh turn of yours, under your intention in_1. Deliver, or change resumes_on, to change that.") {
		t.Fatalf("the fresh turn's card does not say what resumes the work:\n%s", card)
	}
	if strings.Contains(card, workContinuesLine) {
		t.Fatal("the card says nothing is scheduled while a fresh turn runs")
	}
}

// A TURN AN ARRIVAL OPENED IS NEVER CONTINUED UNATTENDED, promise or not:
// the record says an arrival opened it, the reply says nothing continues
// and why, and the condition is left as the identity had it (none set:
// the work continues at the next turn that is not an arrival's).
func TestATurnAnArrivalOpenedIsNeverContinuedUnattended(t *testing.T) {
	app, dir := dispatchApp(t)
	defer stopApp(t, app)
	pressStage(t, app, store.StageTrusted)
	script := cappedUnderPromise(t, dir, false, false)
	obs := fixtureModel(t, app, func(n int, w http.ResponseWriter) { _, _ = io.WriteString(w, script(n)) })
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	if err := app.acquireTurn(ctx); err != nil {
		t.Fatal(err)
	}
	reply, err := app.wake(withTurnSource(ctx, turnSourceArrival), string(interaction.Participant), "[messages] a contact wrote")
	app.releaseTurn()
	if err != nil {
		t.Fatal(err)
	}
	got := recordedReply(t, app, reply)
	if !strings.Contains(got, "Nothing continues by itself: a turn an arrival opened is not continued unattended.") {
		t.Fatalf("the reply does not say why nothing continues: %q", got)
	}
	if on := resumesOnOf(t, app); on != "" {
		t.Fatalf("the condition after an arrival's checkpoint is %q, want it left as the identity had it", on)
	}
	time.Sleep(300 * time.Millisecond)
	if sawRequest(obs, "budget checkpoint")() {
		t.Fatal("a fresh turn followed an arrival's checkpoint")
	}
}

// THE BOUND UNDER A PROMISE IS PROGRESS, NOT A COUNT: a fresh turn that
// was itself a `now` and recorded no state change on the promise is the
// last; one that moved the promise (a signed state_change) is followed by
// another.
func TestAFreshTurnWithoutProgressOnThePromiseIsTheLast(t *testing.T) {
	for _, progress := range []bool{false, true} {
		t.Run(fmt.Sprintf("progress=%t", progress), func(t *testing.T) {
			app, dir := dispatchApp(t)
			defer stopApp(t, app)
			pressStage(t, app, store.StageTrusted)
			script := cappedUnderPromise(t, dir, true, progress)
			obs := fixtureModel(t, app, func(n int, w http.ResponseWriter) { _, _ = io.WriteString(w, script(n)) })
			ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
			defer cancel()
			if err := app.acquireTurn(ctx); err != nil {
				t.Fatal(err)
			}
			if _, err := app.observeChat(ctx, "go on", func(kind, name, args string) {}); err != nil {
				t.Fatal(err)
			}
			waitUntil(t, replyRecorded(app, "capped again"), "the fresh turn ending at its own checkpoint")
			if progress {
				waitUntil(t, sawRequest(obs, "budget checkpoint — continuation 2]"), "a second fresh turn after progress")
				waitUntil(t, func() bool { return resumesOnOf(t, app) == "word" }, "the second fresh turn ending by itself")
				return
			}
			waitUntil(t, func() bool { return resumesOnOf(t, app) == "word" }, "the condition becoming word with no progress")
			if n := promiseNotice(t, app); !strings.Contains(n, "No progress on in_1") {
				t.Fatalf("the record does not say the fresh turn made no progress: %q", n)
			}
			if got := recordedReply(t, app, "capped again"); !strings.Contains(got, "no progress on in_1 was recorded since its last fresh turn; it resumes on your operator's word") {
				t.Fatalf("the reply does not say why nothing continues: %q", got)
			}
			time.Sleep(300 * time.Millisecond)
			if sawRequest(obs, "budget checkpoint — continuation 2]")() {
				t.Fatal("a second fresh turn followed one that made no progress")
			}
		})
	}
}

// A `now` THE PROCESS DID NOT TAKE UP IS TAKEN UP ONCE ACROSS A RESTART:
// with no fresh turn in the record since it was set, boot queues one whose
// fact says the identity restarted; with one in the record, nothing is
// repeated and the condition becomes `word` with the reason.
func TestAHeldNowIsTakenUpOnceAcrossARestart(t *testing.T) {
	app, _ := dispatchApp(t)
	defer stopApp(t, app)
	pressStage(t, app, store.StageTrusted)
	obs := fixtureModel(t, app, func(n int, w http.ResponseWriter) { _, _ = io.WriteString(w, textReply("leg done")) })
	ctx := interaction.WithTurnSource(interaction.WithTurn(context.Background(), "turn_capped"), turnSourceOperator)
	if err := app.store.StartWorkSession(ctx, "ws_held", "keep a promise"); err != nil {
		t.Fatal(err)
	}
	promise, now := "cm_1", "now"
	if err := app.store.SetWorkPromise(ctx, "ws_held", &promise, &now, "The turn ended at a checkpoint under cm_1; a fresh turn follows"); err != nil {
		t.Fatal(err)
	}
	app.releaseHeldPromise(releasedAtRestart)
	waitUntil(t, sawRequest(obs, "the identity was restarted before this fresh turn ran"), "the fresh turn after the restart")
	waitUntil(t, func() bool { return resumesOnOf(t, app) == "word" }, "the fresh turn ending by itself")
	requests := len(obs.snapshot())

	// Set again, with its fresh turn already in the record: not repeated.
	if err := app.store.SetWorkPromise(ctx, "ws_held", nil, &now, "The turn ended at a checkpoint under cm_1; a fresh turn follows"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(10 * time.Millisecond)
	leg := interaction.WithResumeSession(interaction.WithTurnSource(interaction.WithTurn(context.Background(), "turn_leg"), turnSourceContinuation), "ws_held")
	if _, err := app.store.RecordRelatedConversation(leg, string(interaction.System), "[budget checkpoint — continuation 1] Work session ws_held is still active.", "turn_leg", "", interaction.Details{}); err != nil {
		t.Fatal(err)
	}
	app.releaseHeldPromise(releasedAtRestart)
	if on := resumesOnOf(t, app); on != "word" {
		t.Fatalf("a now whose fresh turn ran was not settled: %q", on)
	}
	if n := promiseNotice(t, app); !strings.Contains(n, "interrupted (the identity restarted); nothing was repeated") {
		t.Fatalf("the record does not say why: %q", n)
	}
	time.Sleep(300 * time.Millisecond)
	if len(obs.snapshot()) != requests {
		t.Fatal("a fresh turn was repeated")
	}
}

// A `now` THE TICK TAKES UP SAYS SO, NOT THAT THE IDENTITY RESTARTED.
// The card and the log carry the operator's words; TestAHeldNowIsTakenUpOnceAcrossARestart holds the boot's.
func TestAHeldNowTheTickTakesUpSaysTheHostStartedIt(t *testing.T) {
	app, _ := dispatchApp(t)
	defer stopApp(t, app)
	pressStage(t, app, store.StageTrusted)
	logs := logsink.CaptureForTest(t)
	obs := fixtureModel(t, app, func(n int, w http.ResponseWriter) { _, _ = io.WriteString(w, textReply("leg done")) })
	ctx := interaction.WithTurnSource(interaction.WithTurn(context.Background(), "turn_capped"), turnSourceOperator)
	if err := app.store.StartWorkSession(ctx, "ws_held", "keep a promise"); err != nil {
		t.Fatal(err)
	}
	promise, now := "cm_1", "now"
	if err := app.store.SetWorkPromise(ctx, "ws_held", &promise, &now, "The turn ended by itself under cm_1; a fresh turn follows"); err != nil {
		t.Fatal(err)
	}
	app.releaseHeldPromise(releasedAtTick)
	words := "ended with nothing holding the work, so the host started this one"
	waitUntil(t, sawRequest(obs, words), "the fresh turn the tick started, in the operator's words")
	if sawRequest(obs, "the identity was restarted")() {
		t.Fatal("the tick's fresh turn says the identity was restarted")
	}
	if !sawRequest(obs, "[work continues — continuation 1] Your previous turn "+words+".")() || sawRequest(obs, "[budget checkpoint")() {
		t.Fatal("the tick's fresh turn is not the work's card")
	}
	waitUntil(t, func() bool { return strings.Contains(logs.String(), "the turn before it "+words) }, "the log's continuation line in the operator's words")
	if strings.Contains(logs.String(), "the identity was restarted before this fresh turn ran") {
		t.Fatal("the log says the identity was restarted at a tick")
	}
}
