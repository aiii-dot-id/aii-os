package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// .
// .
// .
// .

func sseChunk(t *testing.T, w http.ResponseWriter, chunk string) {
	t.Helper()
	if _, err := fmt.Fprintf(w, "data: %s\n\n", chunk); err != nil {
		t.Logf("write: %v", err)
	}
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
}

func streamedServer(t *testing.T, handler func(w http.ResponseWriter, r *http.Request, body map[string]any)) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		var body map[string]any
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		handler(w, r, body)
	}))
	t.Cleanup(srv.Close)
	return srv, &calls
}

func TestStreamedCompletionOutlastsTheOldCeiling(t *testing.T) {
	var sawStream, sawUsageAsk atomic.Bool
	srv, _ := streamedServer(t, func(w http.ResponseWriter, r *http.Request, body map[string]any) {
		if v, _ := body["stream"].(bool); v {
			sawStream.Store(true)
		}
		if so, _ := body["stream_options"].(map[string]any); so != nil && so["include_usage"] == true {
			sawUsageAsk.Store(true)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		sseChunk(t, w, `{"id":"chatcmpl-1","choices":[{"index":0,"delta":{"role":"assistant","content":""},"finish_reason":null}]}`)
		for _, word := range []string{"a slow", " local", " model", " still", " answers"} {
			time.Sleep(400 * time.Millisecond)
			sseChunk(t, w, fmt.Sprintf(`{"id":"chatcmpl-1","choices":[{"index":0,"delta":{"content":%q},"finish_reason":null}]}`, word))
		}
		sseChunk(t, w, `{"id":"chatcmpl-1","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`)
		sseChunk(t, w, `{"id":"chatcmpl-1","choices":[],"usage":{"prompt_tokens":40,"completion_tokens":5,"total_tokens":45}}`)
		sseChunk(t, w, "[DONE]")
	})
	c := New(&ClientConfig{Endpoint: srv.URL, APIKey: "k", Model: "m", TimeoutSeconds: 1})
	started := time.Now()
	resp, err := c.Chat(context.Background(), []Message{{Role: "user", Content: "hi"}}, ChatOptions{})
	if err != nil {
		t.Fatalf("a generation that keeps producing must outlast the old total ceiling: %v", err)
	}
	if elapsed := time.Since(started); elapsed < 1500*time.Millisecond {
		t.Fatalf("the server generated for 2 s; the call returned after %s", elapsed)
	}
	if !sawStream.Load() || !sawUsageAsk.Load() {
		t.Fatal("the request must ask for the stream and for usage in it")
	}
	choice := resp.Choices[0]
	if choice.Message.Content != "a slow local model still answers" || choice.Message.Role != "assistant" || choice.FinishReason != "stop" {
		t.Fatalf("assembled: %+v", choice)
	}
	if !resp.Usage.Reported || resp.Usage.PromptTokens != 40 || resp.Usage.CompletionTokens != 5 || resp.ID != "chatcmpl-1" {
		t.Fatalf("usage and id from the stream: %+v id=%q", resp.Usage, resp.ID)
	}
}

func TestStreamedCompletionRefusesSilenceAndNamesTheCeiling(t *testing.T) {
	serverSawCancel := make(chan struct{}, 1)
	srv, _ := streamedServer(t, func(w http.ResponseWriter, r *http.Request, body map[string]any) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		sseChunk(t, w, `{"id":"x","choices":[{"index":0,"delta":{"role":"assistant","content":"partial"},"finish_reason":null}]}`)
		select {
		case <-r.Context().Done():
			serverSawCancel <- struct{}{}
		case <-time.After(8 * time.Second):
		}
	})
	c := New(&ClientConfig{Endpoint: srv.URL, APIKey: "k", Model: "m", TimeoutSeconds: 1})
	started := time.Now()
	_, err := c.Chat(context.Background(), []Message{{Role: "user", Content: "hi"}}, ChatOptions{})
	if err == nil {
		t.Fatal("a server that goes quiet for the ceiling must be refused")
	}
	if elapsed := time.Since(started); elapsed > 4*time.Second {
		t.Fatalf("the refusal took %s; the ceiling is 1 s", elapsed)
	}
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

func TestStreamedToolCallsAssembleByIndex(t *testing.T) {
	srv, _ := streamedServer(t, func(w http.ResponseWriter, r *http.Request, body map[string]any) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		sseChunk(t, w, `{"id":"t","choices":[{"index":0,"delta":{"role":"assistant","content":null,"tool_calls":[{"index":0,"id":"call_a","type":"function","function":{"name":"read","arguments":""}}]},"finish_reason":null}]}`)
		sseChunk(t, w, `{"id":"t","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"path\": "}}]},"finish_reason":null}]}`)
		sseChunk(t, w, `{"id":"t","choices":[{"index":0,"delta":{"tool_calls":[{"index":1,"id":"call_b","type":"function","function":{"name":"grep","arguments":"{\"q\":"}}]},"finish_reason":null}]}`)
		sseChunk(t, w, `{"id":"t","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"/tmp/x\"}"}}]},"finish_reason":null}]}`)
		sseChunk(t, w, `{"id":"t","choices":[{"index":0,"delta":{"tool_calls":[{"index":1,"function":{"arguments":"\"cat\"}"}}]},"finish_reason":null}]}`)
		sseChunk(t, w, `{"id":"t","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`)
		sseChunk(t, w, "[DONE]")
	})
	c := New(&ClientConfig{Endpoint: srv.URL, APIKey: "k", Model: "m"})
	resp, err := c.Chat(context.Background(), []Message{{Role: "user", Content: "hi"}}, ChatOptions{})
	if err != nil {
		t.Fatal(err)
	}
	calls := resp.Choices[0].Message.ToolCalls
	if len(calls) != 2 || resp.Choices[0].FinishReason != "tool_calls" {
		t.Fatalf("two calls: %+v finish=%q", calls, resp.Choices[0].FinishReason)
	}
	if calls[0].ID != "call_a" || calls[0].Function.Name != "read" || calls[0].Function.Arguments != `{"path": "/tmp/x"}` || calls[0].Type != "function" {
		t.Fatalf("call 0 assembled across chunks: %+v", calls[0])
	}
	if calls[1].ID != "call_b" || calls[1].Function.Name != "grep" || calls[1].Function.Arguments != `{"q":"cat"}` {
		t.Fatalf("call 1 assembled across chunks: %+v", calls[1])
	}
	actions, _ := ParseResponse(resp)
	if len(actions) != 2 {
		t.Fatalf("the loop parses the assembled calls as before: %+v", actions)
	}
}

