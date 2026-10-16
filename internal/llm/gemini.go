package llm

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net/http"
	"slices"
	"strings"

	"github.com/aiii-dot-id/aii-os/internal/logsink"
)

const geminiBypassSignature = "skip_thought_signature_validator"

const geminiThinkingShape = "level"

type geminiRequest struct {
	SystemInstruction *geminiContent    `json:"systemInstruction,omitempty"`
	Contents          []geminiContent   `json:"contents"`
	Tools             []geminiTool      `json:"tools,omitempty"`
	ToolConfig        *geminiToolConfig `json:"toolConfig,omitempty"`
	GenerationConfig  geminiGeneration  `json:"generationConfig"`
}

type geminiContent struct {
	Role  string       `json:"role,omitempty"`
	Parts []geminiPart `json:"parts"`
}

type geminiPart struct {
	Raw json.RawMessage `json:"-"`

	Text             *string                 `json:"text,omitempty"`
	Thought          bool                    `json:"thought,omitempty"`
	ThoughtSignature string                  `json:"thoughtSignature,omitempty"`
	FunctionCall     *geminiFunctionCall     `json:"functionCall,omitempty"`
	FunctionResponse *geminiFunctionResponse `json:"functionResponse,omitempty"`

	plain bool
}

type geminiFunctionCall struct {
	ID   string          `json:"id,omitempty"`
	Name string          `json:"name"`
	Args json.RawMessage `json:"args,omitempty"`
}

type geminiFunctionResponse struct {
	ID       string            `json:"id,omitempty"`
	Name     string            `json:"name"`
	Response map[string]string `json:"response"`
}

type geminiTool struct {
	FunctionDeclarations []geminiFunction `json:"functionDeclarations"`
}

type geminiFunction struct {
	Name        string `json:"name"`
	Description string `json:"description"`

	ParametersJSONSchema map[string]any `json:"parametersJsonSchema,omitempty"`
}

type geminiToolConfig struct {
	FunctionCallingConfig geminiCallingConfig `json:"functionCallingConfig"`
}

type geminiCallingConfig struct {
	Mode string `json:"mode"`
}

type geminiGeneration struct {
	MaxOutputTokens int             `json:"maxOutputTokens"`
	Temperature     *float64        `json:"temperature,omitempty"`
	TopP            *float64        `json:"topP,omitempty"`
	ThinkingConfig  *geminiThinking `json:"thinkingConfig,omitempty"`
}

type geminiThinking struct {
	ThinkingLevel   string `json:"thinkingLevel,omitempty"`
	IncludeThoughts bool   `json:"includeThoughts,omitempty"`
}

type geminiResponse struct {
	Candidates []struct {
		Index   int `json:"index"`
		Content struct {
			Parts []geminiPart `json:"parts"`
		} `json:"content"`
		FinishReason string `json:"finishReason"`
	} `json:"candidates"`
	PromptFeedback *struct {
		BlockReason string `json:"blockReason"`
	} `json:"promptFeedback"`
	UsageMetadata *geminiUsage `json:"usageMetadata"`
	ResponseID    string       `json:"responseId"`
	Error         *struct {
		Message string `json:"message"`
	} `json:"error"`
}

type geminiUsage struct {
	Prompt     *int `json:"promptTokenCount"`
	Total      *int `json:"totalTokenCount"`
	Candidates int  `json:"candidatesTokenCount"`
	Thoughts   int  `json:"thoughtsTokenCount"`
	ToolUse    int  `json:"toolUsePromptTokenCount"`
	Cached     *int `json:"cachedContentTokenCount"`
}

func (u *geminiUsage) normalized() Usage {
	if u == nil || u.Prompt == nil || u.Total == nil {
		return Usage{Problem: "missing_usage"}
	}
	v := Usage{PromptTokens: *u.Prompt + u.ToolUse, CompletionTokens: u.Candidates + u.Thoughts, TotalTokens: *u.Total, Reported: true}
	if u.Cached != nil {
		v.CachedPromptTokens, v.CacheReadReported = *u.Cached, true
	}
	v.Validate()
	return v
}

func (p *geminiPart) UnmarshalJSON(data []byte) error {
	type fields geminiPart
	var parsed fields
	if err := json.Unmarshal(data, &parsed); err != nil {
		return err
	}
	*p = geminiPart(parsed)
	p.Raw = append(json.RawMessage(nil), data...)
	var members map[string]json.RawMessage
	if err := json.Unmarshal(data, &members); err != nil {
		return err
	}
	p.plain = p.Text != nil
	for name := range members {
		if name != "text" && name != "thought" {
			p.plain = false
		}
	}
	return nil
}

func (p geminiPart) MarshalJSON() ([]byte, error) {
	if len(p.Raw) > 0 {
		return p.Raw, nil
	}
	type fields geminiPart
	return json.Marshal(fields(p))
}

