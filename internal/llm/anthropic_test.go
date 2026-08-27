package llm

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

type fixedCredential struct {
	token   string
	headers map[string]string
}

func (c fixedCredential) Credential(context.Context) (Credential, error) {
	return Credential{Token: c.token, Headers: c.headers}, nil
}

func (fixedCredential) Stale(context.Context, uint64) error { return nil }

// The Anthropic dialect is a wire transform: internal OpenAI-shaped
// conversation in, Messages-API request out, response mapped back.
// This pins the load-bearing parts: the cache seam becomes a
// cache_control'd stable system block, tools carry input_schema with
// the last one extending the cached span, tool results become user
// tool_result blocks, and tool_use maps back to ToolCalls.
func TestAnthropicDialect(t *testing.T) {
	var captured map[string]interface{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" {
			t.Errorf("path = %s, want /v1/messages", r.URL.Path)
		}
		if r.Header.Get("x-api-key") != "sk-test" || r.Header.Get("anthropic-version") == "" {
			t.Errorf("auth headers wrong: %v", r.Header)
		}
		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, &captured); err != nil {
			t.Errorf("request not JSON: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{
			"content": [
				{"type":"text","text":"noted."},
				{"type":"tool_use","id":"tu_1","name":"note","input":{"content":"x"}}
			],
			"stop_reason": "tool_use",
			"usage": {"input_tokens": 100, "output_tokens": 20, "cache_read_input_tokens": 500}
		}`)
	}))
	defer srv.Close()

	c := New(&ClientConfig{Endpoint: srv.URL, APIKey: "sk-test", Model: "claude-fable-5", Provider: "anthropic", MaxOutputTokens: 4096})

	system := Message{Role: "system", Content: "STABLE-IDENTITY-PREFIX volatile working truth"}
	system.StableLen = len("STABLE-IDENTITY-PREFIX")
	messages := []Message{
		system,
		{Role: "user", Content: "hello"},
		{Role: "assistant", Content: "calling a tool", ToolCalls: []ToolCall{func() ToolCall {
			tc := ToolCall{ID: "tc_0", Type: "function"}
			tc.Function.Name = "read"
			tc.Function.Arguments = `{"file_path":"a.txt"}`
			return tc
		}()}},
		{Role: "tool", ToolCallID: "tc_0", Content: "file contents"},
	}
	tools := []ToolDefinition{
		{Type: "function", Function: ToolFunction{Name: "read", Description: "read a file", Parameters: map[string]interface{}{"type": "object"}}},
		{Type: "function", Function: ToolFunction{Name: "note", Description: "notice", Parameters: map[string]interface{}{"type": "object"}}},
	}

	resp, err := c.Chat(context.Background(), messages, ChatOptions{Tools: tools, ThinkingBudget: 2048})
	if err != nil {
		t.Fatal(err)
	}

	// Request shape: system split at the seam, stable block cache-marked.
	sys := captured["system"].([]interface{})
	if len(sys) != 2 {
		t.Fatalf("system blocks = %d, want 2 (stable+volatile)", len(sys))
	}
	stable := sys[0].(map[string]interface{})
	if stable["text"] != "STABLE-IDENTITY-PREFIX" {
		t.Fatalf("stable block text = %q", stable["text"])
	}
	if stable["cache_control"] == nil {
		t.Fatal("stable system block must carry cache_control")
	}
	if volatile := sys[1].(map[string]interface{}); volatile["cache_control"] != nil {
		t.Fatal("volatile system block must NOT be cache-marked")
	}

	// Tools: input_schema present; last tool extends the cached span.
	tls := captured["tools"].([]interface{})
	if len(tls) != 2 {
		t.Fatalf("tools = %d", len(tls))
	}
	if tls[0].(map[string]interface{})["input_schema"] == nil {
		t.Fatal("tools must carry input_schema")
	}
	if tls[0].(map[string]interface{})["cache_control"] != nil {
		t.Fatal("only the LAST tool carries the cache marker")
	}
	if tls[1].(map[string]interface{})["cache_control"] == nil {
		t.Fatal("the last tool extends the cached span")
	}

	// Thinking: the DEFAULT shape is adaptive, and it carries no budget.
	// This assertion used to require budget_tokens=2048 — it encoded the
	// shape the vendor now rejects with a 400.
	// Unset thinking_mode sends NO thinking parameter. Adaptive was the
	// default until claude-haiku-4-5 rejected it with a 400 live — a
	// provider entry serves many models and cannot carry one shape for
	// all of them. Opus 5 runs adaptive when the parameter is absent, so
	// omitting costs nothing where it matters.
	if _, present := captured["thinking"]; present {
		t.Fatalf("unset thinking_mode must send no thinking parameter: %v", captured["thinking"])
	}

	// Conversation mapping: tool result became a user tool_result block.
	msgs := captured["messages"].([]interface{})
	last := msgs[len(msgs)-1].(map[string]interface{})
	if last["role"] != "user" {
		t.Fatalf("tool result must map to a user message, got %v", last["role"])
	}
	blk := last["content"].([]interface{})[0].(map[string]interface{})
	if blk["type"] != "tool_result" || blk["tool_use_id"] != "tc_0" {
		t.Fatalf("tool_result block wrong: %v", blk)
	}

	// Response mapping: text + tool_use → Content + ToolCalls; stop_reason
	// tool_use → finish_reason tool_calls; cache reads count in usage.
	if len(resp.Choices) != 1 {
		t.Fatal("one choice expected")
	}
	ch := resp.Choices[0]
	if ch.FinishReason != "tool_calls" {
		t.Fatalf("finish = %q", ch.FinishReason)
	}
	if ch.Message.Content != "noted." || len(ch.Message.ToolCalls) != 1 {
		t.Fatalf("mapped message wrong: %+v", ch.Message)
	}
	if ch.Message.ToolCalls[0].Function.Name != "note" || ch.Message.ToolCalls[0].Function.Arguments != `{"content":"x"}` {
		t.Fatalf("tool call mapping wrong: %+v", ch.Message.ToolCalls[0])
	}
	if resp.Usage.PromptTokens != 600 {
		t.Fatalf("usage must include cache reads: %d", resp.Usage.PromptTokens)
	}
	if resp.ModelID != "claude-fable-5" {
		t.Fatalf("response provenance = %q, want configured model", resp.ModelID)
	}
}

func TestAnthropicCacheSeamPreservesPromptBytes(t *testing.T) {
	var captured anthRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Errorf("decode request: %v", err)
		}
		io.WriteString(w, `{"content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn"}`)
	}))
	defer srv.Close()

	const content = "stable\n\nchanging"
	c := New(&ClientConfig{Endpoint: srv.URL, APIKey: "key", Model: "model", Provider: "anthropic"})
	if _, err := c.Chat(t.Context(), []Message{
		{Role: "system", Content: content, StableLen: len("stable")},
		{Role: "user", Content: "hello"},
	}, ChatOptions{}); err != nil {
		t.Fatal(err)
	}
	if len(captured.System) != 2 || captured.System[0].Text+captured.System[1].Text != content {
		t.Fatalf("cache seam changed prompt bytes: %#v", captured.System)
	}
}

func TestClaudeCodeOAuthWireContract(t *testing.T) {
	const billing = "x-anthropic-billing-header: cc_version=test; cc_entrypoint=cli; cch=00000;"
	var captured map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" {
			t.Errorf("Claude request path = %q", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer oauth-token" || r.Header.Get("x-api-key") != "" {
			t.Errorf("OAuth auth headers wrong: %v", r.Header)
		}
		for name, want := range map[string]string{
			"anthropic-version": "2023-06-01", "anthropic-beta": "oauth-test",
			"user-agent": "claude-cli/test", "x-app": "cli",
		} {
			if got := r.Header.Get(name); got != want {
				t.Errorf("%s = %q, want %q", name, got, want)
			}
		}
		if got := r.Header.Get("Content-Type"); got != "application/json" {
			t.Errorf("credential header replaced Content-Type: %q", got)
		}
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Errorf("decode request: %v", err)
		}
		io.WriteString(w, `{"content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn"}`)
	}))
	defer srv.Close()

	c := New(&ClientConfig{
		Endpoint: srv.URL, Model: "claude-opus-5", Provider: "anthropic",
		Credential: fixedCredential{token: "oauth-token", headers: map[string]string{
			"anthropic-beta": "oauth-test", "user-agent": "claude-cli/test", "x-app": "cli",
			"Authorization": "Bearer attacker", "x-api-key": "attacker", "Content-Type": "text/plain",
		}},
		AnthropicOAuthBillingText: billing,
	})
	if _, err := c.Chat(context.Background(), []Message{
		{Role: "system", Content: "resident identity"},
		{Role: "user", Content: "hello"},
	}, ChatOptions{}); err != nil {
		t.Fatal(err)
	}

	system, ok := captured["system"].([]any)
	if !ok || len(system) != 2 {
		t.Fatalf("system = %#v, want billing block then identity block", captured["system"])
	}
	first := system[0].(map[string]any)
	if first["text"] != billing || first["cache_control"] != nil {
		t.Fatalf("first system block must be uncached billing text: %#v", first)
	}
	second := system[1].(map[string]any)
	if second["text"] != "resident identity" {
		t.Fatalf("billing block displaced the identity prompt: %#v", second)
	}

	// Anthropic requires a message. For Firstboot, the verified bootstrap
	// bytes become the sole user message; OAuth billing remains transport
	// metadata in the top-level system field.
	const bootstrapBundleContent = "verified bootstrap bundle content"
	captured = nil
	if _, err := c.Chat(context.Background(), []Message{{Role: "system", Content: bootstrapBundleContent}}, ChatOptions{}); err != nil {
		t.Fatal(err)
	}
	system, ok = captured["system"].([]any)
	if !ok || len(system) != 1 || system[0].(map[string]any)["text"] != billing {
		t.Fatalf("birth system field must contain only OAuth billing metadata: %#v", captured)
	}
	wireMessages, ok := captured["messages"].([]any)
	if !ok || len(wireMessages) != 1 {
		t.Fatalf("birth must carry one Anthropic message: %#v", captured["messages"])
	}
	user := wireMessages[0].(map[string]any)
	content := user["content"].([]any)
	if user["role"] != "user" || len(content) != 1 || content[0].(map[string]any)["text"] != bootstrapBundleContent {
		t.Fatalf("verified bootstrap bytes must be the sole user message: %#v", user)
	}
}

// The OpenAI-compatible path is untouched by the dialect work: one
// system message, seam ignored (prefix caching is implicit there).
func TestOpenAIPathUnchangedBySeam(t *testing.T) {
	var captured map[string]interface{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &captured)
		io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`)
	}))
	defer srv.Close()

	c := New(&ClientConfig{Endpoint: srv.URL, APIKey: "k", Model: "m"})
	sys := Message{Role: "system", Content: "stable volatile"}
	sys.StableLen = 6
	if _, err := c.Chat(context.Background(), []Message{sys, {Role: "user", Content: "hi"}}, ChatOptions{}); err != nil {
		t.Fatal(err)
	}
	msgs := captured["messages"].([]interface{})
	first := msgs[0].(map[string]interface{})
	if first["role"] != "system" || first["content"] != "stable volatile" {
		t.Fatalf("system message must pass through whole: %v", first)
	}
	if _, leaked := first["StableLen"]; leaked {
		t.Fatal("StableLen must never serialize")
	}
}

