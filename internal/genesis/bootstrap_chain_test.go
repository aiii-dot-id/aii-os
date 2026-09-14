package genesis

import (
	"encoding/json"
	"os"
	"strings"
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
// .
// .
// .
// .
// .
func TestBootstrapChainFromPinnedRoot(t *testing.T) {
	root := pinnedRoot()

	chainBytes, err := os.ReadFile("testdata/bootstrap_pubkey_bundle.json")
	if err != nil {
		t.Fatal(err)
	}
	// .
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

	// .
	// .
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

	// .
	// .
	if _, err := verifyBundlePayload(chainBytes, &domainKey, "bootstrap.pubkey"); err == nil {
		t.Fatal("a bootstrap domain key verifying its own cross-signed envelope must FAIL")
	}
}
