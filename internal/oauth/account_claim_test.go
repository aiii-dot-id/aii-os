package oauth

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The authority's own rule is CONFIGURED. It used to be written into the
// shared exchange as "chatgpt_account_id" under an OpenAI namespace,
// where it refused every other authority's perfectly good token.
func TestTheAccountClaimIsAContractNotACodePath(t *testing.T) {
	sp, err := specFor(KindCodex)
	if err != nil {
		t.Fatal(err)
	}
	claim := map[string]string{"oauth_account_claim": `["https://example.test/auth","acct"]`}
	// The claim rides in a header; a claim with no header to ride in is
	// refused by name rather than carried where one reader enforced it and
	// the other ignored it.
	if _, err := applyOverrides(sp, claim); !errors.Is(err, ErrClaimWithoutHeader) {
		t.Fatalf("a claim with no account_header: %v, want ErrClaimWithoutHeader", err)
	}
	claim["account_header"] = "X-Account"
	sp, err = applyOverrides(sp, claim)
	if err != nil {
		t.Fatalf("oauth_account_claim was refused: %v", err)
	}
	if got := strings.Join(sp.oauth.ClaimHeaders["X-Account"], "."); got != "https://example.test/auth.acct" {
		t.Fatalf("claim path = %q, want it as the X-Account claim header", got)
	}
	for _, bad := range []string{"not json", "[]", `"a string"`, "{}"} {
		if _, err := applyOverrides(sp, map[string]string{"oauth_account_claim": bad}); err == nil {
			t.Errorf("%q was accepted as a claim path", bad)
		}
	}
}

// AN AUTHORITY THAT ASKS FOR NO CLAIM GETS NONE ASKED OF IT. This is the
// whole point: a second authority must be able to use this flow without
// a copy of it.
func TestNoConfiguredClaimMeansNoClaimIsRequired(t *testing.T) {
	if got := claimString("not.a.jwt", nil); got != "" {
		t.Fatalf("a nil path read something: %q", got)
	}
}

// The path walks objects: a namespaced vendor claim is two elements, a
// plain one is one, and a path that does not resolve reads empty rather
// than panicking on a type.
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

// The shipped registry keeps OpenAI's rule exactly where it was, as
// configuration: the behaviour that shipped must not change because the
// rule moved.
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

// A REFUSAL NAMES ITS REMEDY. Telling an operator to "sign in with that
// tool first" sends them to install something they may not need — and on
// a platform where that tool keeps nothing we can read, to nothing at
// all. When this credential can sign in natively, the refusal says so.
//
// It is OFFERED and never performed: a sign-in opens a browser and waits
// for a person, so it is the operator's act and not a fallback the
// runtime can take on its own.
func TestARefusalNamesTheSignInWhenThereIsOne(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	// os.UserHomeDir reads USERPROFILE on Windows: with HOME alone this
	// test read the machine user's own sign-in and nothing refused.
	t.Setenv("USERPROFILE", home)

	// New loads eagerly, so the refusal is what it returns.
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
	// The other route is still named: an operator who has the tool should
	// not be told the only way is a browser.
	if !strings.Contains(err.Error(), "sign in with that tool first") {
		t.Fatalf("the refusal drops the tool route: %v", err)
	}

	// And with no sign-in configured the old sentence stands alone:
	// naming a remedy that does not exist is worse than naming none.
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
