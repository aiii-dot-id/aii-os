package app

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/fileperm"
	"github.com/aiii-dot-id/aii-os/internal/llm"
	"github.com/aiii-dot-id/aii-os/internal/oauth"
)

// .
// .
func TestClaudeManualSignInInferenceRefreshAndRestart(t *testing.T) {
	a := newProvidersApp(t)
	fixtureSignInContext(t, a)
	dir, _ := os.Getwd()
	a.cfg.Identity.LedgerPath = filepath.Join(dir, "data", "ledger.jsonl")
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	var reg providerRegistry
	if err := json.Unmarshal(embeddedProviders, &reg); err != nil {
		t.Fatal(err)
	}
	var entry providerEntry
	for _, candidate := range reg.Providers {
		if candidate.OAuth == "anthropic-claude" {
			entry = candidate
			break
		}
	}
	if entry.Name == "" {
		t.Fatal("shipped Claude provider is not bound to its contract")
	}
	contract := reg.OAuth[entry.OAuth]
	if contract.SignIn != "manual" || contract.TokenEncoding != "json" {
		t.Fatal("shipped Claude consent and wire methods are missing")
	}
	required := entry.CredentialOptions["required_scope"]
	if required == "" {
		t.Fatal("Claude provider must declare its inference scope")
	}
	borrowed := filepath.Join(home, "borrowed.json")
	borrowedBytes, _ := json.Marshal(map[string]any{"claudeAiOauth": map[string]any{"accessToken": "borrowed-access", "refreshToken": "borrowed-refresh", "expiresAt": time.Now().Add(-time.Hour).UnixMilli(), "scopes": []string{required}}})
	if err := os.WriteFile(borrowed, borrowedBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	entry.CredentialOptions["default_file"] = borrowed
	var narrow atomic.Bool
	requests := make(chan map[string]any, 8)
	tokenServer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/token" || r.Header.Get("Content-Type") != "application/json" {
			t.Error("token endpoint/encoding not configured")
		}
		var fields map[string]any
		if err := json.NewDecoder(r.Body).Decode(&fields); err != nil {
			t.Error(err)
		}
		requests <- fields
		access, refresh := "owned-access", "owned-refresh"
		if fields["grant_type"] == "refresh_token" {
			access, refresh = "rotated-access", "rotated-refresh"
		}
		answer := map[string]any{"access_token": access, "refresh_token": refresh, "expires_in": 3600}
		if narrow.Load() {
			answer["scope"] = "unrelated:scope"
		}
		json.NewEncoder(w).Encode(answer)
	}))
	defer tokenServer.Close()
	resourceRequests := make(chan string, 8)
	resourceServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" || r.Header.Get("X-Api-Key") != "" {
			t.Error("inference did not use the native OAuth messages path")
		}
		resourceRequests <- r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"id": "fixture-message", "type": "message", "role": "assistant", "model": "fixture-model", "content": []map[string]string{{"type": "text", "text": "alive"}}, "stop_reason": "end_turn", "usage": map[string]int{"input_tokens": 1, "output_tokens": 1}})
	}))
	defer resourceServer.Close()
	entry.URL = resourceServer.URL
	entry.DefaultModel = "fixture-model"
	entry.Models = []string{entry.DefaultModel}
	entry.ThinkingMode = "off"
	contract.AuthorizeURL = "https://authority.example/authorize"
	contract.TokenURL = tokenServer.URL + "/token"
	reg.OAuth = map[string]oauth.Provider{entry.OAuth: contract}
	reg.Providers = []providerEntry{entry}
	if _, err := saveProvidersFile(a.providersPath(), &reg); err != nil {
		t.Fatal(err)
	}
	a.cfg.LLM = LLMConfig{Provider: entry.Name, Model: entry.DefaultModel, TimeoutSeconds: 5, Retries: -1}
	a.oauthTransport = tokenServer.Client().Transport
	a.oauthGuard = func(context.Context, string) error { return nil }

	first, err := a.SignInProvider(entry.Name)
	if err != nil {
		t.Fatal(err)
	}
	auth, err := a.SignInProvider(entry.Name)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.CompleteSignIn(entry.Name, "old-code#"+stateOf(t, first)); err == nil {
		t.Fatal("superseded manual code accepted")
	}
	if err := a.CompleteSignIn(entry.Name, "bare-code"); err == nil {
		t.Fatal("bare manual code accepted")
	}
	if len(requests) != 0 {
		t.Fatal("unbound input reached authority")
	}
	view := a.signInView("provider:" + entry.Name)
	if view == nil || !view.Manual || view.Status != "pending" {
		t.Fatal("manual completion absent from recoverable snapshot")
	}
	if err := a.CompleteSignIn(entry.Name, "issued-code#"+stateOf(t, auth)); err != nil {
		t.Fatal(err)
	}
	request := <-requests
	u, _ := url.Parse(auth)
	q := u.Query()
	verifier, _ := request["code_verifier"].(string)
	sum := sha256.Sum256([]byte(verifier))
	if request["state"] != q.Get("state") || request["redirect_uri"] != contract.RedirectURI || request["client_id"] != contract.ClientID || base64.RawURLEncoding.EncodeToString(sum[:]) != q.Get("code_challenge") {
		t.Fatal("manual exchange lost the captured contract or PKCE binding")
	}
	for key, value := range contract.AuthorizeParams {
		if q.Get(key) != value {
			t.Fatalf("configured authorize parameter %q missing", key)
		}
	}
	if err := a.CompleteSignIn(entry.Name, "issued-code#"+stateOf(t, auth)); err == nil {
		t.Fatal("manual code replay accepted")
	}
	owned, err := a.ownedCredentialPath(entry.Credential, contract.CredentialFile)
	if err != nil {
		t.Fatal(err)
	}
	if private, err := fileperm.IsRestrictedToOwner(owned); err != nil || !private {
		t.Fatal("owned file is not private")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	chat := func(app *App, want string) {
		t.Helper()
		cc, _, err := app.resolveLLM()
		if err != nil {
			t.Fatal(err)
		}
		response, err := llm.New(&cc).Chat(ctx, []llm.Message{{Role: "user", Content: "Reply alive"}}, llm.ChatOptions{})
		if err != nil || response == nil || len(response.Choices) == 0 || response.Choices[0].Message.Content != "alive" {
			t.Fatalf("inference failed: %v", err)
		}
		if got := <-resourceRequests; got != "Bearer "+want {
			t.Fatal("inference used the wrong credential")
		}
	}
	chat(a, "owned-access")
	loaded, err := a.providerEntryNamed(entry.Name)
	if err != nil {
		t.Fatal(err)
	}
	source, err := a.credentialSource(loaded.Credential, loaded.CredentialOptions, loaded.signIn)
	if err != nil {
		t.Fatal(err)
	}
	if !source.Owned() || source.Path() != owned {
		t.Fatal("sign-in kept using the borrowed credential")
	}
	if _, err := source.ForceRefresh(ctx, source.Generation()); err != nil {
		t.Fatal(err)
	}
	request = <-requests
	if request["refresh_token"] != "owned-refresh" || request["scope"] != strings.Join(contract.BaseScopes, " ") {
		t.Fatal("refresh did not use the owned grant and configured scopes")
	}
	if _, present := request["state"]; present {
		t.Fatal("attempt state leaked into refresh")
	}
	config := *a.cfg
	restarted := New(&config)
	restarted.oauthTransport = a.oauthTransport
	restarted.oauthGuard = a.oauthGuard
	chat(restarted, "rotated-access")
	before, err := os.ReadFile(owned)
	if err != nil {
		t.Fatal(err)
	}
	narrow.Store(true)
	auth, err = a.SignInProvider(entry.Name)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.CompleteSignIn(entry.Name, "narrow-code#"+stateOf(t, auth)); err == nil {
		t.Fatal("a grant without inference scope was published")
	}
	<-requests
	auth, err = a.SignInProvider(entry.Name)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.CancelSignIn(entry.Name); err != nil {
		t.Fatal(err)
	}
	if err := a.CompleteSignIn(entry.Name, "cancelled-code#"+stateOf(t, auth)); err == nil {
		t.Fatal("cancelled manual code accepted")
	}
	after, err := os.ReadFile(owned)
	if err != nil || string(before) != string(after) {
		t.Fatal("failed/cancelled consent replaced the current original")
	}
	if raw, err := os.ReadFile(borrowed); err != nil || string(raw) != string(borrowedBytes) {
		t.Fatal("Claude Code's borrowed original changed")
	}
	if len(requests) != 0 {
		t.Fatal("cancelled consent reached authority")
	}
}

