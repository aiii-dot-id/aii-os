package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/logsink"
)

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
			time.Sleep(400 * time.Millisecond) // five gaps: 2 s of generation under a 1 s ceiling
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
}

func TestStreamRefusedByTheServerStepsDownTheLadderOnce(t *testing.T) {
	// A server that streams but never learned stream_options.
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
	// A server that refuses every stream: two steps down, then whole,
	// and whole from then on.
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
}

// requestShape is what a rung of the ladder asks for.
func requestShape(body map[string]any) string {
	streamed, _ := body["stream"].(bool)
	_, so := body["stream_options"]
	return fmt.Sprintf("stream=%v options=%v", streamed, so)
}

// answer serves the completion in the shape the request asked for.
func answer(t *testing.T, w http.ResponseWriter, body map[string]any, content string) {
	t.Helper()
	if streamed, _ := body["stream"].(bool); !streamed {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"choices":[{"message":{"role":"assistant","content":%q},"finish_reason":"stop"}]}`, content)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.WriteHeader(http.StatusOK)
	sseChunk(t, w, fmt.Sprintf(`{"id":"x","choices":[{"index":0,"delta":{"role":"assistant","content":%q},"finish_reason":"stop"}]}`, content))
	sseChunk(t, w, "[DONE]")
}

// shapeLog records each request's shape for a server whose handler runs
// on the server's goroutines.
type shapeLog struct {
	mu     sync.Mutex
	shapes []string
}

func (l *shapeLog) add(body map[string]any) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.shapes = append(l.shapes, requestShape(body))
	return len(l.shapes)
}

func (l *shapeLog) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return strings.Join(l.shapes, "; ")
}

func TestStreamLadderLeavesRefusalsThatAreNotTheStreamsToTheCaller(t *testing.T) {
	for _, tc := range []struct {
		status int
		body   string
	}{
		{http.StatusUnauthorized, `{"error":{"message":"Incorrect API key provided"}}`},
		{http.StatusForbidden, `{"error":{"message":"not permitted"}}`},
		{http.StatusBadRequest, `{"error":{"code":"invalid_api_key","message":"key revoked"}}`},
		{http.StatusNotFound, `{"error":{"message":"model not found"}}`},
		{http.StatusRequestTimeout, `{"error":{"message":"request timeout"}}`},
		{http.StatusConflict, `{"error":{"message":"conflict"}}`},
		{http.StatusRequestEntityTooLarge, `{"error":{"message":"context length exceeded"}}`},
		{http.StatusTooManyRequests, `{"error":{"message":"rate limited"}}`},
	} {
		t.Run(fmt.Sprint(tc.status), func(t *testing.T) {
			var log shapeLog
			srv, _ := streamedServer(t, func(w http.ResponseWriter, r *http.Request, body map[string]any) {
				if log.add(body) == 1 {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(tc.status)
					_, _ = w.Write([]byte(tc.body))
					return
				}
				answer(t, w, body, "streamed")
			})
			logs := logsink.CaptureForTest(t)
			c := New(&ClientConfig{Endpoint: srv.URL, APIKey: "k", Model: "m", Retries: -1})
			if _, err := c.Chat(context.Background(), []Message{{Role: "user", Content: "hi"}}, ChatOptions{}); err == nil || !strings.Contains(err.Error(), fmt.Sprintf("API returned %d", tc.status)) {
				t.Fatalf("the refusal is the caller's, as the server said it: %v", err)
			}
			resp, err := c.Chat(context.Background(), []Message{{Role: "user", Content: "hi"}}, ChatOptions{})
			if err != nil || resp.Choices[0].Message.Content != "streamed" {
				t.Fatalf("the next call: %v %+v", err, resp)
			}
			if got := log.String(); got != "stream=true options=true; stream=true options=true" {
				t.Fatalf("one request for the refusal, then the stream with usage as before: %s", got)
			}
			if mode := c.streamMode.Load(); mode != streamWithUsage {
				t.Fatalf("the client stepped to rung %d", mode)
			}
			if strings.Contains(logs.String(), "refused the stream") {
				t.Fatalf("a line blames the stream for a %d:\n%s", tc.status, logs.String())
			}
		})
	}
}

// A request the server refuses for a field of its own — tool_choice on a
// server that never learned it, reasoning replayed to one that rejects
// it — draws the same refusal on every rung, and that is the proof it was
// not the stream: the whole read's refusal is the caller's, the client
// is not stepped, and the next request streams with usage.
func TestStreamLadderKeepsStreamingPastARefusalOfAnotherField(t *testing.T) {
	var log shapeLog
	srv, _ := streamedServer(t, func(w http.ResponseWriter, r *http.Request, body map[string]any) {
		log.add(body)
		if _, asked := body["tool_choice"]; asked {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"message":"Unrecognized request argument supplied: tool_choice"}}`))
			return
		}
		answer(t, w, body, "streamed")
	})
	logs := logsink.CaptureForTest(t)
	c := New(&ClientConfig{Endpoint: srv.URL, APIKey: "k", Model: "m"})
	if _, err := c.Chat(context.Background(), []Message{{Role: "user", Content: "hi"}}, ChatOptions{DisableTools: true}); err == nil || !strings.Contains(err.Error(), "API returned 400") || !strings.Contains(err.Error(), "tool_choice") {
		t.Fatalf("the whole read's refusal is the caller's: %v", err)
	}
	if got := log.String(); got != "stream=true options=true; stream=true options=false; stream=false options=false" {
		t.Fatalf("the refused call asks each rung once: %s", got)
	}
	resp, err := c.Chat(context.Background(), []Message{{Role: "user", Content: "hi"}}, ChatOptions{})
	if err != nil || resp.Choices[0].Message.Content != "streamed" {
		t.Fatalf("the next call: %v %+v", err, resp)
	}
	if got := log.String(); !strings.HasSuffix(got, "stream=false options=false; stream=true options=true") {
		t.Fatalf("the next call streams with usage: %s", got)
	}
	if strings.Contains(logs.String(), "refused the stream") {
		t.Fatalf("a line blames the stream for another field's refusal:\n%s", logs.String())
	}
}

