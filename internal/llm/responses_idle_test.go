package llm

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

// responsesEvent writes one server-sent event of the Responses dialect.
func responsesEvent(t *testing.T, w http.ResponseWriter, event string) {
	t.Helper()
	if _, err := fmt.Fprintf(w, "data: %s\n\n", event); err != nil {
		t.Logf("write: %v", err)
	}
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
}

func TestResponsesStreamOutlastsTheOldCeiling(t *testing.T) {
	srv, _ := streamedServer(t, func(w http.ResponseWriter, r *http.Request, body map[string]any) {
		if !strings.HasSuffix(r.URL.Path, "/responses") {
			t.Errorf("the Responses dialect asked %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		for _, word := range []string{"a long", " answer", " is still", " being", " written"} {
			time.Sleep(400 * time.Millisecond) // five gaps: 2 s of generation under a 1 s ceiling
			responsesEvent(t, w, fmt.Sprintf(`{"type":"response.output_text.delta","delta":%q}`, word))
		}
		responsesEvent(t, w, `{"type":"response.output_item.done","item":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"a long answer is still being written"}]}}`)
		responsesEvent(t, w, `{"type":"response.completed","response":{"id":"resp-1","output":[],"usage":{"input_tokens":40,"output_tokens":7,"total_tokens":47}}}`)
	})
	c := New(&ClientConfig{Endpoint: srv.URL, APIKey: "k", Model: "gpt-x", Provider: "chatgpt", TimeoutSeconds: 1})
	started := time.Now()
	resp, err := c.Chat(context.Background(), []Message{{Role: "user", Content: "hi"}}, ChatOptions{})
	if err != nil {
		t.Fatalf("a generation that keeps producing must outlast the ceiling: %v", err)
	}
	if elapsed := time.Since(started); elapsed < 1500*time.Millisecond {
		t.Fatalf("the server generated for 2 s; the call returned after %s", elapsed)
	}
	if len(resp.Choices) != 1 || resp.Choices[0].Message.Content != "a long answer is still being written" {
		t.Fatalf("assembled: %+v", resp.Choices)
	}
	if !resp.Usage.Reported || resp.Usage.PromptTokens != 40 || resp.Usage.CompletionTokens != 7 || resp.ID != "resp-1" {
		t.Fatalf("usage and id from the stream: %+v id=%q", resp.Usage, resp.ID)
	}
}

func TestResponsesStreamRefusesSilenceAndNamesTheCeiling(t *testing.T) {
	serverSawCancel := make(chan struct{}, 1)
	srv, _ := streamedServer(t, func(w http.ResponseWriter, r *http.Request, body map[string]any) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		responsesEvent(t, w, `{"type":"response.output_text.delta","delta":"partial"}`)
		select {
		case <-r.Context().Done():
			serverSawCancel <- struct{}{}
		case <-time.After(8 * time.Second):
		}
	})
	c := New(&ClientConfig{Endpoint: srv.URL, APIKey: "k", Model: "gpt-x", Provider: "chatgpt", TimeoutSeconds: 1})
	started := time.Now()
	_, err := c.Chat(context.Background(), []Message{{Role: "user", Content: "hi"}}, ChatOptions{})
	if err == nil {
		t.Fatal("a stream that goes quiet for the ceiling must be refused")
	}
	if elapsed := time.Since(started); elapsed > 4*time.Second {
		t.Fatalf("the refusal took %s; the ceiling is 1 s", elapsed)
	}
	// The idle watch's own words, not net/http's for a total timeout: the
	// operator is told which endpoint went quiet and what the number is.
	for _, want := range []string{"no data from " + srv.URL, "1s", "llm.timeout_seconds"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("the refusal names the endpoint and the ceiling; got %q", err)
		}
	}
	select {
	case <-serverSawCancel:
	case <-time.After(3 * time.Second):
		t.Fatal("the stalled request must be cancelled at the server, not left open")
	}
}

// A server that never answers the request at all is refused at the same
// ceiling, by the same words.
func TestResponsesRequestThatIsNeverAnsweredIsRefusedAtTheCeiling(t *testing.T) {
	srv, _ := streamedServer(t, func(w http.ResponseWriter, r *http.Request, body map[string]any) {
		select {
		case <-r.Context().Done():
		case <-time.After(8 * time.Second):
		}
	})
	c := New(&ClientConfig{Endpoint: srv.URL, APIKey: "k", Model: "gpt-x", Provider: "chatgpt", TimeoutSeconds: 1, Retries: -1})
	started := time.Now()
	_, err := c.Chat(context.Background(), []Message{{Role: "user", Content: "hi"}}, ChatOptions{})
	if err == nil {
		t.Fatal("a request no header answers within the ceiling must be refused")
	}
	if elapsed := time.Since(started); elapsed > 4*time.Second {
		t.Fatalf("the refusal took %s; the ceiling is 1 s", elapsed)
	}
}
