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
	"testing"
	"time"
)

// .
// .
// .
func TestOnlyAnOwnedConstructorCanRefresh(t *testing.T) {
	srv, calls := fakeAuthority(t)
	old := codexHTTP
	codexHTTP = srv.Client()
	defer func() { codexHTTP = old }()
	path := filepath.Join(t.TempDir(), "codex.json")
	writeCodexFile(t, path, true, 5*time.Minute)

	borrowed, err := New(KindCodex, map[string]string{"file": path}, oauthOpts(srv.URL))
	if err != nil {
		t.Fatal(err)
	}
	if borrowed.Owned() {
		t.Fatal("New produced an owned source")
	}
	if _, err := borrowed.Credential(context.Background()); !errors.Is(err, ErrOwnerRefreshRequired) || *calls != 0 {
		t.Fatalf("a marker in the file must not grant refresh to a source built by New: err=%v calls=%d", err, *calls)
	}
	owned, err := NewOwned(KindCodex, path, oauthOpts(srv.URL))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := owned.Credential(context.Background()); err != nil || *calls != 1 {
		t.Fatalf("an owned source must refresh: err=%v calls=%d", err, *calls)
	}
	if _, err := NewOwned(KindCodex, "relative/codex.json", oauthOpts(srv.URL)); err == nil {
		t.Fatal("an owned path must be absolute")
	}
	if _, err := NewOwned(KindCodex, path, map[string]string{"file": path}); err == nil {
		t.Fatal("an owned source without a sign-in contract must be refused")
	}
}

// .
// .
func TestRefreshDoesNotHoldTheSourceMutex(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(600 * time.Millisecond)
		json.NewEncoder(w).Encode(map[string]any{"access_token": fakeJWT(t, "acct-slow", time.Now().Add(time.Hour).Unix()), "refresh_token": "r2", "id_token": "i", "expires_in": 3600})
	}))
	defer srv.Close()
	old := codexHTTP
	codexHTTP = srv.Client()
	defer func() { codexHTTP = old }()
	path := filepath.Join(t.TempDir(), "codex.json")
	writeCodexFile(t, path, true, 5*time.Minute)
	src, err := NewOwned(KindCodex, path, oauthOpts(srv.URL))
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, err := src.Credential(context.Background()); done <- err }()
	time.Sleep(150 * time.Millisecond)
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
	old := codexHTTP
	codexHTTP = srv.Client()
	defer func() { codexHTTP = old }()
	p := codexParams()
	p.TokenURL = srv.URL
	l, _ := NewLogin(p)
	for _, st := range []string{"", "other"} {
		if _, err := l.Complete(context.Background(), "code", st); err == nil {
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
	p, err := ParamsFromOptions(map[string]string{
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
	if u.Query().Get("prompt") != "login" || u.Query().Get("audience") != "x" || u.Query().Has("id_token_add_organizations") {
		t.Fatalf("extensible params not applied / strict false not honoured: %s", l.URL)
	}
	p.AuthorizeParams = map[string]string{"state": "attacker"}
	if _, err := NewLogin(p); err == nil {
		t.Fatal("a configured parameter shadowed the protocol's state")
	}
	if _, err := ParamsFromOptions(map[string]string{"oauth_id_token_add_organizations": "yes"}); err == nil {
		t.Fatal("a misspelled boolean was silently accepted")
	}
	if _, err := ParamsFromOptions(map[string]string{"oauth_authorize_params": "not-json"}); err == nil {
		t.Fatal("a malformed params object was accepted")
	}
}

func TestAuthorityErrorsAreBoundedAndScrubbed(t *testing.T) {
	big := strings.Repeat("x", 100000) + " token eyJabcdefghijklmnop.payload.sig and sk-abcdefghijklmnop"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(400); w.Write([]byte(big)) }))
	defer srv.Close()
	old := codexHTTP
	codexHTTP = srv.Client()
	defer func() { codexHTTP = old }()
	p := codexParams()
	p.TokenURL = srv.URL
	_, err := Refresh(context.Background(), p, "r")
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

// .
// .
// .
func TestOwnedWriteIsExclusiveAndLeavesNoTemp(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "codex.json")
	victim := filepath.Join(dir, "victim")
	os.WriteFile(victim, []byte("untouched"), 0o600)
	os.Symlink(victim, path+".tmp")
	tok := &CodexTokens{Access: fakeJWT(t, "a", time.Now().Add(time.Hour).Unix()), Refresh: "r", AccountID: "a", Expires: time.Now().Add(time.Hour)}
	if err := WriteAuthFile(path, tok); err != nil {
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