func geminiText(text string) geminiPart { return geminiPart{Text: &text} }

func geminiModelPath(model string) string {
	if strings.Contains(model, "/") {
		return model
	}
	return "models/" + model
}

func (c *Client) geminiAuth(req *http.Request, cred Credential) error {
	if c.creds != nil {
		applyAuth(req, cred)
		return nil
	}
	req.Header.Set("x-goog-api-key", cred.Token)
	return nil
}

func (c *Client) geminiStreamAuth(req *http.Request, cred Credential) error {
	req.Header.Set("Accept", "text/event-stream")
	return c.geminiAuth(req, cred)
}

func geminiContents(messages []Message) (system string, contents []geminiContent, err error) {
	var instructions []string

	type call struct{ name, id string }
	called := map[string]call{}
	add := func(role string, parts ...geminiPart) {
		if len(parts) == 0 {
			return
		}
		if n := len(contents); n > 0 && contents[n-1].Role == role {
			contents[n-1].Parts = append(contents[n-1].Parts, parts...)
			return
		}
		contents = append(contents, geminiContent{Role: role, Parts: parts})
	}
	for _, m := range messages {
		switch m.Role {
		case "system":
			instructions = append(instructions, m.Content)
		case "assistant":
			if m.NativeDialect == string(DialectGemini) && len(m.NativeContent) > 0 {
				var parts []geminiPart
				if err := json.Unmarshal(m.NativeContent, &parts); err != nil {
					return "", nil, fmt.Errorf("invalid Gemini replay: %w", err)
				}
				n := 0
				for _, p := range parts {
					if p.FunctionCall == nil || n >= len(m.ToolCalls) {
						continue
					}
					called[m.ToolCalls[n].ID] = call{p.FunctionCall.Name, p.FunctionCall.ID}
					n++
				}
				add("model", parts...)
				continue
			}
			var parts []geminiPart
			if m.Content != "" {
				parts = append(parts, geminiText(m.Content))
			}
			for i, tc := range m.ToolCalls {
				p := geminiPart{FunctionCall: &geminiFunctionCall{Name: tc.Function.Name, Args: normalizeToolInput(tc.Function.Arguments)}}
				if i == 0 {
					p.ThoughtSignature = geminiBypassSignature
				}
				parts = append(parts, p)
				called[tc.ID] = call{name: tc.Function.Name}
			}
			add("model", parts...)
		case "tool":
			fn, ok := called[m.ToolCallID]
			if !ok {
				return "", nil, fmt.Errorf("gemini: the tool result %q answers no call in this request, and a function response must name its function", m.ToolCallID)
			}
			add("user", geminiPart{FunctionResponse: &geminiFunctionResponse{ID: fn.id, Name: fn.name, Response: map[string]string{"output": m.Content}}})
		default:
			if m.Content != "" {
				add("user", geminiText(m.Content))
			}
		}
	}
	return strings.Join(instructions, "\n\n"), contents, nil
}

func (c *Client) chatGemini(ctx context.Context, messages []Message, opts ChatOptions) (*Response, error) {
	system, contents, err := geminiContents(messages)
	if err != nil {
		return nil, err
	}
	req := geminiRequest{Contents: contents, GenerationConfig: geminiGeneration{
		MaxOutputTokens: c.maxOutputTokens, Temperature: c.temperature, TopP: c.topP,
	}}
	if req.GenerationConfig.MaxOutputTokens <= 0 {
		req.GenerationConfig.MaxOutputTokens = DefaultMaxOutputTokens
	}
	switch {
	case len(req.Contents) == 0:

		req.Contents = []geminiContent{{Role: "user", Parts: []geminiPart{geminiText(system)}}}
	case system != "":
		req.SystemInstruction = &geminiContent{Parts: []geminiPart{geminiText(system)}}
	}

	if len(c.extra) > 0 {
		if _, said := c.extraWarned.LoadOrStore("", true); !said {
			logsink.Warn("llm.refusal", "the provider entry states extra request members (%s) and none of them is sent: the Gemini API is typed, and this client sends it only what it defines", strings.Join(slices.Sorted(maps.Keys(c.extra)), ", "))
		}
	}
	if len(opts.Tools) > 0 {
		decls := make([]geminiFunction, len(opts.Tools))
		for i, t := range opts.Tools {
			decls[i] = geminiFunction{Name: t.Function.Name, Description: t.Function.Description, ParametersJSONSchema: t.Function.Parameters}
		}
		req.Tools = []geminiTool{{FunctionDeclarations: decls}}
		if opts.DisableTools {
			req.ToolConfig = &geminiToolConfig{geminiCallingConfig{Mode: "NONE"}}
		}
		if opts.RequireTool {
			req.ToolConfig = &geminiToolConfig{geminiCallingConfig{Mode: "ANY"}}
		}
	}

	if c.thinkingMode != "off" {
		thinking := geminiThinking{ThinkingLevel: strings.ToUpper(c.effortPlan().Wire), IncludeThoughts: c.thinkingMode == geminiThinkingShape && c.summaryPlan().Sent}
		if thinking != (geminiThinking{}) {
			req.GenerationConfig.ThinkingConfig = &thinking
		}
	}
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}
	base := strings.TrimSuffix(c.endpoint, "/") + "/" + geminiModelPath(c.model)
	if c.noStream {
		httpResp, err := c.post(ctx, base+":generateContent", body, c.geminiAuth)
		if err != nil {
			return nil, err
		}
		defer httpResp.Body.Close()
		return geminiReply(httpResp, readGeminiWhole)
	}
	sctx := context.WithValue(ctx, streamKey{}, true)
	httpResp, err := c.post(sctx, base+":streamGenerateContent?alt=sse", body, c.geminiStreamAuth)
	if err != nil {
		return nil, c.idleVerdict(err)
	}
	defer httpResp.Body.Close()
	read := readGeminiStream
	if httpResp.StatusCode == http.StatusOK && !isEventStream(httpResp.Header.Get("Content-Type")) {
		read = readGeminiWhole
	}
	resp, err := geminiReply(httpResp, read)
	return resp, c.idleVerdict(err)
}

