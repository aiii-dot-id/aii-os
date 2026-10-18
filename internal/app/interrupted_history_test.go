package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/conversation"
	"github.com/aiii-dot-id/aii-os/internal/interaction"
	"github.com/aiii-dot-id/aii-os/internal/llm"
)

const interruptedWords = "I have read the file and I think the bug is in the parser, because"

// How the second request of an interrupted turn ends.
const (
	endsStopped  = "the operator stops it"
	endsStreamed = "the operator stops it while the reply is arriving"
	endsFailed   = "the provider fails (HTTP 500)"
	endsRefused  = "the provider refuses (HTTP 429, asking for a wait)"
	endsDropped  = "the connection is dropped"
	providerDown = "the model provider failed: HTTP 500 Internal Server Error"
)

// interruptedModel serves a turn that speaks, calls ls, and is then
// interrupted on its second request — by the operator's stop, by the
// provider answering with an error status, or by the connection going —
// and answers every later request plainly.
func interruptedModel(t *testing.T, a *App, ends string) *contScript {
	t.Helper()
	obs := &contScript{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		n := obs.add(string(body))
		w.Header().Set("Content-Type", "application/json")
		switch n {
		case 1:
			fmt.Fprint(w, callReply(interruptedWords, toolCall("c1", "ls", `{"path":"."}`)))
		case 2:
			switch ends {
			case endsFailed:
				http.Error(w, "fixture provider failure", http.StatusInternalServerError)
				return
			case endsRefused:
				w.Header().Set("Retry-After", "30")
				http.Error(w, "fixture provider refusal: slow down, account acct-7f3a", http.StatusTooManyRequests)
				return
			case endsDropped:
				conn, _, err := w.(http.Hijacker).Hijack()
				if err != nil {
					t.Errorf("the fixture could not drop the connection: %v", err)
					return
				}
				conn.Close()
				return
			case endsStreamed:
				// The reply is already arriving: the headers and a first
				// event are with the client, which is reading the body when
				// the stop lands. (The other stop lands before any header.)
				w.Header().Set("Content-Type", "text/event-stream")
				fmt.Fprint(w, "data: {\"id\":\"r\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\" the\"}}]}\n\n")
				w.(http.Flusher).Flush()
				time.Sleep(300 * time.Millisecond)
			}
			if !a.CancelTurn() {
				t.Error("there was no running turn to stop")
			}
			select {
			case <-r.Context().Done():
			case <-time.After(10 * time.Second):
				t.Error("the stopped turn never abandoned its request")
			}
		default:
			fmt.Fprint(w, textReply("noted"))
		}
	}))
	t.Cleanup(srv.Close)
	a.llmSwap.Swap(llm.New(&llm.ClientConfig{Endpoint: srv.URL, Model: "fake", MaxOutputTokens: 4096, Retries: -1}))
	return obs
}

// interruptedTurn runs one operator turn that must end in an error.
func interruptedTurn(t *testing.T, a *App, msg string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if err := a.acquireTurn(ctx); err != nil {
		t.Fatalf("acquire turn: %v", err)
	}
	if _, err := a.observeChat(ctx, msg, func(kind, name, args string) {}); err == nil {
		t.Fatal("the interrupted turn reported success")
	}
}

var interruptionCauses = []struct {
	ends  string
	cause string
}{
	{endsStopped, "stopped by the operator"},
	{endsStreamed, "stopped by the operator"},
	{endsFailed, providerDown},
	{endsRefused, "the model provider refused the request: HTTP 429 Too Many Requests; it asked for a wait of 30s"},
	{endsDropped, "the substrate failed mid-turn"},
}

