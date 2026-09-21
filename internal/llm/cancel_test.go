package llm

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/logsink"
)

// .
// .
// .
// .
// .
// .
// .
// .
func TestCanceledContextIsNotRetried(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"id":"1","choices":[{"message":{"role":"assistant","content":"hi"}}]}`))
	}))
	defer srv.Close()

	buf := logsink.CaptureForTest(t)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	c := New(&ClientConfig{Endpoint: srv.URL, APIKey: "key", Model: "m", Retries: 4})
	_, err := c.Chat(ctx, []Message{{Role: "user", Content: "hi"}}, ChatOptions{})

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
