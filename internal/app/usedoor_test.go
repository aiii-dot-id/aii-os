package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Switching to a credential-backed provider must be accepted. It was
// not: the use-door probed the bare endpoint with cc.APIKey, which an
// adopted credential deliberately leaves EMPTY, so every such provider
// was refused as "did not validate" — while the same provider worked at
// boot and at birth. Validation has to go through the entry, like every
// other path.
func TestCredentialBackedProviderValidates(t *testing.T) {
	var sawAuth, sawClient string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawAuth = r.Header.Get("Authorization")
		sawClient = r.Header.Get("X-Client-Name")
		if sawAuth == "" {
			w.WriteHeader(http.StatusUnauthorized) // exactly what broke it
			return
		}
		w.Write([]byte(`{"data":[{"id":"m-1","max_input_tokens":123456}]}`))
	}))
	defer srv.Close()

	dir := t.TempDir()
	tok := filepath.Join(dir, "token.json")
	b, _ := json.Marshal(map[string]any{
		"access_token": "tok-abc",
		"expires_at":   time.Now().Add(time.Hour).Unix(),
	})
	if err := os.WriteFile(tok, b, 0o600); err != nil {
		t.Fatal(err)
	}
	entry := providerEntry{
		Name: "Owned", APIType: "anthropic", URL: srv.URL + "/v1",
		Credential: "file:" + tok, DefaultModel: "m-1", Models: []string{"m-1"}, Default: true,
		CredentialOptions: map[string]string{
			"header_X-Client-Name": "claude-code", "billing_text": "billing-marker",
		},
	}
	writeTestProviders(t, dir, entry)
	a := New(&Config{
		LLM:        LLMConfig{Provider: "Owned", Model: "m-1"},
		SourcePath: filepath.Join(dir, "config.json"),
	})

	// The resolver hands the credential over and clears the key.
	cc, resolved, err := a.resolveLLM()
	if err != nil {
		t.Fatalf("resolveLLM: %v", err)
	}
	if cc.Credential == nil || cc.APIKey != "" {
		t.Fatalf("an adopted credential replaces the key: cred=%v key=%q", cc.Credential != nil, cc.APIKey)
	}
	if cc.Provider != "anthropic" || cc.AnthropicOAuthBillingText != "billing-marker" {
		t.Fatalf("Anthropic credential wire data was lost: %+v", cc)
	}

	// And the validation path the use-door uses must succeed anyway.
	models, err := a.discoverForEntry(context.Background(), resolved, cc.APIKey)
	if err != nil {
		t.Fatalf("validation must go through the credential, not the empty key: %v", err)
	}
	if len(models) != 1 || models[0] != "m-1" {
		t.Fatalf("models: %v", models)
	}
	if sawAuth != "Bearer tok-abc" {
		t.Fatalf("the credential must reach the wire, saw %q", sawAuth)
	}
	if sawClient != "claude-code" {
		t.Fatalf("the credential's provider headers must reach the wire, saw %q", sawClient)
	}

	// The window the provider published is what gets budgeted against.
	if _, _, err := a.discoverMetaForEntry(context.Background(), resolved, ""); err != nil {
		t.Fatal(err)
	}
}