func TestStreamRefusedByTheServerStepsDownTheLadderOnce(t *testing.T) {
	// .
	var shapes []string
	srv, calls := streamedServer(t, func(w http.ResponseWriter, r *http.Request, body map[string]any) {
		_, so := body["stream_options"]
		streamed, _ := body["stream"].(bool)
		shapes = append(shapes, fmt.Sprintf("stream=%v options=%v", streamed, so))
		if so {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"message":"unknown parameter"}}`))
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		sseChunk(t, w, `{"id":"x","choices":[{"index":0,"delta":{"role":"assistant","content":"plain stream"},"finish_reason":"stop"}]}`)
		sseChunk(t, w, "[DONE]")
	})
	c := New(&ClientConfig{Endpoint: srv.URL, APIKey: "k", Model: "m"})
	for i := 0; i < 2; i++ {
		resp, err := c.Chat(context.Background(), []Message{{Role: "user", Content: "hi"}}, ChatOptions{})
		if err != nil || resp.Choices[0].Message.Content != "plain stream" {
			t.Fatalf("call %d: %v %+v", i, err, resp)
		}
	}
	if calls.Load() != 3 || strings.Join(shapes, "; ") != "stream=true options=true; stream=true options=false; stream=true options=false" {
		t.Fatalf("one step down, then sticky: %d requests, %v", calls.Load(), shapes)
	}
	// .
	// .
	var shapes2 []string
	srv2, calls2 := streamedServer(t, func(w http.ResponseWriter, r *http.Request, body map[string]any) {
		streamed, _ := body["stream"].(bool)
		_, so := body["stream_options"]
		shapes2 = append(shapes2, fmt.Sprintf("stream=%v options=%v", streamed, so))
		if streamed {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"message":"stream mode is not supported by this endpoint"}}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"whole"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`))
	})
	c2 := New(&ClientConfig{Endpoint: srv2.URL, APIKey: "k", Model: "m"})
	for i := 0; i < 2; i++ {
		resp, err := c2.Chat(context.Background(), []Message{{Role: "user", Content: "hi"}}, ChatOptions{})
		if err != nil || resp.Choices[0].Message.Content != "whole" {
			t.Fatalf("call %d: %v %+v", i, err, resp)
		}
	}
	if calls2.Load() != 4 || shapes2[2] != "stream=false options=false" || shapes2[3] != "stream=false options=false" {
		t.Fatalf("two steps down then whole, sticky: %d requests, %v", calls2.Load(), shapes2)
	}
	// .
	// .
	srv3, calls3 := streamedServer(t, func(w http.ResponseWriter, r *http.Request, body map[string]any) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"message":"model not found"}}`))
	})
	c3 := New(&ClientConfig{Endpoint: srv3.URL, APIKey: "k", Model: "m"})
	if _, err := c3.Chat(context.Background(), []Message{{Role: "user", Content: "hi"}}, ChatOptions{}); err == nil || !strings.Contains(err.Error(), "model not found") || calls3.Load() != 3 {
		t.Fatalf("the caller's 400 comes back from the last rung: %v after %d requests", err, calls3.Load())
	}
	if _, err := c3.Chat(context.Background(), []Message{{Role: "user", Content: "hi"}}, ChatOptions{}); err == nil || calls3.Load() != 4 {
		t.Fatalf("and once walked, the ladder is not walked again: %d requests", calls3.Load())
	}
}

func TestStreamingClientBoundsAnErrorBodyToo(t *testing.T) {
	// .
	// .
	srv, _ := streamedServer(t, func(w http.ResponseWriter, r *http.Request, body map[string]any) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		w.(http.Flusher).Flush()
		select {
		case <-r.Context().Done():
		case <-time.After(8 * time.Second):
		}
	})
	c := New(&ClientConfig{Endpoint: srv.URL, APIKey: "k", Model: "m", TimeoutSeconds: 1, Retries: -1})
	started := time.Now()
	_, err := c.Chat(context.Background(), []Message{{Role: "user", Content: "hi"}}, ChatOptions{})
	if err == nil {
		t.Fatal("a held error body must be refused")
	}
	if elapsed := time.Since(started); elapsed > 4*time.Second {
		t.Fatalf("the refusal took %s under a 1 s ceiling", elapsed)
	}
}

func TestStreamIgnoredByTheServerIsReadWhole(t *testing.T) {
	srv, calls := streamedServer(t, func(w http.ResponseWriter, r *http.Request, body map[string]any) {
		// .
		// .
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		f := w.(http.Flusher)
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"whole`))
		f.Flush()
		for i := 0; i < 3; i++ {
			time.Sleep(500 * time.Millisecond)
			_, _ = w.Write([]byte(" and slow"))
			f.Flush()
		}
		_, _ = w.Write([]byte(`"},"finish_reason":"stop"}]}`))
	})
	c := New(&ClientConfig{Endpoint: srv.URL, APIKey: "k", Model: "m", TimeoutSeconds: 1})
	resp, err := c.Chat(context.Background(), []Message{{Role: "user", Content: "hi"}}, ChatOptions{})
	if err != nil {
		t.Fatalf("a whole answer that keeps arriving is read: %v", err)
	}
	if resp.Choices[0].Message.Content != "whole and slow and slow and slow" || calls.Load() != 1 {
		t.Fatalf("got %+v after %d requests", resp.Choices[0], calls.Load())
	}
}