// The step needs an ANSWER from the lower rung. A server that refuses
// stream_options and then fails the plain stream for a reason of its own
// (a 500) has not shown that the plain stream works, so the client stays
// where it was and the next call asks again.
func TestStreamLadderStepsOnlyOnALowerRungsAnswer(t *testing.T) {
	var log shapeLog
	srv, _ := streamedServer(t, func(w http.ResponseWriter, r *http.Request, body map[string]any) {
		n := log.add(body)
		if _, so := body["stream_options"]; so {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"message":"unknown parameter"}}`))
			return
		}
		if n == 2 {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"error":{"message":"worker crashed"}}`))
			return
		}
		answer(t, w, body, "plain stream")
	})
	c := New(&ClientConfig{Endpoint: srv.URL, APIKey: "k", Model: "m", Retries: -1})
	if _, err := c.Chat(context.Background(), []Message{{Role: "user", Content: "hi"}}, ChatOptions{}); err == nil || !strings.Contains(err.Error(), "500") {
		t.Fatalf("the lower rung's own failure is the caller's: %v", err)
	}
	if mode := c.streamMode.Load(); mode != streamWithUsage {
		t.Fatalf("a lower rung that did not answer stepped the client to rung %d", mode)
	}
	if resp, err := c.Chat(context.Background(), []Message{{Role: "user", Content: "hi"}}, ChatOptions{}); err != nil || resp.Choices[0].Message.Content != "plain stream" {
		t.Fatalf("the next call: %v %+v", err, resp)
	}
	if got := log.String(); got != "stream=true options=true; stream=true options=false; stream=true options=true; stream=true options=false" {
		t.Fatalf("asked again from the top, stepped on the answer: %s", got)
	}
	if mode := c.streamMode.Load(); mode != streamPlain {
		t.Fatalf("answered without stream_options, the client streams without it: rung %d", mode)
	}
}

