package llm

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
	"io"
	"maps"
	"net/http"
	"strings"
	"sync"
	"time"
)

type streamKey struct{}

const (
	streamWithUsage int32 = iota
	streamPlain
	streamOff
)

var errIdle = errors.New("no data within the idle ceiling")

type idleTransport struct {
	rt   http.RoundTripper
	idle time.Duration
}

func (t idleTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := t.rt.RoundTrip(req)
	if err != nil || resp == nil || resp.Body == nil {
		return resp, err
	}
	resp.Body = newIdleBody(resp.Body, t.idle)
	return resp, nil
}

type idleBody struct {
	rc      io.ReadCloser
	idle    time.Duration
	timer   *time.Timer
	mu      sync.Mutex
	expired bool
	closed  bool
}

func newIdleBody(rc io.ReadCloser, idle time.Duration) *idleBody {
	b := &idleBody{rc: rc, idle: idle}
	b.timer = time.AfterFunc(idle, func() {
		b.mu.Lock()
		b.expired = true
		closed := b.closed
		b.closed = true
		b.mu.Unlock()
		if !closed {
			_ = rc.Close()
		}
	})
	return b
}

func (b *idleBody) Read(p []byte) (int, error) {
	n, err := b.rc.Read(p)
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.expired {
		return n, errIdle
	}
	if n > 0 {
		b.timer.Reset(b.idle)
	}
	return n, err
}

func (b *idleBody) Close() error {
	b.timer.Stop()
	b.mu.Lock()
	closed := b.closed
	b.closed = true
	b.mu.Unlock()
	if closed {
		return nil
	}
	return b.rc.Close()
}

func (c *Client) clientFor(ctx context.Context) *http.Client {
	if ctx.Value(streamKey{}) != nil {
		return c.streamHTTPClient()
	}
	return c.httpClient
}

func (c *Client) streamHTTPClient() *http.Client {
	c.streamMu.Lock()
	defer c.streamMu.Unlock()
	base := c.httpClient.Transport
	if c.streamClient != nil && c.streamBase == base {
		return c.streamClient
	}
	rt := base
	if rt == nil {
		rt = http.DefaultTransport
	}
	if t, ok := rt.(*http.Transport); ok {
		t2 := t.Clone()
		t2.ResponseHeaderTimeout = c.idle
		rt = t2
	}
	c.streamClient = &http.Client{
		Transport:     idleTransport{rt: rt, idle: c.idle},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	c.streamBase = base
	return c.streamClient
}

func (c *Client) chatOpenAI(ctx context.Context, body openAIBody) (*Response, error) {
	from := streamOff
	if !c.noStream {
		from = c.streamMode.Load()
	}
	var refusal error
	for mode := from; mode < streamOff; mode++ {
		resp, refused, err := c.chatOpenAIStream(ctx, body, mode)
		if !refused {
			if err == nil && refusal != nil {
				c.stepDown(from, mode, refusal)
			}
			return resp, err
		}
		refusal = err
	}
	whole, err := body.bytes()
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}
	resp, err := c.chatOpenAIWhole(ctx, whole)
	if err == nil && refusal != nil {
		c.stepDown(from, streamOff, refusal)
	}
	return resp, err
}

func streamRefusal(refusal *HTTPError) bool {
	switch refusal.StatusCode {
	case http.StatusBadRequest, http.StatusNotAcceptable, http.StatusUnprocessableEntity:
		if _, names := refusal.refusedToolIndex(DialectOpenAI); names {
			return false
		}
		return !bodyIsAuthFailure([]byte(refusal.Body))
	}
	return false
}

func (c *Client) stepDown(from, to int32, refusal error) {
	if !c.streamMode.CompareAndSwap(from, to) {
		return
	}
	switch to {
	case streamPlain:
		logsink.Warn("llm.refusal", "%s refused the stream with usage asked for (%s) and answered it without — this client streams without stream_options from here on", c.endpoint, clip(refusal.Error(), 200))
	case streamOff:
		logsink.Warn("llm.refusal", "%s refused the streamed completion (%s) and answered it unstreamed — this client's completions run unstreamed under the %s total ceiling from here on", c.endpoint, clip(refusal.Error(), 200), c.idle)
	}
}

