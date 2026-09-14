package llm

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"testing"
)

type iterateTransport func(*http.Request) (*http.Response, error)

func (f iterateTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func iterateReply(r *http.Request, body string) *http.Response {
	return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: r}
}

const iterateOK = `{"content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{"input_tokens":10,"output_tokens":2}}`

func TestCacheThinkingAdmission(t *testing.T) {
	tc := ToolCall{ID: "c1", Type: "function"}
	tc.Function.Name = "read"
	tc.Function.Arguments = "{}"
	msgs := []Message{{Role: "system", Content: "stable"}, {Role: "user", Content: "go"}, {Role: "assistant", ToolCalls: []ToolCall{tc}}, {Role: "tool", ToolCallID: "c1", Content: "ok"}}
	ordinary, err := EstimateInputTokens(msgs, nil)
	if err != nil {
		t.Fatal(err)
	}
	msgs[2].Thinking = []ThinkingBlock{{Kind: "thinking", Text: strings.Repeat("reasoning ", 3000), Signature: "synthetic-signature"}}
	withThinking, err := EstimateInputTokens(msgs, nil)
	if err != nil {
		t.Fatal(err)
	}
	c := New(&ClientConfig{Endpoint: "https://review.invalid", Provider: "anthropic", Model: "claude-opus-5", MaxInputTokens: ordinary})
	wireBytes := 0
	c.httpClient = &http.Client{Transport: iterateTransport(func(r *http.Request) (*http.Response, error) {
		b, err := io.ReadAll(r.Body)
		if err != nil {
			return nil, err
		}
		wireBytes = len(b)
		return iterateReply(r, iterateOK), nil
	})}
	_, err = c.Chat(t.Context(), msgs, ChatOptions{})
	var limit *ContextLimitError
	if errors.As(err, &limit) {
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	t.Fatalf("request admitted and dispatched: estimate without thinking=%d, with 30000 thinking bytes=%d, limit=%d, actual wire bytes=%d", ordinary, withThinking, ordinary, wireBytes)
}
func TestCacheAnthropicReplayPreservesBlockOrder(t *testing.T) {
	const fixture = `{"content":[{"type":"thinking","thinking":"","signature":"sig-A"},{"type":"text","text":"Checking."},{"type":"thinking","thinking":"","signature":"sig-B"},{"type":"tool_use","id":"c1","name":"read","input":{}}],"stop_reason":"tool_use","usage":{"input_tokens":10,"output_tokens":10}}`
	c := New(&ClientConfig{Endpoint: "https://review.invalid", Provider: "anthropic", Model: "claude-fable-5-1"})
	var requests []anthRequest
	c.httpClient = &http.Client{Transport: iterateTransport(func(r *http.Request) (*http.Response, error) {
		var req anthRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			return nil, err
		}
		requests = append(requests, req)
		body := iterateOK
		if len(requests) == 1 {
			body = fixture
		}
		return iterateReply(r, body), nil
	})}
	msgs := []Message{{Role: "system", Content: "stable"}, {Role: "user", Content: "go"}}
	opts := ChatOptions{Tools: []ToolDefinition{{Type: "function", Function: ToolFunction{Name: "read", Parameters: map[string]interface{}{"type": "object"}}}}}
	first, err := c.Chat(t.Context(), msgs, opts)
	if err != nil {
		t.Fatal(err)
	}
	msgs = append(msgs, first.Choices[0].Message, Message{Role: "tool", ToolCallID: "c1", Content: "ok"})
	if _, err := c.Chat(t.Context(), msgs, opts); err != nil {
		t.Fatal(err)
	}
	var source anthResponse
	if err := json.Unmarshal([]byte(fixture), &source); err != nil {
		t.Fatal(err)
	}
	sent := requests[1].Messages[1].Content
	types := func(bs []anthContent) string {
		var out []string
		for _, b := range bs {
			out = append(out, b.Type)
		}
		return strings.Join(out, ",")
	}
	if !reflect.DeepEqual(source.Content, sent) {
		t.Fatalf("provider block order=%s; replayed order=%s", types(source.Content), types(sent))
	}
}
func TestCacheNullUsageStaysUnknown(t *testing.T) {
	var response Response
	if err := json.Unmarshal([]byte(`{"usage":null}`), &response); err != nil {
		t.Fatal(err)
	}
	if response.Usage.Reported {
		t.Fatalf("usage:null became reported usage: %+v", response.Usage)
	}
}
func TestCacheEmptySystemIsNotCacheMarked(t *testing.T) {
	c := New(&ClientConfig{Endpoint: "https://review.invalid", Provider: "anthropic", Model: "claude-opus-5"})
	var req anthRequest
	c.httpClient = &http.Client{Transport: iterateTransport(func(r *http.Request) (*http.Response, error) {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			return nil, err
		}
		return iterateReply(r, iterateOK), nil
	})}
	if _, _, err := c.ChatSimple(t.Context(), "", "hello"); err != nil {
		t.Fatal(err)
	}
	for _, b := range req.System {
		if b.Type == "text" && b.Text == "" && b.CacheControl != nil {
			t.Fatal("empty system text is sent with cache_control")
		}
	}
}
func TestCacheConcurrentAnthropicPrefixesAreIndependent(t *testing.T) {
	c := New(&ClientConfig{Endpoint: "https://review.invalid", Provider: "anthropic", Model: "claude-opus-5"})
	stable := strings.Repeat("stable identity ", 1000)
	c.httpClient = &http.Client{Transport: iterateTransport(func(r *http.Request) (*http.Response, error) {
		var req anthRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			return nil, err
		}
		if len(req.System) != 2 || req.System[0].Text != stable || req.System[0].CacheControl == nil {
			return nil, fmt.Errorf("stable prefix corrupted")
		}
		if req.System[1].Text != "\n"+req.Messages[0].Content[0].Text {
			return nil, fmt.Errorf("cross-request suffix contamination")
		}
		return iterateReply(r, iterateOK), nil
	})}
	var wg sync.WaitGroup
	errs := make(chan error, 16)
	ctx := t.Context()
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			suffix := fmt.Sprintf("request-%d", i)
			_, err := c.Chat(ctx, []Message{{Role: "system", Content: stable + "\n" + suffix, StableLen: len(stable)}, {Role: "user", Content: suffix}}, ChatOptions{})
			errs <- err
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Error(err)
		}
	}
	t.Log("16 concurrent calls retained the same stable prefix and their own suffixes")
}
func TestCacheParallelToolBatchKeepsConsecutiveBlocks(t *testing.T) {
	const n = 64
	calls := make([]ToolCall, n)
	for i := range calls {
		calls[i] = ToolCall{ID: fmt.Sprintf("c%d", i), Type: "function"}
		calls[i].Function.Name = "read"
		calls[i].Function.Arguments = "{}"
	}
	msgs := []Message{{Role: "system", Content: "stable"}, {Role: "user", Content: "go"}, {Role: "assistant", ToolCalls: calls}}
	for _, c := range calls {
		msgs = append(msgs, Message{Role: "tool", ToolCallID: c.ID, Content: "ok"})
	}
	c := New(&ClientConfig{Endpoint: "https://review.invalid", Provider: "anthropic", Model: "claude-opus-5"})
	var req anthRequest
	c.httpClient = &http.Client{Transport: iterateTransport(func(r *http.Request) (*http.Response, error) {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			return nil, err
		}
		return iterateReply(r, iterateOK), nil
	})}
	if _, err := c.Chat(t.Context(), msgs, ChatOptions{}); err != nil {
		t.Fatal(err)
	}
	if len(req.Messages) != 3 || len(req.Messages[1].Content) != n || len(req.Messages[2].Content) != n {
		t.Fatal("parallel batch split unexpectedly")
	}
	for _, b := range req.Messages[1].Content {
		if b.Type != "tool_use" {
			t.Fatal("tool_use run interrupted")
		}
	}
	for _, b := range req.Messages[2].Content {
		if b.Type != "tool_result" {
			t.Fatal("tool_result run interrupted")
		}
	}
	t.Log("64 tool uses and 64 results remain two consecutive runs; this is not 128 lookback positions under the current Claude API contract")
}
