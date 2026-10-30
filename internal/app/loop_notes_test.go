package app

import (
	"net/http"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/interaction"
)

func TestTheLoopsNotesAreRecordedBesideTheReply(t *testing.T) {
	a, _ := dispatchApp(t)
	t.Cleanup(func() { stopApp(t, a) })
	obs := fixtureModel(t, a, func(n int, w http.ResponseWriter) {
		if n == 1 {
			_, _ = w.Write([]byte(chatReply("the first half", "length", nil, defaultReplyUsage)))
			return
		}
		_, _ = w.Write([]byte(textReply("the rest")))
	})
	const note = "[This reply was cut off by the model's output limit. Raise the model's max output tokens to see the rest.]"
	ctx := goldenTurnCtx(t)
	if err := a.acquireTurn(ctx); err != nil {
		t.Fatal(err)
	}
	reply, err := a.observeChat(ctx, "tell me everything", func(kind, name, args string) {})
	if err != nil {
		t.Fatal(err)
	}
	if reply != "the first half" {
		t.Fatalf("the reply is not the identity's words alone: %q", reply)
	}
	rows, err := a.store.RecentTurnsIncludingSystem(20)
	if err != nil {
		t.Fatal(err)
	}
	var replyRow, noteRow bool
	for _, r := range rows {
		switch {
		case r.Role == string(interaction.Resident):
			replyRow = r.Content == "the first half"
		case r.Role == string(interaction.System) && r.Content == note:
			noteRow = true
		}
	}
	if !replyRow || !noteRow {
		t.Fatalf("want the reply recorded as said and the note as a substrate row (reply %t, note %t): %+v", replyRow, noteRow, rows)
	}

	operatorTurn(t, a, "and the rest?")
	reqs := obs.snapshot()
	msgs := wireMessages(t, reqs[len(reqs)-1])
	for i, m := range msgs {
		if m.Role == "assistant" && m.Content == "the first half" {
			if i+1 >= len(msgs) || msgs[i+1].Role != "user" || msgs[i+1].Content != note {
				t.Fatalf("the reply is not followed by the loop's note in the substrate's voice: %+v", msgs)
			}
			return
		}
	}
	t.Fatalf("the reply is not sent back as the identity's words alone: %+v", msgs)
}
