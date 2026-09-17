package oauth

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// .
// .
// .
func TestTheAccountClaimIsAContractNotACodePath(t *testing.T) {
	sp, err := specFor(KindCodex)
	if err != nil {
		t.Fatal(err)
	}
	sp, err = applyOverrides(sp, map[string]string{
		"oauth_account_claim": `["https://example.test/auth","acct"]`,
	})
	if err != nil {
		t.Fatalf("oauth_account_claim was refused: %v", err)
	}
	if got := strings.Join(sp.oauth.AccountClaim, "."); got != "https://example.test/auth.acct" {
		t.Fatalf("claim path = %q", got)
	}
	for _, bad := range []string{"not json", "[]", `"a string"`, "{}"} {
		if _, err := applyOverrides(sp, map[string]string{"oauth_account_claim": bad}); err == nil {
			t.Errorf("%q was accepted as a claim path", bad)
		}
	}
}

// .
// .
// .
func TestNoConfiguredClaimMeansNoClaimIsRequired(t *testing.T) {
	if got := claimString("not.a.jwt", nil); got != "" {
		t.Fatalf("a nil path read something: %q", got)
	}
}

// .
// .
// .
func TestAClaimPathWalksObjects(t *testing.T) {
	tok := fakeJWTClaims(t, map[string]any{
		"plain": "top",
		"https://example.test/auth": map[string]any{
			"acct":   "nested",
			"nested": map[string]any{"deep": "deeper"},
		},
		"notanobject": "scalar",
	})
	for _, tc := range []struct {
		name string
		path []string
		want string
	}{
		{"a plain top-level claim", []string{"plain"}, "top"},
		{"a namespaced claim", []string{"https://example.test/auth", "acct"}, "nested"},
		{"two levels down", []string{"https://example.test/auth", "nested", "deep"}, "deeper"},
		{"through a scalar is not a path", []string{"notanobject", "x"}, ""},
		{"a claim that is not there", []string{"absent"}, ""},
		{"an object is not a string", []string{"https://example.test/auth"}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := claimString(tok, tc.path); got != tc.want {
				t.Fatalf("claimString = %q, want %q", got, tc.want)
			}
		})
	}
}

// .
// .
// .
func TestTheShippedChatGPTEntryStillNamesItsAccountClaim(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "config", "providers.json"))
	if err != nil {
		t.Skipf("registry not readable from here: %v", err)
	}
	var reg struct {
		OAuth     map[string]Provider `json:"oauth"`
		Providers []struct {
			OAuth             string            `json:"oauth"`
			Name              string            `json:"name"`
			Credential        string            `json:"credential"`
			CredentialOptions map[string]string `json:"credential_options"`
		} `json:"providers"`
	}
	if err := json.Unmarshal(raw, &reg); err != nil {
		t.Fatal(err)
	}
	for _, p := range reg.Providers {
		if p.Credential != KindCodex {
			continue
		}
		path := reg.OAuth[p.OAuth].ClaimHeaders["ChatGPT-Account-ID"]
		if len(path) != 2 || path[1] != "chatgpt_account_id" {
			t.Fatalf("%s claim path = %v, want the OpenAI namespaced claim", p.Name, path)
		}
		return
	}
	t.Fatal("no shipped provider uses the codex credential")
}

// .
// .
// .
// .
// .
// .
// .
// .
func TestARefusalNamesTheSignInWhenThereIsOne(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	// .
	_, err := testSource(KindCodex, map[string]string{
		"oauth_client_id": "app_test", "oauth_authorize_url": "https://auth.example/authorize",
		"oauth_token_url":    "https://auth.example/token",
		"oauth_redirect_uri": "http://localhost:1455/auth/callback", "oauth_scope": "openid",
	})
	if err == nil {
		t.Fatal("no credential exists; this must refuse")
	}
	if !strings.Contains(err.Error(), "sign in to this provider from Settings") {
		t.Fatalf("the refusal does not name the sign-in: %v", err)
	}
	// .
	// .
	if !strings.Contains(err.Error(), "sign in with that tool first") {
		t.Fatalf("the refusal drops the tool route: %v", err)
	}

	// .
	// .
	if _, err = testSource(KindClaudeCode); err == nil {
		t.Fatal("no credential exists; this must refuse")
	}
	if strings.Contains(err.Error(), "from Settings") {
		t.Fatalf("a remedy was offered that is not configured: %v", err)
	}
	if !strings.Contains(err.Error(), "sign in with that tool first") {
		t.Fatalf("the plain refusal changed: %v", err)
	}
}
