package llm

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

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// .
// .
// .
// .
// .
const (
	anthropicVersionHeader  = "anthropic-version"
	DefaultAnthropicVersion = "2023-06-01"
)

type anthContent struct {
	Raw          json.RawMessage `json:"-"`
	Type         string          `json:"type"`
	Text         string          `json:"text,omitempty"`
	ID           string          `json:"id,omitempty"`
	Name         string          `json:"name,omitempty"`
	Input        json.RawMessage `json:"input,omitempty"`
	ToolUseID    string          `json:"tool_use_id,omitempty"`
	Content      string          `json:"content,omitempty"`
	CacheControl *anthCache      `json:"cache_control,omitempty"`
	// .
	// .
	// .
	// .
	// .
	// .
	Thinking  *string `json:"thinking,omitempty"`
	Signature string  `json:"signature,omitempty"`
	Data      string  `json:"data,omitempty"`
}

type anthCache struct {
	Type string `json:"type"`
	TTL  string `json:"ttl,omitempty"`
}

type anthMessage struct {
	Role    string        `json:"role"`
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
	Type string `json:"type"`
	// .
	// .
	// .
	Display string `json:"display,omitempty"`
	// .
	// .
	BudgetTokens int `json:"budget_tokens,omitempty"`
}

type anthRequest struct {
	Diagnostics  *anthDiagnostics  `json:"diagnostics,omitempty"`
	Model        string            `json:"model"`
	System       []anthContent     `json:"system,omitempty"`
	Messages     []anthMessage     `json:"messages"`
	Tools        []anthTool        `json:"tools,omitempty"`
	MaxTokens    int               `json:"max_tokens"`
	Temperature  *float64          `json:"temperature,omitempty"`
	TopP         *float64          `json:"top_p,omitempty"`
	Thinking     *anthThinking     `json:"thinking,omitempty"`
	ToolChoice   *anthToolChoice   `json:"tool_choice,omitempty"`
	OutputConfig *anthOutputConfig `json:"output_config,omitempty"`
}

type anthDiagnostics struct {
	PreviousMessageID *string `json:"previous_message_id"`
}

type anthToolChoice struct {
	Type string `json:"type"`
}

type anthResponse struct {
	ID          string          `json:"id"`
	Diagnostics json.RawMessage `json:"diagnostics"`
	Content     []anthContent   `json:"content"`
	StopReason  string          `json:"stop_reason"`
	// .
	Usage *anthUsage `json:"usage"`
}

