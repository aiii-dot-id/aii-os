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
	t.Cleanup(func() { stopApp(t, app) })
	obs := &contScript{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		obs.add(string(body))
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, textReply("noted"))
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
	// A narrow window, so the omission count is in play from turn two.
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
	metric() // the rhythm counter moves between the turns
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
		question := []string{"first question", "second question", "third question"}[i]
		if !strings.HasPrefix(last.Content, question+"\n\n"+conversation.TurnOpen+"\n### Now\n") || !strings.Contains(last.Content, "\n### Rhythm") || !strings.HasSuffix(last.Content, conversation.TurnClose) {
			t.Fatalf("turn %d's current message is not its words closed by the substrate block: %q", i+1, last.Content)
		}
		// The words this turn sent come back next turn as a prefix of what
		// was sent, so the next request reads them from the cache.
		if i > 0 {
			prev := wireMessages(t, reqs[i-1])
			if sent, again := prev[len(prev)-1].Content, msgs[len(msgs)-3].Content; !strings.HasPrefix(sent, again) {
				t.Fatalf("turn %d's copy of the previous question is not a prefix of what that turn sent:\nsent:  %q\nagain: %q", i+1, sent, again)
			}
		}
		for _, m := range msgs[1 : len(msgs)-1] {
			if strings.Contains(m.Content, conversation.TurnOpen) {
				t.Fatalf("turn %d's history kept an earlier turn's block: %q", i+1, m.Content)
			}
		}
	}
	third := wireMessages(t, reqs[2])
	if last := third[len(third)-1].Content; !strings.Contains(last, "older conversation turns are not shown") || !strings.HasPrefix(last, "third question\n\n"+conversation.TurnOpen) {
		t.Fatalf("the third turn's block must declare the omitted history after the words: %q", last)
	}
}

func TestAWorkingStateChangeLeavesTheSystemMessageAlone(t *testing.T) {
	app, obs := promptCacheApp(t)
	if err := app.store.SetStandingState("waiting on the review"); err != nil {
		t.Fatal(err)
	}
	operatorTurn(t, app, "first question")
	if err := app.store.SetStandingState("the review landed; the port is next"); err != nil {
		t.Fatal(err)
	}
	if err := app.store.StartWorkSession(t.Context(), "ws-port", "port the reader"); err != nil {
		t.Fatal(err)
	}
	operatorTurn(t, app, "second question")

	reqs := obs.snapshot()
	if len(reqs) != 2 {
		t.Fatalf("saw %d requests, want 2", len(reqs))
	}
	first, second := wireMessages(t, reqs[0]), wireMessages(t, reqs[1])
	if first[0].Content != second[0].Content {
		t.Fatal("the working state changed and the system message moved with it")
	}
	if strings.Contains(first[0].Content, "What Is Right in Front of You") {
		t.Fatal("the working state is still written into the system message")
	}
	for i, want := range []string{"waiting on the review", "the review landed; the port is next"} {
		msgs := []([]wireMessage){first, second}[i]
		last := msgs[len(msgs)-1].Content
		block := last[strings.Index(last, conversation.TurnOpen):]
		question := []string{"first question", "second question"}[i]
		if !strings.HasPrefix(last, question+"\n\n"+conversation.TurnOpen+"\n# What Is Right in Front of You") || !strings.Contains(block, want) {
			t.Fatalf("turn %d's block does not carry its working state (%q): %q", i+1, want, last)
		}
		for _, m := range msgs[1 : len(msgs)-1] {
			if strings.Contains(m.Content, "What Is Right in Front of You") {
				t.Fatalf("turn %d's history kept an earlier turn's working state: %q", i+1, m.Content)
			}
		}
	}

	if strings.Contains(second[len(second)-1].Content, "### Before you act") {
		t.Fatalf("an active work session still renders the planning brief:\n%s", second[len(second)-1].Content)
	}
}

// An arrival that wakes the identity is recorded as a person's turn, so
// the history already ends with it; the request used to append it again.
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
	if !strings.HasPrefix(msgs[len(msgs)-1].Content, fact) {
		t.Fatalf("the arrival is not the current message: %q", msgs[len(msgs)-1].Content)
	}
}
