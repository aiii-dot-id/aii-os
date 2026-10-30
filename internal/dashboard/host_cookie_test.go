package dashboard

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestOverHTTPSTheSignInCookieIsAHostCookie(t *testing.T) {
	s := New("127.0.0.1", 0, &WSHandler{})
	s.SetAccessToken(true, pageKeyTestToken)
	s.refusedPause = func(time.Duration) {}
	hash := s.accessHash()

	req := httptest.NewRequest(http.MethodPost, "https://127.0.0.1:8443/auth/token", strings.NewReader(pageKeyTestToken))
	req.AddCookie(&http.Cookie{Name: "aii_token_8443", Value: "set-by-a-sibling-name"})
	rec := httptest.NewRecorder()
	s.handleAccessToken(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("signing in over TLS: %d %s", rec.Code, rec.Body.String())
	}
	var host, plain *http.Cookie
	for _, c := range rec.Result().Cookies() {
		switch c.Name {
		case "__Host-aii_token_8443":
			host = c
		case "aii_token_8443":
			plain = c
		}
	}
	if host == nil || host.Value != hash {
		t.Fatalf("THE SIGN-IN OVER TLS DID NOT WRITE THE __Host- COOKIE: %v", rec.Result().Cookies())
	}
	if !host.Secure || host.Path != "/" || host.Domain != "" {
		t.Fatalf("a __Host- cookie must be Secure, Path=/ and carry no Domain, or the browser drops it: %+v", host)
	}
	if plain == nil || plain.MaxAge >= 0 {
		t.Fatalf("the unprefixed cookie the browser held over TLS was not cleared: %+v", plain)
	}

	over := func(scheme, name string) bool {
		r := httptest.NewRequest(http.MethodGet, scheme+"://127.0.0.1:8443/", nil)
		r.AddCookie(&http.Cookie{Name: name, Value: hash})
		return tokenAuthorizedUnder(s.auth.Load(), r)
	}
	if over("https", "aii_token_8443") {
		t.Fatal("OVER TLS THE UNPREFIXED COOKIE STILL ADMITS: a sibling name of the zone could set it")
	}
	if !over("https", "__Host-aii_token_8443") {
		t.Fatal("over TLS the __Host- cookie does not admit")
	}
	if !over("http", "aii_token_8443") {
		t.Fatal("a plain loopback page lost its cookie: it cannot hold a Secure one")
	}
}
