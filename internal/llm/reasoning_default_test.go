package llm

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestThePlanAsksForReadableReasoningByDefault(t *testing.T) {
	for _, tc := range []struct {
		dialect   Dialect
		requested string
		sent      bool
		wire      string
		note      bool
	}{
		{DialectAnthropic, "", true, "summarized", false},
		{DialectResponses, "", true, "auto", false},
		{DialectOpenAI, "", false, "", false},
		{DialectAnthropic, SummaryOmitted, false, "", false},
		{DialectResponses, SummaryOmitted, false, "", false},
		{DialectOpenAI, SummaryOmitted, false, "", false},
		{DialectAnthropic, "summarized", true, "summarized", false},
		{DialectAnthropic, "detailed", true, "summarized", false}, // one setting, both dialects
		{DialectResponses, "summarized", true, "auto", false},
		{DialectResponses, "detailed", true, "detailed", false},
		{DialectResponses, "concise", true, "concise", false},
		{DialectAnthropic, "verbose", false, "", true},
		{DialectResponses, "verbose", false, "", true},
		{DialectOpenAI, "summarized", false, "", true},
	} {
		p := PlanReasoningSummary(tc.dialect, tc.requested)
		if p.Sent != tc.sent || p.Wire != tc.wire || (p.Note != "") != tc.note {
			t.Errorf("%s, entry %q: sent=%v wire=%q note=%q; want sent=%v wire=%q note=%v", tc.dialect, tc.requested, p.Sent, p.Wire, p.Note, tc.sent, tc.wire, tc.note)
		}
	}
	for d, want := range map[Dialect]string{DialectAnthropic: "summarized", DialectResponses: "auto", DialectOpenAI: ""} {
		if got := ReasoningSummaryDefault(d); got != want {
			t.Errorf("%s: the default level is %q, want %q", d, got, want)
		}
		// The default is a level the dialect's own vocabulary holds.
		if want != "" {
			found := false
			for _, v := range ReasoningSummaryLevels(d) {
				found = found || v == want
			}
			if !found {
				t.Errorf("%s: the default %q is not among the levels the dialect accepts %v", d, want, ReasoningSummaryLevels(d))
			}
		}
	}
}

// anthropicBody sends one chat through an anthropic-dialect client and
// returns the request body the server received.
func anthropicBody(t *testing.T, cfg ClientConfig, opts ChatOptions) map[string]any {
	t.Helper()
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&body)
		_, _ = io.WriteString(w, `{"content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`)
	}))
	defer srv.Close()
	cfg.Endpoint, cfg.Provider = srv.URL, "anthropic"
	if cfg.Model == "" {
		cfg.Model = "m"
	}
	if _, err := New(&cfg).Chat(context.Background(), []Message{{Role: "user", Content: "hi"}}, opts); err != nil {
		t.Fatal(err)
	}
	return body
}

func TestTheAnthropicRequestAsksForReadableReasoningByDefault(t *testing.T) {
	display := func(body map[string]any) (any, bool) {
		think, _ := body["thinking"].(map[string]any)
		if think == nil {
			return nil, false
		}
		v, ok := think["display"]
		return v, ok
	}
	// Wherever the request asks the model to think, it asks for the
	// thinking readable.
	if v, _ := display(anthropicBody(t, ClientConfig{APIKey: "k", ThinkingMode: "adaptive"}, ChatOptions{})); v != "summarized" {
		t.Fatalf("adaptive thinking, entry silent: display is %v, want summarized", v)
	}
	if v, _ := display(anthropicBody(t, ClientConfig{APIKey: "k", ThinkingMode: "budget"}, ChatOptions{ThinkingBudget: 2048})); v != "summarized" {
		t.Fatalf("budgeted thinking, entry silent: display is %v, want summarized", v)
	}
	// The entry's "omitted" asks for none, and on the plain API path says
	// nothing: the vendor's default is omitted.
	off := anthropicBody(t, ClientConfig{APIKey: "k", ThinkingMode: "adaptive", ThinkingDisplay: SummaryOmitted}, ChatOptions{})
	if _, present := display(off); present {
		t.Fatalf("the entry said omitted and the request still carries a display: %v", off["thinking"])
	}
	if think, _ := off["thinking"].(map[string]any); think == nil || think["type"] != "adaptive" {
		t.Fatalf("turning readable reasoning off must not turn thinking off: %v", off["thinking"])
	}
	// No thinking asked, no display: the default never invents a thinking
	// parameter to ride on, which is what a model may reject.
	for _, mode := range []string{"", "off"} {
		body := anthropicBody(t, ClientConfig{APIKey: "k", ThinkingMode: mode}, ChatOptions{})
		if _, present := body["thinking"]; present {
			t.Fatalf("mode %q: the default for readable reasoning sent a thinking parameter: %v", mode, body["thinking"])
		}
	}
	if body := anthropicBody(t, ClientConfig{APIKey: "k", ThinkingMode: "budget"}, ChatOptions{}); body["thinking"] != nil {
		t.Fatalf("budgeted thinking with no budget sends none, and the default must not either: %v", body["thinking"])
	}
}

