package oauth

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/aiii-dot-id/aii-os/internal/fileperm"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// CUSTODY IS STRUCTURAL. A file carrying the owned_by marker is still
// BORROWED when opened by New — the marker is data anyone can write —
// and only NewOwned may refresh.
func TestOnlyAnOwnedConstructorCanRefresh(t *testing.T) {
	srv, calls := fakeAuthority(t)
	old := signInHTTP
	signInHTTP = srv.Client()
	defer func() { signInHTTP = old }()
	path := filepath.Join(t.TempDir(), "codex.json")
	writeCodexFile(t, path, true, 5*time.Minute) // marker present, near expiry

	borrowed, err := testSource(KindCodex, map[string]string{"file": path}, oauthOpts(srv.URL))
	if err != nil {
		t.Fatal(err)
	}
	if borrowed.Owned() {
		t.Fatal("New produced an owned source")
	}
	if _, err := borrowed.Credential(context.Background()); !errors.Is(err, ErrOwnerRefreshRequired) || *calls != 0 {
		t.Fatalf("a marker in the file must not grant refresh to a source built by New: err=%v calls=%d", err, *calls)
	}
	owned, err := testOwned(KindCodex, path, oauthOpts(srv.URL))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := owned.Credential(context.Background()); err != nil || *calls != 1 {
		t.Fatalf("an owned source must refresh: err=%v calls=%d", err, *calls)
	}
	if _, err := testOwned(KindCodex, "relative/codex.json", oauthOpts(srv.URL)); err == nil {
		t.Fatal("an owned path must be absolute")
	}
	if _, err := testOwned(KindCodex, path, map[string]string{"file": path}); err == nil {
		t.Fatal("an owned source without a sign-in contract must be refused")
	}
}

