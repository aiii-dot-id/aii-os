package llm

// The Anthropic Messages API dialect (sprint priority 5: a
// multi-provider engine is a wire transform, not an architecture — the
// runtime speaks ONE internal shape, Chat translates at the boundary).
//
// Cache hints (priority 4's tail): the system prompt splits at the
// composer's cache seam (Message.StableLen) into a stable block marked
// cache_control:ephemeral and a volatile block after it — the provider
// caches the byte-stable identity prefix across turns. The last tool
// definition also carries the marker, extending the cached span over
// the (frozen) tool schemas.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// anthropicVersionHeader is the vendor API-version header. Its value
// is operator-settable through a provider entry's
// credential_options as "header_anthropic-version";
// DefaultAnthropicVersion applies only when the operator has not
// set one.
const (
	anthropicVersionHeader  = "anthropic-version"
	DefaultAnthropicVersion = "2023-06-01"
)

type anthContent struct {
	Type         string          `json:"type"`
	Text         string          `json:"text,omitempty"`
	ID           string          `json:"id,omitempty"`
	Name         string          `json:"name,omitempty"`
	Input        json.RawMessage `json:"input,omitempty"`
	ToolUseID    string          `json:"tool_use_id,omitempty"`
	Content      string          `json:"content,omitempty"`
	CacheControl *anthCache      `json:"cache_control,omitempty"`
	// Thinking is a POINTER because the API requires the field to be
	// PRESENT on a thinking block even when its text is empty — which is
	// the vendor default, since display defaults to omitted. A plain
	// string with omitempty dropped it and every replayed turn was
	// rejected: "messages.N.content.0.thinking.thinking: Field required".
	// A non-nil pointer to "" marshals as "" and is not omitted; nil is.
	Thinking  *string `json:"thinking,omitempty"`
	Signature string  `json:"signature,omitempty"`
	Data      string  `json:"data,omitempty"` // redacted_thinking
}

type anthCache struct {
	Type string `json:"type"` // "ephemeral"
}

type anthMessage struct {
	Role    string        `json:"role"` // "user" | "assistant"
	Content []anthContent `json:"content"`
}

type anthTool struct {
	Name         string                 `json:"name"`
	Description  string                 `json:"description,omitempty"`
	InputSchema  map[string]interface{} `json:"input_schema"`
	CacheControl *anthCache             `json:"cache_control,omitempty"`
}

type anthOutputConfig struct {
	Effort string `json:"effort,omitempty"`
}

type anthThinking struct {
	Type string `json:"type"` // "adaptive" (current) | "enabled" (pre-4.6)
	// Display "summarized" returns readable reasoning; omitted (the
	// vendor default) returns blocks whose text is empty. Either way the
	// blocks carry signatures and must be replayed.
	Display string `json:"display,omitempty"`
	// OMITEMPTY IS LOAD-BEARING: adaptive carries no budget, and sending
	// "budget_tokens":0 beside it is a malformed request.
	BudgetTokens int `json:"budget_tokens,omitempty"`
}

type anthRequest struct {
	Model        string            `json:"model"`
	System       []anthContent     `json:"system,omitempty"`
	Messages     []anthMessage     `json:"messages"`
	Tools        []anthTool        `json:"tools,omitempty"`
	MaxTokens    int               `json:"max_tokens"`
	Temperature  *float64          `json:"temperature,omitempty"` // nil = omit (0 is valid)
	TopP         *float64          `json:"top_p,omitempty"`
	Thinking     *anthThinking     `json:"thinking,omitempty"`
	ToolChoice   *anthToolChoice   `json:"tool_choice,omitempty"`
	OutputConfig *anthOutputConfig `json:"output_config,omitempty"`
}

type anthToolChoice struct {
	Type string `json:"type"`
}

type anthResponse struct {
	Content    []anthContent `json:"content"`
	StopReason string        `json:"stop_reason"`
	// POINTER so an absent usage object is nil rather than zeros.
	Usage *anthUsage `json:"usage"`
}

