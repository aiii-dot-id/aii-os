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

type contScript struct {
	mu   sync.Mutex
	reqs []string
}

func (o *contScript) add(body string) int {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.reqs = append(o.reqs, body)
	return len(o.reqs)
}

func (o *contScript) snapshot() []string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]string(nil), o.reqs...)
}

func contToolResp(calls ...string) string {
	return fmt.Sprintf(`{"id":"r","model":"fake","choices":[{"index":0,"message":{"role":"assistant","content":"","tool_calls":[%s]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}`,
		strings.Join(calls, ","))
}

func contTextResp(text string) string {
	return fmt.Sprintf(`{"id":"r","model":"fake","choices":[{"index":0,"message":{"role":"assistant","content":%q},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}`, text)
}

func contCall(id, name, args string) string {
	aj, _ := json.Marshal(args)
	return fmt.Sprintf(`{"id":%q,"type":"function","function":{"name":%q,"arguments":%s}}`, id, name, string(aj))
}

// .
// .
// .
// .
// .
// .
// .
// .
func jsonStringFrom(body, marker string) string {
	i := strings.Index(body, marker)
	if i < 0 {
		return ""
	}
	for j := i; j < len(body); j++ {
		if body[j] == '\\' {
			j++
			continue
		}
		if body[j] == '"' {
			return body[i:j]
		}
	}
	return body[i:]
}

func TestCappedTurnContinuesInAFreshTurn(t *testing.T) {
	app, dir := dispatchApp(t)
	defer app.Stop()

	if err := writeFileForTest(filepath.Join(dir, "probe.txt"), "leg probe"); err != nil {
		t.Fatal(err)
	}
	readArgs, _ := json.Marshal(map[string]string{"file_path": filepath.Join(dir, "probe.txt")})

	obs := &contScript{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		n := obs.add(string(body))
		w.Header().Set("Content-Type", "application/json")
		switch {
		case n == 1:
			_, _ = io.WriteString(w, contToolResp(
				contCall("c1a", "work", `{"action":"start","description":"leg one"}`),
				contCall("c1b", "work", `{"action":"update","plan":"read the probe","steps":1}`),
			))
		case n <= 5:
			_, _ = io.WriteString(w, contToolResp(contCall(fmt.Sprintf("c%d", n), "read", string(readArgs))))
		case n == 6:
			_, _ = io.WriteString(w, contTextResp("leg one checkpointed"))
		default:
			_, _ = io.WriteString(w, contTextResp("resuming from the checkpoint"))
		}
	}))
	defer srv.Close()

	app.llmSwap.Swap(llm.New(&llm.ClientConfig{Endpoint: srv.URL, Model: "fake", MaxOutputTokens: 4096, Retries: -1}))

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	if err := app.acquireTurn(ctx); err != nil {
		t.Fatalf("acquire turn: %v", err)
	}
	if _, err := app.observeChat(ctx, "start leg one", func(kind, name, args string) {}); err != nil {
		t.Fatalf("turn failed: %v", err)
	}

	// .
	// .
	// .
	deadline := time.Now().Add(30 * time.Second)
	var contReq string
	for time.Now().Before(deadline) {
		for _, b := range obs.snapshot() {
			if strings.Contains(b, "budget checkpoint — continuation 1") {
				contReq = b
			}
		}
		if contReq != "" {
			app.turnMeterMu.Lock()
			chain := app.contChain
			app.turnMeterMu.Unlock()
			if chain == 0 {
				break
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	if contReq == "" {
		t.Fatal("no continuation turn reached the model — the capped turn died at its cap")
	}
	// .
	// .
	// .
	// .
	// .
	// .
	fact := jsonStringFrom(contReq, "budget checkpoint — continuation 1")
	for _, want := range []string{"still active", "declare steps= for this leg", "ended at its declared tool budget", "Resume point: read the probe"} {
		if !strings.Contains(fact, want) {
			t.Errorf("the continuation fact omits %q: %q", want, fact)
		}
	}
	app.turnMeterMu.Lock()
	chain := app.contChain
	app.turnMeterMu.Unlock()
	if chain != 0 {
		t.Errorf("contChain = %d after a natural leg, want 0 — the chain never resets and would exhaust", chain)
	}

	// .
	var rows int
	if err := app.store.DB().QueryRow(`SELECT COUNT(*) FROM turn_metrics`).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows < 2 {
		t.Errorf("turn_metrics holds %d row(s), want >= 2 — the continuation leg left no measured trace", rows)
	}
}

// .
// .
func TestDeliveredSessionDoesNotContinue(t *testing.T) {
	app, dir := dispatchApp(t)
	defer app.Stop()

	if err := writeFileForTest(filepath.Join(dir, "probe.txt"), "leg probe"); err != nil {
		t.Fatal(err)
	}
	readArgs, _ := json.Marshal(map[string]string{"file_path": filepath.Join(dir, "probe.txt")})

	obs := &contScript{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		n := obs.add(string(body))
		w.Header().Set("Content-Type", "application/json")
		switch {
		case n == 1:
			_, _ = io.WriteString(w, contToolResp(
				contCall("c1a", "work", `{"action":"start","description":"delivered leg"}`),
				contCall("c1b", "work", `{"action":"update","plan":"read then deliver","steps":1}`),
			))
		case n <= 4:
			_, _ = io.WriteString(w, contToolResp(contCall(fmt.Sprintf("c%d", n), "read", string(readArgs))))
		case n == 5:
			_, _ = io.WriteString(w, contToolResp(contCall("c5", "work", `{"action":"deliver","result":"served: done before the cap"}`)))
		case n == 6:
			_, _ = io.WriteString(w, contTextResp("delivered and capped"))
		default:
			_, _ = io.WriteString(w, contTextResp("this turn should never have happened"))
		}
	}))
	defer srv.Close()

	app.llmSwap.Swap(llm.New(&llm.ClientConfig{Endpoint: srv.URL, Model: "fake", MaxOutputTokens: 4096, Retries: -1}))

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	if err := app.acquireTurn(ctx); err != nil {
		t.Fatalf("acquire turn: %v", err)
	}
	if _, err := app.observeChat(ctx, "start the delivered leg", func(kind, name, args string) {}); err != nil {
		t.Fatalf("turn failed: %v", err)
	}

	// .
	// .
	time.Sleep(2 * time.Second)
	for _, b := range obs.snapshot() {
		if strings.Contains(b, "budget checkpoint") {
			t.Fatal("a continuation turn fired for a delivered session")
		}
	}
	app.turnMeterMu.Lock()
	chain := app.contChain
	app.turnMeterMu.Unlock()
	if chain != 0 {
		t.Errorf("contChain = %d for a delivered session, want 0", chain)
	}
}

