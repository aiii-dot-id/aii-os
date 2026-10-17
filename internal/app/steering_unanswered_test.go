package app

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/conversation"
	"github.com/aiii-dot-id/aii-os/internal/interaction"
	"github.com/aiii-dot-id/aii-os/internal/llm"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

// steering_unanswered_test.go — words that reached a turn mid-flight and
// went unanswered because the turn failed (T79). They used to ride the
// failed turn's reply inside a first-person sentence, "You said while I
// was working: … so I am repeating it here", recorded as the identity's
// words and sent back to the model as something the identity had said.
// Now the reply is the identity's alone; the words are a substrate notice
// beside it, never handed to speech, and read by the next turn once.

const unansweredWords = "stop, the parser is fine — look at the lexer"

// steeredFailingModel serves a turn that takes the operator's words
// mid-flight (steer, run while the turn holds the gate), says spoken (if
// any) and calls ls, then fails on its second request; every later
// request is answered plainly.
func steeredFailingModel(t *testing.T, a *App, spoken string, steer func() (bool, error)) *contScript {
	t.Helper()
	obs := &contScript{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		n := obs.add(string(body))
		w.Header().Set("Content-Type", "application/json")
		switch n {
		case 1:
			if steered, err := steer(); !steered || err != nil {
				t.Errorf("the words did not join the running turn: steered=%v err=%v", steered, err)
			}
			fmt.Fprint(w, callReply(spoken, toolCall("c1", "ls", `{"path":"."}`)))
		case 2:
			http.Error(w, "fixture provider failure", http.StatusInternalServerError)
		default:
			fmt.Fprint(w, textReply("noted"))
		}
	}))
	t.Cleanup(srv.Close)
	a.llmSwap.Swap(llm.New(&llm.ClientConfig{Endpoint: srv.URL, Model: "fake", MaxOutputTokens: 4096, Retries: -1}))
	return obs
}

// unansweredRecord is the substrate notice the failed turn left: its
// text, the row it stands beside and the failed turn.
type unansweredRecord struct{ content, related, turn string }

func unansweredNotices(t *testing.T, a *App) []unansweredRecord {
	t.Helper()
	rows, err := a.store.DB().Query(`SELECT content, COALESCE(related_id,''), turn_id FROM conversations
		WHERE role='system' AND kind='notice' AND json_extract(details,'$.reason_code')=? ORDER BY turn_seq`, store.ReasonSteeringUnanswered)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []unansweredRecord
	for rows.Next() {
		var r unansweredRecord
		if err := rows.Scan(&r.content, &r.related, &r.turn); err != nil {
			t.Fatal(err)
		}
		out = append(out, r)
	}
	return out
}

// wordsRows is every words row of role, as id -> content, in order.
func wordsRows(t *testing.T, a *App, role string) (ids, contents []string) {
	t.Helper()
	rows, err := a.store.DB().Query(`SELECT id, content FROM conversations WHERE role=? AND kind='message' ORDER BY turn_seq`, role)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var id, content string
		if err := rows.Scan(&id, &content); err != nil {
			t.Fatal(err)
		}
		ids, contents = append(ids, id), append(contents, content)
	}
	return ids, contents
}

func noteOn(words ...string) string {
	return "[said while that turn was working, and it ended before answering (" + providerDown + "): «" + strings.Join(words, "» «") + "»]"
}

func countContaining(msgs []wireMessage, s string) int {
	n := 0
	for _, m := range msgs {
		if strings.Contains(m.Content, s) {
			n++
		}
	}
	return n
}