// A CREDENTIAL SAYS WHO RENEWS IT. Every surface that tells the operator
// about an expiry reads Info, and an expiry the host renews at its next
// use asks nothing of them: told "refresh it with its own tool" about the
// identity's own sign-in, they were told to use a tool that does not exist.
// Custody and renewal are read off the source, by the rule the refresh
// itself uses, and nothing here reaches the authority.
func TestInfoSaysWhetherTheHostRenewsTheCredential(t *testing.T) {
	srv, calls := fakeAuthority(t)
	path := filepath.Join(t.TempDir(), "codex.json")
	writeCodexFile(t, path, true, 5*time.Minute) // marker present, near expiry

	borrowed, err := testSource(KindCodex, map[string]string{"file": path}, oauthOpts(srv.URL))
	if err != nil {
		t.Fatal(err)
	}
	if i := borrowed.Info(); i.Error != "" || i.Owned || i.Renews {
		t.Fatalf("a borrowed file is its owner's to refresh, whatever marker it carries: %+v", i)
	}
	owned, err := testOwned(KindCodex, path, oauthOpts(srv.URL))
	if err != nil {
		t.Fatal(err)
	}
	if i := owned.Info(); i.Error != "" || !i.Owned || !i.Renews || i.ExpiresAt.IsZero() {
		t.Fatalf("the identity's own sign-in with a refresh token renews itself: %+v", i)
	}

	// The identity's own file with no refresh token: owned, and nothing
	// renews it.
	bare := filepath.Join(t.TempDir(), "bare.json")
	raw, err := json.Marshal(map[string]any{"owned_by": "aii-os", "tokens": map[string]any{
		"access_token": fakeJWT(t, "acct-o", time.Now().Add(5*time.Minute).Unix()), "account_id": "acct-o"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bare, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	spent, err := testOwned(KindCodex, bare, oauthOpts(srv.URL))
	if err != nil {
		t.Fatal(err)
	}
	if i := spent.Info(); i.Error != "" || !i.Owned || i.Renews {
		t.Fatalf("an owned file with no refresh token cannot be renewed: %+v", i)
	}
	if *calls != 0 {
		t.Fatalf("describing a credential reached the authority %d time(s)", *calls)
	}
}

// THE HOST DOES NOT RENEW A GRANT THE AUTHORITY HAS REFUSED. Renews was
// read off the file alone, and a refused renewal leaves the file as it
// was: a revoked grant went on being described as one the host renews at
// its next use, and what reads Info stayed silent for a credential that
// could not work. The refusal is of the grant in the file, so a sign-in
// that replaces the file, or the authority accepting it after all, ends it.
func TestInfoStopsSayingTheHostRenewsAGrantTheAuthorityRefused(t *testing.T) {
	var refuse atomic.Bool
	var calls atomic.Int32
	refuse.Store(true)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if refuse.Load() {
			w.WriteHeader(http.StatusBadRequest)
			w.Write([]byte(`{"error":"invalid_grant"}`))
			return
		}
		json.NewEncoder(w).Encode(map[string]any{
			"access_token":  fakeJWT(t, "acct-r", time.Now().Add(time.Hour).Unix()),
			"refresh_token": "rotated", "id_token": "id", "expires_in": 3600,
		})
	}))
	defer srv.Close()
	old := signInHTTP
	signInHTTP = srv.Client()
	defer func() { signInHTTP = old }()
	path := filepath.Join(t.TempDir(), "codex.json")
	write := func(refresh string) {
		t.Helper()
		raw, err := json.Marshal(map[string]any{"owned_by": "aii-os", "tokens": map[string]any{
			"access_token": fakeJWT(t, "acct-o", time.Now().Add(5*time.Minute).Unix()), "refresh_token": refresh, "account_id": "acct-o"}})
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, raw, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("revoked")
	src, err := testOwned(KindCodex, path, oauthOpts(srv.URL))
	if err != nil {
		t.Fatal(err)
	}
	if i := src.Info(); !i.Renews || i.Refused {
		t.Fatalf("before any refusal the host renews this credential: %+v", i)
	}
	if _, err := src.Credential(context.Background()); !errors.Is(err, ErrGrantInvalid) {
		t.Fatalf("the authority refused the grant and the source answered %v", err)
	}
	if i := src.Info(); i.Renews || !i.Refused || !i.Owned {
		t.Fatalf("the authority refused to renew this grant and Info still says the host renews it: %+v", i)
	}
	if calls.Load() != 1 {
		t.Fatalf("describing the credential reached the authority: %d call(s)", calls.Load())
	}

	// A sign-in writes another grant into the file: the refusal was not of it.
	write("signed-in-again")
	if i := src.Info(); !i.Renews || i.Refused {
		t.Fatalf("a new grant in the file is still described as refused: %+v", i)
	}

	// The same grant, refused and then accepted: renewed, and said so.
	write("revoked")
	if _, err := src.Credential(context.Background()); !errors.Is(err, ErrGrantInvalid) {
		t.Fatalf("the authority refused the grant and the source answered %v", err)
	}
	refuse.Store(false)
	if _, err := src.Credential(context.Background()); err != nil {
		t.Fatalf("the authority accepted the grant and the source answered %v", err)
	}
	if i := src.Info(); !i.Renews || i.Refused {
		t.Fatalf("a renewed credential is still described as refused: %+v", i)
	}
}

// THE SOURCE MUTEX IS NOT HELD ACROSS THE AUTHORITY ROUND-TRIP. While a
// slow refresh is in flight, the mutex must be acquirable.
func TestRefreshDoesNotHoldTheSourceMutex(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(600 * time.Millisecond)
		json.NewEncoder(w).Encode(map[string]any{"access_token": fakeJWT(t, "acct-slow", time.Now().Add(time.Hour).Unix()), "refresh_token": "r2", "id_token": "i", "expires_in": 3600})
	}))
	defer srv.Close()
	old := signInHTTP
	signInHTTP = srv.Client()
	defer func() { signInHTTP = old }()
	path := filepath.Join(t.TempDir(), "codex.json")
	writeCodexFile(t, path, true, 5*time.Minute)
	src, err := testOwned(KindCodex, path, oauthOpts(srv.URL))
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, err := src.Credential(context.Background()); done <- err }()
	time.Sleep(150 * time.Millisecond) // the refresh is now inside the slow authority
	if !src.mu.TryLock() {
		t.Fatal("the source mutex is held across the refresh round-trip — every reader of this source is blocked by the authority")
	}
	src.mu.Unlock()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestCompletionRequiresTheExactState(t *testing.T) {
	var requests int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests++; w.WriteHeader(500) }))
	defer srv.Close()
	old := signInHTTP
	signInHTTP = srv.Client()
	defer func() { signInHTTP = old }()
	p := codexParams()
	p.TokenURL = srv.URL
	l, _ := NewLogin(p)
	for _, st := range []string{"", "other"} {
		if _, err := l.Exchange(context.Background(), nil, "code", st); err == nil {
			t.Fatalf("state %q was accepted", st)
		}
	}
	if requests != 0 {
		t.Fatalf("the authority was contacted %d time(s) before the state was verified", requests)
	}
	if _, _, err := ParseAuthorizationInput("http://localhost:1455/auth/callback?code=only"); err == nil {
		t.Fatal("a redirect without state was accepted")
	}
}

