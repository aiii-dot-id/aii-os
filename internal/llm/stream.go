package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"
)

// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .

type streamKey struct{}

// .
const (
	streamWithUsage int32 = iota
	streamPlain
	streamOff
)

// .
var errIdle = errors.New("no data within the idle ceiling")

// .
// .
// .
// .
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

// .
// .
// .
func (c *Client) clientFor(ctx context.Context) *http.Client {
	if ctx.Value(streamKey{}) != nil {
		return c.streamHTTPClient()
	}
	return c.httpClient
}

// .
// .
// .
// .
// .
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

// .
// .
// .
func (c *Client) streams() bool { return !c.noStream && c.streamMode.Load() != streamOff }

// .
// .
func (c *Client) stepDown(from int32, refusal error) {
	if !c.streamMode.CompareAndSwap(from, from+1) {
		return
	}
	switch from + 1 {
	case streamPlain:
		log.Printf("LLM: %s refused the stream with usage asked for (%s) — this client streams without stream_options from here on", c.endpoint, clip(refusal.Error(), 200))
	case streamOff:
		log.Printf("LLM: %s refused the streamed completion (%s) — this client's completions run unstreamed under the %s total ceiling from here on", c.endpoint, clip(refusal.Error(), 200), c.idle)
	}
}

// .
// .
func withStreamFields(body []byte, mode int32) ([]byte, error) {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(body, &m); err != nil {
		return nil, err
	}
	m["stream"] = json.RawMessage("true")
	if mode == streamWithUsage {
		m["stream_options"] = json.RawMessage(`{"include_usage":true}`)
	} else {
		delete(m, "stream_options")
	}
	return json.Marshal(m)
}

func isEventStream(contentType string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(contentType)), "text/event-stream")
}

// .
// .
// .
// .
// .
func (c *Client) chatOpenAIStream(ctx context.Context, body []byte) (resp *Response, fallback bool, err error) {
	mode := c.streamMode.Load()
	if mode == streamOff {
		return nil, true, nil
	}
	sbody, err := withStreamFields(body, mode)
	if err != nil {
		return nil, false, fmt.Errorf("stream request: %w", err)
	}
	sctx := context.WithValue(ctx, streamKey{}, true)
	httpResp, err := c.sendAuthed(sctx, func(ctx context.Context) (*http.Request, error) {
		httpReq, err := http.NewRequestWithContext(ctx, "POST",
			strings.TrimSuffix(c.endpoint, "/")+"/chat/completions", bytes.NewReader(sbody))
		if err != nil {
			return nil, fmt.Errorf("create request: %w", err)
		}
		cred, err := c.credential(ctx)
		if err != nil {
			return nil, fmt.Errorf("credential: %w", err)
		}
		httpReq.Header.Set("Content-Type", "application/json")
		httpReq.Header.Set("Accept", "text/event-stream")
		applyAuth(httpReq, cred)
		return httpReq, nil
	})
	if err != nil {
		return nil, false, c.idleVerdict(err)
	}
	defer httpResp.Body.Close()
	if httpResp.StatusCode != http.StatusOK {
		respBody, truncated, readErr := readBounded(httpResp.Body, 64<<10)
		if readErr != nil {
			return nil, false, c.idleVerdict(fmt.Errorf("read API error response: %w", readErr))
		}
		if truncated {
			respBody = append(respBody, []byte(" [truncated]")...)
		}
		apiErr := fmt.Errorf("API returned %d: %s", httpResp.StatusCode, string(respBody))
		if httpResp.StatusCode >= 400 && httpResp.StatusCode < 500 {
			c.stepDown(mode, apiErr)
			return nil, true, apiErr
		}
		return nil, false, apiErr
	}
	if isEventStream(httpResp.Header.Get("Content-Type")) {
		resp, err = readChatStream(httpResp.Body)
	} else {
		// .
		// .
		// .
		resp, err = decodeCompletion(httpResp.Body)
	}
	if err != nil {
		return nil, false, c.idleVerdict(err)
	}
	resp.RequestID = httpResp.Header.Get("x-request-id")
	return resp, false, nil
}

// .
// .
func (c *Client) idleVerdict(err error) error {
	if errors.Is(err, errIdle) {
		return fmt.Errorf("no data from %s for %s (llm.timeout_seconds): the model stopped producing — a local substrate loading a long prompt needs a higher ceiling, a remote one has stalled", c.endpoint, c.idle)
	}
	return err
}

// .
type streamChunk struct {
	ID      string `json:"id"`
	Choices []struct {
		Index int `json:"index"`
		Delta struct {
			Role      string  `json:"role"`
			Content   *string `json:"content"`
			ToolCalls []struct {
				Index    *int   `json:"index"`
				ID       string `json:"id"`
				Type     string `json:"type"`
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"delta"`
		FinishReason *string `json:"finish_reason"`
	} `json:"choices"`
	Usage *Usage `json:"usage"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// .
// .
// .
// .
// .
// .
func readChatStream(r io.Reader) (*Response, error) {
	limited := &io.LimitedReader{R: r, N: maxResponseBytes + 1}
	sc := bufio.NewScanner(limited)
	sc.Buffer(make([]byte, 0, 64<<10), maxResponseBytes)
	var content strings.Builder
	var role, finish, id, provider string
	var calls []ToolCall
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
			for _, tc := range d.ToolCalls {
				pos := -1
				if tc.Index != nil {
					if p, seen := byIndex[*tc.Index]; seen {
						pos = p
					}
				}
				if pos < 0 {
					calls = append(calls, ToolCall{})
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
				call.Function.Arguments += tc.Function.Arguments
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
		if calls[i].Type == "" {
			calls[i].Type = "function"
		}
	}
	if role == "" {
		role = "assistant"
	}
	return &Response{
		ID:      id,
		Usage:   usage,
		Choices: []Choice{{Message: Message{Role: role, Content: content.String(), ToolCalls: calls}, FinishReason: finish}},
	}, nil
}

// .
// .
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
	// .
	if len(response.Choices) > 0 && response.Choices[0].FinishReason == "tool_calls" && len(response.Choices[0].Message.ToolCalls) == 0 {
		return nil, fmt.Errorf("tool_calls finish but 0 parsed. Raw: %s", string(respBody[:min(2000, len(respBody))]))
	}
	return &response, nil
}
