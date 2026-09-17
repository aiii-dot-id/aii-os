package app

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"github.com/aiii-dot-id/aii-os/internal/fileperm"
	"github.com/aiii-dot-id/aii-os/internal/oauth"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func testJWT(t *testing.T, accountID string) string {
	t.Helper()
	hdr := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none"}`))
	claims := map[string]any{"exp": time.Now().Add(time.Hour).Unix(), "https://api.openai.com/auth": map[string]any{"chatgpt_account_id": accountID, "chatgpt_plan_type": "pro"}, "scp": []string{"openid"}}
	b, _ := json.Marshal(claims)
	return hdr + "." + base64.RawURLEncoding.EncodeToString(b) + ".sig"
}

// .
// .
// .
type signInFixture struct {
	a        *App
	name     string
	opts     map[string]string
	redirect string
	calls    *int32
}

func newSignInFixture(t *testing.T, credential string) *signInFixture {
	t.Helper()
	a := newProvidersApp(t)
	fixtureSignInContext(t, a)
	dir, _ := os.Getwd()
	a.cfg.Identity.LedgerPath = filepath.Join(dir, "data", "ledger.jsonl")
	// .
	// .
	// .
	// .
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	var calls int32
	authority := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(30 * time.Millisecond)
		atomicAdd(&calls)
		json.NewEncoder(w).Encode(map[string]any{"access_token": testJWT(t, "acct-native"), "refresh_token": "rt", "id_token": "id", "expires_in": 3600})
	}))
	t.Cleanup(authority.Close)
	a.oauthTransport = authority.Client().Transport
	a.oauthGuard = func(context.Context, string) error { return nil }
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	redirect := "http://127.0.0.1:" + strconv.Itoa(port) + "/oauth/callback"
	const name = "ChatGPT (Plus/Pro)"
	reg := providerRegistry{OAuth: map[string]oauth.Provider{"fixture": {SignIn: "callback"}}, Providers: []providerEntry{{
		OAuth: "fixture", Name: name, APIType: "openai", URL: "https://chatgpt.com/backend-api/codex", Credential: credential,
		CredentialOptions: map[string]string{
			"oauth_client_id": "app_test", "oauth_authorize_url": "https://auth.example/authorize", "oauth_token_url": authority.URL,
			"oauth_redirect_uri": redirect, "oauth_account_claim": `["https://api.openai.com/auth","chatgpt_account_id"]`, "oauth_scope": "openid", "query_client_version": "1.0.0",
		},
	}}}
	if _, err := saveProvidersFile(a.providersPath(), &reg); err != nil {
		t.Fatal(err)
	}
	loaded, err := a.loadProviders()
	if err != nil {
		t.Fatal(err)
	}
	return &signInFixture{a: a, name: name, opts: loaded.Providers[0].CredentialOptions, redirect: redirect, calls: &calls}
}

var addMu sync.Mutex

func atomicAdd(p *int32)  { addMu.Lock(); *p++; addMu.Unlock() }
func load(p *int32) int32 { addMu.Lock(); defer addMu.Unlock(); return *p }

func stateOf(t *testing.T, authURL string) string {
	t.Helper()
	u, err := url.Parse(authURL)
	if err != nil {
		t.Fatal(err)
	}
	return u.Query().Get("state")
}