// The operator owns the vendor API version.
//
// providerEntry.credential_options documents header_<Name> as the way
// to "correct a vendor request fact without a release", oauth.go
// forwards any header_* verbatim, and applyAuth applies them. But
// anthropic.go then Set() the version UNCONDITIONALLY, after applyAuth
// — so an operator who set header_anthropic-version had it silently
// discarded and the config parameter existed on paper only. Live and
// configured state must not diverge silently.
func TestAnthropicVersionIsOperatorSettable(t *testing.T) {
	reply := `{"content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`

	cases := []struct {
		name     string
		override string
		want     string
	}{
		{"default applies when the operator sets nothing", "", DefaultAnthropicVersion},
		{"operator override survives applyAuth", "2026-01-01", "2026-01-01"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var got string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				got = r.Header.Get("anthropic-version")
				w.Header().Set("Content-Type", "application/json")
				io.WriteString(w, reply)
			}))
			defer srv.Close()

			cfg := &ClientConfig{
				Endpoint: srv.URL, Model: "claude-fable-5",
				Provider: "anthropic", MaxOutputTokens: 128,
			}
			if tc.override == "" {
				cfg.APIKey = "sk-test"
			} else {
				cfg.Credential = fixedCredential{
					token:   "tok",
					headers: map[string]string{"anthropic-version": tc.override},
				}
			}

			c := New(cfg)
			if _, err := c.Chat(context.Background(),
				[]Message{{Role: "user", Content: "hi"}}, ChatOptions{}); err != nil {
				t.Fatalf("chat: %v", err)
			}
			if got != tc.want {
				t.Fatalf("anthropic-version = %q, want %q — the operator's configured value must reach the wire", got, tc.want)
			}
		})
	}
}