// (a) THE REPLY IS THE IDENTITY'S WORDS ONLY; the operator's words stand
// beside it as the substrate's notice, on the page record. (c) The next
// turn's model reads the notice once, right after the failed reply and
// its interruption marker; the turn after that does not.
func TestUnansweredSteeringIsTheSubstratesNoteNotTheIdentitysReply(t *testing.T) {
	app, _ := promptCacheApp(t)
	obs := steeredFailingModel(t, app, interruptedWords, func() (bool, error) { return app.Steer(unansweredWords) })
	interruptedTurn(t, app, "first question")

	replyIDs, replies := wordsRows(t, app, string(interaction.Resident))
	if len(replies) != 1 || replies[0] != interruptedWords {
		t.Fatalf("THE FAILED TURN'S REPLY IS NOT THE IDENTITY'S WORDS ALONE: %q", replies)
	}
	notes := unansweredNotices(t, app)
	if len(notes) != 1 || notes[0].content != noteOn(unansweredWords) || notes[0].related != replyIDs[0] || notes[0].turn == "" {
		t.Fatalf("the unanswered words are not recorded beside the failed reply, verbatim: %+v (reply %s)", notes, replyIDs[0])
	}

	operatorTurn(t, app, "second question")
	operatorTurn(t, app, "third question")
	reqs := obs.snapshot()
	if len(reqs) != 4 {
		t.Fatalf("saw %d requests, want 4", len(reqs))
	}
	msgs := wireMessages(t, reqs[2])
	var got []string
	for _, m := range msgs[1:] {
		got = append(got, m.Role+": "+m.Content)
	}
	want := []string{
		"user: first question",
		"user: " + unansweredWords, // the steered row, as the operator said it
		"assistant: " + interruptedWords,
		"user: [the reply above is incomplete — " + providerDown + "]",
		"user: " + noteOn(unansweredWords),
	}
	if len(got) != len(want)+1 || strings.Join(got[:len(want)], "\n") != strings.Join(want, "\n") {
		t.Fatalf("the next turn's history is not the failed exchange with its notes:\n%s\nwant:\n%s", strings.Join(got, "\n---\n"), strings.Join(want, "\n---\n"))
	}
	if !strings.HasPrefix(msgs[len(msgs)-1].Content, "second question\n\n"+conversation.TurnOpen) {
		t.Fatalf("the current message is not the new words closed by the block: %q", msgs[len(msgs)-1].Content)
	}
	for _, body := range reqs[2:] {
		for _, m := range wireMessages(t, body) {
			if m.Role == "assistant" && (strings.Contains(m.Content, unansweredWords) || strings.Contains(m.Content, "You said")) {
				t.Fatalf("THE SUBSTRATE SPOKE IN THE IDENTITY'S VOICE: %q", m.Content)
			}
		}
	}
	third := wireMessages(t, reqs[3])
	if n := countContaining(third, "it ended before answering"); n != 0 {
		t.Fatalf("THE NOTE WAS REPLAYED to the turn after the one it was delivered to (%d times)", n)
	}
	// The control: the third turn's window still holds the failed exchange,
	// so the note's absence is delivery, not the window moving on.
	if countContaining(third, interruptedWords) != 1 || countContaining(third, "the reply above is incomplete") != 1 {
		t.Fatal("the third turn's window lost the failed exchange; the absence above proves nothing")
	}
}

