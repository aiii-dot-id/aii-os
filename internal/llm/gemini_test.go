package llm

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/logsink"
)

// geminiServer answers each request with the next of replies (the last
// one again when they run out) and keeps what it was sent.
type geminiServer struct {
	*httptest.Server
	paths   []string
	headers []http.Header
	bodies  []string
}

func newGeminiServer(t *testing.T, replies ...string) *geminiServer {
	t.Helper()
	g := &geminiServer{}
	g.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		g.paths = append(g.paths, r.URL.RequestURI())
		g.headers = append(g.headers, r.Header.Clone())
		g.bodies = append(g.bodies, string(body))
		reply := replies[min(len(g.bodies), len(replies))-1]
		if strings.HasPrefix(reply, "data:") {
			w.Header().Set("Content-Type", "text/event-stream")
		} else {
			w.Header().Set("Content-Type", "application/json")
		}
		io.WriteString(w, reply)
	}))
	t.Cleanup(g.Close)
	return g
}

func (g *geminiServer) client(cfg ClientConfig) *Client {
	cfg.Endpoint, cfg.Provider = g.URL+"/v1beta", "gemini"
	if cfg.APIKey == "" {
		cfg.APIKey = "fixture-key"
	}
	if cfg.Model == "" {
		cfg.Model = "gemini-fixture"
	}
	return New(&cfg)
}

// sse is a streamed reply: one event per object.
func sse(events ...string) string {
	var b strings.Builder
	for _, e := range events {
		b.WriteString("data: " + e + "\r\n\r\n")
	}
	return b.String()
}

const geminiOK = `{"candidates":[{"content":{"role":"model","parts":[{"text":"ok"}]},"finishReason":"STOP","index":0}],"usageMetadata":{"promptTokenCount":7,"candidatesTokenCount":1,"totalTokenCount":8},"responseId":"r-1"}`

// THE REQUEST, TO THE BYTE. Everything an entry configures reaches the
// API in the member the API names for it, and nothing rides that the API
// does not define: no OpenAI member, no extra, no thinking budget.
func TestGeminiRequestIsPinned(t *testing.T) {
	g := newGeminiServer(t, sse(geminiOK))
	temp, topP := 0.0, 0.9
	c := g.client(ClientConfig{
		Model: "gemini-3.8-flash", MaxOutputTokens: 4096, Temperature: &temp, TopP: &topP,
		ReasoningEffort: "low", EffortLevels: []string{"low", "medium", "high"}, ThinkingMode: "level",
		ThinkingBudget: 8192, Extra: map[string]any{"reasoning_effort": "high", "safetySettings": []any{}},
	})
	tools := []ToolDefinition{{Type: "function", Function: ToolFunction{
		Name: "recall", Description: "Find what was recorded.",
		Parameters: map[string]any{"type": "object", "properties": map[string]any{"query": map[string]any{"type": []any{"string", "null"}, "minLength": 1, "pattern": `^\S`}}, "required": []any{"query"}, "additionalProperties": false},
	}}}
	resp, err := c.Chat(context.Background(), []Message{
		{Role: "system", Content: "You are Ada.", StableLen: 4},
		{Role: "system", Content: "Today is Monday."},
		{Role: "user", Content: "What did we decide?"},
	}, ChatOptions{Tools: tools, RequireTool: true})
	if err != nil {
		t.Fatal(err)
	}
	const want = `{"systemInstruction":{"parts":[{"text":"You are Ada.\n\nToday is Monday."}]},` +
		`"contents":[{"role":"user","parts":[{"text":"What did we decide?"}]}],` +
		`"tools":[{"functionDeclarations":[{"name":"recall","description":"Find what was recorded.","parametersJsonSchema":{"additionalProperties":false,"properties":{"query":{"minLength":1,"pattern":"^\\S","type":["string","null"]}},"required":["query"],"type":"object"}}]}],` +
		`"toolConfig":{"functionCallingConfig":{"mode":"ANY"}},` +
		`"generationConfig":{"maxOutputTokens":4096,"temperature":0,"topP":0.9,"thinkingConfig":{"thinkingLevel":"LOW","includeThoughts":true}}}`
	if g.bodies[0] != want {
		t.Errorf("the request changed:\n got %s\nwant %s", g.bodies[0], want)
	}
	if g.paths[0] != "/v1beta/models/gemini-3.8-flash:streamGenerateContent?alt=sse" {
		t.Errorf("the request went to %s", g.paths[0])
	}
	h := g.headers[0]
	if h.Get("x-goog-api-key") != "fixture-key" || h.Get("Authorization") != "" || h.Get("Content-Type") != "application/json" || h.Get("Accept") != "text/event-stream" {
		t.Errorf("the key rides in x-goog-api-key and nowhere else; headers: %v", h)
	}
	if got := resp.Choices[0]; got.Message.Content != "ok" || got.FinishReason != "stop" || resp.ID != "r-1" || resp.ModelID != "gemini-3.8-flash" {
		t.Errorf("the reply read as %+v", resp)
	}
}