// The vendor replaced the thinking parameter and REJECTS the old shape
// with a 400 on the whole current family. Which shape goes on the wire
// is a registry fact, so all three modes are pinned here.
func TestAnthropicThinkingModeShapes(t *testing.T) {
	for _, tc := range []struct {
		mode       string
		budget     int
		wantType   string
		wantBudget bool
		wantAbsent bool
	}{
		{mode: "", budget: 2048, wantAbsent: true}, // unset sends NOTHING: one entry serves many models, and claude-haiku-4-5 rejects adaptive
		{mode: "adaptive", budget: 0, wantType: "adaptive"},
		{mode: "budget", budget: 2048, wantType: "enabled", wantBudget: true},
		{mode: "budget", budget: 0, wantAbsent: true},
		{mode: "off", budget: 2048, wantAbsent: true},
	} {
		name := tc.mode
		if name == "" {
			name = "unset"
		}
		t.Run(name, func(t *testing.T) {
			var captured map[string]interface{}
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
					t.Errorf("decode: %v", err)
				}
				io.WriteString(w, `{"content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn"}`)
			}))
			defer srv.Close()

			c := New(&ClientConfig{Endpoint: srv.URL, APIKey: "k", Model: "m", Provider: "anthropic",
				ThinkingMode: tc.mode, ThinkingBudget: tc.budget})
			if _, err := c.Chat(context.Background(), []Message{{Role: "user", Content: "hi"}}, ChatOptions{}); err != nil {
				t.Fatal(err)
			}
			think, present := captured["thinking"]
			if tc.wantAbsent {
				if present {
					t.Fatalf("mode %q budget %d: thinking must be absent, got %v", tc.mode, tc.budget, think)
				}
				return
			}
			if !present {
				t.Fatalf("mode %q: thinking absent, want %s", tc.mode, tc.wantType)
			}
			m := think.(map[string]interface{})
			if m["type"] != tc.wantType {
				t.Fatalf("mode %q: type = %v, want %s", tc.mode, m["type"], tc.wantType)
			}
			_, hasBudget := m["budget_tokens"]
			if hasBudget != tc.wantBudget {
				t.Fatalf("mode %q: budget_tokens present = %v, want %v (%v)", tc.mode, hasBudget, tc.wantBudget, m)
			}
		})
	}
}

