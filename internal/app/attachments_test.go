package app

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/dashboard"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

const attachedWords = "please read these two"

var attachedPaths = []string{"uploads/letter.docx", "uploads/proof.png"}

func attachedTurn(t *testing.T, app *App, msg string, paths []string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(dashboard.WithAttachments(context.Background(), paths), 60*time.Second)
	defer cancel()
	if err := app.acquireTurn(ctx); err != nil {
		t.Fatalf("acquire turn: %v", err)
	}
	if _, err := app.observeChat(ctx, msg, func(kind, name, args string) {}); err != nil {
		t.Fatalf("turn failed: %v", err)
	}
}

func TestFilesAttachedToAMessageAreToldAfterItsWordsAndNeverWrittenIntoThem(t *testing.T) {
	app, obs := promptCacheApp(t)
	attachedTurn(t, app, attachedWords, attachedPaths)
	operatorTurn(t, app, "and a second question")

	want := attachedWords + "\n\n[your operator attached a file: uploads/letter.docx]\n[your operator attached a file: uploads/proof.png]"
	reqs := obs.snapshot()
	if len(reqs) < 2 {
		t.Fatalf("two turns made %d requests", len(reqs))
	}
	first, second := wireMessages(t, reqs[0]), wireMessages(t, reqs[len(reqs)-1])
	if got := first[len(first)-1].Content; !strings.HasPrefix(got, want) {
		t.Fatalf("the turn that answers the message reads %q, want it to begin %q", got, want)
	}
	told := false
	for _, m := range second[:len(second)-1] {
		told = told || (m.Role == "user" && m.Content == want)
	}
	if !told {
		t.Fatalf("the next turn's history does not carry the message with its files, as first sent")
	}

	var typed string
	if err := app.store.DB().QueryRow(`SELECT content FROM conversations WHERE role='operator' AND content LIKE 'please read%'`).Scan(&typed); err != nil || typed != attachedWords {
		t.Fatalf("the person's row is %q (%v), want exactly what they typed", typed, err)
	}
	rows, err := app.store.DB().Query(`SELECT n.content FROM conversations n JOIN conversations m ON n.related_id=m.id
		WHERE n.role='system' AND n.kind='notice' AND json_extract(n.details,'$.reason_code')=? AND m.content=? ORDER BY n.turn_seq`, store.ReasonAttached, attachedWords)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var notices []string
	for rows.Next() {
		var path string
		if err := rows.Scan(&path); err != nil {
			t.Fatal(err)
		}
		notices = append(notices, path)
	}
	if strings.Join(notices, "|") != strings.Join(attachedPaths, "|") {
		t.Fatalf("the notices beside the message are %q, want one a file, each its path: %q", notices, attachedPaths)
	}
}

// SAFE keeps its conversation in the transient transcript; a file
// attached there is told the same way.
func TestFilesAttachedInSafeModeAreToldTheSameWay(t *testing.T) {
	app, _ := promptCacheApp(t)
	app.engine.SetSafeMode("fixture")
	t.Cleanup(func() { app.engine.SetSafeMode("") })
	ctx := dashboard.WithAttachments(t.Context(), attachedPaths[:1])
	ref, err := app.recordConversationRef(ctx, "operator", attachedWords)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := app.recordAttachments(ctx, ref.ID); len(got) != 1 || err != nil {
		t.Fatalf("recorded %q %v", got, err)
	}
	conv, _, err := app.buildHistory(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	want := attachedWords + "\n\n[your operator attached a file: uploads/letter.docx]"
	if len(conv) != 1 || conv[0].Role != "user" || conv[0].Content != want {
		t.Fatalf("SAFE history = %+v, want the words then the file's line", conv)
	}
}

// A message with no files is read as it was typed, and nothing is recorded
// beside it.
func TestAMessageWithNoFilesIsUnchanged(t *testing.T) {
	if got := withAttached("just words", nil); got != "just words" {
		t.Fatalf("%q", got)
	}
	if got, err := (&App{}).recordAttachments(t.Context(), "m1"); got != nil || err != nil {
		t.Fatalf("%q %v", got, err)
	}
}

// attachedNotices is every "attached" notice beside the operator message
// whose words are given, oldest first.
func attachedNotices(t *testing.T, a *App, words string) []string {
	t.Helper()
	rows, err := a.store.DB().Query(`SELECT n.content FROM conversations n JOIN conversations m ON n.related_id=m.id
		WHERE n.role='system' AND n.kind='notice' AND json_extract(n.details,'$.reason_code')=? AND m.role='operator' AND m.content=? ORDER BY n.turn_seq`, store.ReasonAttached, words)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var path string
		if err := rows.Scan(&path); err != nil {
			t.Fatal(err)
		}
		out = append(out, path)
	}
	return out
}