func TestStreamTruncatedIsNotComplete(t *testing.T) {
	srv, _ := streamedServer(t, func(w http.ResponseWriter, r *http.Request, body map[string]any) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		sseChunk(t, w, `{"id":"x","choices":[{"index":0,"delta":{"role":"assistant","content":"half an"},"finish_reason":null}]}`)
		// .
	})
	c := New(&ClientConfig{Endpoint: srv.URL, APIKey: "k", Model: "m"})
	if _, err := c.Chat(context.Background(), []Message{{Role: "user", Content: "hi"}}, ChatOptions{}); err == nil || !strings.Contains(err.Error(), "truncated, not complete") {
		t.Fatalf("a clean EOF without a terminal is truncation: %v", err)
	}
	// .
	srv2, _ := streamedServer(t, func(w http.ResponseWriter, r *http.Request, body map[string]any) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		sseChunk(t, w, `{"error":{"message":"context length exceeded"}}`)
		sseChunk(t, w, "[DONE]")
	})
	c2 := New(&ClientConfig{Endpoint: srv2.URL, APIKey: "k", Model: "m"})
	if _, err := c2.Chat(context.Background(), []Message{{Role: "user", Content: "hi"}}, ChatOptions{}); err == nil || !strings.Contains(err.Error(), "provider reported: context length exceeded") {
		t.Fatalf("the provider's message is carried: %v", err)
	}
}

