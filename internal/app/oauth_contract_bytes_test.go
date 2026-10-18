package app

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/cryptotest"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/oauth"
)

type contractTransport func(*http.Request) (*http.Response, error)

func (f contractTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestTheSubscriptionContractsBytesOnTheWire(t *testing.T) {
	a := newProvidersApp(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	write := func(rel, body string) {
		t.Helper()
		p := filepath.Join(home, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(".claude/.credentials.json", `{"claudeAiOauth":{"accessToken":"claude-access","expiresAt":`+
		jsonInt(time.Now().Add(24*time.Hour).UnixMilli())+`,"scopes":["user:profile","user:inference"]}}`)
	// The token carries two claims in the vendor's namespace: the shipped
	// contract keys on one, the older file below on the other.
	token := contractJWT(t, map[string]any{"chatgpt_account_id": "acct-pin", "chatgpt_user_id": "user-pin"})
	write(".codex/auth.json", `{"tokens":{"access_token":"`+token+`"}}`)

	reg, err := a.loadProviders()
	if err != nil {
		t.Fatal(err)
	}
	var claude, chatgpt providerEntry
	for _, e := range reg.Providers {
		switch e.Credential {
		case "claude-code":
			claude = e
		case "codex":
			chatgpt = e
		}
	}
	if claude.Name == "" || chatgpt.Name == "" {
		t.Fatal("the shipped registry lost a subscription entry")
	}
	// The older spelling, as a file written before the contracts were named
	// would carry it, with values that differ from the shipped contract's.
	older := chatgpt
	older.Name = "ChatGPT (older file)"
	older.Default = false
	older.CredentialOptions = map[string]string{}
	for k, v := range chatgpt.CredentialOptions {
		older.CredentialOptions[k] = v
	}
	older.CredentialOptions["oauth_originator"] = "operator-cli"
	older.CredentialOptions["oauth_id_token_add_organizations"] = "false"
	older.CredentialOptions["oauth_account_claim"] = `["https://api.openai.com/auth","chatgpt_user_id"]`
	reg.Providers = append(reg.Providers, older)
	if _, err := saveProvidersFile(a.providersPath(), reg); err != nil {
		t.Fatal(err)
	}
	if older, err = a.providerEntryNamed(older.Name); err != nil {
		t.Fatal(err)
	}

	var sent string
	client := &http.Client{Transport: contractTransport(func(r *http.Request) (*http.Response, error) {
		b, _ := io.ReadAll(r.Body)
		sent = r.Header.Get("Content-Type") + " " + string(b)
		body := `{"access_token":"` + token + `","refresh_token":"rt","expires_in":3600}`
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
	})}
	ctx := t.Context()
	// One seed for every attempt: the same state, verifier and challenge.
	const (
		pkce     = "code_challenge=14ygymAzFeyq9Cf_hC48ZJV1ix8dkuZ6I2GxYU1G9MM&code_challenge_method=S256"
		state    = "p_V7SwqXFKTf0QSXc_15AmYUFPffN5t7"
		verifier = "1fIhlI5bBJhkbsorZWj9pRqVgomieuAkNcmgQ7eaw2OnUonac6dHek-hJiMewb2PjJkyBab0ojRhmu0xaSTVWA"
		form     = "application/x-www-form-urlencoded "
		openai   = "https://auth.openai.com/oauth/authorize?client_id=app_EMoamEEZ73f0CkXaXp7hrann&" + pkce
		chatgptX = form + "client_id=app_EMoamEEZ73f0CkXaXp7hrann&code=code-58&code_verifier=" + verifier +
			"&grant_type=authorization_code&redirect_uri=http%3A%2F%2Flocalhost%3A1455%2Fauth%2Fcallback"
		chatgptR = form + "client_id=app_EMoamEEZ73f0CkXaXp7hrann&grant_type=refresh_token&refresh_token=refresh-58"
		tail     = "&redirect_uri=http%3A%2F%2Flocalhost%3A1455%2Fauth%2Fcallback&response_type=code&scope=openid+profile+email+offline_access&state=" + state
	)
	for _, tc := range []struct {
		entry                           providerEntry
		url, exchange, refresh, account string
	}{
		{claude,
			"https://claude.com/cai/oauth/authorize?client_id=9d1c250a-e61b-44d9-88ed-5944d1962f5e&code=true&" + pkce +
				"&redirect_uri=https%3A%2F%2Fplatform.claude.com%2Foauth%2Fcode%2Fcallback&response_type=code&scope=user%3Aprofile+user%3Ainference&state=" + state,
			`application/json {"client_id":"9d1c250a-e61b-44d9-88ed-5944d1962f5e","code":"code-58","code_verifier":"` + verifier +
				`","grant_type":"authorization_code","redirect_uri":"https://platform.claude.com/oauth/code/callback","state":"` + state + `"}`,
			`application/json {"client_id":"9d1c250a-e61b-44d9-88ed-5944d1962f5e","grant_type":"refresh_token","refresh_token":"refresh-58","scope":"user:profile user:inference"}`,
			""},
		{chatgpt, openai + "&id_token_add_organizations=true&originator=aii-os" + tail, chatgptX, chatgptR, "acct-pin"},
		{older, openai + "&id_token_add_organizations=false&originator=operator-cli" + tail, chatgptX, chatgptR, "user-pin"},
	} {
		t.Run(tc.entry.Name, func(t *testing.T) {
			e := tc.entry
			cryptotest.SetGlobalRandom(t, 58)
			login, err := oauth.NewLogin(e.signIn.Params())
			if err != nil {
				t.Fatal(err)
			}
			if login.URL != tc.url {
				t.Errorf("authorize URL\n got %s\nwant %s", login.URL, tc.url)
			}
			if _, err := login.Exchange(ctx, client, "code-58", login.State()); err != nil {
				t.Fatal(err)
			}
			if sent != tc.exchange {
				t.Errorf("code exchange\n got %s\nwant %s", sent, tc.exchange)
			}
			src, err := a.credentialSource(e.Credential, e.CredentialOptions, e.signIn)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := oauth.RefreshTokens(ctx, client, src.OAuth(), "refresh-58"); err != nil {
				t.Fatal(err)
			}
			if sent != tc.refresh {
				t.Errorf("refresh grant\n got %s\nwant %s", sent, tc.refresh)
			}
			cred, err := src.Credential(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if got := cred.Headers["ChatGPT-Account-ID"]; got != tc.account {
				t.Errorf("ChatGPT-Account-ID = %q, want %q", got, tc.account)
			}
		})
	}
}

// contractJWT is an unsigned access token whose vendor namespace carries
// the given claims.
func contractJWT(t *testing.T, auth map[string]any) string {
	t.Helper()
	b, err := json.Marshal(map[string]any{"exp": time.Now().Add(time.Hour).Unix(), "https://api.openai.com/auth": auth})
	if err != nil {
		t.Fatal(err)
	}
	return base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none"}`)) + "." + base64.RawURLEncoding.EncodeToString(b) + ".sig"
}

func jsonInt(n int64) string { b, _ := json.Marshal(n); return string(b) }

// A CLAIM WITH NO HEADER TO RIDE IN IS A BROKEN ENTRY. oauth_account_claim
// on a credential with no account_header was carried silently: the token
// exchange required the claim and the credential read ignored it. The entry
// is now refused at admission with the reason, and only it.
func TestAnAccountClaimWithNoHeaderIsRefusedByName(t *testing.T) {
	a := newProvidersApp(t)
	reg, err := a.loadProviders()
	if err != nil {
		t.Fatal(err)
	}
	var claude providerEntry
	for _, e := range reg.Providers {
		if e.Credential == "claude-code" {
			claude = e
		}
	}
	if claude.Name == "" || claude.CredentialOptions["account_header"] != "" {
		t.Fatal("the shipped Claude entry is the fixture: present, with no account_header")
	}
	bad := claude
	bad.Name, bad.Default = "Claude (claim, no header)", false
	bad.CredentialOptions = map[string]string{"oauth_account_claim": `["sub"]`}
	reg.Providers = append(reg.Providers, bad)
	if _, err := saveProvidersFile(a.providersPath(), reg); err != nil {
		t.Fatal(err)
	}
	loaded, err := a.loadProviders()
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range loaded.Providers {
		if e.Name == bad.Name {
			t.Fatal("an account claim with no header to ride in was admitted")
		}
	}
	for _, b := range loaded.broken {
		if b.name == bad.Name {
			if !strings.Contains(b.reason, oauth.ErrClaimWithoutHeader.Error()) {
				t.Fatalf("broken for the wrong reason: %s", b.reason)
			}
			if _, err := a.providerEntryNamed(claude.Name); err != nil {
				t.Fatalf("the other entries must stay admitted: %v", err)
			}
			return
		}
	}
	t.Fatal("the refused entry is not listed as broken")
}