// Reasoning was DROPPED between iterations: llm.Message had nowhere to
// put a thinking block, so every tool result arrived to a model that had
// to re-derive its plan from the transcript. The signature is the
// load-bearing half — it is what lets the provider accept the block on
// replay — and it is present even when the text is empty, which is the
// vendor default.
func TestThinkingBlocksRoundTrip(t *testing.T) {
	var captured anthRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Errorf("decode: %v", err)
		}
		io.WriteString(w, `{"content":[
			{"type":"thinking","thinking":"weighing it","signature":"sig-1"},
			{"type":"text","text":"here is the answer"}],"stop_reason":"end_turn"}`)
	}))
	defer srv.Close()

	c := New(&ClientConfig{Endpoint: srv.URL, APIKey: "k", Model: "m", Provider: "anthropic"})
	resp, err := c.Chat(context.Background(), []Message{{Role: "user", Content: "hi"}}, ChatOptions{})
	if err != nil {
		t.Fatal(err)
	}
	got := resp.Choices[0].Message
	if len(got.Thinking) != 1 || got.Thinking[0].Signature != "sig-1" || got.Thinking[0].Text != "weighing it" {
		t.Fatalf("thinking not captured: %+v", got.Thinking)
	}
	if got.Content != "here is the answer" {
		t.Fatalf("text block lost: %q", got.Content)
	}

	// Replay it as history: the block must go back, and BEFORE the text
	// it produced — the API requires reasoning to precede its output.
	if _, err := c.Chat(context.Background(), []Message{
		{Role: "user", Content: "hi"}, got, {Role: "user", Content: "more"},
	}, ChatOptions{}); err != nil {
		t.Fatal(err)
	}
	var assistant *anthMessage
	for i := range captured.Messages {
		if captured.Messages[i].Role == "assistant" {
			assistant = &captured.Messages[i]
		}
	}
	if assistant == nil || len(assistant.Content) < 2 {
		t.Fatalf("assistant turn not replayed with its blocks: %+v", captured.Messages)
	}
	if assistant.Content[0].Type != "thinking" || assistant.Content[0].Signature != "sig-1" {
		t.Fatalf("thinking must be replayed first, got %+v", assistant.Content[0])
	}
	if assistant.Content[1].Type != "text" {
		t.Fatalf("text must follow the thinking, got %+v", assistant.Content[1])
	}
}

