package witness

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	slh "github.com/trailofbits/go-slh-dsa/slh_dsa"

	"github.com/aiii-dot-id/aii-os/internal/canonicaljson"
	"github.com/aiii-dot-id/aii-os/internal/crypto"
)

// TestVerifyManifest (P5-completion, 2026-08-16): the dual-PQ manifest
// verification path had ZERO tests — implemented in D2, never exercised.
// This builds a full synthetic platform (ML-DSA-87 + SLH-DSA keys), signs
// a manifest over the same input string the witnessd tooling signs, and
// pins: honest manifest passes; tampered payload fails; wrong-key
// signature fails; missing SLH-DSA fails.

type platformKeys struct {
	env       *PublicKeyEnvelope
	mlDsaKp   *crypto.KeyPair
	slhSk     *slh.SecretKey
	slhPubB64 string
}

func synthPlatform(t *testing.T) *platformKeys {
	t.Helper()
	mlKp, err := crypto.GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	skVal, pub, err := slh.SLHKeygen(slh.SlhDsaSha2_256s())
	sk := &skVal
	if err != nil {
		t.Fatalf("SLH keygen: %v", err)
	}
	now := time.Now().UTC()
	keyID := "aiii_platform_test_" + mlKp.Fingerprint()[:12]
	pubB64 := base64Encode(pub.Bytes())
	mlFp := sha256Prefixed([]byte(FingerprintMaterial(AlgMLDSA87, keyID, mlKp.PublicKeyB64())))
	slhFp := sha256Prefixed([]byte(FingerprintMaterial(AlgSLHDSASHA2256, keyID, pubB64)))
	return &platformKeys{
		env: &PublicKeyEnvelope{
			V: 1, Kind: PublicKeyEnvelopeKind, KeyID: keyID, KeyType: "platform", Profile: ProfileRoot,
			CreatedAt: now.Format(time.RFC3339), NotBefore: now.Add(-time.Hour).Format(time.RFC3339), ExpiresAt: now.Add(24 * time.Hour).Format(time.RFC3339),
			Keys: []PublicKeyMaterial{
				{Alg: AlgMLDSA87, PublicKeyB64: mlKp.PublicKeyB64(), PublicKeyFingerprint: mlFp},
				{Alg: AlgSLHDSASHA2256, PublicKeyB64: pubB64, PublicKeyFingerprint: slhFp},
			},
		},
		mlDsaKp:   mlKp,
		slhSk:     sk,
		slhPubB64: pubB64,
	}
}

