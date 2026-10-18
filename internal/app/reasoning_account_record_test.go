package app

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/conversation"
	"github.com/aiii-dot-id/aii-os/internal/llm"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

func TestAProvidersSummaryIsRecordedMarkedAndItsRouteIsNamed(t *testing.T) {
	app, _ := dispatchApp(t)
	t.Cleanup(func() { stopApp(t, app) })
	// Enough earlier turns that some are out of view.
	for i := 0; i < 30; i++ {
		if _, err := app.store.AddConversationTurnSeq("operator", fmt.Sprintf("an earlier question, number %d", i)); err != nil {
			t.Fatal(err)
		}
		if _, err := app.store.AddConversationTurnSeq("resident", fmt.Sprintf("an earlier answer, number %d", i)); err != nil {
			t.Fatal(err)
		}
	}
	const summary = "The operator wants it short. A single word will do."
	var mu sync.Mutex
	var sent []string // the current message of each chat request, as the provider received it
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var req struct {
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		}
		if json.Unmarshal(raw, &req) == nil && len(req.Messages) > 0 {
			mu.Lock()
			sent = append(sent, req.Messages[len(req.Messages)-1].Content)
			mu.Unlock()
		}
		w.Header().Set("Content-Type", "application/json")
		body, _ := json.Marshal(map[string]any{"id": "r", "object": "chat.completion", "model": "fake",
			"choices": []any{map[string]any{"index": 0, "finish_reason": "stop",
				"message": map[string]any{"role": "assistant", "content": "noted", "reasoning": summary}}},
			"usage": map[string]any{"prompt_tokens": 10, "completion_tokens": 5, "total_tokens": 15}})
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	app.llmSwap.Swap(llm.New(&llm.ClientConfig{Endpoint: srv.URL, Model: "fake", MaxOutputTokens: 4096, Retries: -1}))
	current := func(words string) string {
		t.Helper()
		mu.Lock()
		defer mu.Unlock()
		for i := len(sent) - 1; i >= 0; i-- {
			if strings.Contains(sent[i], words) {
				return sent[i]
			}
		}
		t.Fatalf("no request carried %q as its current message", words)
		return ""
	}

	if app.thinkingKept() {
		t.Fatal("no reply's thinking is recorded yet, and the app says some is")
	}
	operatorTurn(t, app, "a short answer, please")

	var seq uint64
	if err := app.store.DB().QueryRow(`SELECT turn_seq FROM conversations WHERE role = 'resident' ORDER BY turn_seq DESC LIMIT 1`).Scan(&seq); err != nil {
		t.Fatalf("no reply recorded: %v", err)
	}
	r, ok, err := app.store.ReasoningOfTurn(seq)
	if err != nil || !ok {
		t.Fatalf("the reply (turn %d) has no reasoning beside it: ok=%v err=%v", seq, ok, err)
	}
	if want := "[call 1 · fake · " + store.AccountMark + "]\n" + summary; r.Content != want {
		t.Fatalf("the provider's summary is not kept under its marked header:\n got %q\nwant %q", r.Content, want)
	}
	if want := "fake (" + store.AccountMark + ")"; r.Models != want {
		t.Fatalf("the model does not carry the mark: %q, want %q", r.Models, want)
	}

	// The first turn began with nothing recorded: its message closed on
	// the turns' route alone.
	first := current("a short answer, please")
	if !strings.Contains(first, "older conversation turns are not shown") {
		t.Fatalf("setup: the first turn must have turns out of view:\n%s", first)
	}
	if strings.Contains(first, conversation.ThinkingRouteNote) {
		t.Fatalf("no thinking was recorded when the first turn began, and its message points at some:\n%s", first)
	}
	if !app.thinkingKept() {
		t.Fatal("a reply's thinking is recorded, and the app says none is")
	}
	// The next turn names the route to it, beside the turns' own.
	operatorTurn(t, app, "and one more, please")
	second := current("and one more, please")
	if !strings.Contains(second, "older conversation turns are not shown") || !strings.Contains(second, conversation.ThinkingRouteNote) {
		t.Fatalf("thinking is recorded and turns are out of view: the message must name both routes:\n%s", second)
	}
}
