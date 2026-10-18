package conversation

import (
	"context"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/llm"
)

const qualifierNote = "[the reply above is incomplete — stopped by the operator]"

// A window that starts at the reply: the leading reply goes, as any
// leading assistant row does, and its note goes with it — the history
// opens on a person's words. Only the reply is counted out; the note is
// not a stored turn.
func TestAnInterruptionNoteNeverOpensTheHistory(t *testing.T) {
	client := &scriptLLM{script: []llm.Response{textResp("answer")}}
	loop := New(client, &fakeTools{}, &fakeDefs{}, nil, nil, Config{})
	history := []llm.Message{
		{Role: "assistant", Content: "the unfinished reply"},
		{Role: "user", Content: qualifierNote, Qualifies: true},
		{Role: "user", Content: "next question"},
		{Role: "assistant", Content: "next answer"},
		{Role: "user", Content: "hello"},
	}
	if _, err := loop.RunTurn(context.Background(), llm.Message{Role: "system", Content: "identity"}, history, 3, ""); err != nil {
		t.Fatal(err)
	}
	req := client.requests[0]
	if req[1].Content != "next question" {
		t.Fatalf("the history opens on %q, not on a person's words", req[1].Content)
	}
	for _, m := range req {
		if strings.Contains(m.Content, qualifierNote) {
			t.Fatalf("the note outlived the reply it qualifies: %q", m.Content)
		}
	}
	if last := req[len(req)-1].Content; !strings.Contains(last, HistoryOmissionNote(4)) {
		t.Fatalf("the block must count the reply and not its note (4 omitted): %q", last)
	}
}

// Under pressure: the oldest exchange ends AFTER the note on its reply,
// and a drop that reaches the reply takes the note with it.
func TestDroppingAReplyDropsItsInterruptionNote(t *testing.T) {
	rows := func() []llm.Message {
		return []llm.Message{
			{Role: "system", Content: "stable"},
			{Role: "user", Content: "first question"},
			{Role: "assistant", Content: "the unfinished reply"},
			{Role: "user", Content: qualifierNote, Qualifies: true},
			{Role: "user", Content: "next question"},
			{Role: "assistant", Content: "next answer"},
			{Role: "user", Content: "hello"},
		}
	}
	msgs := rows()
	st := fitState{current: len(msgs) - 1}
	if end := exchangeEnd(msgs, &st); end != 4 {
		t.Fatalf("the oldest exchange ends at %d; want 4, after the note on its reply", end)
	}
	if end := historyChunkEnd(msgs, &fitState{current: len(msgs) - 1, chunk: 2}); end != 4 {
		t.Fatalf("a chunk ending on the reply ends at %d; want 4, after its note", end)
	}
	dropHistory(&msgs, &st, 3)
	if msgs[1].Content != "next question" {
		t.Fatalf("after the drop the history opens on %q", msgs[1].Content)
	}
	if st.current != 3 || st.omitted != 2 {
		t.Fatalf("current=%d omitted=%d; want 3 and 2 (three rows left the request; the note is not a turn)", st.current, st.omitted)
	}
}
