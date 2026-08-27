package llm

import (
	"bytes"
	"context"
	"errors"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// A dead context is not a transport flake. The caller has already
// abandoned this request (steering, shutdown, turn ended); retrying it
// can never succeed and the "LLM retry" line lies about the cause.
// The FYI, 2026-08-24 19:08:28: "LLM retry 1/4 in 50ms after: HTTP
// request failed: Post ...: context canceled" — retry noise for an
// intentional cancellation, then "LLM call FAILED (openai glm-5.3):
// context canceled" as if the provider had faulted. The instrument
// must distinguish: abandoned != provider error.
func TestCanceledContextIsNotRetried(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"id":"1","choices":[{"message":{"role":"assistant","content":"hi"}}]}`))
	}))
	defer srv.Close()

	var buf bytes.Buffer
	log.SetOutput(&buf)
	defer func() { log.SetOutput(osStderr) }()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // caller abandons before the call even starts
	c := New(&ClientConfig{Endpoint: srv.URL, APIKey: "key", Model: "m", Retries: 4})
	_, err := c.Chat(ctx, []Message{{Role: "user", Content: "hi"}}, ChatOptions{})
	log.SetOutput(osStderr)

	if !errors.Is(err, context.Canceled) {
		t.Fatalf("want context.Canceled, got %v", err)
	}
	if n := atomic.LoadInt32(&hits); n != 0 {
		t.Fatalf("server must never be contacted, got %d hits", n)
	}
	got := buf.String()
	if strings.Contains(got, "LLM retry") {
		t.Fatalf("cancellation must not be logged as a retry:\n%s", got)
	}
	if strings.Contains(got, "LLM call FAILED") {
		t.Fatalf("abandonment must not be logged as a provider failure:\n%s", got)
	}
}