// (b) NEVER SYNTHESIZED, and (e) a failure that said nothing still keeps
// the words. Spoken words steered into a turn bound to a voice session:
// the failed turn speaks nothing of them, whether or not it had said
// anything; the next turn's reply is spoken (the control: the seam is
// live) and is only the identity's words. The note quotes the words with
// the channel's marker, as delivered.
func TestUnansweredSpokenWordsNeverReachSpeech(t *testing.T) {
	for _, c := range []struct {
		name, spoken string
	}{{"the turn said nothing", ""}, {"the turn had spoken", interruptedWords}} {
		t.Run(c.name, func(t *testing.T) {
			app, _ := promptCacheApp(t)
			var mu sync.Mutex
			var synthesized []string
			prev := voiceSynthesize
			voiceSynthesize = func(_ *App, _ context.Context, session string, _ uint64, reply string) replyVerdict {
				mu.Lock()
				synthesized = append(synthesized, session+": "+reply)
				mu.Unlock()
				return replyAdmitted
			}
			t.Cleanup(func() { voiceSynthesize = prev })
			heard := voiceMarker + unansweredWords
			obs := steeredFailingModel(t, app, c.spoken, func() (bool, error) {
				return app.steer(roleOperator, heard, &voiceBinding{session: "vs-fixture", gen: 1}, nil)
			})
			interruptedTurn(t, app, "first question")

			opIDs, ops := wordsRows(t, app, string(interaction.Operator))
			if len(ops) != 2 || ops[1] != heard {
				t.Fatalf("the spoken words were not recorded as the operator's: %q", ops)
			}
			replyIDs, replies := wordsRows(t, app, string(interaction.Resident))
			about := opIDs[1]
			if c.spoken == "" {
				if len(replies) != 0 {
					t.Fatalf("a turn that said nothing recorded a reply: %q", replies)
				}
			} else {
				if len(replies) != 1 || replies[0] != c.spoken {
					t.Fatalf("the reply is not the identity's words alone: %q", replies)
				}
				about = replyIDs[0]
			}
			notes := unansweredNotices(t, app)
			if len(notes) != 1 || notes[0].content != noteOn(heard) || notes[0].related != about {
				t.Fatalf("THE UNANSWERED WORDS WERE NOT KEPT beside %s: %+v", about, notes)
			}
			mu.Lock()
			failed := append([]string(nil), synthesized...)
			mu.Unlock()
			if len(failed) != 0 {
				t.Fatalf("the failed turn handed speech %q", failed)
			}

			app.holdVoice(&voiceBinding{session: "vs-fixture", gen: 2})
			operatorTurn(t, app, "second question")
			operatorTurn(t, app, "third question")
			mu.Lock()
			spoken := strings.Join(synthesized, "|")
			mu.Unlock()
			if spoken != "vs-fixture: noted" {
				t.Fatalf("speech saw %q; want only the next turn's own reply", spoken)
			}
			reqs := obs.snapshot()
			if len(reqs) != 4 {
				t.Fatalf("saw %d requests, want 4", len(reqs))
			}
			second := wireMessages(t, reqs[2])
			if n := countContaining(second, noteOn(heard)); n != 1 {
				t.Fatalf("the next turn carries the note %d times, want once", n)
			}
			// Beside the row it is about: the reply's marker when the turn
			// had spoken, else the words themselves.
			before := "[the reply above is incomplete — " + providerDown + "]"
			if c.spoken == "" {
				before = heard
			}
			for i, m := range second {
				if m.Content == noteOn(heard) && (i == 0 || second[i-1].Content != before) {
					t.Fatalf("the note does not follow the row it is about: %q then %q", second[i-1].Content, m.Content)
				}
			}
			if n := countContaining(wireMessages(t, reqs[3]), "it ended before answering"); n != 0 {
				t.Fatalf("THE NOTE WAS REPLAYED to a later turn (%d times)", n)
			}
		})
	}
}

// SAFE keeps the conversation in the transient transcript; the note is
// recorded there and delivered the same way: to the first turn that
// records words after it, and no later one.
func TestUnansweredWordsInSafeModeReachTheNextTurnOnce(t *testing.T) {
	app, _ := promptCacheApp(t)
	app.engine.SetSafeMode("fixture")
	t.Cleanup(func() { app.engine.SetSafeMode("") })
	failed := interaction.WithTurn(t.Context(), "turn-failed")
	if _, err := app.recordConversationRef(failed, string(interaction.Operator), "first question"); err != nil {
		t.Fatal(err)
	}
	app.recordInterruptedTurnContext(failed, conversation.Result{Unanswered: []string{unansweredWords}, Interrupted: providerDown})
	next := interaction.WithTurn(t.Context(), "turn-next")
	if _, err := app.recordConversationRef(next, string(interaction.Operator), "second question"); err != nil {
		t.Fatal(err)
	}
	conv, _, err := app.buildHistory(next)
	if err != nil {
		t.Fatal(err)
	}
	if len(conv) != 3 || conv[0].Content != "first question" || conv[1].Content != noteOn(unansweredWords) || !conv[1].Qualifies || conv[2].Content != "second question" {
		t.Fatalf("SAFE's history does not carry the words' note after the failed turn's words: %+v", conv)
	}
	if _, err := app.recordConversationRef(interaction.WithTurn(t.Context(), "turn-later"), string(interaction.Operator), "third question"); err != nil {
		t.Fatal(err)
	}
	later, _, err := app.buildHistory(interaction.WithTurn(t.Context(), "turn-later"))
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range later {
		if m.Content == noteOn(unansweredWords) {
			t.Fatalf("SAFE replayed the note to a later turn: %+v", later)
		}
	}
}

