package llm

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestAProvidersFailureInsideItsStreamIsTypedInEveryDialect(t *testing.T) {
	const anthStart = "data: {\"type\":\"message_start\",\"message\":{\"id\":\"m\",\"usage\":{\"input_tokens\":1,\"output_tokens\":0}}}\n\n"
	const anthText = "data: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n" +
		"data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"half\"}}\n\n"
	const anthError = "data: {\"type\":\"error\",\"error\":{\"type\":\"overloaded_error\",\"message\":\"Overloaded\"}}\n\n"
	anth := func(r io.Reader) error { _, err := readAnthropicStream(r, nil); return err }
	chat := func(r io.Reader) error { _, err := readChatStream(r, nil); return err }
	responses := func(r io.Reader) error { _, err := readResponsesStream(r, nil); return err }
	gemini := func(r io.Reader) error { _, err := readGeminiStream(r, nil); return err }
	for _, c := range []struct {
		name      string
		read      func(io.Reader) error
		stream    string
		words     string
		generated bool
	}{
		{"anthropic, before any block", anth, anthStart + anthError, "overloaded_error", false},
		{"anthropic, inside a text block", anth, anthStart + anthText + anthError, "overloaded_error", true},
		{"chat completions, first", chat, "data: {\"error\":{\"message\":\"server overloaded\"}}\n\ndata: [DONE]\n\n", "server overloaded", false},
		{"chat completions, after text", chat, "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"half\"}}]}\n\ndata: {\"error\":{\"message\":\"server overloaded\"}}\n\n", "server overloaded", true},
		{"responses, after creation only", responses, "data: {\"type\":\"response.created\"}\n\ndata: {\"type\":\"response.failed\",\"response\":{\"error\":{\"message\":\"server overloaded\"}}}\n\n", "server overloaded", false},
		{"responses, after a delta", responses, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"half\"}\n\ndata: {\"type\":\"response.failed\",\"response\":{\"error\":{\"message\":\"server overloaded\"}}}\n\n", "server overloaded", true},
		{"gemini, first", gemini, sse(`{"error":{"code":503,"message":"The model is overloaded.","status":"UNAVAILABLE"}}`), "The model is overloaded.", false},
		{"gemini, after a part", gemini, sse(`{"candidates":[{"content":{"parts":[{"text":"half"}]}}]}`, `{"error":{"code":503,"message":"The model is overloaded.","status":"UNAVAILABLE"}}`), "The model is overloaded.", true},
	} {
		t.Run(c.name, func(t *testing.T) {
			err := c.read(strings.NewReader(c.stream))
			var failed *StreamFailure
			if !errors.As(err, &failed) {
				t.Fatalf("the provider's failure is not typed: %v", err)
			}
			if !strings.Contains(failed.Text, c.words) || !strings.Contains(err.Error(), c.words) {
				t.Fatalf("the provider's own words are not kept for the log: %q", err)
			}
			if failed.Generated != c.generated {
				t.Fatalf("Generated = %t, want %t: whether the reply had begun decides whether it is asked again", failed.Generated, c.generated)
			}
		})
	}
}

func TestAStreamThatFailedBeforeAnyReplyIsAskedOnceMore(t *testing.T) {
	const start = "data: {\"type\":\"message_start\",\"message\":{\"id\":\"m\",\"usage\":{\"input_tokens\":1,\"output_tokens\":0}}}\n\n"
	const text = "data: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n" +
		"data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"ok\"}}\n\n"
	const finish = "data: {\"type\":\"content_block_stop\",\"index\":0}\n\n" +
		"data: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":1}}\n\n" +
		"data: {\"type\":\"message_stop\"}\n\n"
	const overloaded = "data: {\"type\":\"error\",\"error\":{\"type\":\"overloaded_error\",\"message\":\"Overloaded\"}}\n\n"
	serve := func(t *testing.T, answers ...string) (*Client, *atomic.Int32) {
		var calls atomic.Int32
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			io.Copy(io.Discard, r.Body)
			n := int(calls.Add(1))
			w.Header().Set("Content-Type", "text/event-stream")
			io.WriteString(w, answers[min(n, len(answers))-1])
		}))
		t.Cleanup(srv.Close)
		return New(&ClientConfig{Endpoint: srv.URL, Provider: "anthropic", Model: "fixture", RetryBackoffMS: 1}), &calls
	}
	ask := func(c *Client) (*Response, error) {
		return c.Chat(context.Background(), []Message{{Role: "user", Content: "hello"}}, ChatOptions{})
	}

	t.Run("nothing said: asked again, and answered", func(t *testing.T) {
		c, calls := serve(t, start+overloaded, start+text+finish)
		resp, err := ask(c)
		if err != nil || resp.Choices[0].Message.Content != "ok" {
			t.Fatalf("the second asking's answer is the call's: %v %+v", err, resp)
		}
		if calls.Load() != 2 {
			t.Fatalf("asked %d times, want 2", calls.Load())
		}
		if resp.Usage.UnknownAttempts != 1 {
			t.Fatalf("the failed attempt's cost is not known, and is counted so: %d", resp.Usage.UnknownAttempts)
		}
	})
	t.Run("nothing said twice: the second failure is the call's", func(t *testing.T) {
		c, calls := serve(t, start+overloaded)
		_, err := ask(c)
		var failed *StreamFailure
		if !errors.As(err, &failed) || calls.Load() != 2 {
			t.Fatalf("one retry by default, then the typed failure: %d asked, %v", calls.Load(), err)
		}
	})
	t.Run("some of the reply said: not asked again", func(t *testing.T) {
		c, calls := serve(t, start+text+overloaded, start+text+finish)
		_, err := ask(c)
		var failed *StreamFailure
		if !errors.As(err, &failed) || !failed.Generated || calls.Load() != 1 {
			t.Fatalf("a begun reply is not resubmitted: %d asked, %v", calls.Load(), err)
		}
	})
	t.Run("retries switched off", func(t *testing.T) {
		c, calls := serve(t, start+overloaded, start+text+finish)
		c.retries = normalizeRetries(-1)
		if _, err := ask(c); err == nil || calls.Load() != 1 {
			t.Fatalf("llm.retries=-1 asks once: %d asked, %v", calls.Load(), err)
		}
	})
}