type anthUsage struct {
	InputTokens              int `json:"input_tokens"`
	OutputTokens             int `json:"output_tokens"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
}

// chatAnthropic translates the internal OpenAI-shaped conversation to
// the Anthropic Messages API and back.
func (c *Client) chatAnthropic(ctx context.Context, messages []Message, opts ChatOptions) (*Response, error) {
	tools, thinkingBudget := opts.Tools, opts.ThinkingBudget
	req := anthRequest{Model: c.model, MaxTokens: c.maxOutputTokens, Temperature: c.temperature, TopP: c.topP}
	if opts.RequireTool {
		// "any" = some tool, the model picks which. "tool" would name one
		// and defeat the point: the choice IS the answer we are after.
		req.ToolChoice = &anthToolChoice{Type: "any"}
	}
	systemOnly := len(messages) == 1 && messages[0].Role == "system"
	if req.MaxTokens <= 0 {
		// The API requires max_tokens. In production the resolver has
		// already materialized this, so reaching here means a client
		// built without one — a probe or a test. Same constant either
		// way: a dialect must never invent an allocation the budget on
		// the other side has not reserved.
		req.MaxTokens = DefaultMaxOutputTokens
	}
	// NOTE: c.extra is deliberately NOT applied here — the Messages API
	// is typed; the passthrough is an OpenAI-path affordance.
	// The vendor replaced the thinking parameter and the old shape is now
	// REJECTED: {"type":"enabled","budget_tokens":N} returns 400 on Opus
	// 5, Opus 4.8/4.7, Sonnet 5 and Fable 5 — the entire current family,
	// which is what this dialect actually talks to. Sending it was an
	// outage for any operator who configured a thinking budget. Which
	// shape to send is a vendor fact, so it comes from the registry.
	//
	// Defaulting to adaptive also repairs a second silent case: on Opus
	// 4.8/4.7 an ABSENT thinking parameter runs the model with thinking
	// OFF, so the previous "no budget configured" path was quietly
	// disabling the reasoning the operator assumed they had.
	// THE DEFAULT IS TO SEND NOTHING, and that is a correction.
	//
	// bdc63c7 made adaptive the default because omitting thinking on Opus
	// 4.8/4.7 silently runs them WITHOUT it. True — but a provider entry
	// serves MANY models, and one of them proved the rule wrong live
	// (2026-08-24): claude-haiku-4-5 answers "adaptive thinking is not
	// supported on this model" with a 400, so a single entry cannot carry
	// one thinking shape for every model it can reach.
	//
	// Omitting is the only default that cannot break a model. It is also
	// correct where it matters most: Opus 5 runs adaptive when the
	// parameter is ABSENT. Pre-4.6 models get no thinking, which is what
	// they had before any of this. Only 4.8/4.7 are under-served, and an
	// operator fixes that with one registry line — a degraded model beats
	// an unreachable one.
	switch c.thinkingMode {
	case "adaptive":
		req.Thinking = &anthThinking{Type: "adaptive"}
	case "budget":
		if thinkingBudget > 0 {
			req.Thinking = &anthThinking{Type: "enabled", BudgetTokens: thinkingBudget}
		}
	default: // "" and "off": send nothing, and let the model decide
	}
	// Display is a property of thinking, so it only means anything when
	// thinking is being sent. Omitted (the vendor default) still returns
	// blocks — they carry signatures with empty text — so preservation
	// works either way; "summarized" is what makes the reasoning
	// READABLE, which is the difference between an operator who can see
	// the identity think and one who watches a pause.
	if req.Thinking != nil && c.thinkingDisplay != "" {
		req.Thinking.Display = c.thinkingDisplay
	}
	// Effort had no way onto this path at all: reasoning_effort is the
	// OpenAI-compatible spelling and the Messages API takes it inside
	// output_config, so an operator who set effort on an anthropic entry
	// was configuring nothing. Same operator field, correct wire shape.
	if c.reasoningEffort != "" {
		req.OutputConfig = &anthOutputConfig{Effort: c.reasoningEffort}
	}
	if c.creds != nil && c.oauthBillingText != "" {
		req.System = append(req.System, anthContent{Type: "text", Text: c.oauthBillingText})
	}

	// System: split at the cache seam; stable block carries the marker.
	for _, m := range messages {
		if m.Role != "system" {
			continue
		}
		if systemOnly {
			continue
		}
		if m.StableLen > 0 && m.StableLen < len(m.Content) {
			req.System = append(req.System,
				anthContent{Type: "text", Text: m.Content[:m.StableLen], CacheControl: &anthCache{Type: "ephemeral"}},
				anthContent{Type: "text", Text: m.Content[m.StableLen:]},
			)
		} else {
			req.System = append(req.System, anthContent{Type: "text", Text: m.Content, CacheControl: &anthCache{Type: "ephemeral"}})
		}
	}

	// Tools: schema rename; the LAST tool extends the cached span.
	for i, t := range tools {
		at := anthTool{Name: t.Function.Name, Description: t.Function.Description, InputSchema: t.Function.Parameters}
		if at.InputSchema == nil {
			at.InputSchema = map[string]interface{}{"type": "object"}
		}
		if i == len(tools)-1 {
			at.CacheControl = &anthCache{Type: "ephemeral"}
		}
		req.Tools = append(req.Tools, at)
	}

	// Conversation: assistant tool_calls → tool_use blocks; tool results
	// → user tool_result blocks (consecutive results merge into one user
	// message, as the API requires).
	for _, m := range messages {
		switch m.Role {
		case "system":
			if systemOnly {
				req.Messages = append(req.Messages, anthMessage{
					Role: "user", Content: []anthContent{{Type: "text", Text: m.Content}},
				})
			}
		case "assistant":
			am := anthMessage{Role: "assistant"}
			// Thinking blocks come FIRST — the API requires reasoning to
			// precede the text and tool_use it produced. Replaying them
			// is what keeps a multi-step turn one line of reasoning
			// instead of a fresh derivation after every tool result.
			for _, tb := range m.Thinking {
				if tb.Kind == "redacted_thinking" {
					am.Content = append(am.Content, anthContent{Type: tb.Kind, Data: tb.Data})
					continue
				}
				text := tb.Text
				am.Content = append(am.Content, anthContent{
					Type: "thinking", Thinking: &text, Signature: tb.Signature,
				})
			}
			if m.Content != "" {
				am.Content = append(am.Content, anthContent{Type: "text", Text: m.Content})
			}
			for _, tc := range m.ToolCalls {
				am.Content = append(am.Content, anthContent{
					Type: "tool_use", ID: tc.ID, Name: tc.Function.Name,
					Input: normalizeToolInput(tc.Function.Arguments),
				})
			}
			if len(am.Content) > 0 {
				req.Messages = append(req.Messages, am)
			}
		case "tool":
			block := anthContent{Type: "tool_result", ToolUseID: m.ToolCallID, Content: m.Content}
			if n := len(req.Messages); n > 0 && req.Messages[n-1].Role == "user" && len(req.Messages[n-1].Content) > 0 && req.Messages[n-1].Content[0].Type == "tool_result" {
				req.Messages[n-1].Content = append(req.Messages[n-1].Content, block)
			} else {
				req.Messages = append(req.Messages, anthMessage{Role: "user", Content: []anthContent{block}})
			}
		default: // "user"
			req.Messages = append(req.Messages, anthMessage{Role: "user", Content: []anthContent{{Type: "text", Text: m.Content}}})
		}
	}

	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal anthropic request: %w", err)
	}
	endpoint := strings.TrimSuffix(c.endpoint, "/")
	if endpoint == "" {
		endpoint = "https://api.anthropic.com"
	}
	httpResp, err := c.sendAuthed(ctx, func(ctx context.Context) (*http.Request, error) {
		httpReq, err := http.NewRequestWithContext(ctx, "POST", endpoint+"/v1/messages", bytes.NewReader(body))
		if err != nil {
			return nil, fmt.Errorf("create request: %w", err)
		}
		cred, err := c.credential(ctx)
		if err != nil {
			return nil, fmt.Errorf("credential: %w", err)
		}
		httpReq.Header.Set("Content-Type", "application/json")
		if c.creds != nil {
			// A Claude subscription credential is a BEARER token; the same
			// token sent as x-api-key is rejected 401 (probed 2026-08-20).
			applyAuth(httpReq, cred)
		} else {
			httpReq.Header.Set("x-api-key", cred.Token)
		}
		// The operator owns this value. credential_options carries
		// header_<Name> precisely so a vendor request fact can be
		// corrected from JSON config without a release, and applyAuth
		// has already applied it. Setting it unconditionally here threw
		// that override away silently. Default only when absent.
		if httpReq.Header.Get(anthropicVersionHeader) == "" {
			httpReq.Header.Set(anthropicVersionHeader, DefaultAnthropicVersion)
		}

		return httpReq, nil
	})
	if err != nil {
		return nil, err
	}
	defer httpResp.Body.Close()
	if httpResp.StatusCode != http.StatusOK {
		respBody, truncated, readErr := readBounded(httpResp.Body, 64<<10)
		if readErr != nil {
			return nil, fmt.Errorf("read API error response: %w", readErr)
		}
		if truncated {
			respBody = append(respBody, []byte(" [truncated]")...)
		}
		return nil, fmt.Errorf("API returned %d: %s", httpResp.StatusCode, string(respBody))
	}
	respBody, truncated, err := readBounded(httpResp.Body, maxResponseBytes)
	if err != nil {
		return nil, fmt.Errorf("read response body: %w", err)
	}
	if truncated {
		return nil, fmt.Errorf("API response exceeds %d bytes", maxResponseBytes)
	}

	var ar anthResponse
	if err := json.Unmarshal(respBody, &ar); err != nil {
		return nil, fmt.Errorf("decode anthropic response: %w", err)
	}

	// Map back to the internal shape.
	var msg Message
	msg.Role = "assistant"
	var texts []string
	for _, blk := range ar.Content {
		switch blk.Type {
		case "text":
			texts = append(texts, blk.Text)
		case "thinking", "redacted_thinking":
			text := ""
			if blk.Thinking != nil {
				text = *blk.Thinking
			}
			msg.Thinking = append(msg.Thinking, ThinkingBlock{
				Kind: blk.Type, Text: text, Signature: blk.Signature, Data: blk.Data,
			})
		case "tool_use":
			tc := ToolCall{ID: blk.ID, Type: "function"}
			tc.Function.Name = blk.Name
			tc.Function.Arguments = string(blk.Input)
			msg.ToolCalls = append(msg.ToolCalls, tc)
		}
	}
	msg.Content = strings.Join(texts, "\n")

	// Map the vendor's stop reason onto the OpenAI-compatible vocabulary
	// this package speaks, and PASS THROUGH anything else unchanged. The
	// default arm used to be "stop", which made a refusal and a paused
	// server-tool turn indistinguishable from a completed answer — the
	// caller rendered the vendor's decline as the resident's own words.
	finish := "stop"
	switch ar.StopReason {
	case "tool_use":
		finish = "tool_calls"
	case "max_tokens":
		finish = "length"
	case "", "end_turn", "stop_sequence":
		finish = "stop"
	default:
		finish = ar.StopReason
	}

	return &Response{
		Choices: []Choice{{Message: msg, FinishReason: finish}},
		Usage:   anthUsageOf(ar.Usage),
	}, nil
}

// normalizeToolInput turns the OpenAI-style JSON-string arguments into a
// JSON object for tool_use.input; malformed arguments become an empty
// object rather than a request the API rejects whole.
func normalizeToolInput(arguments string) json.RawMessage {
	trimmed := strings.TrimSpace(arguments)
	if trimmed == "" {
		return json.RawMessage(`{}`)
	}
	if json.Valid([]byte(trimmed)) && strings.HasPrefix(trimmed, "{") {
		return json.RawMessage(trimmed)
	}
	return json.RawMessage(`{}`)
}

// anthUsageOf maps the vendor's usage, or reports it absent. Cache reads
// and cache writes are input the operator paid for, so they belong in
// the prompt total; leaving them out would under-count every cached
// turn, which is most of them.
func anthUsageOf(u *anthUsage) Usage {
	if u == nil {
		return Usage{} // Reported stays false: we do not know
	}
	in := u.InputTokens + u.CacheReadInputTokens + u.CacheCreationInputTokens
	return Usage{
		PromptTokens:     in,
		CompletionTokens: u.OutputTokens,
		TotalTokens:      in + u.OutputTokens,
		// Reads only. Cache CREATION bills at more than fresh input, not
		// less, so folding it in here would understate cost — the error
		// this field exists to stop, pointing the other way.
		CachedPromptTokens: u.CacheReadInputTokens,
		Reported:           true,
	}
}
