package llm

// The wire shape of the addendum fields (2026-08-20): sampling params
// are pointers — a set 0 is SENT, an unset param is ABSENT — and the
// extra map passes through verbatim on the OpenAI-compatible path only,
// with typed fields winning every collision.

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

func f64(v float64) *float64 { return &v }

// captureServer records the last request body and answers with a
// minimal valid response for the given dialect.
func captureServer(t *testing.T, response string) (*httptest.Server, *map[string]any) {
	t.Helper()
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, &got); err != nil {
			t.Errorf("request body is not JSON: %v", err)
		}
		w.Write([]byte(response))
	}))
	t.Cleanup(srv.Close)
	return srv, &got
}

const openAIResp = `{"choices":[{"message":{"role":"assistant","content":"ok"}}]}`
const anthResp = `{"content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn"}`

// OpenAI path: temperature 0 is on the wire as 0 (a pointer, never
// omitempty-eaten), top_p rides, extra keys merge at the top level, and
// a colliding extra key loses to the typed field.
func TestOpenAIWireSamplingAndExtra(t *testing.T) {
	srv, got := captureServer(t, openAIResp)
	c := New(&ClientConfig{
		Endpoint: srv.URL, APIKey: "k", Model: "m",
		Temperature: f64(0), TopP: f64(0.9),
		Extra: map[string]any{
			"repetition_penalty": 1.05,       // novel knob: passes through verbatim
			"model":              "OVERRIDE", // collides with a typed field: typed wins
		},
	})
	if _, err := c.Chat(context.Background(), []Message{{Role: "user", Content: "hi"}}, ChatOptions{}); err != nil {
		t.Fatal(err)
	}
	b := *got
	if v, ok := b["temperature"].(float64); !ok || v != 0 {
		t.Fatalf(`"temperature": 0 must be SENT when set to zero, got %v (present %v)`, b["temperature"], ok)
	}
	if v, ok := b["top_p"].(float64); !ok || v != 0.9 {
		t.Fatalf("top_p must ride, got %v", b["top_p"])
	}
	if v, ok := b["repetition_penalty"].(float64); !ok || v != 1.05 {
		t.Fatalf("extra keys must merge verbatim, got %v", b["repetition_penalty"])
	}
	if b["model"] != "m" {
		t.Fatalf("typed fields must win over extra on collision, got model=%v", b["model"])
	}
}

// OpenAI path, nothing set: temperature/top_p are ABSENT (server
// default), not zero.
func TestOpenAIWireOmitsUnsetSampling(t *testing.T) {
	srv, got := captureServer(t, openAIResp)
	c := New(&ClientConfig{Endpoint: srv.URL, APIKey: "k", Model: "m"})
	if _, err := c.Chat(context.Background(), []Message{{Role: "user", Content: "hi"}}, ChatOptions{}); err != nil {
		t.Fatal(err)
	}
	b := *got
	if _, present := b["temperature"]; present {
		t.Fatalf("unset temperature must be ABSENT, got %v", b["temperature"])
	}
	if _, present := b["top_p"]; present {
		t.Fatalf("unset top_p must be ABSENT, got %v", b["top_p"])
	}
}

// Anthropic path: sampling params ride the typed request; the extra
// passthrough does NOT apply (typed API).
func TestAnthropicWireSamplingNoExtra(t *testing.T) {
	srv, got := captureServer(t, anthResp)
	c := New(&ClientConfig{
		Endpoint: srv.URL, APIKey: "k", Model: "m", Provider: "anthropic",
		Temperature: f64(0.3),
		Extra:       map[string]any{"repetition_penalty": 1.05},
	})
	if _, err := c.Chat(context.Background(), []Message{{Role: "user", Content: "hi"}}, ChatOptions{}); err != nil {
		t.Fatal(err)
	}
	b := *got
	if v, ok := b["temperature"].(float64); !ok || v != 0.3 {
		t.Fatalf("anthropic temperature must ride the typed request, got %v", b["temperature"])
	}
	if _, present := b["repetition_penalty"]; present {
		t.Fatal("extra must NOT apply on the anthropic path (typed API)")
	}
}

