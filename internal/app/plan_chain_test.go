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

// .
// .
// .
// .
// .
// .
// .
func TestPlanningChainEndToEnd(t *testing.T) {
	app, dir := dispatchApp(t)
	defer app.Stop()

	if err := writeFileForTest(filepath.Join(dir, "probe.txt"), "chain probe contents"); err != nil {
		t.Fatal(err)
	}
	readArgs, _ := json.Marshal(map[string]string{"file_path": filepath.Join(dir, "probe.txt")})

	type seen struct {
		mu          sync.Mutex
		reqs        []string
		firstNudge  int
		firstFanout int
	}
	obs := &seen{}

	toolResp := func(calls ...string) string {
		return fmt.Sprintf(`{"id":"r","model":"fake","choices":[{"index":0,"message":{"role":"assistant","content":"","tool_calls":[%s]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}`,
			strings.Join(calls, ","))
	}
	call := func(id, name, args string) string {
		aj, _ := json.Marshal(args)
		return fmt.Sprintf(`{"id":%q,"type":"function","function":{"name":%q,"arguments":%s}}`, id, name, string(aj))
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		obs.mu.Lock()
		obs.reqs = append(obs.reqs, string(body))
		n := len(obs.reqs)
		if obs.firstNudge == 0 && strings.Contains(string(body), "with no work session") {
			obs.firstNudge = n
		}
		if obs.firstFanout == 0 && strings.Contains(string(body), "independent steps this turn and have spawned none") {
			obs.firstFanout = n
		}
		obs.mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		switch {
		case n <= 8:
			_, _ = io.WriteString(w, toolResp(call(fmt.Sprintf("c%d", n), "read", string(readArgs))))
		case n == 9:
			_, _ = io.WriteString(w, toolResp(
				call("c9a", "work", `{"action":"start","description":"chain probe work"}`),
				call("c9b", "work", `{"action":"update","plan":"chain plan: read three things","steps":3,"independent":2}`),
			))
		case n <= 12:
			_, _ = io.WriteString(w, toolResp(call(fmt.Sprintf("c%d", n), "read", string(readArgs))))
		default:
			_, _ = io.WriteString(w, `{"id":"r","model":"fake","choices":[{"index":0,"message":{"role":"assistant","content":"done"},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}`)
		}
	}))
	defer srv.Close()

	app.llmSwap.Swap(llm.New(&llm.ClientConfig{Endpoint: srv.URL, Model: "fake", MaxOutputTokens: 4096, Retries: -1}))

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	// .
	// .
	// .
	// .
	if err := app.acquireTurn(ctx); err != nil {
		t.Fatalf("acquire turn: %v", err)
	}
	if _, err := app.observeChat(ctx, "chain probe: explore the workspace", func(kind, name, args string) {}); err != nil {
		t.Fatalf("turn failed: %v", err)
	}

	obs.mu.Lock()
	defer obs.mu.Unlock()

	// .
	// .
	// .
	if obs.firstNudge != 9 {
		t.Errorf("plan ask first seen in request %d, want 9 — the ask did not fire where the threshold says", obs.firstNudge)
	}
	if obs.firstFanout != 13 {
		t.Errorf("fan-out ask first seen in request %d, want 13 — declared independence did not reach its consumer", obs.firstFanout)
	}
	if obs.firstNudge > 0 {
		body := obs.reqs[obs.firstNudge-1]
		for _, key := range []string{"steps=", "independent=", "work update plan="} {
			if !strings.Contains(body, key) {
				t.Errorf("the ask omits %q — the machine key the meter parses", key)
			}
		}
	}
	if obs.firstFanout > 0 && !strings.Contains(obs.reqs[obs.firstFanout-1], "declared 2 independent steps") {
		t.Error("the fan-out ask does not echo the resident's own number back")
	}

	// .
	var calls, spawned, predicted, independent int
	if err := app.store.DB().QueryRow(
		`SELECT calls, spawned, predicted, independent FROM turn_metrics ORDER BY ts_ms DESC LIMIT 1`).
		Scan(&calls, &spawned, &predicted, &independent); err != nil {
		t.Fatalf("no turn row was minted: %v", err)
	}
	if predicted != 3 || independent != 2 {
		t.Errorf("measured (predicted=%d, independent=%d), want (3, 2) — the declaration did not survive to the row", predicted, independent)
	}
	if spawned != 0 || calls < 13 {
		t.Errorf("measured (calls=%d, spawned=%d) — the fan-out condition this turn scripted did not hold", calls, spawned)
	}

	// .
	// .
	done, failed, _, err := app.store.ToolEventStats(time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if done < 13 || failed != 0 {
		t.Errorf("tool_events: done=%d failed=%d — the trajectory is incomplete or dirty", done, failed)
	}
	if orphans, _ := app.store.AbandonUnfinishedToolCalls(); len(orphans) != 0 {
		t.Errorf("orphaned started rows after a clean turn: %v", orphans)
	}
}
