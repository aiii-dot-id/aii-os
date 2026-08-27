package packagefmt

// This file is the one fresh-signing lane. Verifier tests consume
// immutable public vectors; this test alone proves that newly generated
// ML-DSA-87 + SLH-DSA-SHA2-256s material can still emit and verify the
// production envelope profile.

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/canonicaljson"
	"github.com/aiii-dot-id/aii-os/internal/crypto"
	"github.com/aiii-dot-id/aii-os/internal/sigenvelope"
	slh "github.com/trailofbits/go-slh-dsa/slh_dsa"
)

type roleKeys struct {
	keyID string
	ml    *crypto.KeyPair
	slhSK *slh.SecretKey
	env   *sigenvelope.PublicKeyEnvelope
}

func genRole(keyID, keyType string, notBefore, expiresAt time.Time) (*roleKeys, error) {
	ml, err := crypto.GenerateKeyPair()
	if err != nil {
		return nil, err
	}
	slhSecret, slhPub, err := slh.SLHKeygen(slh.SlhDsaSha2_256s())
	if err != nil {
		return nil, err
	}
	pubB64 := base64.StdEncoding.EncodeToString(slhPub.Bytes())
	env := &sigenvelope.PublicKeyEnvelope{
		V: 1, Kind: "aiii.server_key.public", KeyID: keyID, KeyType: keyType,
		Profile:   crypto.ProfileRoot,
		CreatedAt: notBefore.Format(time.RFC3339),
		NotBefore: notBefore.Format(time.RFC3339),
		ExpiresAt: expiresAt.Format(time.RFC3339),
		Keys: []sigenvelope.PublicKeyMaterial{
			{Alg: crypto.SigAlg, PublicKeyB64: ml.PublicKeyB64(),
				PublicKeyFingerprint: sigenvelope.PublicKeyFingerprint(crypto.SigAlg, keyID, ml.PublicKeyB64())},
			{Alg: crypto.SLHAlg, PublicKeyB64: pubB64,
				PublicKeyFingerprint: sigenvelope.PublicKeyFingerprint(crypto.SLHAlg, keyID, pubB64)},
		},
	}
	return &roleKeys{keyID: keyID, ml: ml, slhSK: &slhSecret, env: env}, nil
}

// signEnvelope emits a complete dual-PQ AIII-SIGNATURE-V1 envelope over
// payload (any JSON-marshalable value) for artifactKind.
func signEnvelope(role *roleKeys, artifactKind string, payload interface{}) ([]byte, error) {
	payloadRaw, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	canonical, err := canonicaljson.CanonicalizeV1(payloadRaw)
	if err != nil {
		return nil, err
	}
	payloadSHA := sigenvelope.SHA256Prefixed(canonical)

	var sigs []sigenvelope.SignatureEntry
	for _, alg := range []string{crypto.SigAlg, crypto.SLHAlg} {
		key, ok := role.env.FindPublicKey(alg)
		if !ok {
			return nil, fmt.Errorf("role %s missing %s", role.keyID, alg)
		}
		input := sigenvelope.SignatureInput(artifactKind, crypto.ProfileRoot, alg, role.keyID, key.PublicKeyFingerprint, payloadSHA)
		var raw []byte
		switch alg {
		case crypto.SigAlg:
			raw, err = crypto.Sign(role.ml, []byte(input))
		case crypto.SLHAlg:
			raw, err = role.slhSK.Sign(rand.Reader, []byte(input), nil)
		}
		if err != nil {
			return nil, err
		}
		sigs = append(sigs, sigenvelope.SignatureEntry{
			Alg: alg, KeyID: role.keyID, PublicKeyFingerprint: key.PublicKeyFingerprint,
			SignatureInputSHA256: sigenvelope.SHA256Prefixed([]byte(input)),
			SigB64:               base64.StdEncoding.EncodeToString(raw),
		})
	}
	return json.Marshal(sigenvelope.Envelope{
		ArtifactKind: artifactKind, Payload: canonical, PayloadSHA256: payloadSHA,
		Canonicalization: sigenvelope.CanonicalizationV1,
		SignatureProfile: crypto.ProfileRoot, Signatures: sigs,
	})
}

func TestFreshDualPQSigningRoundTrip(t *testing.T) {
	now := time.Now().UTC()
	role, err := genRole("aiii_plugin_publisher_certifier_fresh_test_k1", keyTypePublisherCertifier,
		now.Add(-time.Hour), now.Add(24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	payload := statusPayload(7, RevokedEntry{
		ArtifactKind:  artifactKindManifestSig,
		PayloadSHA256: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
	})
	envelope, err := signEnvelope(role, ArtifactKindRevocationStatus, payload)
	if err != nil {
		t.Fatal(err)
	}
	verified, err := sigenvelope.VerifyPayload(envelope, role.env, ArtifactKindRevocationStatus, crypto.ProfileRoot)
	if err != nil {
		t.Fatalf("fresh dual-PQ envelope did not verify: %v", err)
	}
	payloadRaw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	want, err := canonicaljson.CanonicalizeV1(payloadRaw)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(verified, want) {
		t.Fatalf("verified payload = %s, want %s", verified, want)
	}
}