// A provider that omits usage and a provider that genuinely spent
// nothing both decoded to {0,0,0}. Any accounting built on that treats
// UNKNOWN as FREE — the worst direction for a budget to be wrong in,
// because it never refuses and never warns. The three cases that must
// stay apart are reported-nonzero, reported-ZERO, and silent.
func TestUnknownUsageIsNotZeroUsage(t *testing.T) {
	const okAnth = `{"content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn"`
	for _, tc := range []struct {
		name         string
		provider     string
		body         string
		wantReported bool
		wantTotal    int
	}{
		{"openai reports usage", "", `{"choices":[{"message":{"role":"assistant","content":"ok"}}],"usage":{"prompt_tokens":11,"completion_tokens":5,"total_tokens":16}}`, true, 16},
		{"openai reports a GENUINE zero", "", `{"choices":[{"message":{"role":"assistant","content":"ok"}}],"usage":{"prompt_tokens":0,"completion_tokens":0,"total_tokens":0}}`, true, 0},
		{"openai omits usage entirely", "", `{"choices":[{"message":{"role":"assistant","content":"ok"}}]}`, false, 0},
		{"anthropic reports usage", "anthropic", okAnth + `,"usage":{"input_tokens":9,"output_tokens":3}}`, true, 12},
		{"anthropic omits usage", "anthropic", okAnth + `}`, false, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				io.WriteString(w, tc.body)
			}))
			defer srv.Close()
			c := New(&ClientConfig{Endpoint: srv.URL, APIKey: "k", Model: "m", Provider: tc.provider})
			resp, err := c.Chat(context.Background(), []Message{{Role: "user", Content: "hi"}}, ChatOptions{})
			if err != nil {
				t.Fatal(err)
			}
			if resp.Usage.Reported != tc.wantReported {
				t.Fatalf("Reported = %v, want %v (usage %+v)", resp.Usage.Reported, tc.wantReported, resp.Usage)
			}
			if resp.Usage.TotalTokens != tc.wantTotal {
				t.Fatalf("TotalTokens = %d, want %d", resp.Usage.TotalTokens, tc.wantTotal)
			}
		})
	}
}

// Cache reads and cache writes are input the operator paid for. Leaving
// them out under-counts every cached turn, which is most of them.
func TestAnthropicUsageCountsCachedInput(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{"input_tokens":10,"output_tokens":4,"cache_read_input_tokens":100,"cache_creation_input_tokens":7}}`)
	}))
	defer srv.Close()
	c := New(&ClientConfig{Endpoint: srv.URL, APIKey: "k", Model: "m", Provider: "anthropic"})
	resp, err := c.Chat(context.Background(), []Message{{Role: "user", Content: "hi"}}, ChatOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Usage.PromptTokens != 117 || resp.Usage.TotalTokens != 121 {
		t.Fatalf("cached input not counted toward the window: %+v", resp.Usage)
	}
	// ...and separated for COST. Cache READS bill at about a tenth of
	// fresh input; cache CREATION bills at more, so only reads belong
	// here. Collapsing the two overstates cost by up to an order of
	// magnitude on a system that caches deliberately and heavily.
	if resp.Usage.CachedPromptTokens != 100 {
		t.Fatalf("cache reads not separated for cost: %+v", resp.Usage)
	}
}

// AII OS budgeted against an output reserve that only ONE of three
// dialects actually sent: the OpenAI-shaped paths omit max_tokens when
// it is zero, so the provider chose its own ceiling, while the Anthropic
// path substituted a private default. The prompt budget reserved space
// against a limit that was not in force.
func TestEveryDialectSendsTheOutputCeiling(t *testing.T) {
	for _, tc := range []struct {
		name, provider, field string
	}{
		{"openai-compatible", "", "max_tokens"},
		{"anthropic", "anthropic", "max_tokens"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var body map[string]any
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				json.NewDecoder(r.Body).Decode(&body)
				if tc.provider == "anthropic" {
					io.WriteString(w, `{"content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn"}`)
					return
				}
				io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"ok"}}]}`)
			}))
			defer srv.Close()
			c := New(&ClientConfig{Endpoint: srv.URL, APIKey: "k", Model: "m",
				Provider: tc.provider, MaxOutputTokens: 4096})
			if _, err := c.Chat(context.Background(), []Message{{Role: "user", Content: "hi"}}, ChatOptions{}); err != nil {
				t.Fatal(err)
			}
			got, present := body[tc.field]
			if !present {
				t.Fatalf("%s omitted %s — the provider, not AII OS, would choose the ceiling: %v", tc.name, tc.field, body)
			}
			if got != float64(4096) {
				t.Fatalf("%s sent %s = %v, want 4096", tc.name, tc.field, got)
			}
		})
	}
}