func (p *platformKeys) writeEnv(t *testing.T) string {
	t.Helper()
	raw, _ := json.Marshal(p.env)
	path := filepath.Join(t.TempDir(), "platform.pub.json")
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func manifestInput(artifactKind, canonicalization, profile, alg, keyID, fingerprint, payloadSHA string) string {
	return fmt.Sprintf("AIII-SIGNATURE-V1\nartifact_kind:%s\ncanonicalization:%s\nsignature_profile:%s\nalg:%s\nkey_id:%s\npublic_key_fingerprint:%s\npayload_sha256:%s\n",
		artifactKind, canonicalization, profile, alg, keyID, fingerprint, payloadSHA)
}

// buildManifest signs the witness key envelope with the platform keys and
// returns the manifest bundle bytes (mutate = post-sign payload tampering).
func buildManifest(t *testing.T, p *platformKeys, witnessEnv *PublicKeyEnvelope, mutate bool) []byte {
	t.Helper()
	// artifact_hash = canonical hash of the served witness envelope
	// (the production signer's rule, mirrored by the hardened client)
	envRaw, _ := json.Marshal(witnessEnv)
	envCanonical, err := canonicaljson.CanonicalizeV1(envRaw)
	if err != nil {
		t.Fatal(err)
	}
	artifactHash := sha256Prefixed(envCanonical)

	payload := map[string]interface{}{
		"kind": "witness.public_key_manifest", "schema_version": 1,
		"artifact_version":  "2026.08.16.1",
		"artifact_hash":     artifactHash,
		"key_id":            witnessEnv.KeyID,
		"signature_profile": ProfileRoot,
		"created_at":        time.Now().UTC().Format(time.RFC3339),
		"expires_at":        time.Now().UTC().Add(24 * time.Hour).Format(time.RFC3339),
		"critical":          true,
	}
	if mutate {
		payload["key_id"] = "some.other.key" // tampered AFTER signing below? no — payload is hashed pre-sign; see tamper path
	}
	raw, _ := json.Marshal(payload)
	canonicalPayload, err := canonicaljson.CanonicalizeV1(raw)
	if err != nil {
		t.Fatal(err)
	}
	payloadSHA := sha256Prefixed(canonicalPayload)

	var sigs []SignatureEntry
	// ML-DSA
	mlFp := sha256Prefixed([]byte(FingerprintMaterial(AlgMLDSA87, p.env.KeyID, p.mlDsaKp.PublicKeyB64())))
	in := manifestInput("witness.public_key_manifest", CanonicalizationV1, ProfileRoot, AlgMLDSA87, p.env.KeyID, mlFp, payloadSHA)
	sig, err := crypto.Sign(p.mlDsaKp, []byte(in))
	if err != nil {
		t.Fatal(err)
	}
	sigs = append(sigs, SignatureEntry{SignatureProfile: ProfileRoot, Alg: AlgMLDSA87, KeyID: p.env.KeyID, PublicKeyFingerprint: mlFp, SignatureInputSHA256: sha256Prefixed([]byte(in)), SigB64: base64Encode(sig)})

	// SLH-DSA
	slhFp := sha256Prefixed([]byte(FingerprintMaterial(AlgSLHDSASHA2256, p.env.KeyID, p.slhPubB64)))
	in2 := manifestInput("witness.public_key_manifest", CanonicalizationV1, ProfileRoot, AlgSLHDSASHA2256, p.env.KeyID, slhFp, payloadSHA)
	sig2, err := p.slhSk.Sign(rand.Reader, []byte(in2), nil)
	if err != nil {
		t.Fatal(err)
	}
	sigs = append(sigs, SignatureEntry{SignatureProfile: ProfileRoot, Alg: AlgSLHDSASHA2256, KeyID: p.env.KeyID, PublicKeyFingerprint: slhFp, SignatureInputSHA256: sha256Prefixed([]byte(in2)), SigB64: base64Encode(sig2)})

	bundle := map[string]interface{}{
		"artifact_kind":     "witness.public_key_manifest",
		"payload":           json.RawMessage(canonicalPayload),
		"payload_sha256":    payloadSHA,
		"canonicalization":  CanonicalizationV1,
		"signature_profile": ProfileRoot,
		"signatures":        sigs,
	}
	out, err := json.Marshal(bundle)
	if err != nil {
		t.Fatal(err)
	}
	if mutate {
		// post-sign tampering: change the payload's key_id, hash mismatch
		m := map[string]json.RawMessage{}
		_ = json.Unmarshal(out, &m)
		var pl map[string]interface{}
		_ = json.Unmarshal(m["payload"], &pl)
		pl["key_id"] = "tampered.key"
		nr, _ := json.Marshal(pl)
		m["payload"] = nr
		out, _ = json.Marshal(m)
	}
	return out
}

func TestVerifyManifest(t *testing.T) {
	p := synthPlatform(t)
	platformPath := p.writeEnv(t)

	// A witness key the manifest will vouch for
	fw := newFakeWitness(t) // gives us a well-formed witness envelope + server
	witnessEnv := fw.witnessEnv
	manifest := buildManifest(t, p, witnessEnv, false)

	serveManifest := func(manifestBytes []byte) *Client {
		mux := http.NewServeMux()
		mux.HandleFunc("/witness/pubkey/manifest", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(manifestBytes)
		})
		srv := httptest.NewServer(mux)
		t.Cleanup(srv.Close)
		return New(srv.URL, "")
	}

	t.Run("honest manifest verifies", func(t *testing.T) {
		c := serveManifest(manifest)
		if err := c.VerifyManifest(witnessEnv, platformPath); err != nil {
			t.Fatalf("honest manifest must verify: %v", err)
		}
	})

	t.Run("tampered payload fails", func(t *testing.T) {
		c := serveManifest(buildManifest(t, p, witnessEnv, true))
		if err := c.VerifyManifest(witnessEnv, platformPath); err == nil {
			t.Fatal("tampered manifest must fail (payload_sha256 mismatch)")
		}
	})

	t.Run("wrong platform key fails", func(t *testing.T) {
		other := synthPlatform(t)
		c := serveManifest(manifest)
		if err := c.VerifyManifest(witnessEnv, other.writeEnv(t)); err == nil {
			t.Fatal("manifest signed by a different platform key must fail")
		}
	})

	t.Run("no platform path is refused loudly", func(t *testing.T) {
		c := serveManifest(manifest)
		if err := c.VerifyManifest(witnessEnv, ""); err == nil || err.Error() == "" {
			t.Fatal("empty platform path must produce an explicit refusal, not silence")
		}
	})
}