type anthUsage struct {
	missing       bool
	readReported  bool
	writeReported bool
	CacheCreation struct {
		FiveMinutes int `json:"ephemeral_5m_input_tokens"`
		OneHour     int `json:"ephemeral_1h_input_tokens"`
	} `json:"cache_creation"`
	InputTokens              int `json:"input_tokens"`
	OutputTokens             int `json:"output_tokens"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
}

// .
// .
func (c *Client) chatAnthropic(ctx context.Context, messages []Message, opts ChatOptions) (*Response, error) {
	policy, err := c.cachePolicy()
	if err != nil {
		return nil, err
	}
	slots := 4
	marker := func(ttl string) *anthCache {
		if policy.Mode == "off" || slots == 0 {
			return nil
		}
		slots--
		return &anthCache{Type: "ephemeral", TTL: ttl}
	}
	tools, thinkingBudget := opts.Tools, opts.ThinkingBudget
	req := anthRequest{Model: c.model, MaxTokens: c.maxOutputTokens, Temperature: c.temperature, TopP: c.topP}
	if opts.DisableTools {
		req.ToolChoice = &anthToolChoice{Type: "none"}
	}
	if opts.RequireTool {
		// .
		// .
		req.ToolChoice = &anthToolChoice{Type: "any"}
	}
	if policy.Diagnostics {
		req.Diagnostics = &anthDiagnostics{}
		if opts.PreviousResponseID != "" {
			id := opts.PreviousResponseID
			req.Diagnostics.PreviousMessageID = &id
		}
	}
	systemOnly := len(messages) == 1 && messages[0].Role == "system"
	if req.MaxTokens <= 0 {
		// .
		// .
		// .
		// .
		// .
		req.MaxTokens = DefaultMaxOutputTokens
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
	switch c.thinkingMode {
	case "adaptive":
		req.Thinking = &anthThinking{Type: "adaptive"}
	case "budget":
		if thinkingBudget > 0 {
			req.Thinking = &anthThinking{Type: "enabled", BudgetTokens: thinkingBudget}
		}
	default:
	}
	// .
	// .
	// .
	// .
	// .
	// .
	if plan := c.summaryPlan(); req.Thinking != nil && plan.Sent {
		req.Thinking.Display = plan.Wire
	}
	// .
	// .
	// .
	// .
	if plan := c.effortPlan(); plan.Sent {
		req.OutputConfig = &anthOutputConfig{Effort: plan.Wire}
	}
	if c.creds != nil && c.oauthBillingText != "" {
		req.System = append(req.System, anthContent{Type: "text", Text: c.oauthBillingText})
	}

	// .
	if len(tools) > 0 {
		slots--
	}
	if policy.Mode != "explicit" {
		slots--
	}
	// .
	for _, m := range messages {
		if m.Role != "system" || m.Content == "" {
			continue
		}
		if systemOnly {
			continue
		}
		if m.StableLen > 0 && m.StableLen < len(m.Content) {
			req.System = append(req.System,
				anthContent{Type: "text", Text: m.Content[:m.StableLen], CacheControl: marker(policy.TTL)},
				anthContent{Type: "text", Text: m.Content[m.StableLen:]},
			)
		} else {
			req.System = append(req.System, anthContent{Type: "text", Text: m.Content, CacheControl: marker(policy.TTL)})
		}
	}

	if len(tools) > 0 {
		slots++
	}
	// .
	for i, t := range tools {
		schema, lifted := liftTopLevelCombinators(t.Function.Parameters)
		at := anthTool{Name: t.Function.Name, Description: t.Function.Description, InputSchema: schema}
		if at.InputSchema == nil {
			at.InputSchema = map[string]interface{}{"type": "object"}
		}
		if lifted != "" {
			// .
			// .
			// .
			at.Description = strings.TrimSpace(at.Description + "\n\n" + lifted)
		}
		if i == len(tools)-1 {
			at.CacheControl = marker(policy.TTL)
		}
		req.Tools = append(req.Tools, at)
	}

	// .
	// .
	// .
	for _, m := range messages {
		switch m.Role {
		case "system":
			if systemOnly {
				req.Messages = append(req.Messages, anthMessage{
					Role: "user", Content: []anthContent{{Type: "text", Text: m.Content}},
				})
			}
		case "assistant":
			if len(m.NativeContent) > 0 && (m.NativeDialect == "" || m.NativeDialect == "anthropic") {
				var blocks []anthContent
				if err := json.Unmarshal(m.NativeContent, &blocks); err != nil {
					return nil, fmt.Errorf("invalid native assistant replay: %w", err)
				}
				req.Messages = append(req.Messages, anthMessage{Role: "assistant", Content: blocks})
				continue
			}
			am := anthMessage{Role: "assistant"}
			// .
			// .
			// .
			// .
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
		default:
			req.Messages = append(req.Messages, anthMessage{Role: "user", Content: []anthContent{{Type: "text", Text: m.Content}}})
		}
	}

	// .
	// .
	// .
	// .

	if policy.Mode != "explicit" {
		slots++
		ttl := policy.TailTTL
		if ttl == "" {
			ttl = policy.TTL
		}
		marked := false
		for i := len(req.Messages) - 1; i >= 0 && !marked; i-- {
			blocks := req.Messages[i].Content
			for j := len(blocks) - 1; j >= 0; j-- {
				block := &blocks[j]
				if block.Type == "text" && block.Text == "" {
					continue
				}
				if block.Type == "text" || block.Type == "tool_result" || block.Type == "tool_use" {
					block.CacheControl = marker(ttl)
					marked = true
					break
				}
			}
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
			// .
			// .
			applyAuth(httpReq, cred)
		} else {
			httpReq.Header.Set("x-api-key", cred.Token)
		}
		if policy.Diagnostics {
			httpReq.Header.Set("anthropic-beta", addBeta(httpReq.Header.Get("anthropic-beta"), "cache-diagnosis-2026-04-07"))
		}
		// .
		// .
		// .
		// .
		// .
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
	// .
	// .
	// .
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

	// .
	var msg Message
	msg.Role = "assistant"
	msg.NativeDialect = "anthropic"
	msg.NativeContent, err = json.Marshal(ar.Content)
	if err != nil {
		return nil, fmt.Errorf("preserve assistant blocks: %w", err)
	}
	if ar.Usage != nil && !ar.Usage.missing && ar.Usage.OutputTokens >= 0 {
		msg.OutputTokens = ar.Usage.OutputTokens
	}
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

	// .
	// .
	// .
	// .
	// .
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
		Usage:   anthUsageOf(ar.Usage), ID: ar.ID, RequestID: httpResp.Header.Get("request-id"), Diagnostics: ar.Diagnostics,
	}, nil
}

// .
// .
// .
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

// .
// .
// .
// .
func anthUsageOf(u *anthUsage) Usage {
	if u == nil || u.missing {
		return Usage{Problem: "missing_usage"}
	}
	in, ok := tokenSum(u.InputTokens, u.CacheReadInputTokens, u.CacheCreationInputTokens)
	total, valid := tokenSum(in, u.OutputTokens)
	if !ok || !valid {
		return Usage{Problem: "invalid_usage"}
	}
	usage := Usage{PromptTokens: in, CompletionTokens: u.OutputTokens, TotalTokens: total, Reported: true,
		CachedPromptTokens: u.CacheReadInputTokens, CacheWriteTokens: u.CacheCreationInputTokens,
		CacheWrite5mTokens: u.CacheCreation.FiveMinutes, CacheWrite1hTokens: u.CacheCreation.OneHour,
		CacheReadReported: u.readReported || u.CacheReadInputTokens > 0, CacheWriteReported: u.writeReported || u.CacheCreationInputTokens > 0}
	usage.validate()
	return usage
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
// .
// .
// .
// .
// .
// .
func liftTopLevelCombinators(in map[string]interface{}) (map[string]interface{}, string) {
	var present []string
	for _, k := range []string{"anyOf", "oneOf", "allOf"} {
		if _, ok := in[k]; ok {
			present = append(present, k)
		}
	}
	if len(present) == 0 {
		return in, ""
	}
	// .
	// .
	out := make(map[string]interface{}, len(in))
	for k, v := range in {
		out[k] = v
	}
	var notes []string
	for _, k := range present {
		delete(out, k)
		if req := requiredGroups(in[k]); len(req) > 0 {
			switch k {
			case "allOf":
				notes = append(notes, "All of these are required: "+strings.Join(req, "; ")+".")
			default:
				notes = append(notes, "At least one of these is required: "+strings.Join(req, "; ")+".")
			}
			continue
		}
		notes = append(notes, "This tool declares a top-level "+k+" constraint that this provider cannot carry; consult the tool's documentation for the exact rule.")
	}
	return out, strings.Join(notes, " ")
}

// .
// .
// .
// .
func requiredGroups(v interface{}) []string {
	branches, ok := v.([]interface{})
	if !ok {
		return nil
	}
	var out []string
	for _, b := range branches {
		m, ok := b.(map[string]interface{})
		if !ok || len(m) != 1 {
			return nil
		}
		raw, ok := m["required"].([]interface{})
		if !ok || len(raw) == 0 {
			return nil
		}
		var names []string
		for _, n := range raw {
			s, ok := n.(string)
			if !ok {
				return nil
			}
			names = append(names, s)
		}
		out = append(out, strings.Join(names, " and "))
	}
	return out
}