// A client built without one still sends a ceiling, and it is the SAME
// constant the budget reserves — never a second opinion invented by a
// dialect.
func TestDialectFallbackIsTheSharedConstant(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&body)
		io.WriteString(w, `{"content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn"}`)
	}))
	defer srv.Close()
	c := New(&ClientConfig{Endpoint: srv.URL, APIKey: "k", Model: "m", Provider: "anthropic"})
	if _, err := c.Chat(context.Background(), []Message{{Role: "user", Content: "hi"}}, ChatOptions{}); err != nil {
		t.Fatal(err)
	}
	if body["max_tokens"] != float64(DefaultMaxOutputTokens) {
		t.Fatalf("fallback = %v, want the shared DefaultMaxOutputTokens %d", body["max_tokens"], DefaultMaxOutputTokens)
	}
}

// THE IDENTITY STOPS THINKING when a call fails, and the record showed
// nothing: the 400 that blocked every turn on 2026-08-23 appears zero
// times across every log file. The failure was formatted into a chat
// message for whoever was watching and logged nowhere, so anyone
// diagnosing from the log was reading a channel that could not contain
// the answer.
func TestFailedCallIsRecorded(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(400)
		io.WriteString(w, `{"error":{"message":"messages.19.content.0.thinking.thinking: Field required"}}`)
	}))
	defer srv.Close()

	var buf bytes.Buffer
	old := log.Writer()
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(old) })

	c := New(&ClientConfig{Endpoint: srv.URL, APIKey: "k", Model: "m", Provider: "anthropic"})
	if _, err := c.Chat(context.Background(), []Message{{Role: "user", Content: "hi"}}, ChatOptions{}); err == nil {
		t.Fatal("a 400 must be an error")
	}
	got := buf.String()
	if !strings.Contains(got, "LLM call FAILED") {
		t.Fatalf("a failed call left no record: %q", got)
	}
	// The record has to name the substrate and carry the provider's own
	// words, or it cannot be diagnosed from.
	if !strings.Contains(got, "anthropic") || !strings.Contains(got, "Field required") {
		t.Fatalf("the record does not identify the failure: %q", got)
	}
}

// A log line on every successful call would bury the failures it exists
// to surface.
func TestSuccessfulCallIsSilent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn"}`)
	}))
	defer srv.Close()

	var buf bytes.Buffer
	old := log.Writer()
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(old) })

	c := New(&ClientConfig{Endpoint: srv.URL, APIKey: "k", Model: "m", Provider: "anthropic"})
	if _, err := c.Chat(context.Background(), []Message{{Role: "user", Content: "hi"}}, ChatOptions{}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(buf.String(), "LLM call FAILED") {
		t.Fatalf("a successful call was logged as a failure: %q", buf.String())
	}
}

// A provider body is read up to 64KB. That belongs in the operator's
// error, not in every line of a log file.
func TestFailureRecordIsBounded(t *testing.T) {
	if got := clip(strings.Repeat("x", 5000), 500); len(got) > 600 {
		t.Fatalf("clip did not bound the record: %d bytes", len(got))
	}
	if !strings.Contains(clip(strings.Repeat("x", 5000), 500), "more bytes") {
		t.Fatal("a clipped record must say that it was clipped")
	}
	if got := clip("short", 500); got != "short" {
		t.Fatalf("clip altered a short record: %q", got)
	}
}

// DREAM, CONSOLIDATE and MORNING_BRIEF all leave through ChatSimple.
// They run on alarms with nobody watching, and their spend was the one
// cost in the system that could not be seen at all — a conversation turn
// at least has someone reading the reply.
func TestUnattendedCallCostIsRecorded(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{"input_tokens":900,"output_tokens":40,"cache_read_input_tokens":800}}`)
	}))
	defer srv.Close()

	var buf bytes.Buffer
	old := log.Writer()
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(old) })

	c := New(&ClientConfig{Endpoint: srv.URL, APIKey: "k", Model: "m", Provider: "anthropic"})
	if _, _, err := c.ChatSimple(context.Background(), "system", "user"); err != nil {
		t.Fatal(err)
	}
	got := buf.String()
	if !strings.Contains(got, "Unattended call cost") {
		t.Fatalf("a facility pass left no cost record: %q", got)
	}
	// 900 input + 800 cache reads = 1700 prompt, +40 out = 1740 total.
	if !strings.Contains(got, "1740") {
		t.Fatalf("cost record does not carry the total: %q", got)
	}
	// And the cached share, which is what separates spend from window.
	if !strings.Contains(got, "800 cached") {
		t.Fatalf("cost record does not separate cached input: %q", got)
	}
}

// A provider that reports nothing must not be logged as having spent
// nothing — unknown is not free, and that distinction is the whole
// reason Reported exists.
func TestUnattendedCostSilentWhenUnreported(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn"}`)
	}))
	defer srv.Close()

	var buf bytes.Buffer
	old := log.Writer()
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(old) })

	c := New(&ClientConfig{Endpoint: srv.URL, APIKey: "k", Model: "m", Provider: "anthropic"})
	if _, _, err := c.ChatSimple(context.Background(), "system", "user"); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(buf.String(), "Unattended call cost") {
		t.Fatalf("unknown spend was logged as if measured: %q", buf.String())
	}
}

