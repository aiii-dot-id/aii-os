package dashboard

import (
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// .
// .
// .
// .
// .
// .
func TestALinkCarriesTheCredentialAndThenDropsIt(t *testing.T) {
	const token = "0f1e2d3c4b5a69788796a5b4c3d2e1f0"
	sum := sha256.Sum256([]byte(token))
	s := &Server{authRequired: true, authTokenHash: hex.EncodeToString(sum[:])}

	// .
	// .
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/plugins?tab=store&token="+token, nil)
	if !s.redeemTokenQuery(w, r) {
		t.Fatal("a correct token was not redeemed")
	}
	if w.Code != http.StatusSeeOther {
		t.Fatalf("want a redirect, got %d", w.Code)
	}
	loc := w.Header().Get("Location")
	if strings.Contains(loc, token) || strings.Contains(loc, "token=") {
		t.Fatalf("the credential survived into the address bar: %q", loc)
	}
	if !strings.Contains(loc, "tab=store") {
		t.Fatalf("the rest of the query was lost: %q", loc)
	}
	var got *http.Cookie
	for _, c := range w.Result().Cookies() {
		if c.Name == "aii_token" {
			got = c
		}
	}
	if got == nil || got.Value != token {
		t.Fatalf("no usable cookie was set: %+v", got)
	}
	if !got.HttpOnly || got.SameSite != http.SameSiteLaxMode || got.Path != "/" {
		t.Fatalf("the cookie is not scoped safely: %+v", got)
	}
	// .
	// .
	if got.Secure {
		t.Fatal("Secure was set on a plaintext request")
	}

	// .
	// .
	w = httptest.NewRecorder()
	r = httptest.NewRequest(http.MethodGet, "/?token=deadbeef", nil)
	if s.redeemTokenQuery(w, r) {
		t.Fatal("a wrong token was redeemed")
	}
	if len(w.Result().Cookies()) != 0 {
		t.Fatal("a wrong token still set a cookie")
	}

	// .
	// .
	w = httptest.NewRecorder()
	r = httptest.NewRequest(http.MethodPost, "/api/x?token="+token, strings.NewReader("{}"))
	if s.redeemTokenQuery(w, r) {
		t.Fatal("a POST was redirected, losing its body")
	}

	// .
	open := &Server{}
	w = httptest.NewRecorder()
	r = httptest.NewRequest(http.MethodGet, "/?token="+token, nil)
	if open.redeemTokenQuery(w, r) {
		t.Fatal("an open dashboard redeemed a token")
	}

	// .
	r = httptest.NewRequest(http.MethodGet, "/", nil)
	r.AddCookie(&http.Cookie{Name: "aii_token", Value: token})
	if !s.tokenAuthorized(r) {
		t.Fatal("the cookie the link sets is not the one the gate accepts")
	}
}

// .
func TestTheCookieIsSecureUnderTLS(t *testing.T) {
	const token = "aabbccddeeff00112233445566778899"
	sum := sha256.Sum256([]byte(token))
	s := &Server{authRequired: true, authTokenHash: hex.EncodeToString(sum[:])}
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "https://name.example/?token="+token, nil)
	r.TLS = &tls.ConnectionState{}
	if !s.redeemTokenQuery(w, r) {
		t.Fatal("not redeemed under TLS")
	}
	for _, c := range w.Result().Cookies() {
		if c.Name == "aii_token" && !c.Secure {
			t.Fatal("the cookie is not Secure on an https request")
		}
	}
}
