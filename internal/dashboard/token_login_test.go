package dashboard

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"log"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func findCookie(cookies []*http.Cookie, name string) *http.Cookie {
	for _, c := range cookies {
		if c.Name == name {
			return c
		}
	}
	return nil
}

func TestTokenQueryIsScrubbedWithoutAuthenticating(t *testing.T) {
	const token = "right-token"
	s := &Server{}
	s.SetAccessToken(true, token)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/plugins?tab=store&token="+token, nil)
	if !scrubTokenQuery(w, r) {
		t.Fatal("the obsolete token parameter remained in the browser URL")
	}
	if w.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", w.Code)
	}
	loc := w.Header().Get("Location")
	if strings.Contains(loc, token) || strings.Contains(loc, "token=") || !strings.Contains(loc, "tab=store") {
		t.Fatalf("query scrub produced %q", loc)
	}
	if len(w.Result().Cookies()) != 0 {
		t.Fatal("a query credential installed a cookie")
	}
	if s.tokenAuthorized(r) {
		t.Fatal("a query credential authenticated the request")
	}
	if w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("Referrer-Policy") != "no-referrer" {
		t.Fatal("the scrub response can retain or refer the obsolete credential")
	}

	w = httptest.NewRecorder()
	r = httptest.NewRequest(http.MethodPost, "/api/x?token="+token, strings.NewReader("{}"))
	if scrubTokenQuery(w, r) {
		t.Fatal("a POST was redirected and lost its body")
	}
}

func TestAccessTokenLoginUsesCookieSafeVerifier(t *testing.T) {
	const token = "right;token"
	s := &Server{}
	s.SetAccessToken(true, token)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "https://example.test:8443/auth/token", nil)
	s.handleAccessToken(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("missing token status = %d, want 401", w.Code)
	}

	w = httptest.NewRecorder()
	r = httptest.NewRequest(http.MethodPost, "https://example.test:8443/auth/token", strings.NewReader("wrong-token"))
	s.handleAccessToken(w, r)
	if w.Code != http.StatusUnauthorized || len(w.Result().Cookies()) != 0 {
		t.Fatal("a refused login installed a cookie")
	}

	w = httptest.NewRecorder()
	r = httptest.NewRequest(http.MethodPost, "https://example.test:8443/auth/token", strings.NewReader(token))
	s.handleAccessToken(w, r)
	if w.Code != http.StatusNoContent {
		t.Fatalf("correct login status = %d, want 204", w.Code)
	}
	want := sha256.Sum256([]byte(token))
	cookie := findCookie(w.Result().Cookies(), "aii_token_8443")
	if cookie == nil || cookie.Value != hex.EncodeToString(want[:]) || !cookie.HttpOnly || !cookie.Secure || cookie.Path != "/" || cookie.SameSite != http.SameSiteLaxMode {
		t.Fatalf("login did not install the fixed server verifier: %+v", cookie)
	}
	if strings.Contains(cookie.Value, ";") || cookie.Value == token {
		t.Fatalf("configured token bytes reached the cookie: %q", cookie.Value)
	}

	w = httptest.NewRecorder()
	r = httptest.NewRequest(http.MethodGet, "https://example.test:8443/auth/token", nil)
	r.AddCookie(cookie)
	s.handleAccessToken(w, r)
	if w.Code != http.StatusNoContent {
		t.Fatalf("authenticated status = %d, want 204", w.Code)
	}
	if w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("Referrer-Policy") != "no-referrer" {
		t.Fatal("the login route lacks credential response protections")
	}
}

func TestExistingRawTokenCookiesUpgradeOnStatusCheck(t *testing.T) {
	const token = "right-token"
	for _, tc := range []struct {
		name       string
		cookieName string
		expiresOld bool
	}{
		{name: "port scoped", cookieName: "aii_token_8443"},
		{name: "unscoped legacy", cookieName: legacyDashboardCookieName, expiresOld: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := &Server{}
			s.SetAccessToken(true, token)
			w := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodGet, "https://example.test:8443/auth/token", nil)
			r.AddCookie(&http.Cookie{Name: tc.cookieName, Value: token})
			s.handleAccessToken(w, r)
			if w.Code != http.StatusNoContent {
				t.Fatalf("upgrade status = %d, want 204", w.Code)
			}
			cookies := w.Result().Cookies()
			replacement := findCookie(cookies, "aii_token_8443")
			if replacement == nil || replacement.Value != s.accessHash() {
				t.Fatalf("raw cookie was not replaced by the verifier: %+v", cookies)
			}
			if tc.expiresOld {
				old := findCookie(cookies, legacyDashboardCookieName)
				if old == nil || old.MaxAge >= 0 {
					t.Fatalf("legacy cookie was not expired: %+v", cookies)
				}
			}

			w = httptest.NewRecorder()
			r = httptest.NewRequest(http.MethodGet, "https://example.test:8443/auth/token", nil)
			r.AddCookie(replacement)
			s.handleAccessToken(w, r)
			if w.Code != http.StatusNoContent {
				t.Fatalf("replacement cookie was not accepted: %d", w.Code)
			}
		})
	}
}

func TestDashboardPortsKeepIndependentBrowserLogins(t *testing.T) {
	serve := func(token string) *httptest.Server {
		s := &Server{}
		s.SetAccessToken(true, token)
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/auth/token" {
				s.handleAccessToken(w, r)
				return
			}
			if !s.tokenAuthorized(r) {
				http.Error(w, "forbidden", http.StatusForbidden)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		}))
	}
	a, b := serve("token-a"), serve("token-b")
	defer a.Close()
	defer b.Close()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Jar: jar}
	for _, login := range []struct{ url, token string }{
		{a.URL + "/auth/token", "token-a"},
		{b.URL + "/auth/token", "token-b"},
	} {
		resp, err := client.Post(login.url, "text/plain", strings.NewReader(login.token))
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusNoContent {
			t.Fatalf("login %s returned %d", login.url, resp.StatusCode)
		}
	}
	for _, url := range []string{a.URL, b.URL} {
		resp, err := client.Get(url)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusNoContent {
			t.Fatalf("another port overwrote login %s: %d", url, resp.StatusCode)
		}
	}
}

// .
// .
// .
func TestARefusedLoginIsSlowedAndLogged(t *testing.T) {
	s := New("127.0.0.1", 0, &WSHandler{GetConfig: func() (*ConfigState, error) { return &ConfigState{}, nil }})
	s.SetAccessToken(true, "the-right-token")
	addr, err := s.Start(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Shutdown(context.Background())
	var lines bytes.Buffer
	prev := log.Writer()
	log.SetOutput(&lines)
	defer log.SetOutput(prev)
	began := time.Now()
	resp, err := testClient.Post("https://"+addr+"/auth/token", "text/plain", strings.NewReader("a-wrong-guess"))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("a wrong token was answered %d", resp.StatusCode)
	}
	if took := time.Since(began); took < refusedLoginDelay {
		t.Fatalf("a wrong token was answered in %s — at wire speed", took)
	}
	if !strings.Contains(lines.String(), "access token refused from") {
		t.Fatalf("the refusal left no line: %q", lines.String())
	}
	if strings.Contains(lines.String(), "a-wrong-guess") {
		t.Fatal("the offered value was written down")
	}
}
