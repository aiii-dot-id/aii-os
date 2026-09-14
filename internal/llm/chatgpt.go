package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
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

type respInputContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type respInputItem struct {
	Raw       json.RawMessage    `json:"-"`
	Type      string             `json:"type,omitempty"`
	Role      string             `json:"role,omitempty"`
	Content   []respInputContent `json:"content,omitempty"`
	CallID    string             `json:"call_id,omitempty"`
	Name      string             `json:"name,omitempty"`
	Arguments string             `json:"arguments,omitempty"`
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
	Output *string `json:"output,omitempty"`
}

type respTool struct {
	Type        string                 `json:"type"`
	Name        string                 `json:"name"`
	Description string                 `json:"description,omitempty"`
	Parameters  map[string]interface{} `json:"parameters,omitempty"`
}

type respRequest struct {
	CacheKey     string          `json:"prompt_cache_key,omitempty"`
	Include      []string        `json:"include,omitempty"`
	Model        string          `json:"model"`
	Input        []respInputItem `json:"input"`
	Instructions string          `json:"instructions,omitempty"`
	Tools        []respTool      `json:"tools,omitempty"`
	// .
	// .
	// .
	Store      bool           `json:"store"`
	Stream     bool           `json:"stream"`
	Reasoning  *respReasoning `json:"reasoning,omitempty"`
	ToolChoice string         `json:"tool_choice,omitempty"`
}

type respReasoning struct {
	Effort string `json:"effort,omitempty"`
	// .
	// .
	// .
	// .
	Summary string `json:"summary,omitempty"`
}

// .
type respOutputItem struct {
	Raw       json.RawMessage    `json:"-"`
	Type      string             `json:"type"`
	Content   []respInputContent `json:"content,omitempty"`
	Name      string             `json:"name,omitempty"`
	Arguments string             `json:"arguments,omitempty"`
	CallID    string             `json:"call_id,omitempty"`
	ID        string             `json:"id,omitempty"`
	Role      string             `json:"role,omitempty"`
	Status    string             `json:"status,omitempty"`
	Phase     string             `json:"phase,omitempty"`
}

