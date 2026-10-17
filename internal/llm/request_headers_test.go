package llm

import (
	"context"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
)

// headerCredential is a credential source whose credential carries headers
// of its own, as an OAuth credential does (an account id, a vendor fact the
// operator corrected in config).
type headerCredential struct {
	token   string
	headers map[string]string
}

func (h headerCredential) Credential(context.Context) (Credential, error) {
	return Credential{Token: h.token, Headers: h.headers}, nil
}
func (h headerCredential) Stale(context.Context, uint64) error { return nil }

// Every door a request leaves through sends the method, path and headers
// pinned here: what the dialect sets, what a credential's own headers add,
// and which of the two wins where both name one (a credential's header is
// applied after Accept, so it overrides it; it never overrides
// Authorization, x-api-key, x-goog-api-key or Content-Type; an operator's
// anthropic-version is kept). The transport's own headers (User-Agent,
// Accept-Encoding, Content-Length) are not the client's and are left out.
func TestEveryDoorSendsItsPinnedHeaders(t *testing.T) {
	credHeaders := map[string]string{
		"X-Account-Id":      "acct-1",
		"Accept":            "application/x-credential",
		"Authorization":     "Bearer wrong",
		"Content-Type":      "text/plain",
		"Anthropic-Version": "2099-01-01",
	}
	const anthropicOK = `{"content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn"}`
	const openAIOK = `{"choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`
	const responsesOK = "data: {\"type\":\"response.output_item.done\",\"item\":{\"type\":\"message\",\"role\":\"assistant\",\"content\":[{\"type\":\"output_text\",\"text\":\"ok\"}]}}\n\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"r\",\"output\":[],\"usage\":{\"input_tokens\":1,\"output_tokens\":1,\"total_tokens\":2}}}\n\n"
	const streamOK = "data: {\"choices\":[{\"delta\":{\"role\":\"assistant\",\"content\":\"ok\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"
	for _, tc := range []struct {
		name  string
		cfg   ClientConfig
		reply string
		ctype string
		call  func(*Client) error
		path  string
		want  map[string]string
	}{
		{"openai-compatible whole, key", ClientConfig{NoStream: true}, openAIOK, "application/json", chatOnce, "/chat/completions",
			map[string]string{"Content-Type": "application/json", "Authorization": "Bearer key"}},
		{"openai-compatible whole, credential", ClientConfig{NoStream: true, Credential: headerCredential{"tok", credHeaders}}, openAIOK, "application/json", chatOnce, "/chat/completions",
			map[string]string{"Content-Type": "application/json", "Authorization": "Bearer tok", "X-Account-Id": "acct-1", "Accept": "application/x-credential", "Anthropic-Version": "2099-01-01"}},
		{"openai-compatible streamed, key", ClientConfig{}, streamOK, "text/event-stream", chatOnce, "/chat/completions",
			map[string]string{"Content-Type": "application/json", "Accept": "text/event-stream", "Authorization": "Bearer key"}},
		{"openai-compatible streamed, credential", ClientConfig{Credential: headerCredential{"tok", credHeaders}}, streamOK, "text/event-stream", chatOnce, "/chat/completions",
			map[string]string{"Content-Type": "application/json", "Accept": "application/x-credential", "Authorization": "Bearer tok", "X-Account-Id": "acct-1", "Anthropic-Version": "2099-01-01"}},
		{"chatgpt responses, credential", ClientConfig{Provider: "chatgpt", Credential: headerCredential{"tok", credHeaders}}, responsesOK, "text/event-stream", chatOnce, "/responses",
			map[string]string{"Content-Type": "application/json", "Accept": "application/x-credential", "Authorization": "Bearer tok", "X-Account-Id": "acct-1", "Anthropic-Version": "2099-01-01"}},
		{"chatgpt responses, key", ClientConfig{Provider: "chatgpt"}, responsesOK, "text/event-stream", chatOnce, "/responses",
			map[string]string{"Content-Type": "application/json", "Accept": "text/event-stream", "Authorization": "Bearer key"}},
		{"embeddings, key", ClientConfig{}, `{"data":[{"index":0,"embedding":[1]}]}`, "application/json", func(c *Client) error {
			_, err := c.Embed(context.Background(), "embed-model", []string{"a"})
			return err
		}, "/embeddings", map[string]string{"Content-Type": "application/json", "Authorization": "Bearer key"}},
		{"anthropic, key", ClientConfig{Provider: "anthropic"}, anthropicOK, "application/json", chatOnce, "/v1/messages",
			map[string]string{"Content-Type": "application/json", "X-Api-Key": "key", "Anthropic-Version": DefaultAnthropicVersion}},
		{"anthropic, key, cache diagnostics", ClientConfig{Provider: "anthropic", Cache: &CachePolicy{Diagnostics: true}}, anthropicOK, "application/json", chatOnce, "/v1/messages",
			map[string]string{"Content-Type": "application/json", "X-Api-Key": "key", "Anthropic-Version": DefaultAnthropicVersion, "Anthropic-Beta": "cache-diagnosis-2026-04-07"}},
		{"anthropic, credential", ClientConfig{Provider: "anthropic", Credential: headerCredential{"tok", credHeaders}}, anthropicOK, "application/json", chatOnce, "/v1/messages",
			map[string]string{"Content-Type": "application/json", "Authorization": "Bearer tok", "X-Account-Id": "acct-1", "Accept": "application/x-credential", "Anthropic-Version": "2099-01-01"}},
		{"gemini streamed, key", ClientConfig{Provider: "gemini"}, "data: " + geminiOK + "\n\n", "text/event-stream", chatOnce, "/models/model:streamGenerateContent",
			map[string]string{"Content-Type": "application/json", "Accept": "text/event-stream", "X-Goog-Api-Key": "key"}},
		{"gemini whole, key", ClientConfig{Provider: "gemini", NoStream: true}, geminiOK, "application/json", chatOnce, "/models/model:generateContent",
			map[string]string{"Content-Type": "application/json", "X-Goog-Api-Key": "key"}},
		{"gemini streamed, credential", ClientConfig{Provider: "gemini", Credential: headerCredential{"tok", credHeaders}}, "data: " + geminiOK + "\n\n", "text/event-stream", chatOnce, "/models/model:streamGenerateContent",
			map[string]string{"Content-Type": "application/json", "Accept": "application/x-credential", "Authorization": "Bearer tok", "X-Account-Id": "acct-1", "Anthropic-Version": "2099-01-01"}},
		{"gemini embeddings, key", ClientConfig{Provider: "gemini"}, `{"embeddings":[{"values":[1]}]}`, "application/json", func(c *Client) error {
			_, err := c.Embed(context.Background(), "embed-model", []string{"a"})
			return err
		}, "/models/embed-model:batchEmbedContents", map[string]string{"Content-Type": "application/json", "X-Goog-Api-Key": "key"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var method, path string
			got := map[string]string{}
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				method, path = r.Method, r.URL.Path
				for k, v := range r.Header {
					switch k {
					case "User-Agent", "Accept-Encoding", "Content-Length":
						continue
					}
					got[k] = strings.Join(v, ", ")
				}
				io.Copy(io.Discard, r.Body)
				w.Header().Set("Content-Type", tc.ctype)
				io.WriteString(w, tc.reply)
			}))
			defer srv.Close()
			cfg := tc.cfg
			cfg.Endpoint, cfg.Model, cfg.Retries = srv.URL, "model", -1
			if cfg.Credential == nil {
				cfg.APIKey = "key"
			}
			if err := tc.call(New(&cfg)); err != nil {
				t.Fatalf("call: %v", err)
			}
			if method != http.MethodPost || path != tc.path {
				t.Fatalf("request = %s %s, want POST %s", method, path, tc.path)
			}
			if !maps.Equal(got, tc.want) {
				t.Fatalf("headers:\n got %v\nwant %v", sorted(got), sorted(tc.want))
			}
		})
	}
}

func sorted(m map[string]string) []string {
	var out []string
	for k, v := range m {
		out = append(out, k+": "+v)
	}
	slices.Sort(out)
	return out
}