// .
// .
// .
func TestNativeSignInWritesAnOwnedFileTheProviderThenResolves(t *testing.T) {
	f := newSignInFixture(t, "codex")
	if _, err := f.a.credentialSource("codex", f.opts); err == nil {
		t.Fatal("the credential resolved before any sign-in and with no borrowed file")
	}
	if !f.a.providerDirectoryLive()[0].CanSignIn {
		t.Fatal("the directory does not offer sign-in")
	}
	authURL, err := f.a.SignInProvider(f.name)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.a.CompleteSignIn(f.name, f.redirect+"?code=c1&state="+stateOf(t, authURL)); err != nil {
		t.Fatalf("complete by paste: %v", err)
	}
	owned, _ := f.a.ownedCredentialPath("codex")
	if ok, err := fileperm.IsRestrictedToOwner(owned); err != nil || !ok {
		t.Fatalf("own credential file missing or not private: ok=%v err=%v", ok, err)
	}
	src, err := f.a.credentialSource("codex", f.opts)
	if err != nil {
		t.Fatalf("after sign-in the provider must resolve: %v", err)
	}
	if src.Path() != owned || !src.Owned() {
		t.Fatalf("resolved %s owned=%v, want the identity's own file %s as an owned source", src.Path(), src.Owned(), owned)
	}
	cred, err := src.Credential(context.Background())
	if err != nil || cred.Headers["ChatGPT-Account-ID"] != "acct-native" {
		t.Fatalf("credential from the owned file: %+v %v", cred, err)
	}
	if err := f.a.CompleteSignIn(f.name, f.redirect+"?code=c2&state="+stateOf(t, authURL)); err == nil {
		t.Fatal("a completed sign-in was completable twice")
	}
}

// .
// .
func TestSignInRefusesAnUnsupportedCredentialKind(t *testing.T) {
	f := newSignInFixture(t, "../../escape")
	if f.a.providerDirectoryLive()[0].CanSignIn {
		t.Fatal("sign-in offered for an unknown credential kind")
	}
	if _, err := f.a.SignInProvider(f.name); err == nil || !strings.Contains(err.Error(), "no owned-credential file") {
		t.Fatalf("an unknown kind must be refused before any path is formed: %v", err)
	}
	if p, err := f.a.ownedCredentialPath("../../escape"); err == nil {
		t.Fatalf("a path was formed from a raw kind: %s", p)
	}
	if p, err := f.a.ownedCredentialPath("codex"); err != nil || filepath.Base(p) != "codex.json" {
		t.Fatalf("the closed table maps codex to codex.json: %q %v", p, err)
	}
}

// .
func TestASupersededSignInCannotComplete(t *testing.T) {
	f := newSignInFixture(t, "codex")
	urlA, err := f.a.SignInProvider(f.name)
	if err != nil {
		t.Fatal(err)
	}
	f.a.signInMu.Lock()
	loginA := f.a.signIns["provider:"+f.name].login
	f.a.signInMu.Unlock()
	urlB, err := f.a.SignInProvider(f.name)
	if err != nil {
		t.Fatal(err)
	}
	// .
	if err := f.a.finishSignIn("provider:"+f.name, loginA, "cA", loginA.State()); err == nil {
		t.Fatal("the superseded attempt's callback completed")
	}
	// .
	// .
	if err := f.a.CompleteSignIn(f.name, f.redirect+"?code=cA&state="+stateOf(t, urlA)); err == nil {
		t.Fatal("a stale paste completed the current attempt")
	}
	if load(f.calls) != 0 {
		t.Fatal("the authority was contacted for a superseded attempt")
	}
	if p, _ := f.a.ownedCredentialPath("codex"); fileExists(p) {
		t.Fatal("a superseded attempt wrote a credential")
	}
	if err := f.a.CompleteSignIn(f.name, f.redirect+"?code=cB&state="+stateOf(t, urlB)); err != nil {
		t.Fatalf("the current attempt must complete: %v", err)
	}
}

// .
// .
func TestOnlyOneCompletionWins(t *testing.T) {
	f := newSignInFixture(t, "codex")
	authURL, _ := f.a.SignInProvider(f.name)
	input := f.redirect + "?code=c&state=" + stateOf(t, authURL)
	var wg sync.WaitGroup
	errs := make([]error, 8)
	for i := range errs {
		wg.Add(1)
		go func(i int) { defer wg.Done(); errs[i] = f.a.CompleteSignIn(f.name, input) }(i)
	}
	wg.Wait()
	ok := 0
	for _, e := range errs {
		if e == nil {
			ok++
		}
	}
	if ok != 1 || load(f.calls) != 1 {
		t.Fatalf("completions succeeded=%d authority calls=%d, want exactly one of each", ok, load(f.calls))
	}
}