// The subscription path speaks as a captured client, whose own request
// says display "omitted". By default it now asks for the thinking
// readable; turned off, it says "omitted" in that client's word, which is
// the request it sent before.
func TestTheSubscriptionRequestAsksByDefaultAndSaysOmittedWhenToldTo(t *testing.T) {
	send := func(displaySetting string) map[string]any {
		t.Helper()
		var body map[string]any
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			raw, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(raw, &body)
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"m\",\"usage\":{\"input_tokens\":1,\"output_tokens\":0}}}\n\n"+
				"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n"+
				"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"ok\"}}\n\n"+
				"event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n"+
				"event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":1}}\n\n"+
				"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
		}))
		defer srv.Close()
		c := New(&ClientConfig{Endpoint: srv.URL, Provider: "anthropic", Model: "fixture", Credential: fixedCredential{token: "fixture"},
			ClaudeCode: capturedClaudeProfile(), ThinkingMode: "adaptive", ThinkingDisplay: displaySetting})
		if _, err := c.Chat(context.Background(), []Message{{Role: "user", Content: "hello"}}, ChatOptions{}); err != nil {
			t.Fatal(err)
		}
		return body
	}
	if profile := capturedClaudeProfile().ThinkingDisplay; profile != SummaryOmitted {
		t.Fatalf("setup: the shipped profile's own display is %q; this test reads it as the client's word for none", profile)
	}
	for setting, want := range map[string]string{"": "summarized", SummaryOmitted: "omitted", "summarized": "summarized"} {
		think, _ := send(setting)["thinking"].(map[string]any)
		if think == nil || think["type"] != "adaptive" || think["display"] != want {
			t.Errorf("entry %q: thinking is %v, want adaptive with display %q", setting, think, want)
		}
	}
}

func TestTheResponsesRequestAsksForASummaryByDefault(t *testing.T) {
	send := func(cfg ClientConfig) map[string]any {
		t.Helper()
		var body map[string]any
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewDecoder(r.Body).Decode(&body)
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, streamOneItem+"\n"+streamTerminal)
		}))
		defer srv.Close()
		cfg.Endpoint, cfg.APIKey, cfg.Model, cfg.Provider = srv.URL, "k", "gpt-x", "chatgpt"
		if _, err := New(&cfg).Chat(context.Background(), []Message{{Role: "user", Content: "hi"}}, ChatOptions{}); err != nil {
			t.Fatal(err)
		}
		return body
	}
	r, _ := send(ClientConfig{})["reasoning"].(map[string]any)
	if r == nil || r["summary"] != "auto" {
		t.Fatalf("entry silent: reasoning is %v, want a summary asked at auto", r)
	}
	if _, present := r["effort"]; present {
		t.Fatalf("asking for a summary invented an effort: %v", r)
	}
	if r, _ := send(ClientConfig{ThinkingDisplay: "detailed"})["reasoning"].(map[string]any); r == nil || r["summary"] != "detailed" {
		t.Fatalf("entry detailed: reasoning is %v", r)
	}
	if body := send(ClientConfig{ThinkingDisplay: SummaryOmitted}); body["reasoning"] != nil {
		t.Fatalf("the entry said omitted and the request still speaks of reasoning: %v", body["reasoning"])
	}
	// Effort and visibility stay independent levers.
	both, _ := send(ClientConfig{ThinkingDisplay: SummaryOmitted, ReasoningEffort: "high", EffortLevels: []string{"low", "high"}})["reasoning"].(map[string]any)
	if both == nil || both["effort"] != "high" || both["summary"] != nil {
		t.Fatalf("effort without a summary: reasoning is %v", both)
	}
}

// A dialect with no such parameter sends nothing of it, whatever the
// entry says: there is nothing to ask in.
func TestAChatCompletionsRequestCarriesNoReasoningVisibility(t *testing.T) {
	for _, setting := range []string{"", SummaryOmitted, "summarized"} {
		var body map[string]any
		srv, _ := streamedServer(t, func(w http.ResponseWriter, r *http.Request, b map[string]any) {
			body = b
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			sseChunk(t, w, `{"id":"r","choices":[{"index":0,"delta":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`)
			sseChunk(t, w, "[DONE]")
		})
		c := New(&ClientConfig{Endpoint: srv.URL, APIKey: "k", Model: "m", ThinkingDisplay: setting})
		if _, err := c.Chat(context.Background(), []Message{{Role: "user", Content: "hi"}}, ChatOptions{}); err != nil {
			t.Fatal(err)
		}
		for _, key := range []string{"thinking", "reasoning", "display", "summary"} {
			if _, present := body[key]; present {
				t.Fatalf("entry %q: a chat-completions request carries %q: %v", setting, key, body[key])
			}
		}
	}
}
