package oauth

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"github.com/aiii-dot-id/aii-os/internal/fileperm"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func codexParams() OAuthParams {
	return OAuthParams{
		ClientID: "app_test", AuthorizeURL: "https://auth.example/oauth/authorize",
		TokenURL: "https://auth.example/oauth/token", RedirectURI: "http://localhost:1455/auth/callback",
		Scope: "openid profile email offline_access", Originator: "aii-os", IDTokenAddOrganizations: true,
	}
}

// .
// .
func fakeJWT(t *testing.T, accountID string, exp int64) string {
	t.Helper()
	hdr := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none"}`))
	claims := map[string]any{"exp": exp, "https://api.openai.com/auth": map[string]any{"chatgpt_account_id": accountID, "chatgpt_plan_type": "pro"}, "scp": []string{"openid"}}
	b, _ := json.Marshal(claims)
	return hdr + "." + base64.RawURLEncoding.EncodeToString(b) + ".sig"
}

// .
// .
// .
func TestTheSignInContractIsConfiguredNotConstant(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	// .
	p := filepath.Join(home, ".codex", "auth.json")
	os.MkdirAll(filepath.Dir(p), 0o700)
	os.WriteFile(p, []byte(`{"tokens":{"access_token":"`+fakeJWT(t, "acct", time.Now().Add(time.Hour).Unix())+`","account_id":"acct"}}`), 0o600)
	opts := map[string]string{
		"oauth_client_id": "app_from_registry", "oauth_authorize_url": "https://auth.openai.com/oauth/authorize",
		"oauth_token_url": "https://auth.openai.com/oauth/token", "oauth_redirect_uri": "http://localhost:1455/auth/callback",
		"oauth_scope": "openid profile email offline_access", "oauth_originator": "aii-os", "oauth_id_token_add_organizations": "true",
	}
	src, err := New(KindCodex, opts)
	if err != nil {
		t.Fatal(err)
	}
	got := src.OAuth()
	if got.ClientID != "app_from_registry" || got.TokenURL != "https://auth.openai.com/oauth/token" || !got.IDTokenAddOrganizations || !got.Complete() {
		t.Fatalf("the sign-in contract did not come from the options: %+v", got)
	}
	// .
	src2, err := New(KindCodex, opts, map[string]string{"oauth_client_id": "app_operator"})
	if err != nil {
		t.Fatal(err)
	}
	if src2.OAuth().ClientID != "app_operator" {
		t.Fatalf("operator override of oauth_client_id not honoured: %q", src2.OAuth().ClientID)
	}
	// .
	if _, err := NewLogin(OAuthParams{}); err == nil {
		t.Fatal("a kind with no sign-in contract must refuse to start one")
	}
}

func TestNewLoginBuildsAPKCEAuthorizeURL(t *testing.T) {
	l, err := NewLogin(codexParams())
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(l.URL)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	sum := sha256.Sum256([]byte(l.verifier))
	want := base64.RawURLEncoding.EncodeToString(sum[:])
	if q.Get("code_challenge") != want || q.Get("code_challenge_method") != "S256" {
		t.Fatalf("the challenge is not S256 of the verifier: %q", q.Get("code_challenge"))
	}
	if len(l.verifier) < 43 || len(l.verifier) > 128 {
		t.Fatalf("verifier length %d outside RFC 7636", len(l.verifier))
	}
	for k, v := range map[string]string{"response_type": "code", "client_id": "app_test", "redirect_uri": "http://localhost:1455/auth/callback",
		"scope": "openid profile email offline_access", "state": l.State(), "id_token_add_organizations": "true", "originator": "aii-os"} {
		if q.Get(k) != v {
			t.Fatalf("%s = %q, want %q", k, q.Get(k), v)
		}
	}
	l2, _ := NewLogin(codexParams())
	if l2.State() == l.State() || l2.verifier == l.verifier {
		t.Fatal("two logins share state or verifier")
	}
}

func TestParseAuthorizationInput(t *testing.T) {
	code, state, err := ParseAuthorizationInput("http://localhost:1455/auth/callback?code=abc&state=xyz")
	if err != nil || code != "abc" || state != "xyz" {
		t.Fatalf("url: %q %q %v", code, state, err)
	}
	if _, _, err := ParseAuthorizationInput("  abc  "); err == nil {
		t.Fatal("a bare code without state was accepted")
	}
	if c, s, err := ParseAuthorizationInput("abc#xyz"); err != nil || c != "abc" || s != "xyz" {
		t.Fatalf("hash: %q %q %v", c, s, err)
	}
	if _, _, err := ParseAuthorizationInput(""); err == nil {
		t.Fatal("empty accepted")
	}
	if _, _, err := ParseAuthorizationInput("http://localhost:1455/auth/callback?state=only"); err == nil {
		t.Fatal("a URL without a code was accepted")
	}
}

