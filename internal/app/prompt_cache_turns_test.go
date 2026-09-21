package app

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/conversation"
	"github.com/aiii-dot-id/aii-os/internal/llm"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

// .
// .
// .
// .
// .
// .

type wireMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

func wireMessages(t *testing.T, body string) []wireMessage {
	t.Helper()
	var req struct {
		Messages []wireMessage `json:"messages"`
	}
	if err := json.Unmarshal([]byte(body), &req); err != nil {
		t.Fatalf("request body: %v", err)
	}
	return req.Messages
}

func promptCacheApp(t *testing.T) (*App, *contScript) {
	t.Helper()
	app, _ := dispatchApp(t)
	t.Cleanup(app.Stop)
	obs := &contScript{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		obs.add(string(body))
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, contTextResp("noted"))
	}))
	t.Cleanup(srv.Close)
	app.llmSwap.Swap(llm.New(&llm.ClientConfig{Endpoint: srv.URL, Model: "fake", MaxOutputTokens: 4096, Retries: -1}))
	return app, obs
}

func operatorTurn(t *testing.T, app *App, msg string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if err := app.acquireTurn(ctx); err != nil {
		t.Fatalf("acquire turn: %v", err)
	}
	if _, err := app.observeChat(ctx, msg, func(kind, name, args string) {}); err != nil {
		t.Fatalf("turn failed: %v", err)
	}
}

func TestConsecutiveTurnsSendTheSameSystemMessage(t *testing.T) {
	app, obs := promptCacheApp(t)
	// .
	app.cfgMu.Lock()
	app.cfg.Prompt.RecentTurns = 2
	app.cfgMu.Unlock()
	metric := func() {
		if err := app.store.InsertTurnMetric(store.TurnMetric{TsMs: time.Now().UTC().UnixMilli(), Calls: 3, ReadOnly: 1, Rounds: 2}); err != nil {
			t.Fatal(err)
		}
	}
	metric()
	operatorTurn(t, app, "first question")
	metric()
	operatorTurn(t, app, "second question")
	metric()
	operatorTurn(t, app, "third question")

	reqs := obs.snapshot()
	if len(reqs) != 3 {
		t.Fatalf("saw %d requests, want 3", len(reqs))
	}
	var first []wireMessage
	for i, body := range reqs {
		msgs := wireMessages(t, body)
		if i == 0 {
			first = msgs
		}
		if msgs[0].Content != first[0].Content {
			t.Fatalf("turn %d sent a different system message than turn 1 — the prefix every provider cache keys on moved", i+1)
		}
		for _, moving := range []string{"### Rhythm", "older conversation turns are not shown", conversation.TurnOpen + "\n"} {
			if strings.Contains(msgs[0].Content, moving) {
				t.Fatalf("turn %d carries %q in the system message", i+1, moving)
			}
		}
		last := msgs[len(msgs)-1]
		if !strings.HasPrefix(last.Content, conversation.TurnOpen+"\n### Rhythm") {
			t.Fatalf("turn %d's current message does not open with the substrate block: %q", i+1, last.Content)
		}
		for _, m := range msgs[1 : len(msgs)-1] {
			if strings.Contains(m.Content, conversation.TurnOpen) {
				t.Fatalf("turn %d's history kept an earlier turn's block: %q", i+1, m.Content)
			}
		}
	}
	third := wireMessages(t, reqs[2])
	if last := third[len(third)-1].Content; !strings.Contains(last, "older conversation turns are not shown") || !strings.HasSuffix(last, "third question") {
		t.Fatalf("the third turn's block must declare the omitted history ahead of the words: %q", last)
	}
}

// .
// .
func TestAWokenArrivalReachesTheModelOnce(t *testing.T) {
	app, obs := promptCacheApp(t)
	fact := "[messages] someone on test:alice wrote: the lighthouse is red"
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if err := app.acquireTurn(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := app.wake(ctx, "participant", fact); err != nil {
		app.releaseTurn()
		t.Fatalf("wake: %v", err)
	}
	app.releaseTurn()
	reqs := obs.snapshot()
	if len(reqs) == 0 {
		t.Fatal("the wake reached no model")
	}
	msgs := wireMessages(t, reqs[0])
	carried := 0
	for _, m := range msgs {
		carried += strings.Count(m.Content, fact)
	}
	if carried != 1 {
		t.Fatalf("the woken arrival reached the model %d times in one request", carried)
	}
	if !strings.HasSuffix(msgs[len(msgs)-1].Content, fact) {
		t.Fatalf("the arrival is not the current message: %q", msgs[len(msgs)-1].Content)
	}
}
