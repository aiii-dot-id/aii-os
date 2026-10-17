package app

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/llm"
)

func TestTheWorkDeclarationReachesEveryReader(t *testing.T) {
	app, dir := dispatchApp(t)
	defer stopApp(t, app)
	// The capped turn would otherwise wake a continuation leg on its own
	// schedule; this test reads one turn, then asks a second itself.
	off := false
	app.cfgMu.Lock()
	app.cfg.Agency.TurnContinuation = &off
	app.cfgMu.Unlock()

	if err := writeFileForTest(filepath.Join(dir, "probe.txt"), "chain probe contents"); err != nil {
		t.Fatal(err)
	}
	readArgs, _ := json.Marshal(map[string]string{"file_path": filepath.Join(dir, "probe.txt")})

	type seen struct {
		mu         sync.Mutex
		reqs       []string
		checkpoint int // request # whose messages first carry the checkpoint
	}
	obs := &seen{}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		obs.mu.Lock()
		obs.reqs = append(obs.reqs, string(body))
		n := len(obs.reqs)
		if obs.checkpoint == 0 && strings.Contains(string(body), "Declared budget reached: you estimated 3 remaining calls") {
			obs.checkpoint = n
		}
		obs.mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		switch {
		case n == 1: // the resident declares unasked: a session, then its numbers
			_, _ = io.WriteString(w, callReply("",
				toolCall("c1a", "work", `{"action":"start","description":"chain probe work"}`),
				toolCall("c1b", "work", `{"action":"update","plan":"chain plan: read three things","steps":3,"independent":2}`),
			))
		case n <= 11: // keeps reading through the checkpoint's four grace rounds
			_, _ = io.WriteString(w, callReply("", toolCall(fmt.Sprintf("c%d", n), "read", string(readArgs))))
		default: // the checkpoint's final call, and the second turn
			_, _ = io.WriteString(w, textReply("done"))
		}
	}))
	defer srv.Close()

	app.llmSwap.Swap(llm.New(&llm.ClientConfig{Endpoint: srv.URL, Model: "fake", MaxOutputTokens: 4096, Retries: -1}))

	turn := func(words string) {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()
		// observeChat's contract: the CALLER owns the turn token (the
		// dashboard acquires before observing; observeChat only releases).
		if err := app.acquireTurn(ctx); err != nil {
			t.Fatalf("acquire turn: %v", err)
		}
		if _, err := app.observeChat(ctx, words, func(kind, name, args string) {}); err != nil {
			t.Fatalf("turn failed: %v", err)
		}
	}
	turn("chain probe: explore the workspace")

	obs.mu.Lock()
	firstTurn := len(obs.reqs)
	checkpoint := obs.checkpoint
	obs.mu.Unlock()

	// KEEP THE BUDGET: steps=3 buys six remaining calls, excluding start
	// and the declaration itself. The sixth read runs in round seven;
	// request eight carries the checkpoint, then four grace rounds finish.
	if checkpoint != 8 {
		t.Errorf("the checkpoint first reached request %d, want 8 — the soft cap did not measure remaining calls", checkpoint)
	}
	if firstTurn != 12 {
		t.Errorf("the capped turn made %d requests, want 12 (eleven tool rounds and the checkpoint's final call)", firstTurn)
	}

	// RECORD: the row the rhythm line reads, the declaration with its
	// ordinal (the update was the turn's second call).
	var calls, spawned, predicted, declaredAt, independent int
	if err := app.store.DB().QueryRow(
		`SELECT calls, spawned, predicted, declared_ordinal, independent FROM turn_metrics ORDER BY ts_ms DESC LIMIT 1`).
		Scan(&calls, &spawned, &predicted, &declaredAt, &independent); err != nil {
		t.Fatalf("no turn row was minted: %v", err)
	}
	if predicted != 3 || declaredAt != 2 || independent != 2 {
		t.Errorf("recorded (predicted=%d, declared_at=%d, independent=%d), want (3, 2, 2) — the declaration did not survive to the row", predicted, declaredAt, independent)
	}
	if calls != 12 || spawned != 0 {
		t.Errorf("recorded (calls=%d, spawned=%d), want (12, 0)", calls, spawned)
	}

	// MIRROR: the next turn's rhythm line holds the estimate against what
	// it took.
	turn("and now?")
	obs.mu.Lock()
	defer obs.mu.Unlock()
	if len(obs.reqs) != firstTurn+1 {
		t.Fatalf("the second turn made %d requests, want 1", len(obs.reqs)-firstTurn)
	}
	if mirror := "first estimates totaled 3 remaining tool-call requests; 10 were emitted after those declarations"; !strings.Contains(obs.reqs[firstTurn], mirror) {
		t.Errorf("the next turn's rhythm line does not mirror the declaration (%q)", mirror)
	}

	// NO ASK, anywhere: not the plan ask, the scalar ask, the fan-out ask,
	// the first-act ask, the brief or its mirror.
	for i, body := range obs.reqs {
		for _, ask := range []string{"with no work session", "has no plan", "rounds into this turn",
			"independent steps this turn", "first call that changes something", "Before you act", "Mirror:"} {
			if strings.Contains(body, ask) {
				t.Errorf("request %d carries an ask (%q)", i+1, ask)
			}
		}
	}
}