// (a) THE NEXT REQUEST CARRIES THE UNFINISHED WORDS WITH THEIR CAUSE.
// The identity's words go back as theirs, byte for byte; the note that
// they are unfinished follows them at once, as its own message in the
// substrate's bracketed voice, naming what ended them. (b) The turn's
// tool events, system rows of the same table, stay out of the history.
func TestAnInterruptedReplyReachesTheNextTurnWithItsCause(t *testing.T) {
	for _, c := range interruptionCauses {
		t.Run(c.ends, func(t *testing.T) {
			app, _ := promptCacheApp(t)
			obs := interruptedModel(t, app, c.ends)
			interruptedTurn(t, app, "first question")
			operatorTurn(t, app, "second question")

			reqs := obs.snapshot()
			if len(reqs) != 3 {
				t.Fatalf("saw %d requests, want 3", len(reqs))
			}
			msgs := wireMessages(t, reqs[2])
			// (b) The tool events are recorded — the exclusion is not vacuous —
			// and none of them is sent.
			rows, err := app.store.RecentTurnsIncludingSystem(50)
			if err != nil {
				t.Fatal(err)
			}
			toolRows := 0
			for _, r := range rows {
				if r.Role == string(interaction.System) && strings.Contains(r.Content, "→ ls(") {
					toolRows++
				}
			}
			if toolRows == 0 {
				t.Fatal("the interrupted turn recorded no tool event; the exclusion below would prove nothing")
			}
			for _, m := range msgs {
				if strings.Contains(m.Content, "→ ls(") {
					t.Fatalf("a tool event entered the model's history: %q", m.Content)
				}
			}
			note := "[the reply above is incomplete — " + c.cause + "]"
			var got []string
			for _, m := range msgs[1:] {
				got = append(got, m.Role+": "+m.Content)
			}
			if len(msgs) != 5 {
				t.Fatalf("want system, question, the unfinished reply, its note and the current message; got:\n%s", strings.Join(got, "\n---\n"))
			}
			if msgs[1].Role != "user" || msgs[1].Content != "first question" {
				t.Fatalf("history does not open on the question the interrupted turn answered: %q", got[0])
			}
			if msgs[2].Role != "assistant" || msgs[2].Content != interruptedWords {
				t.Fatalf("the identity's unfinished words are not sent back as theirs, verbatim: %s", got[1])
			}
			if msgs[3].Role != "user" || msgs[3].Content != note {
				t.Fatalf("THE UNFINISHED REPLY REACHED THE MODEL WITHOUT ITS INTERRUPTION — want %q right after it, got:\n%s", note, strings.Join(got, "\n---\n"))
			}
			if !strings.HasPrefix(msgs[4].Content, "second question\n\n"+conversation.TurnOpen) {
				t.Fatalf("the current message is not the new words closed by the block: %q", msgs[4].Content)
			}
			for _, m := range msgs {
				if m.Role == "assistant" && strings.Contains(m.Content, "incomplete") {
					t.Fatalf("THE SUBSTRATE SPOKE IN THE IDENTITY'S VOICE: %q", m.Content)
				}

				if strings.Contains(m.Content, "acct-7f3a") || strings.Contains(m.Content, "fixture provider") {
					t.Fatalf("THE PROVIDER'S OWN WORDS REACHED THE MODEL: %q", m.Content)
				}
			}

		})
	}
}

func TestAStopWhileTheReplyIsArrivingIsStillAStop(t *testing.T) {
	app, _ := promptCacheApp(t)
	interruptedModel(t, app, endsStreamed)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if err := app.acquireTurn(ctx); err != nil {
		t.Fatalf("acquire turn: %v", err)
	}
	_, err := app.observeChat(ctx, "first question", func(kind, name, args string) {})
	if err == nil || !strings.Contains(err.Error(), "read response stream") {
		t.Fatalf("rig: the stop did not land in the read of the reply's body: %v", err)
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("A STOPPED TURN'S ERROR IS NOT A CANCELLATION TO ITS CALLERS: %v", err)
	}
	if !errors.Is(err, conversation.StoppedByOperator) {
		t.Fatalf("the stopped turn's error does not say who stopped it: %v", err)
	}
	// The body's read already returned the stop: the turn does not say it
	// a second time (the log's line for a stopped turn reads once).
	if n := strings.Count(err.Error(), string(conversation.StoppedByOperator)); n != 1 {
		t.Fatalf("THE STOPPED TURN'S ERROR NAMES THE STOP %d TIMES: %v", n, err)
	}
	rows, err := app.store.RecentTurnsIncludingSystem(50)
	if err != nil {
		t.Fatal(err)
	}
	const note = "[the reply above is incomplete — stopped by the operator]"
	for _, r := range rows {
		if r.Role == string(interaction.System) && r.Content == note {
			return
		}
	}
	t.Fatalf("the unfinished reply is not marked %q", note)
}