// Every status that can refuse a field steps the ladder when the field
// is what was refused: 422 is a validating server's word for a
// stream_options it forbids, 406 a server that cannot answer the
// text/event-stream only the streamed request Accepts. (400 is
// TestStreamRefusedByTheServerStepsDownTheLadderOnce.)
func TestStreamLadderStepsOnEveryStatusThatRefusesAField(t *testing.T) {
	for _, tc := range []struct {
		name    string
		refuses func(r *http.Request, body map[string]any) int
		shapes  string // two calls: the walk to the rung that answers, then that rung alone
	}{
		{"422 stream_options", func(_ *http.Request, body map[string]any) int {
			if _, so := body["stream_options"]; so {
				return http.StatusUnprocessableEntity
			}
			return 0
		}, "stream=true options=true; stream=true options=false; stream=true options=false"},
		{"406 event-stream", func(r *http.Request, _ map[string]any) int {
			if strings.Contains(r.Header.Get("Accept"), "text/event-stream") {
				return http.StatusNotAcceptable
			}
			return 0
		}, "stream=true options=true; stream=true options=false; stream=false options=false; stream=false options=false"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var log shapeLog
			srv, _ := streamedServer(t, func(w http.ResponseWriter, r *http.Request, body map[string]any) {
				log.add(body)
				if status := tc.refuses(r, body); status != 0 {
					w.WriteHeader(status)
					_, _ = w.Write([]byte(`{"detail":"refused"}`))
					return
				}
				answer(t, w, body, "answered")
			})
			c := New(&ClientConfig{Endpoint: srv.URL, APIKey: "k", Model: "m"})
			for i := 0; i < 2; i++ {
				if resp, err := c.Chat(context.Background(), []Message{{Role: "user", Content: "hi"}}, ChatOptions{}); err != nil || resp.Choices[0].Message.Content != "answered" {
					t.Fatalf("call %d: %v %+v", i, err, resp)
				}
			}
			if got := log.String(); got != tc.shapes {
				t.Fatalf("stepped and sticky: %s", got)
			}
		})
	}
}

// Calls racing down the ladder step it once: every call is answered, the
// client lands on the rung that answered and never moves back up, and
// the step is logged once.
func TestStreamLadderStepsOnceUnderConcurrentCalls(t *testing.T) {
	srv, _ := streamedServer(t, func(w http.ResponseWriter, r *http.Request, body map[string]any) {
		if _, so := body["stream_options"]; so {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"message":"unknown parameter"}}`))
			return
		}
		answer(t, w, body, "plain stream")
	})
	logs := logsink.CaptureForTest(t)
	c := New(&ClientConfig{Endpoint: srv.URL, APIKey: "k", Model: "m"})
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			resp, err := c.Chat(context.Background(), []Message{{Role: "user", Content: "hi"}}, ChatOptions{})
			if err == nil && resp.Choices[0].Message.Content != "plain stream" {
				err = fmt.Errorf("answered %q", resp.Choices[0].Message.Content)
			}
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if mode := c.streamMode.Load(); mode != streamPlain {
		t.Fatalf("the client landed on rung %d", mode)
	}
	if n := strings.Count(logs.String(), "stream_options from here on"); n != 1 {
		t.Fatalf("the step is logged once, logged %d times:\n%s", n, logs.String())
	}
}

func TestStreamingClientBoundsAnErrorBodyToo(t *testing.T) {
	// A status with a body that never arrives is refused by the same
	// idle watch, not held open for the turn's life.
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
		// A server that never learned `stream`: a plain completion, and
		// slowly — the idle watch, not a total ceiling, bounds it.
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
		// The connection ends here: no finish reason, no [DONE].
	})
	c := New(&ClientConfig{Endpoint: srv.URL, APIKey: "k", Model: "m"})
	if _, err := c.Chat(context.Background(), []Message{{Role: "user", Content: "hi"}}, ChatOptions{}); err == nil || !strings.Contains(err.Error(), "truncated, not complete") {
		t.Fatalf("a clean EOF without a terminal is truncation: %v", err)
	}
	// A provider error inside the stream is the provider's word.
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
	// llm.stream=false: the request carries no stream field and the
	// answer is read whole under the total ceiling, as before.
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