func geminiReply(httpResp *http.Response, read func(io.Reader) (*Response, error)) (*Response, error) {
	if httpResp.StatusCode != http.StatusOK {
		refusal, err := refusalError(httpResp)
		if err != nil {
			return nil, err
		}
		return nil, refusal
	}
	return read(httpResp.Body)
}

type geminiTurn struct {
	parts []geminiPart

	text    []*strings.Builder
	finish  string
	blocked bool
	usage   *geminiUsage
	id      string
}

func (t *geminiTurn) done() bool { return t.finish != "" || t.blocked }

func (t *geminiTurn) add(r geminiResponse) {
	if r.ResponseID != "" {
		t.id = r.ResponseID
	}
	if r.UsageMetadata != nil {
		t.usage = r.UsageMetadata
	}
	if r.PromptFeedback != nil && r.PromptFeedback.BlockReason != "" {
		t.blocked = true
	}
	for _, cand := range r.Candidates {
		if cand.Index != 0 {
			continue
		}
		for _, p := range cand.Content.Parts {

			if n := len(t.parts); n > 0 && p.plain && t.text[n-1] != nil && t.parts[n-1].Thought == p.Thought {
				t.text[n-1].WriteString(*p.Text)
				continue
			}
			var text *strings.Builder
			if p.plain {
				text = new(strings.Builder)
				text.WriteString(*p.Text)
			}
			t.parts, t.text = append(t.parts, p), append(t.text, text)
		}
		if cand.FinishReason != "" {
			t.finish = cand.FinishReason
		}
	}
}

var geminiRefusals = map[string]bool{
	"SAFETY": true, "RECITATION": true, "LANGUAGE": true, "BLOCKLIST": true, "PROHIBITED_CONTENT": true, "SPII": true,
	"IMAGE_SAFETY": true, "IMAGE_PROHIBITED_CONTENT": true, "IMAGE_RECITATION": true,
}

func (t *geminiTurn) response() (*Response, error) {
	msg := Message{Role: "assistant", NativeDialect: string(DialectGemini)}
	for i, joined := range t.text {
		if joined != nil && joined.Len() != len(*t.parts[i].Text) {
			text := joined.String()
			t.parts[i] = geminiPart{Text: &text, Thought: t.parts[i].Thought}
		}
	}
	var err error
	if msg.NativeContent, err = json.Marshal(t.parts); err != nil {
		return nil, fmt.Errorf("preserve the reply's parts: %w", err)
	}
	var text strings.Builder
	var account []string
	thinking := false
	for _, p := range t.parts {
		wasThinking := thinking
		thinking = p.Thought && p.Text != nil
		switch {
		case p.FunctionCall != nil:
			tc := ToolCall{ID: p.FunctionCall.ID, Type: "function"}
			if tc.ID == "" {

				if tc.ID, err = geminiCallID(); err != nil {
					return nil, err
				}
			}
			tc.Function.Name = p.FunctionCall.Name
			tc.Function.Arguments = string(p.FunctionCall.Args)
			if tc.Function.Arguments == "" {
				tc.Function.Arguments = "{}"
			}
			msg.ToolCalls = append(msg.ToolCalls, tc)
		case p.Text == nil:
		case p.Thought:

			if !wasThinking {
				account = append(account, "")
			}
			account[len(account)-1] += *p.Text
		default:
			text.WriteString(*p.Text)
		}
	}
	msg.Content = text.String()
	usage := t.usage.normalized()
	if usage.Reported {
		msg.OutputTokens = usage.CompletionTokens
	}

	finish := t.finish
	switch {
	case finish == "STOP" && len(msg.ToolCalls) > 0:
		finish = "tool_calls"
	case finish == "STOP":
		finish = "stop"
	case finish == "MAX_TOKENS":
		finish = "length"
	case t.blocked, geminiRefusals[finish]:
		finish = "refusal"
	}
	return &Response{
		Choices: []Choice{{Message: msg, FinishReason: finish, ReasoningAccount: strings.Join(slices.DeleteFunc(account, blank), "\n\n")}},
		Usage:   usage, ID: t.id,
	}, nil
}

