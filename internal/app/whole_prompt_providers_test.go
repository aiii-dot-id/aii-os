package app

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/dashboard"
	"github.com/aiii-dot-id/aii-os/internal/genesis/genesistest"
	"github.com/aiii-dot-id/aii-os/internal/llm"
)

func providerShapes() []requestShape {
	var shapes []requestShape
	for _, founding := range []struct {
		name, about string
		refuse      bool
	}{
		{"firstboot_founding", "birth's founding request: the platform's packet as the only system message", false},
		{"firstboot_founding_as_user", "birth's founding request again, after a provider refused a request with no user message: the same packet as the only user message", true},
	} {
		shapes = append(shapes, requestShape{
			name: founding.name, about: founding.about,
			send: func(t *testing.T) sentRequest {
				bodies := &sentBodies{}
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.Method == http.MethodGet {
						fmt.Fprint(w, `{"data":[{"id":"birth-model"}]}`)
						return
					}
					body := bodies.add(r)
					if founding.refuse && !strings.Contains(body, `"role":"user"`) {
						w.WriteHeader(http.StatusBadRequest)
						fmt.Fprint(w, noUserQuery)
						return
					}
					fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","content":"Hello."},"finish_reason":"stop"}]}`)
				}))
				t.Cleanup(srv.Close)
				a := birthApp(t, providerEntry{Name: "Local", APIType: "openai", URL: srv.URL + "/v1", APIKey: "k", DefaultModel: "birth-model", Default: true})
				t.Cleanup(func() { stopApp(t, a) })
				// The constitution the birth attests is the live one: the
				// fixture's stand-in is replaced, so nothing here is born
				// under words the platform did not serve.
				a.ring0Content = genesistest.NewRoot(t).Constitution(t)
				if greeting, err := a.handleGenesis(t.Context(), &dashboard.GenesisRequest{Provider: "Local", Endpoint: srv.URL + "/v1", Model: "birth-model"}); err != nil || greeting != "Hello." {
					t.Fatalf("birth = %q, %v", greeting, err)
				}
				sent := bodies.snapshot()
				if !founding.refuse {
					return sentRequest{body: onlyRequest(t, sent)}
				}
				// Refused as the only system message (on each rung the
				// transport tries), then sent once as the only user message.
				if len(sent) < 2 || !strings.Contains(sent[len(sent)-1], `"role":"user"`) || strings.Contains(sent[0], `"role":"user"`) {
					t.Fatalf("birth sent %d founding requests, want the system message refused and then the user message", len(sent))
				}
				return sentRequest{body: sent[len(sent)-1]}
			},
		})
	}
	for _, probe := range []struct{ name, about, carrying, tools string }{
		{"probe_minimal", "the minimal inference request a provider must answer before it is adopted", "Reply with the single word OK.", ""},
		{"probe_tool_call", "the tool-call probe asked beside it: can this model call a tool?", "Call report_ready", "probe_report_ready"},
	} {
		shapes = append(shapes, requestShape{
			name: probe.name, about: probe.about, tools: probe.tools,
			send: func(t *testing.T) sentRequest {
				a, dir := goldenTurnApp(t)
				bodies := &sentBodies{}
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if strings.Contains(bodies.add(r), "report_ready") {
						replyingWith(callReply("", toolCall("call_ready_1", "report_ready", `{"ready":true}`)))(0, w)
						return
					}
					replyingWith(textReply("OK"))(0, w)
				}))
				t.Cleanup(srv.Close)
				cc := llm.ClientConfig{Endpoint: srv.URL, Model: "fake", APIKey: "fixture", NoStream: true, Retries: -1}
				if _, err := a.probeSubstrate(llm.New(&cc), cc, providerEntry{Name: "fixture"}, nil, 10); err != nil {
					t.Fatal(err)
				}
				var found []string
				sent := bodies.snapshot()
				for _, b := range sent {
					if strings.Contains(b, probe.carrying) {
						found = append(found, b)
					}
				}
				if len(sent) != 2 || len(found) != 1 {
					t.Fatalf("the probes sent %d requests, %d carrying %q; want 2 and 1", len(sent), len(found), probe.carrying)
				}
				return sentRequest{body: found[0], dir: dir}
			},
		})
	}
	return append(shapes, requestShape{
		name:    "claude_subscription_operator",
		about:   "turn_operator's request on the Claude subscription: the Anthropic dialect's encoding, its billing block first in the system",
		tools:   "resident",
		dialect: "anthropic",
		send: func(t *testing.T) sentRequest {
			a, dir := goldenTurnApp(t)
			bodies := &sentBodies{}
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodGet {
					fmt.Fprint(w, `{"data":[{"id":"claude-opus-5-5","type":"model"}]}`)
					return
				}
				bodies.add(r)
				w.Header().Set("Content-Type", "text/event-stream")
				fmt.Fprint(w, anthropicNoted)
			}))
			t.Cleanup(srv.Close)
			// A sign-in of the fixture's own, named as the file to read, and
			// a Keychain service no machine has: no sign-in of the
			// operator's is read.
			signIn := filepath.Join(dir, "claude-sign-in.json")
			if err := os.WriteFile(signIn, []byte(`{"claudeAiOauth":{"accessToken":"fixture-token","refreshToken":"fixture-refresh","expiresAt":`+fmt.Sprint(time.Now().Add(2*time.Hour).UnixMilli())+`,"scopes":["user:profile","user:inference"]}}`), 0o600); err != nil {
				t.Fatal(err)
			}
			writeTestProviders(t, dir,
				providerEntry{Name: "test", APIType: "openai", URL: "https://x", APIKey: "sk-x", DefaultModel: "m"},
				providerEntry{Name: "Claude", APIType: "anthropic", URL: srv.URL, Credential: "claude-code", DefaultModel: "claude-opus-5-5", Default: true,
					CredentialOptions: map[string]string{"file": signIn, "keychain_service": "aii-os-whole-prompt-absent", "base_url": srv.URL}})
			a.cfgMu.Lock()
			a.cfg.LLM.Provider, a.cfg.LLM.Model = "Claude", "claude-opus-5-5"
			a.cfgMu.Unlock()
			cc, _, err := a.resolveLLM()
			if err != nil {
				t.Fatal(err)
			}
			a.llmSwap.Swap(a.newLLMClient(cc, 200000))
			turnPathNamed(t, "operator").run(t, a)
			waitUntil(t, func() bool { return !a.TurnActive() }, "the turn to end")
			return sentRequest{body: onlyRequest(t, bodies.snapshot()), dir: dir, more: func(s string) string {
				// The billing block's prompt id is random per request, and
				// its checksum is of the body that carries it.
				s = claudePromptID.ReplaceAllString(s, "cc_prompt_id=<prompt id>")
				return claudeChecksum.ReplaceAllString(s, "cch=<checksum>")
			}}
		},
	})
}

var (
	claudePromptID = regexp.MustCompile(`cc_prompt_id=[0-9a-f-]{36}`)
	claudeChecksum = regexp.MustCompile(`cch=[0-9a-f]+`)
)

// sentBodies keeps each request body a fake provider received, in order.
type sentBodies struct {
	mu     sync.Mutex
	bodies []string
}

func (s *sentBodies) add(r *http.Request) string {
	b, _ := io.ReadAll(r.Body)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.bodies = append(s.bodies, string(b))
	return string(b)
}

func (s *sentBodies) snapshot() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.bodies...)
}

// anthropicNoted is an Anthropic stream that says "noted" and ends.
const anthropicNoted = `event: message_start
data: {"type":"message_start","message":{"id":"msg_fixture","type":"message","role":"assistant","model":"claude-opus-5-5","content":[],"usage":{"input_tokens":10,"output_tokens":0}}}

event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"noted"}}

event: content_block_stop
data: {"type":"content_block_stop","index":0}

event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":5}}

event: message_stop
data: {"type":"message_stop"}

`
