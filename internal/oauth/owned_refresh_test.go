package oauth

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// .
func fakeAuthority(t *testing.T) (*httptest.Server, *int) {
	t.Helper()
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		json.NewEncoder(w).Encode(map[string]any{
			"access_token":  fakeJWT(t, "acct-r", time.Now().Add(time.Hour).Unix()),
			"refresh_token": "rotated", "id_token": "id", "expires_in": 3600,
		})
	}))
	t.Cleanup(srv.Close)
	return srv, &calls
}

func writeCodexFile(t *testing.T, path string, owned bool, expIn time.Duration) {
	t.Helper()
	doc := map[string]any{"tokens": map[string]any{
		"access_token": fakeJWT(t, "acct-o", time.Now().Add(expIn).Unix()), "refresh_token": "orig-refresh", "account_id": "acct-o"}}
	if owned {
		doc["owned_by"] = "aii-os"
	}
	raw, _ := json.Marshal(doc)
	os.MkdirAll(filepath.Dir(path), 0o700)
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
}

func oauthOpts(tokenURL string) map[string]string {
	return map[string]string{
		"oauth_client_id": "app_test", "oauth_authorize_url": "https://auth.example/authorize", "oauth_token_url": tokenURL,
		"oauth_redirect_uri": "http://localhost:1455/auth/callback", "oauth_scope": "openid",
		"oauth_account_claim": `["https://api.openai.com/auth","chatgpt_account_id"]`,
	}
}

// .
// .
// .
func TestAnOwnedFileRefreshesItselfNearExpiry(t *testing.T) {
	srv, calls := fakeAuthority(t)
	old := signInHTTP
	signInHTTP = srv.Client()
	defer func() { signInHTTP = old }()
	path := filepath.Join(t.TempDir(), "codex.json")
	writeCodexFile(t, path, true, 5*time.Minute)
	src, err := testOwned(KindCodex, path, oauthOpts(srv.URL))
	if err != nil {
		t.Fatal(err)
	}
	cred, err := src.Credential(context.Background())
	if err != nil {
		t.Fatalf("an owned file near expiry must refresh, got %v", err)
	}
	if *calls != 1 || !strings.Contains(cred.Token, ".") {
		t.Fatalf("authority calls=%d token=%q", *calls, cred.Token)
	}
	raw, _ := os.ReadFile(path)
	if !strings.Contains(string(raw), `"rotated"`) || !strings.Contains(string(raw), `"owned_by": "aii-os"`) {
		t.Fatalf("the owned file was not rewritten with the rotated pair: %s", raw)
	}
	if cred.Headers["ChatGPT-Account-ID"] != "acct-r" {
		t.Fatalf("the account id must come from the fresh token: %v", cred.Headers)
	}
}

// .
// .
func TestABorrowedFileIsNeverRefreshed(t *testing.T) {
	srv, calls := fakeAuthority(t)
	old := signInHTTP
	signInHTTP = srv.Client()
	defer func() { signInHTTP = old }()
	path := filepath.Join(t.TempDir(), "auth.json")
	writeCodexFile(t, path, false, 5*time.Minute)
	src, err := testSource(KindCodex, map[string]string{"file": path}, oauthOpts(srv.URL))
	if err != nil {
		t.Fatal(err)
	}
	_, err = src.Credential(context.Background())
	if !errors.Is(err, ErrOwnerRefreshRequired) {
		t.Fatalf("a borrowed file near expiry must be refused as owner-refresh-required, got %v", err)
	}
	if *calls != 0 {
		t.Fatalf("the authority was called for a borrowed file (%d calls)", *calls)
	}
}

func TestParamsFromOptionsReadsTheContract(t *testing.T) {
	p, err := ParamsFromOptions(map[string]string{"oauth_client_id": "c", "oauth_authorize_url": "a", "oauth_token_url": "t", "oauth_redirect_uri": "r", "oauth_scope": "s", "header_x-app": "cli"})
	if err != nil || !p.Complete() || p.ClientID != "c" {
		t.Fatalf("params not read: %+v %v", p, err)
	}
	if _, err := ParamsFromOptions(map[string]string{"bogus": "x"}); err == nil {
		t.Fatal("an unknown option must still be refused")
	}
}