func blank(s string) bool { return strings.TrimSpace(s) == "" }

func geminiCallID() (string, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("name a tool call: %w", err)
	}
	return "call_" + hex.EncodeToString(b[:]), nil
}

func readGeminiWhole(r io.Reader) (*Response, error) {
	body, truncated, err := readBounded(r, maxResponseBytes)
	if err != nil {
		return nil, fmt.Errorf("read response body: %w", err)
	}
	if truncated {
		return nil, fmt.Errorf("API response exceeds %d bytes", maxResponseBytes)
	}
	var reply geminiResponse
	if err := json.Unmarshal(body, &reply); err != nil {
		return nil, fmt.Errorf("decode response: %w\nbody: %s", err, string(body[:min(2000, len(body))]))
	}
	var turn geminiTurn
	turn.add(reply)
	if !turn.done() {
		return nil, fmt.Errorf("the reply carries no finish reason after %d part(s): not a finished reply", len(turn.parts))
	}
	return turn.response()
}

func readGeminiStream(r io.Reader) (*Response, error) {
	limited := &io.LimitedReader{R: r, N: maxResponseBytes + 1}
	sc := bufio.NewScanner(limited)
	sc.Buffer(make([]byte, 0, 64<<10), maxResponseBytes)
	var turn geminiTurn
	var provider string
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "" {
			continue
		}
		var event geminiResponse
		if err := json.Unmarshal([]byte(payload), &event); err != nil {
			return nil, fmt.Errorf("decode stream event: %w", err)
		}
		if event.Error != nil {
			provider = event.Error.Message
			if provider == "" {
				provider = payload
			}
			continue
		}
		turn.add(event)
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("read response stream: %w", err)
	}
	if limited.N == 0 {
		return nil, fmt.Errorf("API response stream exceeds %d bytes", maxResponseBytes)
	}
	if provider != "" {
		return nil, fmt.Errorf("provider reported: %.400s", provider)
	}
	if !turn.done() {
		return nil, fmt.Errorf("response stream ended without a finish reason after %d part(s) — truncated, not complete", len(turn.parts))
	}
	return turn.response()
}

func (c *Client) embedGemini(ctx context.Context, model string, inputs []string) ([][]float32, error) {
	type request struct {
		Model   string        `json:"model"`
		Content geminiContent `json:"content"`
	}
	path := geminiModelPath(model)
	requests := make([]request, len(inputs))
	for i, input := range inputs {
		requests[i] = request{Model: path, Content: geminiContent{Parts: []geminiPart{geminiText(input)}}}
	}
	body, err := json.Marshal(map[string]any{"requests": requests})
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}
	httpResp, err := c.post(ctx, strings.TrimSuffix(c.endpoint, "/")+"/"+path+":batchEmbedContents", body, c.geminiAuth)
	if err != nil {
		return nil, err
	}
	defer httpResp.Body.Close()
	if httpResp.StatusCode != http.StatusOK {
		refusal, err := refusalError(httpResp)
		if err != nil {
			return nil, err
		}
		return nil, refusal
	}
	respBody, truncated, err := readBounded(httpResp.Body, maxResponseBytes)
	if err != nil {
		return nil, fmt.Errorf("read response body: %w", err)
	}
	if truncated {
		return nil, fmt.Errorf("API response exceeds %d bytes", maxResponseBytes)
	}
	var parsed struct {
		Embeddings []struct {
			Values []float32 `json:"values"`
		} `json:"embeddings"`
	}
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return nil, fmt.Errorf("decode embeddings response: %w", err)
	}
	if len(parsed.Embeddings) != len(inputs) {
		return nil, fmt.Errorf("embeddings: %d vectors for %d inputs", len(parsed.Embeddings), len(inputs))
	}
	out := make([][]float32, len(inputs))
	for i, e := range parsed.Embeddings {
		if len(e.Values) == 0 {
			return nil, fmt.Errorf("embeddings: vector %d is empty", i)
		}
		out[i] = e.Values
	}
	return out, nil
}
