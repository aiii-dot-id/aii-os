package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"uuid"

	config "github.com/aiii-dot-id/aii-os/config"
)

func capturedClaudeProfile() *ClaudeCodeProfile {
	var reg struct {
		Providers []struct {
			CredentialOptions map[string]string  `json:"credential_options"`
			Profile           *ClaudeCodeProfile `json:"claude_code"`
		} `json:"providers"`
	}
	if err := json.Unmarshal(config.Providers, &reg); err != nil {
		panic(err)
	}
	for _, e := range reg.Providers {
		if e.Profile != nil {
			e.Profile.Version = e.CredentialOptions["client_version"]
			return e.Profile
		}
	}
	panic("shipped Claude profile absent")
}

// This synthetic request has fixed IDs and no captured environment or user data.
// Its checksum was computed independently with libxxhash XXH64 and the public
// normalization rules; the expected signed bytes are frozen in the fixture.
func TestClaudeCodeSyntheticChecksum(t *testing.T) {
	captured, err := os.ReadFile("testdata/claude-code-2.1.283-request.json")
	if err != nil {
		t.Fatal(err)
	}
	captured = bytes.TrimSpace(captured)
	unsigned := bytes.Replace(captured, []byte("cch=8ac5e;"), []byte("cch=00000;"), 1)
	got, err := signClaudeBody(unsigned, capturedClaudeProfile().Checksum)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, captured) {
		t.Fatal("checksum does not reproduce the frozen synthetic vector")
	}
	if got := claudeFingerprint("Reply with the single word: alive", capturedClaudeProfile()); got != "aba" {
		t.Fatalf("fixed fingerprint: %s", got)
	}
	for _, tc := range []struct{ in, want string }{
		{`{"model":"opus","max_tokens":12,"messages":[{"content":"model cch=00000;"}]}`, `{"model":"","messages":[{"content":"model cch=00000;"}]}`},
		{`{"a":1,"max_tokens":2,"fallbacks":[]}`, `{"a":1,}`},
		{`{"tool":{"model":"x","max_tokens":2,"keep":false}}`, `{"tool":{"model":"","keep":false}}`},
		{`{"max_tokens":2,"fallbacks":[]}`, `{}`},
		{`{"a":1,"max_tokens":2}`, `{"a":1}`},
	} {
		got, err := normalizeClaudeHash([]byte(tc.in), capturedClaudeProfile().Checksum)
		if err != nil || string(got) != tc.want {
			t.Errorf("%s: %s, %v; want %s", tc.in, got, err, tc.want)
		}
	}
}