// .
// .
func TestCompleteExchangesTheCodeAndRefreshRotates(t *testing.T) {
	var seen []url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		seen = append(seen, r.PostForm)
		n := len(seen)
		json.NewEncoder(w).Encode(map[string]any{
			"access_token": fakeJWT(t, "acct-1", time.Now().Add(time.Hour).Unix()), "refresh_token": "refresh-" + string(rune('0'+n)), "id_token": "id", "expires_in": 3600,
		})
	}))
	defer srv.Close()
	old := codexHTTP
	codexHTTP = srv.Client()
	defer func() { codexHTTP = old }()
	p := codexParams()
	p.TokenURL = srv.URL
	l, _ := NewLogin(p)
	if _, err := l.Complete(context.Background(), "code-1", "wrong-state"); err == nil || len(seen) != 0 {
		t.Fatalf("a state mismatch must be refused before any request (err=%v, requests=%d)", err, len(seen))
	}
	tok, err := l.Complete(context.Background(), "code-1", l.State())
	if err != nil {
		t.Fatal(err)
	}
	f := seen[0]
	if f.Get("grant_type") != "authorization_code" || f.Get("code") != "code-1" || f.Get("code_verifier") != l.verifier || f.Get("client_id") != "app_test" || f.Get("redirect_uri") != p.RedirectURI {
		t.Fatalf("exchange form wrong: %v", f)
	}
	if tok.AccountID != "acct-1" || tok.Refresh != "refresh-1" || tok.Expires.Before(time.Now().Add(50*time.Minute)) {
		t.Fatalf("tokens wrong: %+v", tok)
	}
	rot, err := Refresh(context.Background(), p, tok.Refresh)
	if err != nil {
		t.Fatal(err)
	}
	if f := seen[1]; f.Get("grant_type") != "refresh_token" || f.Get("refresh_token") != "refresh-1" || f.Get("client_id") != "app_test" {
		t.Fatalf("refresh form wrong: %v", f)
	}
	if rot.Refresh != "refresh-2" {
		t.Fatalf("refresh did not rotate the pair: %+v", rot)
	}
}

// .
// .
func TestServeCallbackDeliversTheCodeOnce(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	p := codexParams()
	p.RedirectURI = "http://127.0.0.1:" + itoa(port) + "/auth/callback"
	l, _ := NewLogin(p)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	done := make(chan string, 1)
	go func() { c, _ := l.ServeCallback(ctx); done <- c }()
	time.Sleep(150 * time.Millisecond)
	if r, err := http.Get(p.RedirectURI + "?code=bad&state=not-mine"); err != nil || r.StatusCode != http.StatusBadRequest {
		t.Fatalf("a wrong state was not refused: %v %v", r, err)
	}
	if r, err := http.Get(p.RedirectURI + "?code=good&state=" + l.State()); err != nil || r.StatusCode != http.StatusOK {
		t.Fatalf("the matching redirect was not accepted: %v %v", r, err)
	}
	select {
	case c := <-done:
		if c != "good" {
			t.Fatalf("delivered %q, want good", c)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the code was not delivered")
	}
}

func TestWriteAuthFileIsPrivateAndParseable(t *testing.T) {
	p := filepath.Join(t.TempDir(), "creds", "codex.json")
	tok := &CodexTokens{Access: fakeJWT(t, "acct-9", time.Now().Add(time.Hour).Unix()), Refresh: "r", IDToken: "i", AccountID: "acct-9", Expires: time.Now().Add(time.Hour)}
	if err := WriteAuthFile(p, tok); err != nil {
		t.Fatal(err)
	}
	// .
	// .
	if ok, err := fileperm.IsRestrictedToOwner(p); err != nil || !ok {
		t.Fatalf("not private: ok=%v err=%v", ok, err)
	}
	raw, _ := os.ReadFile(p)
	parsed, err := parseCodex(raw)
	if err != nil || parsed.access != tok.Access || parsed.headers["ChatGPT-Account-ID"] != "acct-9" {
		t.Fatalf("the file AII OS writes must be what parseCodex reads: %+v %v", parsed, err)
	}
	if !strings.Contains(string(raw), `"owned_by": "aii-os"`) {
		t.Fatal("the owned marker is missing")
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	return string(b)
}
