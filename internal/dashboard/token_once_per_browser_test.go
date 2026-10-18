package dashboard

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestTheTokenIsAskedForOnceInEachBrowser(t *testing.T) {
	const token = "a-token-of-honest-length-for-this-test-0123456789"
	s := New("example.test", 8443, nil)
	s.AllowHost("example.test:8443")
	s.SetAccessToken(true, token)
	ask := func(c *http.Cookie) int {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, "https://example.test:8443/auth/token", nil)
		if c != nil {
			r.AddCookie(c)
		}
		s.server.Handler.ServeHTTP(w, r)
		return w.Code
	}
	paste := func() *http.Cookie {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, "https://example.test:8443/auth/token", strings.NewReader(token))
		s.server.Handler.ServeHTTP(w, r)
		if w.Code != http.StatusNoContent {
			t.Fatalf("pasting the token: status %d", w.Code)
		}
		c := findCookie(w.Result().Cookies(), "aii_token_8443")
		if c == nil {
			t.Fatal("pasting the token set no cookie")
		}
		return c
	}

	if code := ask(nil); code != http.StatusUnauthorized {
		t.Fatalf("a browser that has not signed in was not asked: %d", code)
	}
	first := paste()
	if year := 365 * 24 * time.Hour; time.Duration(first.MaxAge)*time.Second < year-24*time.Hour {
		t.Fatalf("the cookie keeps a browser signed in for %v, not a year", time.Duration(first.MaxAge)*time.Second)
	}
	for i := 0; i < 3; i++ {
		if code := ask(first); code != http.StatusNoContent {
			t.Fatalf("a signed-in browser was asked again (status %d)", code)
		}
	}
	if code := ask(nil); code != http.StatusUnauthorized {
		t.Fatalf("a second browser was let in without the token: %d", code)
	}
	second := paste()
	if code := ask(second); code != http.StatusNoContent {
		t.Fatalf("the second browser, signed in, was asked again: %d", code)
	}
}