func TestClaudeProfileRejectsInvalidOptions(t *testing.T) {
	for _, mutate := range []func(*ClaudeCodeProfile){func(p *ClaudeCodeProfile) { p.Checksum.Seed = "bad" }, func(p *ClaudeCodeProfile) { p.RequestPath = "//other.example/messages" }, func(p *ClaudeCodeProfile) { p.Headers["Authorization"] = "injected" }, func(p *ClaudeCodeProfile) { p.FingerprintPositions = []int{-1} }, func(p *ClaudeCodeProfile) { p.AccountUUID = "bad" }, func(p *ClaudeCodeProfile) { p.Checksum.HexLength = 17 }} {
		p := capturedClaudeProfile()
		mutate(p)
		if err := p.Validate(); err == nil {
			t.Fatal("invalid profile accepted")
		}
	}
	if err := capturedClaudeProfile().Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestClaudeProfileRequestIsolation(t *testing.T) {
	var mu sync.Mutex
	var requests []map[string]any
	var sessions, prompts, requestIDs []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		var req map[string]any
		if err := json.Unmarshal(b, &req); err != nil {
			t.Error(err)
		}
		if r.URL.RequestURI() != "/v1/messages?beta=true" {
			t.Errorf("path %s", r.URL)
		}
		if r.Header.Get("Authorization") != "Bearer fixture" || r.Header.Get("x-api-key") != "" {
			t.Error("credential routing changed")
		}
		if r.Header.Get("User-Agent") != "claude-cli/2.1.283 (external, sdk-cli)" {
			t.Error("wrong client profile")
		}
		sid, pid, rid := r.Header.Get("X-Claude-Code-Session-Id"), r.Header.Get("x-claude-code-prompt-id"), r.Header.Get("x-client-request-id")
		for _, v := range []string{sid, pid, rid} {
			if _, err := uuid.Parse(v); err != nil {
				t.Error(err)
			}
		}
		billing := req["system"].([]any)[0].(map[string]any)["text"].(string)
		if !strings.Contains(billing, "cc_prompt_id="+pid+";") {
			t.Error("header/body prompt id mismatch")
		}
		start := strings.Index(billing, "cch=") + 4
		unsigned := bytes.Replace(b, []byte("cch="+billing[start:start+5]+";"), []byte("cch=00000;"), 1)
		signed, err := signClaudeBody(unsigned, capturedClaudeProfile().Checksum)
		if err != nil || !bytes.Equal(signed, b) {
			t.Errorf("bad request checksum: %v", err)
		}
		var metadata map[string]string
		if err := json.Unmarshal([]byte(req["metadata"].(map[string]any)["user_id"].(string)), &metadata); err != nil {
			t.Error(err)
		}
		if metadata["session_id"] != sid || len(metadata["device_id"]) != 64 || metadata["account_uuid"] != "" {
			t.Error("metadata mismatch")
		}
		if req["stream"] != true || req["diagnostics"] == nil || req["tools"] == nil {
			t.Error("profile envelope missing")
		}
		if req["system"].([]any)[1].(map[string]any)["text"] != "resident identity" {
			t.Error("identity changed")
		}
		mu.Lock()
		requests = append(requests, req)
		sessions = append(sessions, sid)
		prompts = append(prompts, pid)
		requestIDs = append(requestIDs, rid)
		mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, anthropicFixtureStream("end_turn", `null`))
	}))
	defer srv.Close()
	c := New(&ClientConfig{Endpoint: srv.URL, Provider: "anthropic", Model: "fixture", Credential: fixedCredential{token: "fixture"}, ClaudeCode: capturedClaudeProfile()})
	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			resp, err := c.Chat(context.Background(), []Message{{Role: "system", Content: "resident identity"}, {Role: "user", Content: "keep cch=00000; literally"}}, ChatOptions{})
			if err != nil {
				t.Error(err)
			} else if resp.Choices[0].Message.Content != "alive" {
				t.Error("stream text lost")
			}
		}()
	}
	wg.Wait()
	if len(requests) != 4 {
		t.Fatalf("requests %d", len(requests))
	}
	seen := map[string]bool{}
	for i := range sessions {
		if sessions[i] != sessions[0] {
			t.Error("session changed on one client")
		}
		for _, id := range []string{prompts[i], requestIDs[i]} {
			if seen[id] {
				t.Error("correlation id reused across calls")
			}
			seen[id] = true
		}
	}
}

func anthropicFixtureStream(reason, details string) string {
	return `event: message_start
data: {"type":"message_start","message":{"id":"msg_fixture","content":[],"usage":{"input_tokens":11,"output_tokens":0}}}

event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":"","vendor_field":"preserve"}}

data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"alive"}}

data: {"type":"content_block_stop","index":0}

data: {"type":"message_delta","delta":{"stop_reason":"` + reason + `","stop_details":` + details + `},"usage":{"output_tokens":4}}

data: {"type":"message_stop"}

`
}