func withStreamFields(body openAIBody, mode int32) ([]byte, error) {
	m := maps.Clone(body.members)
	if m == nil {
		m = body.req.members()
	}
	m["stream"] = true
	if mode == streamWithUsage {
		m["stream_options"] = map[string]bool{"include_usage": true}
	} else {
		delete(m, "stream_options")
	}
	return json.Marshal(m)
}

func isEventStream(contentType string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(contentType)), "text/event-stream")
}

func (c *Client) chatOpenAIStream(ctx context.Context, body openAIBody, mode int32) (resp *Response, refused bool, err error) {
	sbody, err := withStreamFields(body, mode)
	if err != nil {
		return nil, false, fmt.Errorf("stream request: %w", err)
	}
	sctx := context.WithValue(ctx, streamKey{}, true)
	httpResp, err := c.post(sctx, strings.TrimSuffix(c.endpoint, "/")+"/chat/completions", sbody, bearerStream)
	if err != nil {
		return nil, false, c.idleVerdict(err)
	}
	defer httpResp.Body.Close()
	if httpResp.StatusCode != http.StatusOK {
		refusal, err := refusalError(httpResp)
		if err != nil {
			return nil, false, c.idleVerdict(err)
		}
		return nil, streamRefusal(refusal), refusal
	}
	if isEventStream(httpResp.Header.Get("Content-Type")) {
		resp, err = readChatStream(httpResp.Body)
	} else {

		resp, err = decodeCompletion(httpResp.Body)
	}
	if err != nil {
		return nil, false, c.idleVerdict(err)
	}
	resp.RequestID = httpResp.Header.Get("x-request-id")
	return resp, false, nil
}

func (c *Client) idleVerdict(err error) error {
	if errors.Is(err, errIdle) {
		return fmt.Errorf("no data from %s for %s (llm.timeout_seconds): the model stopped producing — a local substrate loading a long prompt needs a higher ceiling, a remote one has stalled", c.endpoint, c.idle)
	}
	return err
}

