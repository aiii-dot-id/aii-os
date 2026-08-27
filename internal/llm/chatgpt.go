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

// The ChatGPT subscription dialect.
//
// A ChatGPT Plus/Pro credential is not valid on the OpenAI API platform —
// it carries connector scopes, not inference scopes (probed 2026-08-20:
// 403 "Missing scopes: api.model.read", 429 billing_not_active). Its
// inference surface is the ChatGPT backend, speaking the RESPONSES API,
// and it has two non-negotiable requirements discovered the same way:
// `store` must be false and `stream` must be true. Both were 400s until
// set, then 200 with an SSE body.
//
// Streaming is consumed here and assembled into one Response, so the rest
// of the runtime — which is request/response by design — is unchanged.
// This is a PRIVATE surface: it can change without notice, which is the
// standing cost of supporting a subscription rather than an API key.

type respInputContent struct {
	Type string `json:"type"` // "input_text" | "output_text"
	Text string `json:"text"`
}

type respInputItem struct {
	Type      string             `json:"type,omitempty"` // "" (a message) | "function_call" | "function_call_output"
	Role      string             `json:"role,omitempty"`
	Content   []respInputContent `json:"content,omitempty"`
	CallID    string             `json:"call_id,omitempty"`
	Name      string             `json:"name,omitempty"`      // function_call
	Arguments string             `json:"arguments,omitempty"` // function_call
	Output    string             `json:"output,omitempty"`    // function_call_output
}

type respTool struct {
	Type        string                 `json:"type"` // "function"
	Name        string                 `json:"name"`
	Description string                 `json:"description,omitempty"`
	Parameters  map[string]interface{} `json:"parameters,omitempty"`
}

type respRequest struct {
	Model        string          `json:"model"`
	Input        []respInputItem `json:"input"`
	Instructions string          `json:"instructions,omitempty"`
	Tools        []respTool      `json:"tools,omitempty"`
	// NO MaxOutputTokens: this backend rejects it (see the construction
	// site). The field is absent rather than unset so that adding it back
	// is a deliberate act, not an oversight someone corrects.
	Store      bool           `json:"store"`  // MUST be false
	Stream     bool           `json:"stream"` // MUST be true
	Reasoning  *respReasoning `json:"reasoning,omitempty"`
	ToolChoice string         `json:"tool_choice,omitempty"`
}

type respReasoning struct {
	Effort string `json:"effort"`
}

// respOutputItem is one item of the completed response's output array.
type respOutputItem struct {
	Type      string             `json:"type"` // "message" | "function_call" | "reasoning"
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
		Status           string           `json:"status"`
		Output           []respOutputItem `json:"output"`
		IncompleteDetail *struct {
			Reason string `json:"reason"`
		} `json:"incomplete_details"`
		// POINTER so an absent usage object is nil rather than zeros:
		// unknown spend must never read as free spend.
		Usage *respUsage `json:"usage"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	} `json:"response"`
}

// chatGPTResponses runs one turn against the ChatGPT backend.
func (c *Client) chatGPTResponses(ctx context.Context, messages []Message, opts ChatOptions) (*Response, error) {
	tools := opts.Tools
	// max_output_tokens is NOT SENT on this backend. It rejects the
	// parameter outright — live 400, 2026-08-24: {"detail":"Unsupported
	// parameter: max_output_tokens"} — and a refused parameter refuses
	// the whole request, so the operator was told their working
	// subscription could not do minimal inference.
	//
	// This is a regression I introduced in 13aab5d. Before it the field
	// was zero and omitempty dropped it; materializing the output reserve
	// so every dialect CARRIES the ceiling started sending it here, where
	// it is not accepted. The principle in that commit stands for the two
	// dialects that honour a ceiling; this one does not offer the knob.
	//
	// The consequence is the divergence 13aab5d set out to remove, and it
	// is unavoidable here rather than overlooked: the prompt budget
	// reserves an output allowance this endpoint will not be held to.
	// Stated plainly instead of pretended away.
	req := respRequest{
		Model:  c.model,
		Store:  false, // required
		Stream: true,  // required
	}
	if opts.RequireTool {
		req.ToolChoice = "required"
	}
	if c.reasoningEffort != "" {
		req.Reasoning = &respReasoning{Effort: c.reasoningEffort}
	}
	var instructions []string
	for _, m := range messages {
		switch m.Role {
		case "system":
			instructions = append(instructions, m.Content)
		case "tool":
			req.Input = append(req.Input, respInputItem{
				Type: "function_call_output", CallID: m.ToolCallID, Output: m.Content,
			})
		case "assistant":
			if strings.TrimSpace(m.Content) != "" {
				req.Input = append(req.Input, respInputItem{
					Role: "assistant", Content: []respInputContent{{Type: "output_text", Text: m.Content}},
				})
			}
			// The CALL must be echoed, not only its result. A
			// function_call_output whose function_call is absent from the
			// input is rejected: "No tool call found for function call
			// output with call_id ..." (probed 2026-08-20). An earlier
			// comment here claimed the results carried the calls; they do
			// not, and that broke the second turn of every tool use —
			// which is every non-trivial thing an identity does.
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
		// The dialect requires at least one input item; a system-only turn
		// (the firstboot prompt is exactly this) becomes one user item.
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
	return readResponsesStream(httpResp.Body)
}

// readResponsesStream consumes the SSE body and assembles one Response.
//
// The terminal `response.completed` event on this backend carries usage
// but an EMPTY output array (observed 2026-08-20) — the content arrives
// as `response.output_item.done` events, one per finished item. So items
// are collected as they complete and the terminal event supplies usage
// and the incomplete reason. Where a backend does populate the terminal
// output, that wins, since it is the authoritative final list.
func readResponsesStream(r io.Reader) (*Response, error) {
	limited := &io.LimitedReader{R: r, N: maxResponseBytes + 1}
	sc := bufio.NewScanner(limited)
	sc.Buffer(make([]byte, 0, 64<<10), maxResponseBytes)

	var items []respOutputItem
	var usage Usage
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
			if u := ev.Response.Usage; u != nil {
				usage = Usage{
					PromptTokens:     u.InputTokens,
					CompletionTokens: u.OutputTokens,
					TotalTokens:      u.TotalTokens,
					Reported:         true,
				}
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
	// A CLEAN EOF IS NOT A COMPLETION. The terminal event
	// (response.completed / response.incomplete) is what says the model
	// finished, how much it cost, and whether it was cut short — and it
	// used to be required only when NOTHING had been collected. One
	// output_item.done followed by a dropped connection therefore
	// returned success: actionable text or tool calls, an unknown final
	// status, however many further items never arrived, and usage of
	// zero silently under-reporting the spend.
	//
	// A truncated stream and a backend that never sends a terminal event
	// are INDISTINGUISHABLE here, which is the reason this has to be
	// strict: accepting one accepts the other. Failing is also
	// recoverable in a way that acting on half a response is not — the
	// caller retries.
	if !completed {
		return nil, fmt.Errorf("response stream ended without a terminal event after %d output item(s) — truncated, not complete", len(items))
	}
	return assembleResponse(items, usage, incomplete), nil
}

func assembleResponse(items []respOutputItem, usage Usage, incomplete string) *Response {
	var text strings.Builder
	msg := Message{Role: "assistant"}
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
}
