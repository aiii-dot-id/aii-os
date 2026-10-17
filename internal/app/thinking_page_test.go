package app

import (
	"context"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/dashboard"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

func TestThePageReadsAReplysThinkingFromTheRecord(t *testing.T) {
	app, _ := dispatchApp(t)
	t.Cleanup(func() { stopApp(t, app) })
	h := &dashboard.WSHandler{}
	app.wireInteractionHooks(h)
	if h.Thinking == nil {
		t.Fatal("the dashboard is handed no reader of a reply's thinking")
	}
	reply := func(text string, calls []store.ReasoningCall) uint64 {
		t.Helper()
		seq, err := app.store.AddConversationTurnSeq("resident", text)
		if err != nil {
			t.Fatal(err)
		}
		if err := app.store.AddReasoning(seq, calls); err != nil {
			t.Fatal(err)
		}
		return seq
	}
	raw := reply("four", []store.ReasoningCall{{Model: "glm", Text: "two and two make four"}})
	summary := reply("the later date", []store.ReasoningCall{{Model: "claude", Text: "Weighed the two readings.", Account: true}})
	none := reply("noted", nil)

	got, ok, err := h.Thinking(context.Background(), raw)
	if err != nil || !ok || got.Content != "two and two make four" || got.Models != "glm" || got.MayBeSummary || got.CreatedAt == "" {
		t.Fatalf("raw thinking: %+v ok=%v err=%v", got, ok, err)
	}
	got, ok, err = h.Thinking(context.Background(), summary)
	if err != nil || !ok || !got.MayBeSummary || got.Content != "[call 1 · claude · "+store.AccountMark+"]\nWeighed the two readings." {
		t.Fatalf("a provider's version must read as one: %+v ok=%v err=%v", got, ok, err)
	}
	if _, ok, err := h.Thinking(context.Background(), none); err != nil || ok {
		t.Fatalf("a reply with no thinking recorded: ok=%v err=%v", ok, err)
	}
	if _, ok, err := h.Thinking(context.Background(), 987654); err != nil || ok {
		t.Fatalf("a turn that is no reply: ok=%v err=%v", ok, err)
	}
}