// Two operator settings that previously reached nothing: readable
// reasoning, and effort on the anthropic path (reasoning_effort is the
// OpenAI spelling; the Messages API takes it inside output_config).
func TestThinkingDisplayAndEffortReachTheWire(t *testing.T) {
	var captured map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&captured)
		io.WriteString(w, `{"content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn"}`)
	}))
	defer srv.Close()

	c := New(&ClientConfig{Endpoint: srv.URL, APIKey: "k", Model: "m", Provider: "anthropic",
		ThinkingMode: "adaptive", ThinkingDisplay: "summarized", ReasoningEffort: "xhigh"})
	if _, err := c.Chat(context.Background(), []Message{{Role: "user", Content: "hi"}}, ChatOptions{}); err != nil {
		t.Fatal(err)
	}
	think, _ := captured["thinking"].(map[string]any)
	if think == nil || think["display"] != "summarized" {
		t.Fatalf("thinking display did not reach the wire: %v", captured["thinking"])
	}
	oc, _ := captured["output_config"].(map[string]any)
	if oc == nil || oc["effort"] != "xhigh" {
		t.Fatalf("effort did not reach the wire: %v", captured["output_config"])
	}
}

// LIVE 400, 2026-08-23: "messages.19.content.0.thinking.thinking: Field
// required". Thinking blocks arrive with EMPTY text by default —
// display defaults to omitted — and the field carried omitempty, so
// every replayed turn dropped it and the provider rejected the request.
// The signature alone is not a thinking block.
//
// Asserted against the RAW body, not the decoded struct: a struct with
// the same bug round-trips through itself perfectly.
func TestEmptyThinkingStillSendsTheField(t *testing.T) {
	var raw map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&raw)
		io.WriteString(w, `{"content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn"}`)
	}))
	defer srv.Close()

	c := New(&ClientConfig{Endpoint: srv.URL, APIKey: "k", Model: "m", Provider: "anthropic"})
	prior := Message{Role: "assistant", Content: "an answer",
		Thinking: []ThinkingBlock{{Kind: "thinking", Text: "", Signature: "sig-1"}}}
	if _, err := c.Chat(context.Background(), []Message{
		{Role: "user", Content: "hi"}, prior, {Role: "user", Content: "more"},
	}, ChatOptions{}); err != nil {
		t.Fatal(err)
	}

	msgs, _ := raw["messages"].([]any)
	var block map[string]any
	for _, m := range msgs {
		mm := m.(map[string]any)
		if mm["role"] != "assistant" {
			continue
		}
		for _, b := range mm["content"].([]any) {
			if bb := b.(map[string]any); bb["type"] == "thinking" {
				block = bb
			}
		}
	}
	if block == nil {
		t.Fatalf("no thinking block on the wire: %v", raw["messages"])
	}
	if _, present := block["thinking"]; !present {
		t.Fatalf("the thinking field was OMITTED when empty — this is the live 400: %v", block)
	}
	if block["thinking"] != "" || block["signature"] != "sig-1" {
		t.Fatalf("thinking block malformed: %v", block)
	}
}

// A redacted block carries opaque data and no text. Replaying it as a
// plain thinking block would be a different malformed request.
func TestRedactedThinkingReplaysAsItself(t *testing.T) {
	var raw map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&raw)
		io.WriteString(w, `{"content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn"}`)
	}))
	defer srv.Close()

	c := New(&ClientConfig{Endpoint: srv.URL, APIKey: "k", Model: "m", Provider: "anthropic"})
	prior := Message{Role: "assistant", Content: "an answer",
		Thinking: []ThinkingBlock{{Kind: "redacted_thinking", Data: "opaque"}}}
	if _, err := c.Chat(context.Background(), []Message{
		{Role: "user", Content: "hi"}, prior, {Role: "user", Content: "more"},
	}, ChatOptions{}); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, m := range raw["messages"].([]any) {
		mm := m.(map[string]any)
		if mm["role"] != "assistant" {
			continue
		}
		for _, b := range mm["content"].([]any) {
			bb := b.(map[string]any)
			if bb["type"] == "redacted_thinking" && bb["data"] == "opaque" {
				found = true
			}
			if bb["type"] == "thinking" {
				t.Fatalf("a redacted block was replayed as a plain thinking block: %v", bb)
			}
		}
	}
	if !found {
		t.Fatalf("redacted block did not survive replay: %v", raw["messages"])
	}
}