type respCompleted struct {
	Type     string `json:"type"`
	Response struct {
		ID               string           `json:"id"`
		Status           string           `json:"status"`
		Output           []respOutputItem `json:"output"`
		IncompleteDetail *struct {
			Reason string `json:"reason"`
		} `json:"incomplete_details"`
		// .
		// .
		Usage *respUsage `json:"usage"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	} `json:"response"`
}

// .
func (c *Client) chatGPTResponses(ctx context.Context, messages []Message, opts ChatOptions) (*Response, error) {
	tools := opts.Tools
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
	policy, err := c.cachePolicy()
	if err != nil {
		return nil, err
	}
	req := respRequest{
		CacheKey: policy.Key, Include: []string{"reasoning.encrypted_content"}, Model: c.model,
		Store:  false,
		Stream: true,
	}
	if opts.DisableTools {
		req.ToolChoice = "none"
	}
	if opts.RequireTool {
		req.ToolChoice = "required"
	}
	// .
	// .
	// .
	// .
	// .
	effort, summary := c.effortPlan(), c.summaryPlan()
	if effort.Sent || summary.Sent {
		req.Reasoning = &respReasoning{Effort: effort.Wire, Summary: summary.Wire}
	}
	var instructions []string
	for _, m := range messages {
		switch m.Role {
		case "system":
			instructions = append(instructions, m.Content)
		case "tool":
			// .
			// .
			out := m.Content
			req.Input = append(req.Input, respInputItem{
				Type: "function_call_output", CallID: m.ToolCallID, Output: &out,
			})
		case "assistant":
			if m.NativeDialect == "chatgpt" && len(m.NativeContent) > 0 {
				var items []json.RawMessage
				if err := json.Unmarshal(m.NativeContent, &items); err != nil {
					return nil, fmt.Errorf("invalid Responses replay: %w", err)
				}
				for _, item := range items {
					req.Input = append(req.Input, respInputItem{Raw: item})
				}
				continue
			}
			if strings.TrimSpace(m.Content) != "" {
				req.Input = append(req.Input, respInputItem{
					Role: "assistant", Content: []respInputContent{{Type: "output_text", Text: m.Content}},
				})
			}
			// .
			// .
			// .
			// .
			// .
			// .
			// .
			for _, tc := range m.ToolCalls {
				req.Input = append(req.Input, respInputItem{
					Type: "function_call", CallID: tc.ID,
					Name: tc.Function.Name, Arguments: tc.Function.Arguments,
				})
			}
		default:
			req.Input = append(req.Input, respInputItem{
				Role: "user", Content: []respInputContent{{Type: "input_text", Text: m.Content}},
			})
		}
	}
	req.Instructions = strings.Join(instructions, "\n\n")
	for _, t := range tools {
		req.Tools = append(req.Tools, respTool{
			Type: "function", Name: t.Function.Name,
			Description: t.Function.Description, Parameters: t.Function.Parameters,
		})
	}
	if len(req.Input) == 0 {
		// .
		// .
		req.Input = append(req.Input, respInputItem{
			Role: "user", Content: []respInputContent{{Type: "input_text", Text: req.Instructions}},
		})
		req.Instructions = ""
	}

	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}
	httpResp, err := c.sendAuthed(ctx, func(ctx context.Context) (*http.Request, error) {
		hr, err := http.NewRequestWithContext(ctx, "POST",
			strings.TrimSuffix(c.endpoint, "/")+"/responses", bytes.NewReader(body))
		if err != nil {
			return nil, fmt.Errorf("create request: %w", err)
		}
		cred, err := c.credential(ctx)
		if err != nil {
			return nil, fmt.Errorf("credential: %w", err)
		}
		hr.Header.Set("Content-Type", "application/json")
		hr.Header.Set("Accept", "text/event-stream")
		applyAuth(hr, cred)
		return hr, nil
	})
	if err != nil {
		return nil, err
	}
	defer httpResp.Body.Close()
	if httpResp.StatusCode != http.StatusOK {
		b, truncated, readErr := readBounded(httpResp.Body, 64<<10)
		if readErr != nil {
			return nil, fmt.Errorf("read API error response: %w", readErr)
		}
		if truncated {
			b = append(b, []byte(" [truncated]")...)
		}
		return nil, fmt.Errorf("API returned %d: %s", httpResp.StatusCode, string(b))
	}
	response, err := readResponsesStream(httpResp.Body)
	if response != nil {
		response.RequestID = httpResp.Header.Get("x-request-id")
	}
	return response, err
}

// .
// .
// .
// .
// .
// .
// .
// .
func readResponsesStream(r io.Reader) (*Response, error) {
	limited := &io.LimitedReader{R: r, N: maxResponseBytes + 1}
	sc := bufio.NewScanner(limited)
	sc.Buffer(make([]byte, 0, 64<<10), maxResponseBytes)

	var items []respOutputItem
	var usage Usage
	var responseID string
	var incomplete, lastErr string
	completed := false

	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "" || payload == "[DONE]" {
			continue
		}
		var probe struct {
			Type string `json:"type"`
		}
		if json.Unmarshal([]byte(payload), &probe) != nil {
			continue
		}
		switch probe.Type {
		case "response.output_item.done":
			var ev struct {
				Item respOutputItem `json:"item"`
			}
			if json.Unmarshal([]byte(payload), &ev) == nil {
				items = append(items, ev.Item)
			}
		case "response.completed", "response.incomplete":
			var ev respCompleted
			if err := json.Unmarshal([]byte(payload), &ev); err != nil {
				return nil, fmt.Errorf("decode completed event: %w", err)
			}
			if len(ev.Response.Output) > 0 {
				items = ev.Response.Output
			}
			responseID = ev.Response.ID
			if u := ev.Response.Usage; u != nil {
				usage = u.normalized()
			}
			if ev.Response.IncompleteDetail != nil {
				incomplete = ev.Response.IncompleteDetail.Reason
			}
			completed = true
		case "response.failed", "error":
			var ev respCompleted
			_ = json.Unmarshal([]byte(payload), &ev)
			if ev.Response.Error != nil && ev.Response.Error.Message != "" {
				lastErr = ev.Response.Error.Message
			} else {
				lastErr = payload
			}
		}
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("read response stream: %w", err)
	}
	if limited.N == 0 {
		return nil, fmt.Errorf("API response stream exceeds %d bytes", maxResponseBytes)
	}
	if lastErr != "" {
		return nil, fmt.Errorf("provider reported: %.400s", lastErr)
	}
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
	if !completed {
		return nil, fmt.Errorf("response stream ended without a terminal event after %d output item(s) — truncated, not complete", len(items))
	}
	response := assembleResponse(items, usage, incomplete)
	response.ID = responseID
	return response, nil
}

func assembleResponse(items []respOutputItem, usage Usage, incomplete string) *Response {
	var text strings.Builder
	msg := Message{Role: "assistant", NativeDialect: "chatgpt"}
	msg.NativeContent, _ = json.Marshal(items)
	if usage.Reported {
		msg.OutputTokens = usage.CompletionTokens
	}
	for _, item := range items {
		switch item.Type {
		case "message":
			for _, ct := range item.Content {
				if ct.Type == "output_text" {
					text.WriteString(ct.Text)
				}
			}
		case "function_call":
			tc := ToolCall{ID: item.CallID, Type: "function"}
			if tc.ID == "" {
				tc.ID = item.ID
			}
			tc.Function.Name = item.Name
			tc.Function.Arguments = item.Arguments
			msg.ToolCalls = append(msg.ToolCalls, tc)
		}
	}
	msg.Content = text.String()
	finish := "stop"
	if len(msg.ToolCalls) > 0 {
		finish = "tool_calls"
	} else if incomplete != "" {
		finish = "length"
	}
	return &Response{Choices: []Choice{{Message: msg, FinishReason: finish}}, Usage: usage}
}

type respUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
	TotalTokens  int `json:"total_tokens"`
	Details      *struct {
		Read  *int `json:"cached_tokens"`
		Write *int `json:"cache_write_tokens"`
	} `json:"input_tokens_details"`
	missing bool
}

func (u *respUsage) UnmarshalJSON(data []byte) error {
	type plain respUsage
	var p plain
	if err := json.Unmarshal(data, &p); err != nil {
		return err
	}
	*u = respUsage(p)
	var fields map[string]*json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	u.missing = fields["input_tokens"] == nil || fields["output_tokens"] == nil || fields["total_tokens"] == nil
	return nil
}
func (u *respUsage) normalized() Usage {
	if u.missing {
		return Usage{Problem: "missing_usage"}
	}
	v := Usage{PromptTokens: u.InputTokens, CompletionTokens: u.OutputTokens, TotalTokens: u.TotalTokens, Reported: true}
	if u.Details != nil {
		if u.Details.Read != nil {
			v.CachedPromptTokens = *u.Details.Read
			v.CacheReadReported = true
		}
		if u.Details.Write != nil {
			v.CacheWriteTokens = *u.Details.Write
			v.CacheWriteReported = true
		}
	}
	v.validate()
	return v
}
func (i *respOutputItem) UnmarshalJSON(data []byte) error {
	type plain respOutputItem
	var p plain
	if err := json.Unmarshal(data, &p); err != nil {
		return err
	}
	*i = respOutputItem(p)
	i.Raw = append(json.RawMessage(nil), data...)
	return nil
}
func (i respOutputItem) MarshalJSON() ([]byte, error) {
	type plain respOutputItem
	if len(i.Raw) > 0 {
		return i.Raw, nil
	}
	return json.Marshal(plain(i))
}
func (i respInputItem) MarshalJSON() ([]byte, error) {
	type plain respInputItem
	if len(i.Raw) > 0 {
		return i.Raw, nil
	}
	return json.Marshal(plain(i))
}