func TestAnthropicStreamCompletionAndRefusal(t *testing.T) {
	for _, reason := range []string{"end_turn", "refusal", "max_tokens", "pause_turn"} {
		a, err := readAnthropicStream(strings.NewReader(anthropicFixtureStream(reason, `{"type":"fixture_refusal"}`)))
		if err != nil {
			t.Fatal(err)
		}
		if a.StopReason != reason || string(a.StopDetails) != `{"type":"fixture_refusal"}` || a.Usage.InputTokens != 11 || a.Usage.OutputTokens != 4 {
			t.Fatalf("terminal fields lost: %+v", a)
		}
		if !bytes.Contains(a.Content[0].Raw, []byte(`"vendor_field":"preserve"`)) {
			t.Fatal("native content lost")
		}
	}
	full := anthropicFixtureStream("end_turn", `null`)
	for _, bad := range []string{strings.ReplaceAll(full, `data: {"type":"message_stop"}`, ""), strings.Replace(full, `"index":0`, `"index":4`, 1), strings.Replace(full, `"text_delta"`, `"unknown_delta"`, 1), strings.Replace(full, `"text_delta"`, `"input_json_delta"`, 1), "data: {broken}\n\n", "data: {\"type\":\"error\",\"error\":{\"type\":\"overloaded_error\"}}\n\n"} {
		if _, err := readAnthropicStream(strings.NewReader(bad)); err == nil {
			t.Errorf("accepted malformed/truncated stream %q", bad)
		}
	}
}

func TestAnthropicStreamThinkingAndToolReplay(t *testing.T) {
	frames := []string{
		`{"type":"message_start","message":{"id":"m","content":[],"usage":{"input_tokens":3,"output_tokens":0}}}`,
		`{"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":"","signature":""}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"opaque-signature"}}`,
		`{"type":"content_block_stop","index":0}`,
		`{"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"call_1","name":"echo","input":{},"vendor":"kept"}}`,
		`{"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{\"value\":"}}`,
		`{"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"\"alive\"}"}}`,
		`{"type":"content_block_stop","index":1}`,
		`{"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":7}}`,
		`{"type":"message_stop"}`,
	}
	var stream strings.Builder
	for _, f := range frames {
		fmt.Fprintf(&stream, "data: %s\n\n", f)
	}
	a, err := readAnthropicStream(strings.NewReader(stream.String()))
	if err != nil {
		t.Fatal(err)
	}
	if len(a.Content) != 2 || a.Content[0].Thinking == nil || *a.Content[0].Thinking != "" || a.Content[0].Signature != "opaque-signature" || string(a.Content[1].Input) != `{"value":"alive"}` {
		t.Fatalf("replay material lost: %+v", a.Content)
	}
	raw, err := json.Marshal(a.Content)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(raw, []byte(`"vendor":"kept"`)) {
		t.Fatal("tool native field lost")
	}
}

// The output limit can end a reply inside a tool call's arguments: the
// block closes on half a JSON object and only then does the stream say
// max_tokens. That is a cut reply (finish "length", usage kept, the
// half-written call left out and never run). The same half-written
// arguments under any other stop reason are a malformed stream and still
// fail.
func TestAnOutputLimitInsideStreamedToolArgumentsIsACutReplyAndNotADecodeFailure(t *testing.T) {
	stream := func(stop string) string {
		frames := []string{
			`{"type":"message_start","message":{"id":"m","content":[],"usage":{"input_tokens":11,"output_tokens":0}}}`,
			`{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
			`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"writing it"}}`,
			`{"type":"content_block_stop","index":0}`,
			`{"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"call_1","name":"write","input":{}}}`,
			`{"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{\"path\":\"a.txt\",\"content\":\"half of the fi"}}`,
			`{"type":"content_block_stop","index":1}`,
			`{"type":"message_delta","delta":{"stop_reason":"` + stop + `"},"usage":{"output_tokens":9}}`,
			`{"type":"message_stop"}`,
		}
		var b strings.Builder
		for _, f := range frames {
			fmt.Fprintf(&b, "data: %s\n\n", f)
		}
		return b.String()
	}
	chat := func(stop string) (*Response, error) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			io.Copy(io.Discard, r.Body)
			w.Header().Set("Content-Type", "text/event-stream")
			io.WriteString(w, stream(stop))
		}))
		defer srv.Close()
		c := New(&ClientConfig{Endpoint: srv.URL, APIKey: "k", Model: "m", Provider: "anthropic"})
		return c.Chat(context.Background(), []Message{{Role: "user", Content: "write the file"}}, ChatOptions{})
	}

	resp, err := chat("max_tokens")
	if err != nil {
		t.Fatalf("an output-limit cut inside tool arguments failed the call: %v", err)
	}
	choice := resp.Choices[0]
	if choice.FinishReason != "length" {
		t.Fatalf("finish = %q, want the declared cut (length)", choice.FinishReason)
	}
	if len(choice.Message.ToolCalls) != 0 || bytes.Contains(choice.Message.NativeContent, []byte("tool_use")) {
		t.Fatalf("the half-written call was kept: calls=%+v native=%s", choice.Message.ToolCalls, choice.Message.NativeContent)
	}
	if choice.Message.Content != "writing it" {
		t.Fatalf("the text before the cut was lost: %q", choice.Message.Content)
	}
	if !resp.Usage.Reported || resp.Usage.PromptTokens != 11 || resp.Usage.CompletionTokens != 9 {
		t.Fatalf("the cut reply's usage was lost: %+v", resp.Usage)
	}

	if _, err := chat("tool_use"); err == nil {
		t.Fatal("half-written tool arguments under a stop reason other than max_tokens were accepted")
	}
}