// .
// .
// .
// .
// .
func TestStopCancelsTheInFlightTurn(t *testing.T) {
	app, _ := dispatchApp(t)

	// .
	// .
	// .
	// .
	// .
	hang := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if strings.Contains(string(body), "hang forever") {
			<-r.Context().Done()
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"r","model":"fake","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`)
	}))
	defer hang.Close()
	app.llmSwap.Swap(llm.New(&llm.ClientConfig{Endpoint: hang.URL, Model: "fake", MaxOutputTokens: 64, Retries: -1}))

	turnDone := make(chan error, 1)
	go func() {
		ctx := context.Background()
		if err := app.acquireTurn(ctx); err != nil {
			turnDone <- err
			return
		}
		_, err := app.observeChat(ctx, "hang forever", func(kind, name, args string) {})
		turnDone <- err
	}()

	// .
	deadline := time.Now().Add(5 * time.Second)
	for app.turnCancelRegistered() == false && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}

	stopped := make(chan struct{})
	go func() { app.Stop(); close(stopped) }()
	select {
	case <-stopped:
	case <-time.After(15 * time.Second):
		t.Fatal("Stop sat behind the in-flight turn — the cancel hook did not fire")
	}
	select {
	case err := <-turnDone:
		if err == nil {
			t.Fatal("the hung turn reported success — it can only have been cancelled")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the turn never ended after Stop")
	}
}
