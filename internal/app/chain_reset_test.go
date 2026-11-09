package app

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/interaction"
)

// continuesLine closes the reply of a turn capped at its declared budget
// after eight tool calls whose work a fresh turn was scheduled to continue.
const continuesLine = "[This turn ended at its declared budget after 8 tool calls; the work continues automatically in a fresh turn.]"

// cappedOnce is a model that caps the first turn it is sent at its declared
// budget after eight tool calls (two setup calls, then steps=1 buys two
// remaining calls; the four grace rounds read a file), with "capped here"
// as its words, and answers every later
// request with words alone, so the leg that follows ends by itself.
func cappedOnce(t *testing.T, dir string) func(n int) string {
	t.Helper()
	if err := writeFileForTest(filepath.Join(dir, "probe.txt"), "leg probe"); err != nil {
		t.Fatal(err)
	}
	return func(n int) string {
		switch {
		case n == 1:
			return callReply("",
				toolCall("c1a", "work", `{"action":"start","description":"capped work"}`),
				toolCall("c1b", "work", `{"action":"update","plan":"read the probe","steps":1}`))
		case n <= 7:
			return callReply("", toolCall(fmt.Sprintf("c%d", n), "read", probeArgs(dir, n)))
		case n == 8:
			return textReply("capped here")
		}
		return textReply("leg done")
	}
}

// standDown spends the chain to its bound, as the turns before this one
// would have: the next capped turn that nobody asked for is refused.
func standDown(app *App) {
	bound := continuationsSetting.value(app.configSnapshot())
	app.turn.mu.Lock()
	app.turn.contChain = bound.N
	app.turn.mu.Unlock()
}

// THE OPERATOR'S MESSAGE, TYPED OR SPOKEN, STARTS A NEW CHAIN. With the
// chain stood down at its bound, the operator types (or says) "go on", and
// the turn that answers is capped at its declared budget: its work is
// continued in a fresh turn, and the reply says so. Without the reset at
// the message's entry that turn is refused its continuation and its reply
// says nothing continues, because eight fresh turns have already been
// started: the stand-down outlived the look it waits for.
func TestTheOperatorsTypedOrSpokenMessageStartsANewChain(t *testing.T) {
	for _, path := range []string{"typed", "spoken"} {
		t.Run(path, func(t *testing.T) {
			app, dir := dispatchApp(t)
			defer stopApp(t, app)
			script := cappedOnce(t, dir)
			obs := fixtureModel(t, app, func(n int, w http.ResponseWriter) { _, _ = io.WriteString(w, script(n)) })

			ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
			defer cancel()
			if err := app.acquireTurn(ctx); err != nil {
				t.Fatalf("acquire turn: %v", err)
			}
			standDown(app) // with the gate held: no other turn's ending moves the chain
			var reply string
			var err error
			switch path {
			case "typed":
				reply, err = app.observeChat(ctx, "go on", func(kind, name, args string) {})
			case "spoken":
				reply, err = app.wake(ctx, roleOperator, voiceMarker+"go on")
				app.releaseTurn()
			}
			if err != nil {
				t.Fatalf("turn failed: %v", err)
			}
			if want := "capped here\n\n" + continuesLine; recordedReply(t, app, reply) != want {
				t.Fatalf("the operator's %s message did not start a new chain: the reply = %q, want %q", path, reply, want)
			}
			waitUntil(t, func() bool {
				for _, b := range obs.snapshot() {
					if strings.Contains(b, "budget checkpoint — continuation 1]") {
						return true
					}
				}
				return false
			}, "the fresh turn, the first leg of a new chain, reaching the model")
		})
	}
}

