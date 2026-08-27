package test

import (
	"encoding/base64"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/crypto"
	"github.com/aiii-dot-id/aii-os/internal/ledger"
)

// TestVerifyExternalLedger locks the stranger's path: a ledger verified
// from file alone, key extracted from the genesis payload. Self-contained
// (mints its own ledger in a tempdir) — the runtime's suite never depends
// on out-of-tree fixtures; fixtures serve the runtime, not the reverse.
// Binary-produced-ledger verification belongs to the e2e harness.
func TestVerifyExternalLedger(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ledger.jsonl")

	mintLedger(t, path)
	events, err := ledger.ReadAll(path)
	if err != nil || len(events) != 2 {
		t.Fatalf("mint: %v (%d events)", err, len(events))
	}

	var gp struct {
		PubKeyB64 string `json:"public_key"`
	}
	if err := json.Unmarshal(events[0].Payload, &gp); err != nil || gp.PubKeyB64 == "" {
		t.Fatalf("genesis carries no pubkey: %v", err)
	}
	pubBytes, err := base64.StdEncoding.DecodeString(gp.PubKeyB64)
	if err != nil {
		t.Fatalf("pubkey decode: %v", err)
	}
	keys := map[string][]byte{events[0].SigKeyID: pubBytes}
	if err := ledger.VerifyChain(path, keys); err != nil {
		t.Errorf("stranger verification failed: %v", err)
	}
}

func mintLedger(t *testing.T, path string) *crypto.KeyPair {
	t.Helper()
	kp, err := crypto.GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	l, err := ledger.New(path)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	_, err = l.Append(ledger.EventRing0Genesis, kp.Fingerprint(), 0,
		map[string]interface{}{"name": "Stranger", "public_key": kp.PublicKeyB64()}, kp)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := l.Append(ledger.EventRelationshipUpsert, kp.Fingerprint(), 1,
		map[string]interface{}{
			"id":                        "rel_test",
			"counterpart_name":          "Op",
			"relationship_type":         "founding_operator",
			"operator_approval_excerpt": "Operator Op approved this founding.",
			"operator_approval_turn":    1,
			"approval_basis":            "conversation_turn",
		}, kp); err != nil {
		t.Fatal(err)
	}
	return kp
}