// A MESSAGE WITH FILES THAT ARRIVES WHILE A TURN RUNS KEEPS ITS FILES, BY
// EITHER WAY OUT OF THE QUEUE. Which way it leaves depends on whether the
// model happened to call a tool: delivered at the tool boundary into the
// running turn, or, when no boundary comes, opening a turn of its own.
// By both the files are recorded beside the words, the identity reads
// them after the words on the request that first carries them, and the
// next turn reads the same from the record.
func TestAMessageWithFilesThatWaitsOnARunningTurnKeepsItsFiles(t *testing.T) {
	const words = "and this one, which came while you worked"
	want := words + "\n\n[your operator attached a file: uploads/letter.docx]"
	told := func(t *testing.T, reqs []string) {
		t.Helper()
		for _, req := range reqs {
			if strings.Contains(requestText(t, req), want) {
				return
			}
		}
		t.Fatalf("no request of %d carried the words with their file's line %q", len(reqs), want)
	}

	t.Run("delivered at a tool boundary", func(t *testing.T) {
		a, _ := promptCacheApp(t)
		steered := make(chan error, 1)
		obs := fixtureModel(t, a, func(n int, w http.ResponseWriter) {
			if n == 1 {
				ok, err := a.steer(roleOperator, words, nil, dashboard.WithAttachments(t.Context(), attachedPaths[:1]))
				if err == nil && !ok {
					err = fmt.Errorf("the steer was not queued")
				}
				steered <- err
				fmt.Fprint(w, callReply("", toolCall("r1", "ls", `{"path":"."}`)))
				return
			}
			fmt.Fprint(w, textReply("noted"))
		})
		operatorTurn(t, a, "start on the first one")
		if err := <-steered; err != nil {
			t.Fatal(err)
		}
		reqs := obs.snapshot()
		if len(reqs) != 2 {
			t.Fatalf("fixture rounds=%d", len(reqs))
		}
		told(t, reqs[1:])
		if got := attachedNotices(t, a, words); len(got) != 1 || got[0] != attachedPaths[0] {
			t.Fatalf("the notices beside the delivered message are %q, want its one file", got)
		}
		operatorTurn(t, a, "and a later question")
		reqs = obs.snapshot()
		told(t, reqs[len(reqs)-1:])
	})

	t.Run("opening a turn of its own", func(t *testing.T) {
		a, obs := promptCacheApp(t)
		// The words' turn is given the token by the release that kept
		// it for them (releaseTurn) and never takes it itself: held here
		// as that release leaves it.
		<-a.turnGate
		a.runLeftoverSteerTurn([]steerEntry{{role: roleOperator, content: words,
			recordCtx: dashboard.WithAttachments(t.Context(), attachedPaths[:1])}})
		reqs := obs.snapshot()
		if len(reqs) != 1 {
			t.Fatalf("the waiting message opened %d requests, want its one turn", len(reqs))
		}
		told(t, reqs)
		if got := attachedNotices(t, a, words); len(got) != 1 || got[0] != attachedPaths[0] {
			t.Fatalf("the notices beside the waiting message are %q, want its one file", got)
		}
	})
}

// A TURN IS NOT RUN ON HALF OF WHAT WAS SENT. Where the note of an
// attached file cannot be written the record holds the words without the
// file; the message is not answered, and the failure names the file and
// says so. (The fixture is a path the record refuses: bytes that are not
// text. The dashboard refuses such a path at Send; this is the wall
// behind that one.)
func TestAMessageWhoseFileCannotBeRecordedIsNotAnswered(t *testing.T) {
	a, obs := promptCacheApp(t)
	ctx, cancel := context.WithTimeout(dashboard.WithAttachments(context.Background(), []string{"uploads/\xff.txt"}), 60*time.Second)
	defer cancel()
	if err := a.acquireTurn(ctx); err != nil {
		t.Fatal(err)
	}
	_, err := a.observeChat(ctx, "read this", func(kind, name, args string) {})
	if err == nil || !strings.Contains(err.Error(), "was attached could not be recorded") || !strings.Contains(err.Error(), "it was not answered") {
		t.Fatalf("the turn's error is %v, want it to say the file's note could not be recorded and the message was not answered", err)
	}
	if n := len(obs.snapshot()); n != 0 {
		t.Fatalf("the model was asked %d time(s) about a message the record holds half of", n)
	}
}