// WORDS SAID WHILE A TURN RUNS ARE READ, AND THE WORK CONTINUES. Said
// while a turn runs, they are read at its next tool boundary
// (DrainSteering), or open the turn after it when no boundary came
// (runLeftoverSteerTurn). The chain's count once decided whether the
// capped turn was continued, and only the operator's words reset it; a
// session's work is now carried on by the record's brakes (continueWork,
// 2026-11-06), so a capped turn continues whoever spoke, and the chain
// stood at its old bound (standDown) decides nothing.
func TestTheOperatorsWordsSaidWhileATurnRunsStartANewChain(t *testing.T) {
	for _, c := range []struct {
		name, role string
	}{
		{"the operator's", roleOperator},
		{"a participant's", roleParticipant},
	} {
		want := "capped here\n\n" + continuesLine

		t.Run("read at a tool boundary/"+c.name, func(t *testing.T) {
			app, dir := dispatchApp(t)
			defer stopApp(t, app)
			script := cappedOnce(t, dir)
			obs := fixtureModel(t, app, func(n int, w http.ResponseWriter) {
				if n == 2 {
					// Said while the turn's second request is with the model.
					if taken, err := app.steer(c.role, "keep going, mid-turn", nil, nil); !taken || err != nil {
						t.Errorf("the words were not taken for the running turn: %v, %v", taken, err)
					}
				}
				_, _ = io.WriteString(w, script(n))
			})
			ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
			defer cancel()
			if err := app.acquireTurn(ctx); err != nil {
				t.Fatalf("acquire turn: %v", err)
			}
			standDown(app)
			// A turn nobody typed: the substrate's, as a continuation leg's is.
			reply, err := app.wake(ctx, string(interaction.System), "[resume the capped work]")
			app.releaseTurn()
			if err != nil {
				t.Fatalf("turn failed: %v", err)
			}
			read := false
			for _, b := range obs.snapshot()[2:8] {
				read = read || strings.Contains(b, "keep going, mid-turn")
			}
			if !read {
				t.Fatal("rig: the words said mid-turn were not read at a tool boundary of that turn")
			}
			if recordedReply(t, app, reply) != want {
				t.Fatalf("%s words read at a tool boundary: the reply = %q, want %q", c.name, reply, want)
			}
		})

		t.Run("opening the turn after/"+c.name, func(t *testing.T) {
			app, dir := dispatchApp(t)
			defer stopApp(t, app)
			script := cappedOnce(t, dir)
			fixtureModel(t, app, func(n int, w http.ResponseWriter) { _, _ = io.WriteString(w, script(n)) })
			// The words' turn is given the token by the release that kept
			// it for them (releaseTurn) and never takes it itself: held
			// here as that release leaves it.
			<-app.turnGate
			standDown(app)
			app.runLeftoverSteerTurn([]steerEntry{{role: c.role, content: "keep going, said as it ended", recordCtx: context.Background()}})
			if got := recordedReply(t, app, "capped here"); got != want {
				t.Fatalf("%s words left by a turn opened their own: its reply = %q, want %q", c.name, got, want)
			}
		})
	}
}

// AT A STAND-DOWN THE PAGE SAYS NOTHING CONTINUES, AND WHY. End to end,
// on a live identity: the operator's message opens a turn the model caps,
// and its reply says the work continues (a first leg was scheduled); that
// leg is capped too having recorded no outcome, so the record's brake
// stands the work down (no progress since its last fresh turn; work
// continues, 2026-11-06, in place of the chain's bound the test once set
// to one), and the leg's reply closes on what happened. The leg's words
// moved nothing, so they stay on the page with the row beside them
// (the floor for an unpromised leg), and the outbox holds nothing of it.
func TestAStandDownAtTheOperatorsBoundIsToldOnThePage(t *testing.T) {
	app, dir := dispatchApp(t)
	defer stopApp(t, app)
	opening := cappedOnce(t, dir)
	readArgs, _ := json.Marshal(map[string]string{"file_path": filepath.Join(dir, "probe.txt")})
	fixtureModel(t, app, func(n int, w http.ResponseWriter) {
		switch {
		case n <= 8: // two setup calls, two remaining calls, four grace reads
			_, _ = io.WriteString(w, opening(n))
		case n == 9: // one declaration call, then two remaining calls and four grace reads
			_, _ = io.WriteString(w, callReply("",
				toolCall("l9a", "work", `{"action":"update","steps":1}`),
				toolCall("l9b", "read", string(readArgs))))
		case n <= 14:
			_, _ = io.WriteString(w, callReply("", toolCall(fmt.Sprintf("l%d", n), "read", probeArgs(dir, n))))
		case n == 15:
			_, _ = io.WriteString(w, textReply("leg capped"))
		default:
			_, _ = io.WriteString(w, textReply("nothing should ask this"))
		}
	})

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	if err := app.acquireTurn(ctx); err != nil {
		t.Fatalf("acquire turn: %v", err)
	}
	reply, err := app.observeChat(ctx, "start the capped work", func(kind, name, args string) {})
	if err != nil {
		t.Fatalf("turn failed: %v", err)
	}
	if want := "capped here\n\n" + continuesLine; recordedReply(t, app, reply) != want {
		t.Fatalf("rig: the operator's turn did not hand its work on: %q", reply)
	}

	waitUntil(t, replyRecorded(app, "leg capped"), "the first leg's reply")
	waitUntil(t, func() bool { return resumesOnOf(t, app) == "word" }, "the stand-down")
	want := "leg capped\n\n" + nothingContinues(7, "no progress on this work was recorded since its last fresh turn; it resumes on your operator's word")
	if got := recordedReply(t, app, "leg capped"); got != want {
		t.Errorf("at the stand-down the identity's history holds %q, want %q", got, want)
	}
	if r := waitRowBeside(t, app); r != wordsHeldUnpromisedRow {
		t.Errorf("the row beside the leg's words: %q", r)
	}
	if page := outboxReply(t, app, "wake_cont_"); page != "" {
		t.Errorf("a leg that moved nothing reached the operator's outbox: %q", page)
	}
	if n := promisesInTheRecord(t, app); n != 1 {
		t.Errorf("%d recorded replies promise a fresh turn; want the operator's turn's one, which a leg kept", n)
	}
}
