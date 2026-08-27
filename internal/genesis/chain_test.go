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

// The live-signed Ring 0 artifact pins the producer contract independently
// of the Go test minter that previously drifted with the consumer.
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

// The ring5 domain-key chain, pinned against the REAL platform
// artifacts (all public material, captured 2026-08-18 when the live
// break surfaced: the platform moved Ring 5 to a dedicated signing key
// and the root no longer verified bundles directly).
//
//	root (genesis /genesis/pubkey, birth anchor)
//	  └─ signs → ring5_pubkey_bundle.json  (artifact_kind ring5.pubkey)
//	               └─ carries → ring5 domain key
//	                              └─ signs → ring5_bundle.json (ring5.bundle)
//
// These fixtures are interop vectors in the gold-vector sense: they
// were produced by ai3-bundle (the C-codebase Go tooling that signs
// the live aiii.id artifacts). If this test breaks, the two
// implementations have diverged on the envelope format.
func TestRing5DomainKeyChain(t *testing.T) {
	rootBytes, err := os.ReadFile("testdata/ring0_pubkey.json")
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

	// Link 1: the root vouches for the domain key.
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

	// Link 2: the domain key verifies the live Ring 5 bundle.
	bundleBytes, err := os.ReadFile("testdata/ring5_bundle.json")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := verifyBundle(bundleBytes, &domainKey, "ring5.bundle"); err != nil {
		t.Fatalf("domain key must verify the ring5 bundle: %v", err)
	}

	// The failure that started this: the root does NOT directly verify
	// a bundle signed by the domain key — without the chain the client
	// must refuse, never accept.
	if _, err := verifyBundle(bundleBytes, &root, "ring5.bundle"); err == nil {
		t.Fatal("root directly verifying a domain-key-signed bundle must FAIL — the chain is not optional")
	}

	// And the domain key must not verify the chain bundle (no
	// self-vouching: only the root vouches for keys).
	if _, err := verifyBundlePayload(chainBytes, &domainKey, "ring5.pubkey"); err == nil {
		t.Fatal("a domain key verifying its own cross-signed envelope must FAIL")
	}
}

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