// The limit can also end a reply on a call before its first argument byte.
// That call is not one the model finished: it is left out like a
// half-written one, where the same block under tool_use is a call with no
// arguments and runs. A reply that was nothing but the cut call comes back
// with no text and no call, as finish "length". Whole arguments that are
// not an object were never the limit's doing and fail under any stop.
func TestACallTheOutputLimitCutBeforeItsArgumentsIsLeftOut(t *testing.T) {
	read := func(stop string, deltas ...string) (anthResponse, error) {
		frames := []string{
			`{"type":"message_start","message":{"content":[]}}`,
			`{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"call_1","name":"write","input":{}}}`,
		}
		for _, d := range deltas {
			frames = append(frames, `{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":`+d+`}}`)
		}
		frames = append(frames,
			`{"type":"content_block_stop","index":0}`,
			`{"type":"message_delta","delta":{"stop_reason":"`+stop+`"}}`,
			`{"type":"message_stop"}`)
		var b strings.Builder
		for _, f := range frames {
			fmt.Fprintf(&b, "data: %s\n\n", f)
		}
		return readAnthropicStream(strings.NewReader(b.String()))
	}
	for name, deltas := range map[string][]string{"no delta": nil, "an empty delta": {`""`}, "half an object": {`"{\"path\":\"a"`}} {
		a, err := read("max_tokens", deltas...)
		if err != nil {
			t.Fatalf("%s: a call the output limit cut failed the reply: %v", name, err)
		}
		if len(a.Content) != 0 || !a.cutCall || a.cutTool != "write" || a.StopReason != "max_tokens" {
			t.Fatalf("%s: the cut call was kept or not named: content=%+v cut=%v tool=%q stop=%q", name, a.Content, a.cutCall, a.cutTool, a.StopReason)
		}
	}
	a, err := read("tool_use")
	if err != nil || len(a.Content) != 1 || string(a.Content[0].Input) != "{}" || a.cutCall {
		t.Fatalf("a finished call with no arguments was not kept: %+v cut=%v err=%v", a.Content, a.cutCall, err)
	}
	if _, err := read("max_tokens", `"[1]"`); err == nil {
		t.Fatal("whole arguments that are not an object were taken for a cut")
	}
}

func TestAnthropicEmptyToolInputDelta(t *testing.T) {
	stream := `data: {"type":"message_start","message":{"content":[]}}

data: {"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"call","name":"get_time","input":{}}}

data: {"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":""}}

data: {"type":"content_block_stop","index":0}

data: {"type":"message_delta","delta":{"stop_reason":"tool_use"}}

data: {"type":"message_stop"}

`
	a, err := readAnthropicStream(strings.NewReader(stream))
	if err != nil {
		t.Fatal(err)
	}
	if string(a.Content[0].Input) != "{}" {
		t.Fatalf("empty tool arguments changed: %s", a.Content[0].Input)
	}
}