// An entry's extra members are not sent on this dialect, and the client
// says so, once: an entry written for the OpenAI-shaped route an earlier
// release gave Gemini may state some, and they were sent there.
func TestGeminiSaysOnceThatAnEntrysExtraIsNotSent(t *testing.T) {
	logs := logsink.CaptureForTest(t)
	g := newGeminiServer(t, sse(geminiOK))
	c := g.client(ClientConfig{Extra: map[string]any{"service_tier": "flex", "prompt_cache_key": "k"}})
	for range 2 {
		if _, err := c.Chat(context.Background(), []Message{{Role: "user", Content: "hi"}}, ChatOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	if said := logs.String(); strings.Count(said, "none of them is sent") != 1 || !strings.Contains(said, "prompt_cache_key, service_tier") {
		t.Errorf("the client said: %q", said)
	}
	if strings.Contains(g.bodies[0], "service_tier") {
		t.Errorf("an extra member was sent: %s", g.bodies[0])
	}
	logs = logsink.CaptureForTest(t)
	if _, err := g.client(ClientConfig{}).Chat(context.Background(), []Message{{Role: "user", Content: "hi"}}, ChatOptions{}); err != nil || logs.String() != "" {
		t.Errorf("an entry with no extra: %v; said %q", err, logs.String())
	}
}

func TestGeminiRequestDefaultsAndRefusedSettings(t *testing.T) {
	for _, tc := range []struct {
		name string
		cfg  ClientConfig
		opts ChatOptions
		want string // the request after its contents
	}{
		{"nothing configured, a model nobody has said thinks", ClientConfig{}, ChatOptions{},
			`"generationConfig":{"maxOutputTokens":8192}}`},
		{"a model that thinks", ClientConfig{ThinkingMode: "level"}, ChatOptions{},
			`"generationConfig":{"maxOutputTokens":8192,"thinkingConfig":{"includeThoughts":true}}}`},
		{"readable reasoning declined", ClientConfig{ThinkingMode: "level", ThinkingDisplay: SummaryOmitted}, ChatOptions{},
			`"generationConfig":{"maxOutputTokens":8192}}`},
		{"a level this model does not take is not sent", ClientConfig{ThinkingMode: "level", ThinkingDisplay: SummaryOmitted, ReasoningEffort: "minimal", EffortLevels: []string{"low", "medium", "high"}}, ChatOptions{},
			`"generationConfig":{"maxOutputTokens":8192}}`},
		{"a level the operator set, on a model with no row", ClientConfig{ReasoningEffort: "high"}, ChatOptions{},
			`"generationConfig":{"maxOutputTokens":8192,"thinkingConfig":{"thinkingLevel":"HIGH"}}}`},
		{"the operator's off sends none of it", ClientConfig{ThinkingMode: "off", ReasoningEffort: "high"}, ChatOptions{},
			`"generationConfig":{"maxOutputTokens":8192}}`},
		{"tools withheld for one call keep their declarations", ClientConfig{},
			ChatOptions{Tools: []ToolDefinition{{Type: "function", Function: ToolFunction{Name: "noop", Description: "Nothing."}}}, DisableTools: true},
			`"tools":[{"functionDeclarations":[{"name":"noop","description":"Nothing."}]}],"toolConfig":{"functionCallingConfig":{"mode":"NONE"}},"generationConfig":{"maxOutputTokens":8192}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := newGeminiServer(t, sse(geminiOK))
			if _, err := g.client(tc.cfg).Chat(context.Background(), []Message{{Role: "user", Content: "hi"}}, tc.opts); err != nil {
				t.Fatal(err)
			}
			if want := `{"contents":[{"role":"user","parts":[{"text":"hi"}]}],` + tc.want; g.bodies[0] != want {
				t.Errorf("request:\n got %s\nwant %s", g.bodies[0], want)
			}
		})
	}
}

// A request of a system message and nothing else (the firstboot prompt)
// is that text as the user's content, with nothing added: the API takes no
// request without a content.
func TestGeminiSystemOnlyRequestIsTheSameTextAsTheUsers(t *testing.T) {
	g := newGeminiServer(t, sse(geminiOK))
	c := g.client(ClientConfig{})
	if _, err := c.Chat(context.Background(), []Message{{Role: "system", Content: "The founding text."}}, ChatOptions{}); err != nil {
		t.Fatal(err)
	}
	if want := `{"contents":[{"role":"user","parts":[{"text":"The founding text."}]}],"generationConfig":{"maxOutputTokens":8192}}`; g.bodies[0] != want {
		t.Errorf("request:\n got %s\nwant %s", g.bodies[0], want)
	}
}

// The two parts of this reply that carry what must come back: the first
// call of a parallel pair, signed, and a member this build does not know.
const (
	signedCall   = `{"functionCall":{"name":"weather","args":{"city":"Paris"},"id":"fc-1"},"thoughtSignature":"U0lHLUE=","futureMember":{"kept":true}}`
	unsignedCall = `{"functionCall":{"name":"weather","args":{"city":"London"}}}`
)

// A GEMINI 3 MODEL'S SIGNATURES COME BACK EXACTLY AS THEY ARRIVED. The
// API refuses the next request of a turn, with a 400, when the first
// function call of a step returns without the signature it was sent
// with; and it refuses parallel calls whose results are interleaved with
// them. So within the turn the reply's parts go back as they came — the
// streamed text joined, everything else untouched — and the results
// follow as one content, in the calls' order, each naming its function,
// with the API's id for a call only where the API gave one.
func TestGeminiReplaysSignedPartsWithinTheTurn(t *testing.T) {
	first := sse(
		`{"candidates":[{"content":{"role":"model","parts":[{"text":"Weighing the two ","thought":true}]},"index":0}]}`,
		`{"candidates":[{"content":{"role":"model","parts":[{"text":"cities.","thought":true}]},"index":0}]}`,
		`{"candidates":[{"content":{"role":"model","parts":[{"text":"Checking "}]},"index":0}]}`,
		`{"candidates":[{"content":{"role":"model","parts":[{"text":"both."}]},"index":0}]}`,
		`{"candidates":[{"content":{"role":"model","parts":[`+signedCall+`,`+unsignedCall+`]},"finishReason":"STOP","index":0}],"usageMetadata":{"promptTokenCount":40,"candidatesTokenCount":12,"thoughtsTokenCount":30,"totalTokenCount":82,"cachedContentTokenCount":16},"responseId":"r-7"}`,
	)
	g := newGeminiServer(t, first, sse(geminiOK))
	c := g.client(ClientConfig{})
	convo := []Message{{Role: "user", Content: "Weather in Paris and London?"}}
	resp, err := c.Chat(context.Background(), convo, ChatOptions{})
	if err != nil {
		t.Fatal(err)
	}
	choice := resp.Choices[0]
	msg := choice.Message
	if msg.Content != "Checking both." || choice.ReasoningAccount != "Weighing the two cities." || choice.FinishReason != "tool_calls" || choice.Reasoning != "" {
		t.Fatalf("the reply read as content %q, account %q, finish %q", msg.Content, choice.ReasoningAccount, choice.FinishReason)
	}
	if len(msg.ToolCalls) != 2 || msg.ToolCalls[0].ID != "fc-1" || msg.ToolCalls[0].Function.Name != "weather" || msg.ToolCalls[0].Function.Arguments != `{"city":"Paris"}` ||
		!strings.HasPrefix(msg.ToolCalls[1].ID, "call_") || msg.ToolCalls[1].Function.Arguments != `{"city":"London"}` {
		t.Fatalf("the calls read as %+v", msg.ToolCalls)
	}
	wantUsage := Usage{PromptTokens: 40, CompletionTokens: 42, TotalTokens: 82, Reported: true, CachedPromptTokens: 16, CacheReadReported: true}
	if resp.Usage != wantUsage || msg.OutputTokens != 42 {
		t.Fatalf("usage read as %+v (output on the message %d), want %+v", resp.Usage, msg.OutputTokens, wantUsage)
	}

	convo = append(convo, msg,
		Message{Role: "tool", ToolCallID: msg.ToolCalls[0].ID, Content: `{"temp":"15C"}`},
		Message{Role: "tool", ToolCallID: msg.ToolCalls[1].ID, Content: ""},
		Message{Role: "user", Content: "Be brief."})
	if _, err := c.Chat(context.Background(), convo, ChatOptions{}); err != nil {
		t.Fatal(err)
	}
	want := `{"contents":[{"role":"user","parts":[{"text":"Weather in Paris and London?"}]},` +
		`{"role":"model","parts":[{"text":"Weighing the two cities.","thought":true},{"text":"Checking both."},` + signedCall + `,` + unsignedCall + `]},` +
		`{"role":"user","parts":[{"functionResponse":{"id":"fc-1","name":"weather","response":{"output":"{\"temp\":\"15C\"}"}}},{"functionResponse":{"name":"weather","response":{"output":""}}},{"text":"Be brief."}]}],` +
		`"generationConfig":{"maxOutputTokens":8192}}`
	if g.bodies[1] != want {
		t.Errorf("the turn's second request:\n got %s\nwant %s", g.bodies[1], want)
	}
}

// A PART THAT CARRIES A SIGNATURE IS NEVER JOINED TO ANOTHER ("Don't merge
// one part with a signature with another part without a signature", the
// thinking guide). The deltas of one text are one part; the part that
// brings the reply's signature stays its own, empty text and all ("the
// model may return the thought signature in a part with an empty text
// content part"); a first part that is signed (a Gemini 2.5 model's) is
// not grown by the text after it; and a part with a member this build
// does not know is left as it came. The words read the same either way.
func TestGeminiASignedPartIsNeverJoined(t *testing.T) {
	const (
		signedEnd   = `{"text":"","thoughtSignature":"U0lHLUVORA=="}`
		signedFirst = `{"text":"Hel","thoughtSignature":"U0lHLTI1"}`
		signedThink = `{"text":"Weigh","thought":true,"thoughtSignature":"U0lHLVQ="}`
		unknown     = `{"text":"!","futureMember":1}`
	)
	event := func(parts, tail string) string {
		return `{"candidates":[{"content":{"parts":[` + parts + `]}` + tail + `,"index":0}]}`
	}
	g := newGeminiServer(t, sse(
		event(signedThink, ``), event(`{"text":"ing it.","thought":true}`, ``),
		event(signedFirst, ``), event(`{"text":"lo"}`, ``), event(`{"text":" there"}`, ``), event(unknown, ``), event(`{"text":" Bye"}`, ``),
		event(signedEnd, `,"finishReason":"STOP"`),
	), sse(geminiOK))
	c := g.client(ClientConfig{})
	convo := []Message{{Role: "user", Content: "Hi."}}
	resp, err := c.Chat(context.Background(), convo, ChatOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if got := resp.Choices[0]; got.Message.Content != "Hello there! Bye" || got.ReasoningAccount != "Weighing it." {
		t.Fatalf("the reply read as %q, its account as %q", got.Message.Content, got.ReasoningAccount)
	}
	if _, err := c.Chat(context.Background(), append(convo, resp.Choices[0].Message, Message{Role: "user", Content: "And?"}), ChatOptions{}); err != nil {
		t.Fatal(err)
	}
	want := `{"role":"model","parts":[` + signedThink + `,{"text":"ing it.","thought":true},` + signedFirst + `,{"text":"lo there"},` + unknown + `,{"text":" Bye"},` + signedEnd + `]}`
	if !strings.Contains(g.bodies[1], want) {
		t.Errorf("the parts went back as\n%s\nwant them to hold\n%s", g.bodies[1], want)
	}
}

// A call the API did not return in this turn has no signature to carry:
// one rebuilt from the record, or made by another model before a switch.
// The first call of each such message carries the value Google documents
// for exactly that, the later ones none, and nothing of another dialect's
// blocks is sent.
func TestGeminiCallFromAnotherSourceCarriesTheBypassSignature(t *testing.T) {
	g := newGeminiServer(t, sse(geminiOK))
	call := func(id, name, args string) ToolCall {
		tc := ToolCall{ID: id, Type: "function"}
		tc.Function.Name, tc.Function.Arguments = name, args
		return tc
	}
	convo := []Message{
		{Role: "user", Content: "Go."},
		{Role: "assistant", Content: "On it.", ToolCalls: []ToolCall{call("toolu_1", "read", `{"path":"a"}`), call("toolu_2", "read", "not json")},
			NativeDialect: "anthropic", NativeContent: json.RawMessage(`[{"type":"thinking","thinking":"x","signature":"s"}]`)},
		{Role: "tool", ToolCallID: "toolu_1", Content: "A"},
		{Role: "tool", ToolCallID: "toolu_2", Content: "B"},
	}
	if _, err := g.client(ClientConfig{}).Chat(context.Background(), convo, ChatOptions{}); err != nil {
		t.Fatal(err)
	}
	want := `{"contents":[{"role":"user","parts":[{"text":"Go."}]},` +
		`{"role":"model","parts":[{"text":"On it."},{"thoughtSignature":"skip_thought_signature_validator","functionCall":{"name":"read","args":{"path":"a"}}},{"functionCall":{"name":"read","args":{}}}]},` +
		`{"role":"user","parts":[{"functionResponse":{"name":"read","response":{"output":"A"}}},{"functionResponse":{"name":"read","response":{"output":"B"}}}]}],` +
		`"generationConfig":{"maxOutputTokens":8192}}`
	if g.bodies[0] != want {
		t.Errorf("request:\n got %s\nwant %s", g.bodies[0], want)
	}
}

// A function response must name its function, and the name is the
// call's. A result whose call is not in the request is refused before
// anything is sent, naming the result.
func TestGeminiToolResultWithoutItsCallIsRefused(t *testing.T) {
	g := newGeminiServer(t, sse(geminiOK))
	_, err := g.client(ClientConfig{}).Chat(context.Background(), []Message{
		{Role: "user", Content: "Go."}, {Role: "tool", ToolCallID: "gone", Content: "x"},
	}, ChatOptions{})
	if err == nil || !strings.Contains(err.Error(), `"gone"`) || len(g.bodies) != 0 {
		t.Fatalf("an orphan result: err=%v, requests sent=%d", err, len(g.bodies))
	}
}

// Thinking is output: the API counts it apart from the answer, bills
// both, and its total is their sum with the prompt. A count the API
// leaves out beside a present total is its zero (proto3 JSON omits a
// zero); a block without a prompt count or a total is missing, never
// zero; and counts that do not add up are no measurement: the reply is
// kept, its usage is usage that was not reported, and what the API sent
// is carried in words for the log.
func TestGeminiUsage(t *testing.T) {
	reply := func(usage string) string {
		return sse(`{"candidates":[{"content":{"parts":[{"text":"ok"}]},"finishReason":"STOP"}]` + usage + `}`)
	}
	for _, tc := range []struct {
		name, usage string
		want        Usage
	}{
		{"thinking and answer", `,"usageMetadata":{"promptTokenCount":10,"candidatesTokenCount":3,"thoughtsTokenCount":20,"totalTokenCount":33}`,
			Usage{PromptTokens: 10, CompletionTokens: 23, TotalTokens: 33, Reported: true}},
		{"an answer of no tokens", `,"usageMetadata":{"promptTokenCount":10,"totalTokenCount":10}`,
			Usage{PromptTokens: 10, TotalTokens: 10, Reported: true}},
		{"a cached prefix and a tool-use prompt", `,"usageMetadata":{"promptTokenCount":100,"toolUsePromptTokenCount":5,"candidatesTokenCount":2,"totalTokenCount":107,"cachedContentTokenCount":64}`,
			Usage{PromptTokens: 105, CompletionTokens: 2, TotalTokens: 107, Reported: true, CachedPromptTokens: 64, CacheReadReported: true}},
		{"no usage block", ``, Usage{Problem: "missing_usage"}},
		{"no total", `,"usageMetadata":{"promptTokenCount":10,"candidatesTokenCount":3}`, Usage{Problem: "missing_usage"}},
		{"counts that do not add up", `,"usageMetadata":{"promptTokenCount":10,"candidatesTokenCount":3,"totalTokenCount":99}`, Usage{Problem: "invalid_usage", Refused: "input=10 output=3 total=99 cache_read=0 cache_write=0 cache_write_5m=0 cache_write_1h=0"}},
		{"a cache larger than the prompt", `,"usageMetadata":{"promptTokenCount":10,"candidatesTokenCount":3,"totalTokenCount":13,"cachedContentTokenCount":11}`, Usage{Problem: "invalid_usage", Refused: "input=10 output=3 total=13 cache_read=11 cache_write=0 cache_write_5m=0 cache_write_1h=0"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := newGeminiServer(t, reply(tc.usage))
			resp, err := g.client(ClientConfig{}).Chat(context.Background(), []Message{{Role: "user", Content: "hi"}}, ChatOptions{})
			if err != nil || len(resp.Choices) != 1 || resp.Choices[0].Message.Content != "ok" {
				t.Fatalf("err = %v, reply = %+v: a finished reply is kept whatever its usage says", err, resp)
			}
			if resp.Usage != tc.want {
				t.Errorf("usage = %+v, want %+v", resp.Usage, tc.want)
			}
		})
	}
}

// How a reply ended reaches the caller in the package's words, and a
// reason the package has no word for as the API named it. A stream that
// ends without one is truncated, and an error event is the provider's.
func TestGeminiFinishReasons(t *testing.T) {
	candidate := func(parts, reason string) string {
		return `{"candidates":[{"content":{"parts":[` + parts + `]},"finishReason":"` + reason + `"}],"usageMetadata":{"promptTokenCount":1,"totalTokenCount":1}}`
	}
	for _, tc := range []struct {
		name, stream, finish, errHas string
	}{
		{"an ordinary end", sse(candidate(`{"text":"done"}`, "STOP")), "stop", ""},
		{"a call", sse(candidate(`{"functionCall":{"name":"f","args":{}}}`, "STOP")), "tool_calls", ""},
		{"the output limit", sse(candidate(`{"text":"half a sen"}`, "MAX_TOKENS")), "length", ""},
		{"a reply the API withheld", sse(candidate(``, "SAFETY")), "refusal", ""},
		{"a reply it stopped for recitation", sse(candidate(`{"text":"It was the"}`, "RECITATION")), "refusal", ""},
		{"a prompt it refused", sse(`{"promptFeedback":{"blockReason":"OTHER"},"usageMetadata":{"promptTokenCount":1,"totalTokenCount":1}}`), "refusal", ""},
		{"a call it could not parse", sse(candidate(``, "MALFORMED_FUNCTION_CALL")), "MALFORMED_FUNCTION_CALL", ""},
		{"a signature it missed", sse(candidate(``, "MISSING_THOUGHT_SIGNATURE")), "MISSING_THOUGHT_SIGNATURE", ""},
		{"a stream cut short", sse(`{"candidates":[{"content":{"parts":[{"text":"half"}]}}]}`), "", "truncated, not complete"},
		{"an error event", sse(`{"candidates":[{"content":{"parts":[{"text":"half"}]}}]}`, `{"error":{"code":503,"message":"The model is overloaded.","status":"UNAVAILABLE"}}`), "", "provider reported: The model is overloaded."},
		{"an event that is not JSON", "data: {not json\r\n\r\n", "", "decode stream event"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := newGeminiServer(t, tc.stream)
			resp, err := g.client(ClientConfig{}).Chat(context.Background(), []Message{{Role: "user", Content: "hi"}}, ChatOptions{})
			if tc.errHas != "" {
				if err == nil || !strings.Contains(err.Error(), tc.errHas) {
					t.Fatalf("err = %v, want one saying %q", err, tc.errHas)
				}
				return
			}
			if err != nil || resp.Choices[0].FinishReason != tc.finish {
				t.Fatalf("finish = %+v (err %v), want %q", resp, err, tc.finish)
			}
		})
	}
}

// The facility doors mint what comes back, so they take a Gemini reply
// only when it ended ordinarily: text as text, a call's arguments as the
// structured payload, and anything else a typed refusal with no product.
func TestGeminiFacilityDoorsRefuseAnUnfinishedReply(t *testing.T) {
	const text, args = "é界🙂", `{"operations":[]}`
	reply := func(parts, reason string) string {
		return sse(`{"candidates":[{"content":{"parts":[` + parts + `]},"finishReason":"` + reason + `"}],"usageMetadata":{"promptTokenCount":1,"candidatesTokenCount":1,"totalTokenCount":2}}`)
	}
	tool := ToolDefinition{Type: "function", Function: ToolFunction{Name: "commit", Parameters: map[string]any{"type": "object"}}}
	for _, tc := range []struct {
		wire, reason string
	}{{"STOP", ""}, {"MAX_TOKENS", "length"}, {"SAFETY", "refusal"}} {
		t.Run(tc.wire, func(t *testing.T) {
			c := newGeminiServer(t, reply(`{"text":"`+text+`"}`, tc.wire)).client(ClientConfig{MaxInputTokens: 100000})
			got, _, _, err := c.ChatSimple(context.Background(), "system", "user")
			structured, _, viaTool, _, serr := newGeminiServer(t, reply(`{"functionCall":{"name":"commit","args":`+args+`}}`, tc.wire)).client(ClientConfig{}).ChatStructured(context.Background(), "system", "user", tool)
			if tc.reason == "" {
				if err != nil || got != text || serr != nil || structured != args || !viaTool {
					t.Fatalf("an ordinary reply: text %q (%v), structured %q via tool %t (%v)", got, err, structured, viaTool, serr)
				}
				return
			}
			var incomplete, sincomplete *IncompleteResponseError
			if !errors.As(err, &incomplete) || got != "" || incomplete.Reason != tc.reason || incomplete.Got != 3 {
				t.Errorf("an unfinished text reply was a product: %q, %v", got, err)
			}
			if !errors.As(serr, &sincomplete) || structured != "" || viaTool || sincomplete.Reason != tc.reason {
				t.Errorf("an unfinished structured reply was a product: %q, %v", structured, serr)
			}
		})
	}
}

// llm.stream=false asks generateContent and reads the reply whole; and a
// server that answers the streamed request whole is read as it answered.
func TestGeminiUnstreamed(t *testing.T) {
	g := newGeminiServer(t, geminiOK)
	resp, err := g.client(ClientConfig{NoStream: true}).Chat(context.Background(), []Message{{Role: "user", Content: "hi"}}, ChatOptions{})
	if err != nil || resp.Choices[0].Message.Content != "ok" || g.paths[0] != "/v1beta/models/gemini-fixture:generateContent" || g.headers[0].Get("Accept") == "text/event-stream" {
		t.Fatalf("unstreamed: %+v, %v, path %s", resp, err, g.paths)
	}
	g = newGeminiServer(t, geminiOK)
	resp, err = g.client(ClientConfig{}).Chat(context.Background(), []Message{{Role: "user", Content: "hi"}}, ChatOptions{})
	if err != nil || resp.Choices[0].Message.Content != "ok" || !strings.HasSuffix(g.paths[0], ":streamGenerateContent?alt=sse") {
		t.Fatalf("a whole answer to a streamed request: %+v, %v", resp, err)
	}
	if _, err := newGeminiServer(t, `{"candidates":[{"content":{"parts":[{"text":"half"}]}}]}`).client(ClientConfig{NoStream: true}).Chat(context.Background(), []Message{{Role: "user", Content: "hi"}}, ChatOptions{}); err == nil || !strings.Contains(err.Error(), "no finish reason") {
		t.Fatalf("a whole reply with no finish reason was taken as finished: %v", err)
	}
}

func TestGeminiStreamIsReadUnderTheIdleWatch(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, `data: {"candidates":[{"content":{"parts":[{"text":"half"}]}}]}`+"\r\n\r\n")
		w.(http.Flusher).Flush()
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer srv.Close()
	defer close(release)
	c := New(&ClientConfig{Endpoint: srv.URL, APIKey: "k", Model: "m", Provider: "gemini", Retries: -1})
	c.idle = time.Second
	_, err := c.Chat(context.Background(), []Message{{Role: "user", Content: "hi"}}, ChatOptions{})
	if err == nil || !strings.Contains(err.Error(), "llm.timeout_seconds") {
		t.Fatalf("a quiet stream was not refused by the idle watch: %v", err)
	}
}

// Gemini's embeddings are batchEmbedContents: one request per input, each
// naming the model, and the vectors back in input order.
func TestGeminiEmbeddings(t *testing.T) {
	g := newGeminiServer(t, `{"embeddings":[{"values":[0.5,1]},{"values":[2,3]}]}`)
	got, err := g.client(ClientConfig{}).Embed(context.Background(), "gemini-embedding-2", []string{"a", "b"})
	if err != nil || len(got) != 2 || got[0][0] != 0.5 || got[1][1] != 3 {
		t.Fatalf("vectors = %v, %v", got, err)
	}
	const want = `{"requests":[{"model":"models/gemini-embedding-2","content":{"parts":[{"text":"a"}]}},{"model":"models/gemini-embedding-2","content":{"parts":[{"text":"b"}]}}]}`
	if g.bodies[0] != want || g.paths[0] != "/v1beta/models/gemini-embedding-2:batchEmbedContents" || g.headers[0].Get("x-goog-api-key") != "fixture-key" {
		t.Errorf("request to %s:\n got %s\nwant %s", g.paths[0], g.bodies[0], want)
	}
	if _, err := newGeminiServer(t, `{"embeddings":[{"values":[1]}]}`).client(ClientConfig{}).Embed(context.Background(), "m", []string{"a", "b"}); err == nil {
		t.Error("one vector for two inputs was taken")
	}
}

// A credential source's bearer authorizes the request in place of a key:
// Google takes an OAuth token in Authorization, and no key header rides
// beside it.
func TestGeminiCredentialSourceRidesAsBearer(t *testing.T) {
	g := newGeminiServer(t, sse(geminiOK))
	c := New(&ClientConfig{Endpoint: g.URL, Provider: "gemini", Model: "m", Credential: staticCredential{"oauth-token"}})
	if _, err := c.Chat(context.Background(), []Message{{Role: "user", Content: "hi"}}, ChatOptions{}); err != nil {
		t.Fatal(err)
	}
	if h := g.headers[0]; h.Get("Authorization") != "Bearer oauth-token" || h.Get("x-goog-api-key") != "" {
		t.Errorf("headers: %v", h)
	}
}

type staticCredential struct{ token string }

func (s staticCredential) Credential(context.Context) (Credential, error) {
	return Credential{Token: s.token, Headers: map[string]string{"x-goog-api-key": "never"}}, nil
}
func (staticCredential) Stale(context.Context, uint64) error { return nil }

// A context change resets provider reasoning that is bound to what
// preceded it. A Gemini reply's parts are not reset: the API validates
// that a function call of the current turn still carries its signature,
// so a reset would turn a request the API takes into one it refuses.
func TestResetThinkingLeavesGeminiPartsAsTheyCame(t *testing.T) {
	native := json.RawMessage(`[{"text":"t","thought":true},` + signedCall + `]`)
	messages := []Message{{Role: "assistant", NativeDialect: "gemini", NativeContent: native, OutputTokens: 9}}
	if n := ResetThinking(messages); n != 0 || string(messages[0].NativeContent) != string(native) || messages[0].OutputTokens != 9 {
		t.Fatalf("reset removed %d block(s) and left %s", n, messages[0].NativeContent)
	}
}

// The cache policy of a Gemini entry is the API's own: it caches a
// repeated prefix by itself, and a request carries no setting for it.
func TestGeminiCachePolicy(t *testing.T) {
	for _, ok := range []CachePolicy{{}, {Mode: CacheAuto}} {
		if err := ValidateCachePolicy(ok, DialectGemini, false); err != nil {
			t.Errorf("%+v refused: %v", ok, err)
		}
	}
	for _, refused := range []CachePolicy{{Mode: CacheOff}, {Mode: CacheExplicit}, {TTL: "1h"}, {TailTTL: "5m"}, {Key: "k"}, {Diagnostics: true}} {
		var policy *CachePolicyError
		if err := ValidateCachePolicy(refused, DialectGemini, true); !errors.As(err, &policy) {
			t.Errorf("%+v was taken: %v", refused, err)
		}
	}
	if modes := CacheModes(DialectGemini, true); len(modes) != 1 || modes[0] != string(CacheAuto) || CacheTTLs(DialectGemini, true, []string{"30m"}) != nil {
		t.Errorf("a Gemini entry is offered modes %v and TTLs %v", modes, CacheTTLs(DialectGemini, true, []string{"30m"}))
	}
}