// .
// .
func TestASignInBindsItsKindAtStart(t *testing.T) {
	f := newSignInFixture(t, "codex")
	authURL, _ := f.a.SignInProvider(f.name)
	// .
	reg, _ := f.a.loadProviders()
	reg.Providers[0].Credential = "claude-code"
	if _, err := saveProvidersFile(f.a.providersPath(), reg); err != nil {
		t.Fatal(err)
	}
	if err := f.a.CompleteSignIn(f.name, f.redirect+"?code=c&state="+stateOf(t, authURL)); err != nil {
		t.Fatal(err)
	}
	codexPath, _ := f.a.ownedCredentialPath("codex")
	claudePath, _ := f.a.ownedCredentialPath("claude-code")
	if _, err := os.Stat(codexPath); err != nil {
		t.Fatal("the credential was not written under the kind bound at start")
	}
	if _, err := os.Stat(claudePath); err == nil {
		t.Fatal("the credential was written under the edited kind")
	}
}

// .
// .
// .
func TestCannotSeeOwnedCredentialIsNotBorrowed(t *testing.T) {
	f := newSignInFixture(t, "codex")
	// .
	home := os.Getenv("HOME")
	os.MkdirAll(filepath.Join(home, ".codex"), 0o700)
	os.WriteFile(filepath.Join(home, ".codex", "auth.json"), []byte(`{"tokens":{"access_token":"`+testJWT(t, "acct-borrowed")+`","account_id":"acct-borrowed"}}`), 0o600)
	// .
	// .
	owned, _ := f.a.ownedCredentialPath("codex")
	os.MkdirAll(filepath.Dir(filepath.Dir(owned)), 0o755)
	os.WriteFile(filepath.Dir(owned), []byte("not a directory"), 0o600)
	if _, err := f.a.credentialSource("codex", f.opts); err == nil || !strings.Contains(err.Error(), "cannot inspect") {
		t.Fatalf("an uninspectable own credential was read as absent and the borrowed file used: %v", err)
	}
}

// .
// .
func TestASignInTimeoutDropsThePendingAttempt(t *testing.T) {
	f := newSignInFixture(t, "codex")
	f.a.signInTimeout = 200 * time.Millisecond
	authURL, _ := f.a.SignInProvider(f.name)
	time.Sleep(600 * time.Millisecond)
	if err := f.a.CompleteSignIn(f.name, f.redirect+"?code=c&state="+stateOf(t, authURL)); err == nil {
		t.Fatal("a timed-out attempt was still completable")
	}
}

// .
// .
// .
// .
// .
// .
// .
func TestAnIdentityRunFromItsOwnDirectoryResolvesItsOwnedCredential(t *testing.T) {
	f := newSignInFixture(t, "codex")
	f.a.cfg.Identity.LedgerPath = "data/ledger.jsonl"
	owned, err := f.a.ownedCredentialPath("codex")
	if err != nil || !filepath.IsAbs(owned) || !strings.HasSuffix(owned, filepath.Join("data", "credentials", "codex.json")) {
		t.Fatalf("the owned path must be absolute under the identity's directory: %q %v", owned, err)
	}
	authURL, err := f.a.SignInProvider(f.name)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.a.CompleteSignIn(f.name, f.redirect+"?code=c1&state="+stateOf(t, authURL)); err != nil {
		t.Fatalf("complete: %v", err)
	}
	src, err := f.a.credentialSource("codex", f.opts)
	if err != nil {
		t.Fatalf("after sign-in the provider must resolve from a relative config: %v", err)
	}
	if src.Path() != owned || !src.Owned() {
		t.Fatalf("resolved %s owned=%v, want %s owned", src.Path(), src.Owned(), owned)
	}
}