func TestClaudeProfileJSONOverridesReachWire(t *testing.T) {
	p := capturedClaudeProfile()
	// This exercises the public JSON names rather than assigning Go fields.
	if err := json.Unmarshal([]byte(`{"request_path":"/configured?beta=custom","stream":false,"metadata":false,"diagnostics":false,"empty_tools":false,"billing_template":"configured {client_version}.{fingerprint}; cch={checksum}; id={prompt_id}","headers":{"user-agent":"configured/{client_version}","x-fixture":"{entrypoint}"},"entrypoint":"configured","thinking_display":"summarized","context_management":{"edits":[]},"output_config":{"effort":"high"},"fingerprint_salt":"configured","fingerprint_positions":[0,2],"fingerprint_length":4,"checksum":{"seed":"0x1234","marker":"cch=","hex_length":6,"empty_string_keys":[],"omit_keys":[],"trailing_group_comma":false}}`), p); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.RequestURI() != p.RequestPath || r.Header.Get("User-Agent") != "configured/2.1.283" || r.Header.Get("x-fixture") != "configured" {
			t.Error("configured route/headers ignored")
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		for _, key := range []string{"stream", "metadata", "diagnostics", "tools"} {
			if _, ok := body[key]; ok {
				t.Errorf("explicit disabled %s ignored", key)
			}
		}
		if body["thinking"].(map[string]any)["display"] != "summarized" || body["output_config"].(map[string]any)["effort"] != "high" {
			t.Error("configured model parameters ignored")
		}
		if len(body["context_management"].(map[string]any)["edits"].([]any)) != 0 {
			t.Error("context policy ignored")
		}
		if !strings.HasPrefix(body["system"].([]any)[0].(map[string]any)["text"].(string), "configured 2.1.283.") {
			t.Error("billing template ignored")
		}
		io.WriteString(w, `{"content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`)
	}))
	defer srv.Close()
	c := New(&ClientConfig{Endpoint: srv.URL, Provider: "anthropic", Model: "fixture", Credential: fixedCredential{token: "fixture"}, ClaudeCode: p, ThinkingMode: "adaptive", EffortLevels: []string{"high"}})
	if _, err := c.Chat(context.Background(), []Message{{Role: "user", Content: "hello"}}, ChatOptions{}); err != nil {
		t.Fatal(err)
	}
}

func TestClaudeChecksumChangesOnlyFirstSystemBlock(t *testing.T) {
	p := capturedClaudeProfile()
	billing := "x-anthropic-billing-header: cch=00000;"
	raw, err := json.Marshal(map[string]any{"context_management": map[string]string{"text": billing}, "system": []map[string]string{{"type": "text", "text": billing}}, "messages": []map[string]string{{"role": "user", "content": billing}}})
	if err != nil {
		t.Fatal(err)
	}
	signed, err := signClaudeBody(raw, p.Checksum)
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Context  map[string]string   `json:"context_management"`
		System   []map[string]string `json:"system"`
		Messages []map[string]string `json:"messages"`
	}
	if err := json.Unmarshal(signed, &got); err != nil {
		t.Fatal(err)
	}
	if got.Context["text"] != billing || got.Messages[0]["content"] != billing || got.System[0]["text"] == billing {
		t.Fatal("checksum changed the wrong occurrence")
	}
}

func TestClaudeProfileDefaultsDoNotReplaceUnsupportedExplicitRequests(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req anthRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Error(err)
		}
		if req.OutputConfig != nil || req.Thinking.Display != "" {
			t.Error("profile default replaced an explicit unsupported request")
		}
		io.WriteString(w, `{"content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn"}`)
	}))
	defer srv.Close()
	c := New(&ClientConfig{Endpoint: srv.URL, Provider: "anthropic", Model: "fixture", Credential: fixedCredential{token: "fixture"}, ClaudeCode: capturedClaudeProfile(), ThinkingMode: "adaptive", ThinkingDisplay: "unsupported", ReasoningEffort: "unsupported", EffortLevels: []string{"medium"}})
	if _, err := c.Chat(context.Background(), []Message{{Role: "user", Content: "hello"}}, ChatOptions{}); err != nil {
		t.Fatal(err)
	}
}