func TestStreamUsageAbsentStaysUnreportedAndStreamCanBeDisabled(t *testing.T) {
	srv, _ := streamedServer(t, func(w http.ResponseWriter, r *http.Request, body map[string]any) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		sseChunk(t, w, `{"id":"x","choices":[{"index":0,"delta":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`)
		sseChunk(t, w, "[DONE]")
	})
	c := New(&ClientConfig{Endpoint: srv.URL, APIKey: "k", Model: "m"})
	resp, err := c.Chat(context.Background(), []Message{{Role: "user", Content: "hi"}}, ChatOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Usage.Reported {
		t.Fatal("a stream that never reported usage is not a measured zero")
	}
	// .
	// .
	var streamed atomic.Bool
	srv2, _ := streamedServer(t, func(w http.ResponseWriter, r *http.Request, body map[string]any) {
		if _, present := body["stream"]; present {
			streamed.Store(true)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"whole"},"finish_reason":"stop"}]}`))
	})
	c2 := New(&ClientConfig{Endpoint: srv2.URL, APIKey: "k", Model: "m", NoStream: true})
	if resp, err := c2.Chat(context.Background(), []Message{{Role: "user", Content: "hi"}}, ChatOptions{}); err != nil || resp.Choices[0].Message.Content != "whole" || streamed.Load() {
		t.Fatalf("stream disabled: %v %+v streamed=%v", err, resp, streamed.Load())
	}
}

func TestStreamKeepAliveCommentsAreNotData(t *testing.T) {
	srv, _ := streamedServer(t, func(w http.ResponseWriter, r *http.Request, body map[string]any) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		f := w.(http.Flusher)
		_, _ = fmt.Fprint(w, ": keep-alive\n\n")
		f.Flush()
		time.Sleep(600 * time.Millisecond)
		_, _ = fmt.Fprint(w, ": keep-alive\n\nevent: message\n")
		f.Flush()
		time.Sleep(600 * time.Millisecond)
		sseChunk(t, w, `{"id":"x","choices":[{"index":0,"delta":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`)
		sseChunk(t, w, "[DONE]")
	})
	c := New(&ClientConfig{Endpoint: srv.URL, APIKey: "k", Model: "m", TimeoutSeconds: 1})
	resp, err := c.Chat(context.Background(), []Message{{Role: "user", Content: "hi"}}, ChatOptions{})
	if err != nil || resp.Choices[0].Message.Content != "ok" {
		t.Fatalf("comments keep the watch fed and carry no data: %v %+v", err, resp)
	}
}
