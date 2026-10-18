package app

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"github.com/aiii-dot-id/aii-os/internal/fileperm"
	"github.com/aiii-dot-id/aii-os/internal/oauth"
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

// signInFixture: an app with an identity directory, a fake authority
// reached through the entry's own oauth_token_url, and a free redirect
// port. calls counts authority round-trips.
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
	// Isolate both platform home variables so sign-in fixtures cannot read
	// credentials belonging to the account running the test.
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
	port := fixedTestPort(t)
	redirect := "http://127.0.0.1:" + strconv.Itoa(port) + "/oauth/callback"
	const name = "ChatGPT (Plus/Pro)"
	reg := providerRegistry{OAuth: map[string]oauth.Provider{"fixture": {SignIn: "callback"}}, Providers: []providerEntry{{
		OAuth: "fixture", Name: name, APIType: "openai", URL: "https://chatgpt.com/backend-api/codex", Credential: credential,
		CredentialOptions: map[string]string{
			"oauth_client_id": "app_test", "oauth_authorize_url": "https://auth.example/authorize", "oauth_token_url": authority.URL,
			"oauth_redirect_uri": redirect, "oauth_account_claim": `["https://api.openai.com/auth","chatgpt_account_id"]`, "oauth_scope": "openid", "query_client_version": "1.0.0",
			// The header the claim rides in: an entry naming a claim names it.
			"account_header": "ChatGPT-Account-ID",
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

// THE IDENTITY'S OWN SIGN-IN IS RENEWED BY THE HOST, so its access token's
// expiry asks nothing of the operator. The status line used to say "no
// longer usable — refresh it with its own tool; the identity cannot think
// until you do" and the card showed EXPIRED with the sign-in controls, from
// the skew boundary until the next request refreshed the token. Each was
// false: there is no other tool, and the next turn refreshes it.
func TestAnOwnedCredentialTheHostRenewsIsNotWarnedAboutNorShownExpired(t *testing.T) {
	f := newSignInFixture(t, "codex")
	authURL, err := f.a.SignInProvider(f.name)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.a.CompleteSignIn(f.name, f.redirect+"?code=c1&state="+stateOf(t, authURL)); err != nil {
		t.Fatalf("complete by paste: %v", err)
	}
	// The same file five minutes from its nominal expiry: inside the skew,
	// so a borrowed credential in this state is refused and warned about.
	owned, err := f.a.ownedCredentialPath("codex")
	if err != nil {
		t.Fatal(err)
	}
	if err := oauth.WriteTokenFile(owned, &oauth.Tokens{Access: testJWT(t, "acct-native"), Refresh: "rt", Expires: time.Now().Add(5 * time.Minute)}); err != nil {
		t.Fatal(err)
	}
	reg, err := f.a.loadProviders()
	if err != nil {
		t.Fatal(err)
	}
	entry := reg.Providers[0]
	f.a.adoptProvider(entry)
	asked := load(f.calls)
	if got := f.a.credentialWarning(); got != "" {
		t.Fatalf("the host renews this credential itself and the status line says %q", got)
	}
	ci := f.a.credentialInfo(entry)
	if ci == nil || ci.Error != "" || ci.Expired || ci.ExpiresAt != "" {
		t.Fatalf("a credential the host renews was published with an expiry the page reads as EXPIRED: %+v", ci)
	}
	if load(f.calls) != asked {
		t.Fatal("describing the credential spent its refresh token")
	}
}

// credentialReplacedMark reads the runtime's mark that the running client
// holds a credential source a sign-in has replaced.
func credentialReplacedMark(a *App) bool {
	a.providers.runtime.mu.RLock()
	defer a.providers.runtime.mu.RUnlock()
	return a.providers.runtime.credentialReplaced
}

// THE RUNNING CLIENT TAKES A NEW SIGN-IN. A client holds the credential
// source it was built with, so one built on Claude Code's or Codex's file
// went on reading that file after the operator signed in: the page said
// connected, the probe said ok, and every turn failed on the borrowed
// file's expiry until a restart. A sign-in that writes the file the
// identity's entry resolves marks the runtime as no longer matching, which
// is what has the reload rebuild the client. The file decides: the identity
// keeps one for a credential kind, so a sign-in begun from another entry of
// that kind marks it too, and a sign-in for a credential the identity is
// not on marks nothing.
func TestASignInThatWritesTheCredentialTheIdentityIsOnMarksItsClientForRebuild(t *testing.T) {
	signIn := func(t *testing.T, on func(providerEntry) providerEntry) *signInFixture {
		t.Helper()
		f := newSignInFixture(t, "codex")
		reg, err := f.a.loadProviders()
		if err != nil {
			t.Fatal(err)
		}
		f.a.adoptProvider(on(reg.Providers[0]))
		if credentialReplacedMark(f.a) {
			t.Fatal("marked before any sign-in")
		}
		authURL, err := f.a.SignInProvider(f.name)
		if err != nil {
			t.Fatal(err)
		}
		if err := f.a.CompleteSignIn(f.name, f.redirect+"?code=c1&state="+stateOf(t, authURL)); err != nil {
			t.Fatalf("complete by paste: %v", err)
		}
		return f
	}
	t.Run("the entry the identity is on", func(t *testing.T) {
		f := signIn(t, func(e providerEntry) providerEntry { return e })
		if !credentialReplacedMark(f.a) {
			t.Fatal("the identity is on this entry, its client was built before the sign-in, and nothing has it rebuilt")
		}
	})
	t.Run("another entry of the same credential", func(t *testing.T) {
		f := signIn(t, func(e providerEntry) providerEntry { e.Name = "Elsewhere"; return e })
		if !credentialReplacedMark(f.a) {
			t.Fatal("the sign-in wrote the file the identity's entry resolves, under another entry's name, and nothing has its client rebuilt")
		}
	})
	t.Run("an entry pinned to a file of its own", func(t *testing.T) {
		f := signIn(t, func(e providerEntry) providerEntry {
			e.CredentialOptions = map[string]string{"file": filepath.Join(t.TempDir(), "auth.json")}
			return e
		})
		if credentialReplacedMark(f.a) {
			t.Fatal("an entry that never reads the identity's own file was marked by a sign-in that wrote it")
		}
	})
	t.Run("another credential", func(t *testing.T) {
		f := signIn(t, func(e providerEntry) providerEntry {
			e.Credential, e.signIn = "claude-code", oauth.Provider{}
			return e
		})
		if credentialReplacedMark(f.a) {
			t.Fatal("a sign-in for a credential the identity is not on marked the running client")
		}
	})
}

func TestAClientResolvedBeforeASignInIsStillMarkedWhenItIsAdopted(t *testing.T) {
	f := newSignInFixture(t, "codex")
	home := os.Getenv("HOME")
	os.MkdirAll(filepath.Join(home, ".codex"), 0o700)
	os.WriteFile(filepath.Join(home, ".codex", "auth.json"), []byte(`{"tokens":{"access_token":"`+testJWT(t, "acct-borrowed")+`","account_id":"acct-borrowed"}}`), 0o600)
	resolve := func() (providerEntry, bool) {
		t.Helper()
		reg, err := f.a.loadProviders()
		if err != nil {
			t.Fatal(err)
		}
		cc, entry, err := f.a.resolveLLMConfig(LLMConfig{Provider: f.name, Model: "gpt-fixture"}, reg)
		if err != nil {
			t.Fatal(err)
		}
		return entry, cc.Credential.(oauthAdapter).s.Owned()
	}
	running, owned := resolve()
	if owned {
		t.Fatal("the fixture's first client is not on the borrowed file")
	}
	f.a.adoptRuntime(running, nil)
	// A door resolves its candidate and waits for the turn in progress.
	candidate, _ := resolve()

	authURL, err := f.a.SignInProvider(f.name)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.a.CompleteSignIn(f.name, f.redirect+"?code=c1&state="+stateOf(t, authURL)); err != nil {
		t.Fatalf("complete by paste: %v", err)
	}
	if !credentialReplacedMark(f.a) {
		t.Fatal("the sign-in did not mark the running client")
	}

	// The turn ends and the door adopts what it resolved before the sign-in.
	f.a.adoptRuntime(candidate, nil)
	if !credentialReplacedMark(f.a) {
		t.Fatal("adopting a client resolved before the sign-in spent the mark: the identity is on the borrowed file and nothing rebuilds its client")
	}
	// What a reload resolves now is on the identity's own file, and adopting
	// that spends the mark.
	fresh, owned := resolve()
	if !owned {
		t.Fatal("a client resolved after the sign-in is not on the identity's own file")
	}
	f.a.adoptRuntime(fresh, nil)
	if credentialReplacedMark(f.a) {
		t.Fatal("a client resolved after the sign-in is still marked for rebuild")
	}
}

func TestAReloadRebuildsAClientWhoseCredentialWasSignedInAnew(t *testing.T) {
	f := pass5New(t)
	before := f.a.llmSwap.Current()
	f.a.reloadConfig()
	if f.a.llmSwap.Current() != before {
		t.Fatal("a reload of an unchanged configuration replaced the running client")
	}
	f.a.providers.runtime.mu.Lock()
	f.a.providers.runtime.credentialReplaced = true
	f.a.providers.runtime.mu.Unlock()
	f.a.reloadConfig()
	rebuilt := f.a.llmSwap.Current()
	if rebuilt == before {
		t.Fatal("the running client still holds the credential source a sign-in replaced")
	}
	if credentialReplacedMark(f.a) {
		t.Fatal("the rebuilt client was adopted and the mark was not spent")
	}
	f.a.reloadConfig()
	if f.a.llmSwap.Current() != rebuilt {
		t.Fatal("the client was rebuilt again with nothing to rebuild it for")
	}
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

// END TO END: unresolvable before; sign-in offered; PKCE URL; completed by
// paste; the identity's own 0600 file; the provider resolves THAT file
// as an OWNED source and yields a credential; no second completion.
func TestNativeSignInWritesAnOwnedFileTheProviderThenResolves(t *testing.T) {
	f := newSignInFixture(t, "codex")
	if _, err := f.a.credentialSource("codex", f.opts); err == nil {
		t.Fatal("the credential resolved before any sign-in and with no borrowed file")
	}
	if !f.a.providerDirectory().Providers[0].CanSignIn {
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

// PATH CONFINEMENT: the credential kind is configurable and never
// reaches a path. An unknown kind gets no file, no attempt, no offer.
func TestSignInRefusesAnUnsupportedCredentialKind(t *testing.T) {
	f := newSignInFixture(t, "../../escape")
	if f.a.providerDirectory().Providers[0].CanSignIn {
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

// SUPERSESSION: a newer attempt makes the older one uncompletable.
func TestASupersededSignInCannotComplete(t *testing.T) {
	f := newSignInFixture(t, "codex")
	urlA, err := f.a.SignInProvider(f.name)
	if err != nil {
		t.Fatal(err)
	}
	f.a.providers.custody.mu.Lock()
	loginA := f.a.providers.custody.signIns["provider:"+f.name].login
	f.a.providers.custody.mu.Unlock()
	urlB, err := f.a.SignInProvider(f.name)
	if err != nil {
		t.Fatal(err)
	}
	// A's own callback arriving late: its login is no longer registered.
	if err := f.a.finishSignIn("provider:"+f.name, loginA, "cA", loginA.State()); err == nil {
		t.Fatal("the superseded attempt's callback completed")
	}
	// A stale paste for A against the current attempt: refused without
	// consuming the current attempt.
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

// ONE COMPLETION: concurrent completions of the same attempt claim it
// once — one exchange, one writer.
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

// BOUND AT START: a provider edit during the sign-in cannot change the
// kind the result is written under.
func TestASignInBindsItsKindAtStart(t *testing.T) {
	f := newSignInFixture(t, "codex")
	authURL, _ := f.a.SignInProvider(f.name)
	// The operator edits the entry mid-flight to a different (also known) kind.
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

// CANNOT-SEE IS NOT ABSENT: when the identity's own file cannot be
// inspected, resolution refuses rather than silently using a borrowed
// file that would speak as a different account.
func TestCannotSeeOwnedCredentialIsNotBorrowed(t *testing.T) {
	f := newSignInFixture(t, "codex")
	// A borrowed file exists and would otherwise be used.
	home := os.Getenv("HOME")
	os.MkdirAll(filepath.Join(home, ".codex"), 0o700)
	os.WriteFile(filepath.Join(home, ".codex", "auth.json"), []byte(`{"tokens":{"access_token":"`+testJWT(t, "acct-borrowed")+`","account_id":"acct-borrowed"}}`), 0o600)
	// Make the credentials DIRECTORY a regular file: Stat of the owned
	// path answers ENOTDIR — not ENOENT — deterministically, root or not.
	owned, _ := f.a.ownedCredentialPath("codex")
	os.MkdirAll(filepath.Dir(filepath.Dir(owned)), 0o755)
	os.WriteFile(filepath.Dir(owned), []byte("not a directory"), 0o600)
	if _, err := f.a.credentialSource("codex", f.opts); err == nil || !strings.Contains(err.Error(), "cannot inspect") {
		t.Fatalf("an uninspectable own credential was read as absent and the borrowed file used: %v", err)
	}
}

// LIFECYCLE: an attempt whose callback times out is dropped, so a stale
// paste finds no attempt.
func TestASignInTimeoutDropsThePendingAttempt(t *testing.T) {
	f := newSignInFixture(t, "codex")
	f.a.signInTimeout = 200 * time.Millisecond
	authURL, _ := f.a.SignInProvider(f.name)
	time.Sleep(600 * time.Millisecond)
	if err := f.a.CompleteSignIn(f.name, f.redirect+"?code=c&state="+stateOf(t, authURL)); err == nil {
		t.Fatal("a timed-out attempt was still completable")
	}
}

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