func TestManualRedirectAndExplicitFileBoundaries(t *testing.T) {
	for _, tc := range []struct {
		method, url string
		ok          bool
	}{
		{"manual", "https://authority.example/new-return?app=configured", true},
		{"manual", "http://authority.example/return", false},
		{"manual", "https://user@authority.example/return", false},
		{"manual", "https://authority.example/return#fragment", false},
		{"callback", "https://dashboard.example/oauth/callback", true},
		{"callback", "https://authority.example/oauth/code/callback", false},
	} {
		if got := validateSignInRedirect(tc.method, tc.url) == nil; got != tc.ok {
			t.Fatalf("%s %s: accepted=%v", tc.method, tc.url, got)
		}
	}
	f := newSignInFixture(t, "codex")
	reg, err := f.a.loadProviders()
	if err != nil {
		t.Fatal(err)
	}
	reg.Providers[0].CredentialOptions["file"] = filepath.Join(t.TempDir(), "borrowed.json")
	if _, err := saveProvidersFile(f.a.providersPath(), reg); err != nil {
		t.Fatal(err)
	}
	entry, err := f.a.providerEntryNamed(f.name)
	if err != nil {
		t.Fatal(err)
	}
	if canSignIn(entry) {
		t.Fatal("pinned file advertised unused native sign-in")
	}
	if _, err := f.a.SignInProvider(f.name); err == nil || !strings.Contains(err.Error(), "file override") {
		t.Fatalf("pinned file not refused with a remedy: %v", err)
	}
}
