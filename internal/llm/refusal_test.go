package llm

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// A provider's refusal reaches the caller as the typed *HTTPError on every
// door a request leaves through, not only on the retry path: the status
// and the provider's body, structurally, so a caller classifies the
// status without parsing prose. The rendered text is the one it always
// was ("API returned <status>: <body>").
func TestEveryDialectReturnsATypedRefusal(t *testing.T) {
	const refusalBody = `{"error":{"type":"permission_error","message":"not permitted"}}`
	for _, tc := range []struct {
		name string
		cfg  ClientConfig
		call func(*Client) error
	}{
		{"openai-compatible streamed", ClientConfig{}, chatOnce},
		{"openai-compatible whole", ClientConfig{NoStream: true}, chatOnce},
		{"anthropic", ClientConfig{Provider: "anthropic"}, chatOnce},
		{"chatgpt responses", ClientConfig{Provider: "chatgpt"}, chatOnce},
		{"gemini streamed", ClientConfig{Provider: "gemini"}, chatOnce},
		{"gemini whole", ClientConfig{Provider: "gemini", NoStream: true}, chatOnce},
		{"embeddings", ClientConfig{}, func(c *Client) error {
			_, err := c.Embed(context.Background(), "embed-model", []string{"a"})
			return err
		}},
		{"gemini embeddings", ClientConfig{Provider: "gemini"}, func(c *Client) error {
			_, err := c.Embed(context.Background(), "embed-model", []string{"a"})
			return err
		}},
	} {
		for _, size := range []struct {
			name, body, want string
		}{
			{"whole", refusalBody, refusalBody},
			{"truncated", strings.Repeat("x", 64<<10+10), strings.Repeat("x", 64<<10) + " [truncated]"},
		} {
			t.Run(tc.name+"/"+size.name, func(t *testing.T) {
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(http.StatusForbidden)
					w.Write([]byte(size.body))
				}))
				defer srv.Close()
				cfg := tc.cfg
				cfg.Endpoint, cfg.APIKey, cfg.Model, cfg.Retries = srv.URL, "key", "model", -1
				err := tc.call(New(&cfg))
				var refusal *HTTPError
				if !errors.As(err, &refusal) {
					t.Fatalf("the refusal is prose, not a typed *HTTPError: %.160v", err)
				}
				if refusal.StatusCode != http.StatusForbidden || refusal.Body != size.want || refusal.Retryable {
					t.Fatalf("typed refusal = {%d, %d body bytes, retryable %t}, want {403, %d bytes, false}", refusal.StatusCode, len(refusal.Body), refusal.Retryable, len(size.want))
				}
				if want := "API returned 403: " + size.want; err.Error() != want {
					t.Fatalf("the refusal's text changed:\n got %.120q\nwant %.120q", err.Error(), want)
				}
			})
		}
	}
}

func chatOnce(c *Client) error {
	_, err := c.Chat(context.Background(), []Message{{Role: "user", Content: "hi"}}, ChatOptions{})
	return err
}