// A FAILED TURN WITH NO WORDS OF ITS OWN STILL DELIVERS ITS NOTE. A
// timer's wake records its fact as a system row, and steered words whose
// record failed still reach the loop: such a turn, failing, has
// unanswered words and no row to stand beside. A note about no row would
// be shown and never delivered (the window selects notes by their row);
// it stands beside the conversation's newest words instead, and reaches
// the next turn once.
func TestAFailedTurnWithNoWordsOfItsOwnStillDeliversItsNote(t *testing.T) {
	for _, safe := range []bool{false, true} {
		t.Run(map[bool]string{false: "recorded", true: "SAFE beside a recorded row"}[safe], func(t *testing.T) {
			wordlessFailureDeliversItsNote(t, safe)
		})
	}
}

func wordlessFailureDeliversItsNote(t *testing.T, safe bool) {
	app, _ := promptCacheApp(t)
	operatorTurn(t, app, "first question")
	ids, replies := wordsRows(t, app, string(interaction.Resident))
	if len(replies) == 0 {
		t.Fatal("the first turn recorded no reply; the anchor below would prove nothing")
	}
	newest := ids[len(ids)-1]
	if safe {
		// SAFE began after the recorded exchange: the note is transient,
		// the row it stands beside durable.
		app.engine.SetSafeMode("fixture")
		t.Cleanup(func() { app.engine.SetSafeMode("") })
	}

	failed := interaction.WithTurn(t.Context(), "turn-wordless")
	app.recordInterruptedTurnContext(failed, conversation.Result{Unanswered: []string{unansweredWords}, Interrupted: providerDown})
	related := ""
	if safe {
		for _, r := range app.engine.SafeTranscript() {
			if r.Record.Details.ReasonCode == store.ReasonSteeringUnanswered {
				related = r.Record.RelatedID
			}
		}
	} else if notes := unansweredNotices(t, app); len(notes) == 1 {
		related = notes[0].related
	}
	if related != newest {
		t.Fatalf("THE NOTE STANDS BESIDE %q, not the conversation's newest words %s: it would never be delivered", related, newest)
	}

	next := interaction.WithTurn(t.Context(), "turn-next")
	if _, err := app.recordConversationRef(next, string(interaction.Operator), "second question"); err != nil {
		t.Fatal(err)
	}
	conv, _, err := app.buildHistory(next)
	if err != nil {
		t.Fatal(err)
	}
	carried := 0
	for i, m := range conv {
		if m.Content == noteOn(unansweredWords) {
			carried++
			if i == 0 || conv[i-1].Role != "assistant" || conv[i-1].Content != replies[len(replies)-1] || !m.Qualifies {
				t.Fatalf("the note does not follow the words it stands beside: %+v", conv)
			}
		}
	}
	if carried != 1 {
		t.Fatalf("the next turn carries the note %d times, want once: %+v", carried, conv)
	}
	later := interaction.WithTurn(t.Context(), "turn-later")
	if _, err := app.recordConversationRef(later, string(interaction.Operator), "third question"); err != nil {
		t.Fatal(err)
	}
	conv, _, err = app.buildHistory(later)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range conv {
		if m.Content == noteOn(unansweredWords) {
			t.Fatal("THE NOTE WAS REPLAYED to a later turn")
		}
	}
}

// Only an empty conversation leaves nothing to stand beside. The note is
// still recorded, for the page, and the host says loudly that it cannot
// reach the next turn: never silently undeliverable.
func TestAnUnansweredNoteWithNothingToStandBesideIsLoud(t *testing.T) {
	app, _ := promptCacheApp(t)
	if _, words := wordsRows(t, app, string(interaction.Operator)); len(words) != 0 {
		t.Fatalf("the fixture's conversation is not empty: %q", words)
	}
	if _, words := wordsRows(t, app, string(interaction.Resident)); len(words) != 0 {
		t.Fatalf("the fixture's conversation is not empty: %q", words)
	}
	logs := logsink.CaptureForTest(t)
	app.recordInterruptedTurnContext(interaction.WithTurn(t.Context(), "turn-alone"),
		conversation.Result{Unanswered: []string{unansweredWords}, Interrupted: providerDown})
	notes := unansweredNotices(t, app)
	if len(notes) != 1 || notes[0].related != "" {
		t.Fatalf("the note was not kept for the page: %+v", notes)
	}
	if !logs.Contains("will not reach the next turn") {
		t.Fatalf("an undeliverable note was recorded SILENTLY:\n%s", logs.String())
	}
}