func TestAuthorizeParamsAreExtensibleAndBooleansStrict(t *testing.T) {
	p, err := ParamsFromOptions(OAuthParams{}, map[string]string{
		"oauth_client_id": "c", "oauth_authorize_url": "https://a.example/authorize", "oauth_token_url": "t", "oauth_redirect_uri": "r", "oauth_scope": "s",
		"oauth_authorize_params": `{"prompt":"login","audience":"x"}`, "oauth_id_token_add_organizations": "false",
	})
	if err != nil {
		t.Fatal(err)
	}
	l, err := NewLogin(p)
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(l.URL)
	// The older boolean option is an authorize parameter, sent as written:
	// "false" overrides a contract's "true" rather than vanishing.
	if u.Query().Get("prompt") != "login" || u.Query().Get("audience") != "x" || u.Query().Get("id_token_add_organizations") != "false" {
		t.Fatalf("extensible params not applied / strict false not sent: %s", l.URL)
	}
	p.AuthorizeParams = map[string]string{"state": "attacker"}
	if _, err := NewLogin(p); err == nil {
		t.Fatal("a configured parameter shadowed the protocol's state")
	}
	if _, err := ParamsFromOptions(OAuthParams{}, map[string]string{"oauth_id_token_add_organizations": "yes"}); err == nil {
		t.Fatal("a misspelled boolean was silently accepted")
	}
	if _, err := ParamsFromOptions(OAuthParams{}, map[string]string{"oauth_authorize_params": "not-json"}); err == nil {
		t.Fatal("a malformed params object was accepted")
	}
}

func TestAuthorityErrorsAreBoundedAndScrubbed(t *testing.T) {
	big := strings.Repeat("x", 100000) + " token eyJabcdefghijklmnop.payload.sig and sk-abcdefghijklmnop"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(400); w.Write([]byte(big)) }))
	defer srv.Close()
	old := signInHTTP
	signInHTTP = srv.Client()
	defer func() { signInHTTP = old }()
	p := codexParams()
	p.TokenURL = srv.URL
	_, err := RefreshTokens(context.Background(), nil, p, "r")
	if err == nil {
		t.Fatal("a 400 must be an error")
	}
	if len(err.Error()) > 400 {
		t.Fatalf("authority error not bounded: %d bytes", len(err.Error()))
	}
	if strings.Contains(err.Error(), "eyJ") || strings.Contains(err.Error(), "sk-abc") {
		t.Fatalf("token-shaped content leaked into the error: %s", err)
	}
}

// The credential is written through an exclusive temp: a pre-planted
// predictable ".tmp" symlink is not followed, no temp file is left, and
// the result is private.
func TestOwnedWriteIsExclusiveAndLeavesNoTemp(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "codex.json")
	victim := filepath.Join(dir, "victim")
	os.WriteFile(victim, []byte("untouched"), 0o600)
	os.Symlink(victim, path+".tmp") // the old predictable temp name
	tok := &Tokens{Access: fakeJWT(t, "a", time.Now().Add(time.Hour).Unix()), Refresh: "r", Expires: time.Now().Add(time.Hour)}
	if err := WriteTokenFile(path, tok); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(victim); string(b) != "untouched" {
		t.Fatal("a planted symlink at the predictable temp name was followed")
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.Contains(e.Name(), ".tmp") && e.Name() != "codex.json.tmp" {
			t.Fatalf("temp file left behind: %s", e.Name())
		}
	}
	if ok, err := fileperm.IsRestrictedToOwner(path); err != nil || !ok {
		t.Fatalf("not restricted to the owner: ok=%v err=%v", ok, err)
	}
}
