package oauth

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"
)

// .
// .
func TestConfiguredJSONExchangeAndRefresh(t *testing.T) {
	contract, ok := ProviderTemplate("anthropic-claude", testCatalog())
	if !ok {
		t.Fatal("missing shipped Claude sign-in contract")
	}
	contract.ClientID = "future-client"
	contract.AuthorizeURL = "https://consent.example/new-authorize"
	contract.RedirectURI = "https://consent.example/new-return"
	contract.BaseScopes = []string{"fixture:inference", "fixture:profile"}
	contract.TokenHeaders = map[string]string{"X-Token-Revision": "future"}
	contract.ResourceHeaders = map[string]string{"X-Resource-Only": "resource"}
	contract.TokenParams = map[string]any{"future_state": "{state}", "enabled": true, "lifetime": 1234, "metadata": map[string]any{"revision": "future"}}
	contract.RefreshParams = map[string]any{"future_scope": "{scope}", "extension": []any{"one", "two"}}
	requests := make(chan map[string]any, 4)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.Header.Get("Content-Type") != "application/json" || r.Header.Get("X-Token-Revision") != "future" || r.Header.Get("X-Resource-Only") != "" {
			t.Error("configured token encoding/headers were not used")
		}
		var fields map[string]any
		if err := json.NewDecoder(r.Body).Decode(&fields); err != nil {
			t.Error(err)
		}
		requests <- fields
		json.NewEncoder(w).Encode(map[string]any{"access_token": "fixture-access", "refresh_token": "fixture-refresh", "expires_in": 3600})
	}))
	defer server.Close()
	contract.TokenURL = server.URL + "/new-token"
	p := contract.Params()
	login, err := NewLogin(p)
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(login.URL)
	q := u.Query()
	if q.Get("client_id") != contract.ClientID || q.Get("redirect_uri") != contract.RedirectURI || q.Get("scope") != strings.Join(contract.BaseScopes, " ") {
		t.Fatal("configured consent values were replaced")
	}
	if _, err := login.Exchange(context.Background(), server.Client(), "fixture-code", "wrong"); err == nil {
		t.Fatal("mismatched state accepted")
	}
	if len(requests) != 0 {
		t.Fatal("mismatched state reached token endpoint")
	}
	tokens, err := login.Exchange(context.Background(), server.Client(), "fixture-code", login.State())
	if err != nil {
		t.Fatal(err)
	}
	if tokens.Scope != p.Scope {
		t.Fatal("omitted code-response scope did not retain requested scope")
	}
	request := <-requests
	verifier, _ := request["code_verifier"].(string)
	sum := sha256.Sum256([]byte(verifier))
	if request["future_state"] != login.State() || request["code"] != "fixture-code" || request["client_id"] != contract.ClientID || request["redirect_uri"] != contract.RedirectURI || base64.RawURLEncoding.EncodeToString(sum[:]) != q.Get("code_challenge") {
		t.Fatal("code exchange did not bind consent, code, state and verifier")
	}
	if request["enabled"] != true || request["lifetime"] != float64(1234) || !reflect.DeepEqual(request["metadata"], map[string]any{"revision": "future"}) {
		t.Fatal("JSON parameters lost their types")
	}
	if _, err := RefreshTokens(context.Background(), server.Client(), p, tokens.Refresh); err != nil {
		t.Fatal(err)
	}
	request = <-requests
	if request["future_scope"] != p.Scope || request["refresh_token"] != tokens.Refresh || request["grant_type"] != "refresh_token" {
		t.Fatal("configured refresh parameters were not sent")
	}
	if _, leaked := request["future_state"]; leaked {
		t.Fatal("code-only state leaked into refresh")
	}
	if _, leaked := request["enabled"]; leaked {
		t.Fatal("code-only extras leaked into refresh")
	}
}

func TestGrantConfigurationCannotReplaceProtocolAuthority(t *testing.T) {
	base := OAuthParams{ClientID: "configured", TokenURL: "https://authority.example/token", RedirectURI: "https://authority.example/return"}
	for _, key := range []string{"grant_type", "client_id", "client_secret", "code", "code_verifier", "redirect_uri", "refresh_token", "device_code"} {
		t.Run(key, func(t *testing.T) {
			p := base
			p.TokenParams = map[string]any{key: "replacement"}
			if _, err := ExchangeCode(context.Background(), nil, p, "code", "verifier"); err == nil || !strings.Contains(err.Error(), "protocol field") {
				t.Fatalf("code parameter collision not refused: %v", err)
			}
			p.RefreshParams = p.TokenParams
			if _, err := RefreshTokens(context.Background(), nil, p, "refresh"); err == nil || !strings.Contains(err.Error(), "protocol field") {
				t.Fatalf("refresh parameter collision not refused: %v", err)
			}
		})
	}
	for _, header := range []string{"Authorization", "Host", "Content-Type", "Content-Length", "Proxy-Authorization"} {
		p := base
		p.TokenHeaders = map[string]string{header: "replacement"}
		if _, err := ExchangeCode(context.Background(), nil, p, "code", "verifier"); err == nil || !strings.Contains(err.Error(), "token header") {
			t.Fatalf("header collision not refused: %v", err)
		}
	}
	p := base
	p.TokenEncoding = "unknown"
	if _, err := ExchangeCode(context.Background(), nil, p, "code", "verifier"); err == nil || !strings.Contains(err.Error(), "token_encoding") {
		t.Fatalf("unknown encoding not refused: %v", err)
	}
	p = base
	p.TokenParams = map[string]any{"state": "{state}"}
	if _, err := ExchangeCode(context.Background(), nil, p, "code", "verifier"); err == nil || !strings.Contains(err.Error(), "unavailable") {
		t.Fatalf("missing dynamic state not refused: %v", err)
	}
}

func TestGrantContractCopiesNestedJSON(t *testing.T) {
	var original Provider
	if err := json.Unmarshal([]byte(`{"token_encoding":"json","token_params":{"metadata":{"versions":[1,2]}} ,"refresh_params":{"scope":"{scope}"},"token_headers":{"X-Version":"one"}}`), &original); err != nil {
		t.Fatal(err)
	}
	copy, _ := ProviderTemplate("configured", map[string]Provider{"configured": original})
	copy.TokenParams["metadata"].(map[string]any)["versions"].([]any)[0] = float64(9)
	copy.TokenHeaders["X-Version"] = "changed"
	if original.TokenParams["metadata"].(map[string]any)["versions"].([]any)[0] != float64(1) || original.TokenHeaders["X-Version"] != "one" {
		t.Fatal("a contract copy mutated its source")
	}
	params := original.Params()
	params.RefreshParams["scope"] = "changed"
	if original.RefreshParams["scope"] != "{scope}" {
		t.Fatal("resolved parameters mutated the registry")
	}
}
