package genesis

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// The bootstrap domain-key chain and packet, pinned against the REAL
// platform artifacts (all public material, captured from
// bootstrap.aiii.id 2026-08-20). The interop-vector twin of the ring5
// chain test — the bootstrap domain had no such pin.
//
//	pinned root (ring0_pubkey.json, the birth anchor)
//	  └─ signs → bootstrap_pubkey_bundle.json (artifact_kind bootstrap.pubkey)
//	               └─ carries → bootstrap domain key
//	                              └─ signs → bootstrap_packet.json (bootstrap.packet)
//	                                           └─ payload.bootstrap_markdown = BOOTSTRAP.md
//
// This is step 5's data path (fetch → verify → extract the conversation
// prompt), verified end to end against the live-signed artifacts and the
// SHIPPED root. If it breaks, either the servers re-signed under a new
// key (update the fixtures + pin) or an implementation drifted.
func TestBootstrapChainFromPinnedRoot(t *testing.T) {
	root := pinnedRoot() // the embedded ring0_pubkey.json — the real birth anchor

	chainBytes, err := os.ReadFile("testdata/bootstrap_pubkey_bundle.json")
	if err != nil {
		t.Fatal(err)
	}
	// Link 1: the shipped root vouches for the bootstrap domain key.
	content, err := verifyBundlePayload(chainBytes, root, "bootstrap.pubkey")
	if err != nil {
		t.Fatalf("the shipped root must verify the cross-signed bootstrap domain-key bundle: %v", err)
	}
	var domainKey publicKeyEnvelope
	if err := json.Unmarshal(content, &domainKey); err != nil {
		t.Fatal(err)
	}
	if domainKey.KeyID != "aiii_bootstrap_20260804_k14" {
		t.Fatalf("unexpected bootstrap domain key id %q", domainKey.KeyID)
	}

	// Link 2: the domain key verifies the bootstrap packet, and the
	// EXTRACTED text is the real BOOTSTRAP.md — the prompt step 5 runs.
	packetBytes, err := os.ReadFile("testdata/bootstrap_packet.json")
	if err != nil {
		t.Fatal(err)
	}
	prompt, err := verifyBundle(packetBytes, &domainKey, "bootstrap.packet")
	if err != nil {
		t.Fatalf("the bootstrap domain key must verify the packet: %v", err)
	}
	if !strings.Contains(prompt, "You are about to meet your User") {
		t.Fatalf("extracted bootstrap prompt is not the real BOOTSTRAP.md: %.80q", prompt)
	}

	// No self-vouching: the domain key must not verify its own chain
	// envelope — only the root vouches for keys.
	if _, err := verifyBundlePayload(chainBytes, &domainKey, "bootstrap.pubkey"); err == nil {
		t.Fatal("a bootstrap domain key verifying its own cross-signed envelope must FAIL")
	}
}
