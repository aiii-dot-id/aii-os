package genesis

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

// RING 0 AND RING 5 ARE NOT STORED HERE. They used to be: a captured
// ring0_bundle.json, ring5_pubkey_bundle.json and ring5_bundle.json,
// verified offline. A stored copy of live material is a copy that goes
// stale, and one did — the Ring 5 posture in this directory was version
// 1 for six months after the platform moved to version 2. The chain is
// now proved in live_smoke_test.go against what the platform serves at
// the moment of the check, which is the only version that can be used.
//
// The session token crosses only the boundary that authenticates it.
// Ring 5 and domain-key bundles are public, self-proving material.
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
