package llm

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/logsink"
)

// answering is a client of provider configured for model, served body: a
// stream when the body is one, a whole reply otherwise.
func answering(t *testing.T, provider, model, body string) *Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		if strings.HasPrefix(body, "event:") || strings.HasPrefix(body, "data:") {
			w.Header().Set("Content-Type", "text/event-stream")
		} else {
			w.Header().Set("Content-Type", "application/json")
		}
		io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	return New(&ClientConfig{Endpoint: srv.URL, Provider: provider, APIKey: "fixture-key", Model: model, Retries: -1})
}

const (
	openAIWhole = `{"id":"chatcmpl-1","object":"chat.completion","created":1,"model":"%s","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":5,"completion_tokens":1,"total_tokens":6}}`
	openAIChunk = `data: {"id":"chatcmpl-1","object":"chat.completion.chunk","created":1,"model":"%s","choices":[{"index":0,"delta":{"role":"assistant","content":"ok"},"finish_reason":null}]}

data: {"id":"chatcmpl-1","object":"chat.completion.chunk","created":1,"model":"%s","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}

data: [DONE]

`
	anthropicWhole = `{"id":"msg_1","type":"message","role":"assistant","model":"%s","content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{"input_tokens":5,"output_tokens":1}}`
	anthropicSSE   = `event: message_start
data: {"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","model":"%s","content":[],"stop_reason":null,"usage":{"input_tokens":5,"output_tokens":0}}}

event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"ok"}}

event: content_block_stop
data: {"type":"content_block_stop","index":0}

event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"end_turn","stop_sequence":null},"usage":{"output_tokens":1}}

event: message_stop
data: {"type":"message_stop"}

`
	geminiWhole  = `{"candidates":[{"content":{"parts":[{"text":"ok."}],"role":"model"},"finishReason":"STOP","index":0}],"usageMetadata":{"promptTokenCount":115,"candidatesTokenCount":2,"totalTokenCount":117,"promptTokensDetails":[{"modality":"TEXT","tokenCount":115}],"serviceTier":"standard"},"modelVersion":"%s","responseId":"FTbOatuwPIarsOIP1bP8yAg"}`
	responsesSSE = `event: response.output_item.done
data: {"type":"response.output_item.done","output_index":0,"item":{"type":"message","id":"msg_1","status":"completed","role":"assistant","content":[{"type":"output_text","text":"ok"}]}}

event: response.completed
data: {"type":"response.completed","response":{"id":"resp_1","object":"response","status":"completed","model":"%s","output":[],"usage":{"input_tokens":5,"output_tokens":1,"total_tokens":6}}}

`
)

func shaped(body, model string) string { return strings.ReplaceAll(body, "%s", model) }

// THE RECORD NAMES THE MODEL THAT ANSWERED. Every reply's ModelID is what the identity's acts on it are
// signed with, and it was the configured name whatever the provider said:
// a provider that resolves an alias to a dated version, or falls back to
// another model, left a record naming a model that did not answer. Each
// dialect's own member is read, whole and streamed; where the reply names
// none, the configured model stands.
func TestTheRecordNamesTheModelThatAnswered(t *testing.T) {
	for _, tc := range []struct {
		name, provider, configured, body, want string
	}{
		{"openai-compatible, whole", "openai", "gpt-5.2", shaped(openAIWhole, "gpt-5.2-2026-08-14"), "gpt-5.2-2026-08-14"},
		{"openai-compatible, streamed", "openai", "gpt-5.2", shaped(openAIChunk, "gpt-5.2-2026-08-14"), "gpt-5.2-2026-08-14"},
		{"anthropic, whole", "anthropic", "claude-sonnet-4-5", shaped(anthropicWhole, "claude-sonnet-4-5-20250929"), "claude-sonnet-4-5-20250929"},
		{"anthropic, streamed", "anthropic", "claude-sonnet-4-5", shaped(anthropicSSE, "claude-sonnet-4-5-20250929"), "claude-sonnet-4-5-20250929"},
		{"gemini, whole", "gemini", "gemini-flash-latest", shaped(geminiWhole, "gemini-3.8-flash"), "gemini-3.8-flash"},
		{"gemini, streamed", "gemini", "gemini-flash-latest", "data: " + shaped(geminiWhole, "gemini-3.8-flash") + "\r\n\r\n", "gemini-3.8-flash"},
		{"responses", "chatgpt", "gpt-5.6-sol", shaped(responsesSSE, "gpt-5.6-sol-2026-09-01"), "gpt-5.6-sol-2026-09-01"},

		{"openai-compatible, naming none", "openai", "local-model", strings.ReplaceAll(shaped(openAIWhole, ""), `"model":"",`, ""), "local-model"},
		{"anthropic, naming none", "anthropic", "claude-sonnet-4-5", strings.ReplaceAll(shaped(anthropicSSE, ""), `"model":"",`, ""), "claude-sonnet-4-5"},
		{"gemini, naming none", "gemini", "gemini-3.8-flash", strings.ReplaceAll(shaped(geminiWhole, ""), `"modelVersion":"",`, ""), "gemini-3.8-flash"},
		{"responses, naming none", "chatgpt", "gpt-5.6-sol", strings.ReplaceAll(shaped(responsesSSE, ""), `"model":"",`, ""), "gpt-5.6-sol"},
		{"a member that is not text", "openai", "local-model", strings.Replace(openAIWhole, `"%s"`, `{"name":"x"}`, 1), "local-model"},
	} {
		c := answering(t, tc.provider, tc.configured, tc.body)
		resp, err := c.Chat(context.Background(), []Message{{Role: "user", Content: "Reply with the single word OK."}}, ChatOptions{})
		if err != nil {
			t.Errorf("%s: the reply was refused: %v", tc.name, err)
			continue
		}
		if len(resp.Choices) != 1 || !strings.HasPrefix(resp.Choices[0].Message.Content, "ok") {
			t.Errorf("%s: the reply was not kept: %+v", tc.name, resp.Choices)
		}
		if resp.ModelID != tc.want {
			t.Errorf("%s: the record names %q, want %q", tc.name, resp.ModelID, tc.want)
		}
	}
}

