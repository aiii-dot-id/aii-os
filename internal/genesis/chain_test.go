package genesis

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
func TestFetchBundleSendsGenesisTokenOnlyToBootstrap(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("X-Genesis-Token")
		w.Write([]byte("{}"))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, srv.URL, srv.URL)
	if _, _, err := c.fetchBundle(srv.URL, "bootstrap"); err != nil {
		t.Fatal(err)
	}
	if got != "" {
		t.Fatalf("no token set: header must be absent, got %q", got)
	}
	c.SetToken("tok_123")
	if _, _, err := c.fetchBundle(srv.URL, "bootstrap"); err != nil {
		t.Fatal(err)
	}
	if got != "tok_123" {
		t.Fatalf("token set: header must be sent, got %q", got)
	}
	for _, kind := range []string{"ring0", "ring5", "ring5.manifest", "ring5.pubkey", "bootstrap.pubkey"} {
		got = ""
		if _, _, err := c.fetchBundle(srv.URL, kind); err != nil {
			t.Fatal(err)
		}
		if got != "" {
			t.Fatalf("%s is public: genesis token was disclosed", kind)
		}
	}
}

func TestFetchBundleDoesNotRedirectGenesisToken(t *testing.T) {
	var reached atomic.Bool
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		reached.Store(true)
	}))
	defer target.Close()
	source := httptest.NewServer(http.RedirectHandler(target.URL, http.StatusFound))
	defer source.Close()

	c := NewClient(source.URL, source.URL, source.URL)
	c.SetToken("tok_123")
	if _, _, err := c.fetchBundle(source.URL, "bootstrap"); err == nil {
		t.Fatal("redirected bootstrap fetch was accepted")
	}
	if reached.Load() {
		t.Fatal("genesis token reached the redirect target")
	}
}