// LIVE 400, 2026-08-24: {"detail":"Unsupported parameter:
// max_output_tokens"}. This backend rejects the parameter outright, and
// a refused parameter refuses the whole request — the operator was told
// a working ChatGPT subscription could not do minimal inference.
//
// A regression from 13aab5d: before it the field was zero and omitempty
// dropped it; materializing the output reserve so every dialect carries
// the ceiling started sending it here. The other two dialects keep the
// ceiling (TestEveryDialectSendsTheOutputCeiling); this one must not,
// and the asymmetry is pinned so it cannot be "tidied" back.
func TestChatGPTOmitsTheOutputCeiling(t *testing.T) {
	var raw string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		raw = string(b)
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "event: response.completed\ndata: {\"response\":{\"output\":[{\"type\":\"message\",\"content\":[{\"type\":\"output_text\",\"text\":\"ok\"}]}],\"usage\":{\"input_tokens\":5,\"output_tokens\":1,\"total_tokens\":6}}}\n\n")
	}))
	defer srv.Close()

	c := New(&ClientConfig{Endpoint: srv.URL, APIKey: "k", Model: "gpt-5.6-sol",
		Provider: "chatgpt", MaxOutputTokens: 8192})
	// The RESPONSE is irrelevant here — this asserts what goes out on the
	// wire, and the request is fully formed before any reply is parsed.
	_, _ = c.Chat(context.Background(), []Message{{Role: "user", Content: "hi"}}, ChatOptions{})
	if raw == "" {
		t.Fatal("no request reached the server")
	}
	if strings.Contains(raw, "max_output_tokens") {
		t.Fatalf("the ChatGPT backend was sent a parameter it rejects: %s", raw)
	}
}

// The facilities are emission-agnostic BY DESIGN: "the local model may
// have weak tool_calls, so the contract is plain JSON the loop parses."
// That is a five-platform constraint, so offering a tool must not
// require one. Both channels return the same payload shape, and the
// caller parses it the same way.
func TestChatStructuredAcceptsEitherChannel(t *testing.T) {
	envelope := `{"operations":[{"op":"upsert","id":"b1"}],"ring3_view":"you believe the build is green"}`

	for _, tc := range []struct {
		name        string
		body        string
		wantViaTool bool
	}{
		{
			name:        "substrate calls the tool",
			body:        `{"content":[{"type":"tool_use","id":"t1","name":"emit_consolidation","input":` + envelope + `}],"stop_reason":"tool_use"}`,
			wantViaTool: true,
		},
		{
			name:        "substrate answers in prose (weak tool calls)",
			body:        `{"content":[{"type":"text","text":` + strconv.Quote(envelope) + `}],"stop_reason":"end_turn"}`,
			wantViaTool: false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				io.WriteString(w, tc.body)
			}))
			defer srv.Close()

			tool := ToolDefinition{Type: "function"}
			tool.Function.Name = "emit_consolidation"
			tool.Function.Parameters = map[string]interface{}{"type": "object"}

			c := New(&ClientConfig{Endpoint: srv.URL, APIKey: "k", Model: "m", Provider: "anthropic"})
			payload, _, viaTool, err := c.ChatStructured(context.Background(), "system", "user", tool)
			if err != nil {
				t.Fatal(err)
			}
			if viaTool != tc.wantViaTool {
				t.Fatalf("viaTool = %v, want %v", viaTool, tc.wantViaTool)
			}
			// The PAYLOAD is what matters: both channels must yield a
			// parseable envelope, so the facility needs no branch.
			var got map[string]interface{}
			if err := json.Unmarshal([]byte(payload), &got); err != nil {
				t.Fatalf("payload from the %s channel does not parse: %v (%q)", tc.name, err, payload)
			}
			if got["ring3_view"] != "you believe the build is green" {
				t.Fatalf("payload lost content: %v", got)
			}
		})
	}
}