// A keepalive frame is not the generation. oMLX 0.7.0 writes its
// keepalive chunks with the stream's id and the model "keepalive"; the
// chunk that ends the generation names the model that answered, and
// nothing after it is taken.
func TestAKeepaliveFrameDoesNotNameTheModel(t *testing.T) {
	keepalive := `data: {"id":"chatcmpl-1","object":"chat.completion.chunk","created":0,"model":"keepalive","choices":[{"index":0,"delta":{"role":"assistant","content":""},"finish_reason":null}]}` + "\n\n"
	chunks := shaped(openAIChunk, "qwen3-next-80b")
	done := strings.Index(chunks, "data: [DONE]")
	body := keepalive + keepalive + chunks[:done] + keepalive + chunks[done:]
	resp, err := answering(t, "openai", "qwen-alias", body).Chat(context.Background(), []Message{{Role: "user", Content: "hi"}}, ChatOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if resp.ModelID != "qwen3-next-80b" {
		t.Fatalf("the record names %q, want the generation's own model", resp.ModelID)
	}
}

// A name the record cannot carry is not taken. The reported name is
// stamped into the signed payload of every act the reply leads to, so a
// provider's string must not set the size of those records or put control
// characters in them: past the bound, or not printable text, the
// configured model stands and the log says why, once for the provider.
// A name that is taken and differs from the configured one is said once
// for each provider and name, and not again on the next call.
func TestAReportedNameIsBoundedAndSaidOnce(t *testing.T) {
	for name, reported := range map[string]string{
		"past the bound":      strings.Repeat("m", maxReportedModelBytes+1),
		"a control character": "model\nINFO forged log line",
		"an escape":           "model\u001b[2J",
		"invalid UTF-8":       "model\xff",
	} {
		answeredModelSaid.Clear()
		logs := logsink.CaptureForTest(t)
		c := answering(t, "openai", "configured", "")
		got := c.answeredModel(reported)
		if got != "configured" {
			t.Errorf("%s: the record names %q, want the configured model", name, got)
		}
		c.answeredModel(reported)
		if n := strings.Count(logs.String(), "a name the record cannot carry"); n != 1 {
			t.Errorf("%s: said %d times, want once:\n%s", name, n, logs.String())
		}
	}

	answeredModelSaid.Clear()
	logs := logsink.CaptureForTest(t)
	c := answering(t, "anthropic", "claude-sonnet-4-5", shaped(anthropicWhole, "claude-sonnet-4-5-20250929"))
	for range 3 {
		if _, err := c.Chat(context.Background(), []Message{{Role: "user", Content: "hi"}}, ChatOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	const line = `anthropic answered as model "claude-sonnet-4-5-20250929" where "claude-sonnet-4-5" is configured`
	if n := strings.Count(logs.String(), line); n != 1 {
		t.Fatalf("the difference was said %d times over three calls, want once:\n%s", n, logs.String())
	}
	if got := c.answeredModel(strings.Repeat("m", maxReportedModelBytes)); got != strings.Repeat("m", maxReportedModelBytes) {
		t.Fatalf("a name at the bound was not taken: %d bytes", len(got))
	}
	if got := c.answeredModel("claude-sonnet-4-5"); got != "claude-sonnet-4-5" || strings.Contains(logs.String(), `answered as model "claude-sonnet-4-5" where`) {
		t.Fatal("a reply naming the configured model was said as a difference")
	}
}