type streamChunk struct {
	ID      string `json:"id"`
	Choices []struct {
		Index int `json:"index"`
		Delta struct {
			Role    string  `json:"role"`
			Content *string `json:"content"`

			ReasoningContent *string         `json:"reasoning_content"`
			Reasoning        json.RawMessage `json:"reasoning"`
			ToolCalls        []struct {
				Index    *int   `json:"index"`
				ID       string `json:"id"`
				Type     string `json:"type"`
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
				ExtraContent json.RawMessage `json:"extra_content"`
			} `json:"tool_calls"`
		} `json:"delta"`
		FinishReason *string `json:"finish_reason"`
	} `json:"choices"`
	Usage *Usage `json:"usage"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

func readChatStream(r io.Reader) (*Response, error) {
	limited := &io.LimitedReader{R: r, N: maxResponseBytes + 1}
	sc := bufio.NewScanner(limited)
	sc.Buffer(make([]byte, 0, 64<<10), maxResponseBytes)
	var content, reasoning, account strings.Builder
	var role, finish, id, provider string
	var calls []ToolCall

	var args []*strings.Builder
	byIndex := map[int]int{}
	var usage Usage
	done := false
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "" {
			continue
		}
		if payload == "[DONE]" {
			done = true
			break
		}
		var ch streamChunk
		if err := json.Unmarshal([]byte(payload), &ch); err != nil {
			return nil, fmt.Errorf("decode stream chunk: %w", err)
		}
		if ch.Error != nil && ch.Error.Message != "" {
			provider = ch.Error.Message
			continue
		}
		if ch.ID != "" {
			id = ch.ID
		}
		if ch.Usage != nil {
			usage = *ch.Usage
		}
		for _, choice := range ch.Choices {
			if choice.Index != 0 {
				continue
			}
			d := choice.Delta
			if d.Role != "" {
				role = d.Role
			}
			if d.Content != nil {
				content.WriteString(*d.Content)
			}
			if d.ReasoningContent != nil {
				reasoning.WriteString(*d.ReasoningContent)
			}
			if text, ok := jsonText(d.Reasoning); ok {
				account.WriteString(text)
			}
			for _, tc := range d.ToolCalls {
				pos := -1
				if tc.Index != nil {
					if p, seen := byIndex[*tc.Index]; seen {
						pos = p
					}
				}
				if pos < 0 {
					calls = append(calls, ToolCall{})
					args = append(args, new(strings.Builder))
					pos = len(calls) - 1
					if tc.Index != nil {
						byIndex[*tc.Index] = pos
					}
				}
				call := &calls[pos]
				if tc.ID != "" {
					call.ID = tc.ID
				}
				if tc.Type != "" {
					call.Type = tc.Type
				}
				if tc.Function.Name != "" && call.Function.Name == "" {
					call.Function.Name = tc.Function.Name
				}
				if len(tc.ExtraContent) > 0 && string(tc.ExtraContent) != "null" {
					call.ExtraContent = tc.ExtraContent
				}
				args[pos].WriteString(tc.Function.Arguments)
			}
			if choice.FinishReason != nil && *choice.FinishReason != "" {
				finish = *choice.FinishReason
			}
		}
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("read response stream: %w", err)
	}
	if limited.N == 0 {
		return nil, fmt.Errorf("API response exceeds %d bytes", maxResponseBytes)
	}
	if provider != "" {
		return nil, fmt.Errorf("provider reported: %.400s", provider)
	}
	if !done && finish == "" {
		return nil, fmt.Errorf("response stream ended without [DONE] or a finish reason after %d bytes of content and %d tool call(s) — truncated, not complete", content.Len(), len(calls))
	}
	if finish == "tool_calls" && len(calls) == 0 {
		return nil, fmt.Errorf("tool_calls finish but 0 parsed from the stream")
	}
	for i := range calls {
		calls[i].Function.Arguments = args[i].String()
		if calls[i].Type == "" {
			calls[i].Type = "function"
		}
	}
	if role == "" {
		role = "assistant"
	}
	return &Response{
		ID:    id,
		Usage: usage,
		Choices: []Choice{{Message: Message{Role: role, Content: content.String(), ToolCalls: calls}, FinishReason: finish,
			Reasoning: reasoning.String(), ReasoningAccount: accountBeside(reasoning.String(), account.String())}},
	}, nil
}

func jsonText(raw json.RawMessage) (string, bool) {
	if len(raw) == 0 {
		return "", false
	}
	var s string
	if json.Unmarshal(raw, &s) != nil {
		return "", false
	}
	return s, true
}

func accountBeside(raw, account string) string {
	if strings.TrimSpace(raw) != "" || strings.TrimSpace(account) == "" {
		return ""
	}
	return account
}

func decodeCompletion(r io.Reader) (*Response, error) {
	respBody, truncated, err := readBounded(r, maxResponseBytes)
	if err != nil {
		return nil, fmt.Errorf("read response body: %w", err)
	}
	if truncated {
		return nil, fmt.Errorf("API response exceeds %d bytes", maxResponseBytes)
	}
	var response Response
	if err := json.Unmarshal(respBody, &response); err != nil {
		return nil, fmt.Errorf("decode response: %w\nbody: %s", err, string(respBody[:min(2000, len(respBody))]))
	}

	var withReasoning struct {
		Choices []struct {
			Message struct {
				ReasoningContent json.RawMessage `json:"reasoning_content"`
				Reasoning        json.RawMessage `json:"reasoning"`
			} `json:"message"`
		} `json:"choices"`
	}
	if json.Unmarshal(respBody, &withReasoning) == nil {
		for i := range response.Choices {
			var text string
			if i < len(withReasoning.Choices) && json.Unmarshal(withReasoning.Choices[i].Message.ReasoningContent, &text) == nil {
				response.Choices[i].Reasoning = text
			}

			if i < len(withReasoning.Choices) {
				if account, ok := jsonText(withReasoning.Choices[i].Message.Reasoning); ok {
					response.Choices[i].ReasoningAccount = accountBeside(response.Choices[i].Reasoning, account)
				}
			}
		}
	}

	for i := range response.Choices {
		for j := range response.Choices[i].Message.ToolCalls {
			if tc := &response.Choices[i].Message.ToolCalls[j]; string(tc.ExtraContent) == "null" {
				tc.ExtraContent = nil
			}
		}
	}

	if len(response.Choices) > 0 && response.Choices[0].FinishReason == "tool_calls" && len(response.Choices[0].Message.ToolCalls) == 0 {
		return nil, fmt.Errorf("tool_calls finish but 0 parsed. Raw: %s", string(respBody[:min(2000, len(respBody))]))
	}
	return &response, nil
}
