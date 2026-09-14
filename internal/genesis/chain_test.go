package genesis

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
)

// .
// .
// .
// .
// .
func TestRing0ProductionVector(t *testing.T) {
	bundle, err := os.ReadFile("testdata/ring0_bundle.json")
	if err != nil {
		t.Fatal(err)
	}
	laws, err := verifyBundle(bundle, pinnedRoot(), "ring0.bundle")
	if err != nil {
		t.Fatalf("production Ring 0 bundle must verify: %v", err)
	}
	if !strings.HasPrefix(laws, "# Ring 0 Constitutional Axioms\n") {
		t.Fatalf("unexpected Ring 0 laws: %.80q", laws)
	}
}

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
// .
// .
// .
// .
func TestRing5DomainKeyChain(t *testing.T) {
	rootBytes, err := os.ReadFile("testdata/ring0_root_pubkey.json")
	if err != nil {
		t.Fatal(err)
	}
	var root publicKeyEnvelope
	if err := json.Unmarshal(rootBytes, &root); err != nil {
		t.Fatal(err)
	}

	chainBytes, err := os.ReadFile("testdata/ring5_pubkey_bundle.json")
	if err != nil {
		t.Fatal(err)
	}

	// .
	content, err := verifyBundlePayload(chainBytes, &root, "ring5.pubkey")
	if err != nil {
		t.Fatalf("root must verify the cross-signed domain-key bundle: %v", err)
	}
	var domainKey publicKeyEnvelope
	if err := json.Unmarshal(content, &domainKey); err != nil {
		t.Fatal(err)
	}
	if domainKey.KeyID != "aiii_ring5_20260602_k14" {
		t.Fatalf("unexpected domain key id %q", domainKey.KeyID)
	}

	// .
	bundleBytes, err := os.ReadFile("testdata/ring5_bundle.json")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := verifyBundle(bundleBytes, &domainKey, "ring5.bundle"); err != nil {
		t.Fatalf("domain key must verify the ring5 bundle: %v", err)
	}

	// .
	// .
	// .
	if _, err := verifyBundle(bundleBytes, &root, "ring5.bundle"); err == nil {
		t.Fatal("root directly verifying a domain-key-signed bundle must FAIL — the chain is not optional")
	}

	// .
	// .
	if _, err := verifyBundlePayload(chainBytes, &domainKey, "ring5.pubkey"); err == nil {
		t.Fatal("a domain key verifying its own cross-signed envelope must FAIL")
	}
}

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
