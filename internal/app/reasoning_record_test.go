package app

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/llm"
)

// A turn's raw reasoning is recorded beside the reply it produced, through
// the production path end to end: the provider's reasoning_content, the
// loop, the reply seam (App.recordReply), the store. Keyed by the reply's
// own turn, so the identity reaches it with recall source=reasoning
// query="turn:N" from a conversation hit.
func TestAReplysReasoningIsRecordedBesideIt(t *testing.T) {
	app, _ := dispatchApp(t)
	t.Cleanup(func() { stopApp(t, app) })
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"r","object":"chat.completion","model":"fake","choices":[{"index":0,"message":{"role":"assistant","content":"noted","reasoning_content":"The operator wants a short answer; noted will do."},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":2,"total_tokens":12}}`)
	}))
	t.Cleanup(srv.Close)
	app.llmSwap.Swap(llm.New(&llm.ClientConfig{Endpoint: srv.URL, Model: "fake", MaxOutputTokens: 4096, Retries: -1}))

	operatorTurn(t, app, "a short answer, please")

	var seq uint64
	var reply string
	if err := app.store.DB().QueryRow(`SELECT turn_seq, content FROM conversations WHERE role = 'resident' ORDER BY turn_seq DESC LIMIT 1`).Scan(&seq, &reply); err != nil {
		t.Fatalf("no reply recorded: %v", err)
	}
	r, ok, err := app.store.ReasoningOfTurn(seq)
	if err != nil || !ok {
		t.Fatalf("the reply (turn %d, %q) has no reasoning beside it: ok=%v err=%v", seq, reply, ok, err)
	}
	if r.Content != "The operator wants a short answer; noted will do." || !strings.Contains(r.Models, "fake") {
		t.Fatalf("recorded reasoning: %+v", r)
	}
}