// (c) A note never outlives its reply. Once the stepped window has moved
// past the unfinished words, their note is not sent alone — a bracketed
// "the reply above is incomplete" over some other reply would be a false
// statement about the identity's own words.
func TestAnInterruptionNoteLeavesTheWindowWithItsReply(t *testing.T) {
	app, _ := promptCacheApp(t)
	app.cfgMu.Lock()
	app.cfg.Prompt.RecentTurns = 2
	app.cfgMu.Unlock()
	obs := interruptedModel(t, app, endsStopped)
	interruptedTurn(t, app, "first question")
	operatorTurn(t, app, "second question")
	operatorTurn(t, app, "third question")

	reqs := obs.snapshot()
	if len(reqs) != 4 {
		t.Fatalf("saw %d requests, want 4", len(reqs))
	}
	carries := func(body string) (reply, note bool) {
		for _, m := range wireMessages(t, body) {
			reply = reply || m.Content == interruptedWords
			note = note || strings.Contains(m.Content, "the reply above is incomplete")
		}
		return
	}
	// The control: while the reply is in the window, so is its note.
	if reply, note := carries(reqs[2]); !reply || !note {
		t.Fatalf("the second turn's window holds the reply (%v) and its note (%v); want both", reply, note)
	}
	if reply, note := carries(reqs[3]); reply || note {
		t.Fatalf("the third turn's window moved past the reply, yet sent reply=%v note=%v", reply, note)
	}
}

// (d) THE NOTE IS PART OF A STABLE PREFIX. It is rendered from the stored
// rows alone, so two consecutive turns with no new interruption send the
// same history up to the newest exchange, the note included, and a
// provider cache reads all of it from the first of them.
func TestAnInterruptionNoteKeepsTheHistoryPrefixStable(t *testing.T) {
	app, _ := promptCacheApp(t)
	obs := interruptedModel(t, app, endsFailed)
	interruptedTurn(t, app, "first question")
	operatorTurn(t, app, "second question")
	operatorTurn(t, app, "third question")

	reqs := obs.snapshot()
	if len(reqs) != 4 {
		t.Fatalf("saw %d requests, want 4", len(reqs))
	}
	second, third := wireMessages(t, reqs[2]), wireMessages(t, reqs[3])
	if len(third) != len(second)+2 {
		t.Fatalf("the third turn should send the second's history plus one exchange: %d then %d messages", len(second), len(third))
	}
	noted := false
	for i := 0; i < len(second)-1; i++ {
		if second[i] != third[i] {
			t.Fatalf("message %d moved between consecutive turns:\nthen: %q\nnow:  %q", i, second[i].Content, third[i].Content)
		}
		noted = noted || strings.Contains(second[i].Content, "the reply above is incomplete")
	}
	if !noted {
		t.Fatal("the note is not in the shared prefix; the stability above proves nothing about it")
	}
	if words := third[len(second)-1].Content; !strings.HasPrefix(second[len(second)-1].Content, words) {
		t.Fatalf("the second question comes back as %q, not a prefix of what it sent", words)
	}
}

// SAFE keeps its conversation in the transient transcript, not the store;
// an interrupted reply there is marked the same way and reaches the
// model the same way: the words as theirs, the note right after.
func TestAnInterruptedReplyInSafeModeReachesTheModelWithItsCause(t *testing.T) {
	app, _ := promptCacheApp(t)
	app.engine.SetSafeMode("fixture")
	t.Cleanup(func() { app.engine.SetSafeMode("") })
	app.recordInterruptedTurnContext(t.Context(), conversation.Result{Spoken: interruptedWords, Interrupted: "the substrate failed mid-turn"})
	conv, _, err := app.buildHistory(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(conv) != 2 || conv[0].Role != "assistant" || conv[0].Content != interruptedWords ||
		conv[1].Role != "user" || conv[1].Content != "[the reply above is incomplete — the substrate failed mid-turn]" || !conv[1].Qualifies {
		t.Fatalf("SAFE's history does not carry the reply and then its note: %+v", conv)
	}
}
