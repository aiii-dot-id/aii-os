package sigenvelope

// Pins the envelope law where it lives. The exact-set rule mirrors the
// reference verifier (ai3-bundle validateSignatureSet) and the doctrine
// (AIII_SERVER_KEYS.md §6-§7; TRUST_AND_SIGNING.md §8.5 "invalid
// signatures or object mismatches never soft-pass"): a ROOT envelope
// carries exactly one verifying entry per required algorithm —
// duplicates, extras, unknown algorithms, and duplicate JSON member
// names all reject. Consumers (genesis, packagefmt, witness) pin their
// own artifact rules; the grammar itself is pinned here. Keys are
// throwaway, generated in-test, never persisted. Hostile bundles are
// derived from ONE honest signing by JSON surgery — tampering after
// signing is the attack being modeled, and it keeps the SLH-DSA cost to
// a single sign.

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"

	slh "github.com/trailofbits/go-slh-dsa/slh_dsa"

	"github.com/aiii-dot-id/aii-os/internal/canonicaljson"
	"github.com/aiii-dot-id/aii-os/internal/crypto"
)

const testKind = "test.artifact"

type testSigner struct {
	env   *PublicKeyEnvelope
	ml    *crypto.KeyPair
	slhSk *slh.SecretKey
}

func newTestSigner(t *testing.T) *testSigner {
	t.Helper()
	ml, err := crypto.GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	slhSkVal, slhPub, err := slh.SLHKeygen(slh.SlhDsaSha2_256s())
	if err != nil {
		t.Fatalf("SLH keygen: %v", err)
	}
	now := time.Now().UTC()
	keyID := "aiii_test_" + ml.Fingerprint()[:12]
	slhPubB64 := base64.StdEncoding.EncodeToString(slhPub.Bytes())
	env := &PublicKeyEnvelope{
		V: 1, Kind: "aiii.server_key.public", KeyID: keyID, KeyType: "platform", Profile: crypto.ProfileRoot,
		CreatedAt: now.Format(time.RFC3339),
		NotBefore: now.Add(-time.Hour).Format(time.RFC3339),
		ExpiresAt: now.Add(24 * time.Hour).Format(time.RFC3339),
		Keys: []PublicKeyMaterial{
			{Alg: crypto.SigAlg, PublicKeyB64: ml.PublicKeyB64(), PublicKeyFingerprint: PublicKeyFingerprint(crypto.SigAlg, keyID, ml.PublicKeyB64())},
			{Alg: crypto.SLHAlg, PublicKeyB64: slhPubB64, PublicKeyFingerprint: PublicKeyFingerprint(crypto.SLHAlg, keyID, slhPubB64)},
		},
	}
	return &testSigner{env: env, ml: ml, slhSk: &slhSkVal}
}

func (s *testSigner) signBundle(t *testing.T, payload []byte) []byte {
	t.Helper()
	canonicalPayload, err := canonicaljson.CanonicalizeV1(payload)
	if err != nil {
		t.Fatal(err)
	}
	payloadSHA := SHA256Prefixed(canonicalPayload)
	entries := make([]SignatureEntry, 0, 2)
	for _, alg := range []string{crypto.SigAlg, crypto.SLHAlg} {
		mat, ok := s.env.FindPublicKey(alg)
		if !ok {
			t.Fatalf("signer has no %s material", alg)
		}
		in := SignatureInput(testKind, crypto.ProfileRoot, alg, s.env.KeyID, mat.PublicKeyFingerprint, payloadSHA)
		var sig []byte
		switch alg {
		case crypto.SigAlg:
			sig, err = crypto.Sign(s.ml, []byte(in))
		case crypto.SLHAlg:
			sig, err = s.slhSk.Sign(rand.Reader, []byte(in), nil)
		}
		if err != nil {
			t.Fatalf("sign %s: %v", alg, err)
		}
		entries = append(entries, SignatureEntry{
			Alg: alg, KeyID: s.env.KeyID, PublicKeyFingerprint: mat.PublicKeyFingerprint,
			SignatureInputSHA256: SHA256Prefixed([]byte(in)), SigB64: base64.StdEncoding.EncodeToString(sig),
		})
	}
	out, err := json.Marshal(Envelope{
		ArtifactKind: testKind, Payload: payload, PayloadSHA256: payloadSHA,
		Canonicalization: CanonicalizationV1, SignatureProfile: crypto.ProfileRoot, Signatures: entries,
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// mutateSigs rewrites the signatures array of a marshaled bundle.
func mutateSigs(t *testing.T, bundle []byte, f func([]json.RawMessage) []json.RawMessage) []byte {
	t.Helper()
	var m map[string]json.RawMessage
	if err := json.Unmarshal(bundle, &m); err != nil {
		t.Fatal(err)
	}
	var sigs []json.RawMessage
	if err := json.Unmarshal(m["signatures"], &sigs); err != nil {
		t.Fatal(err)
	}
	nr, err := json.Marshal(f(sigs))
	if err != nil {
		t.Fatal(err)
	}
	m["signatures"] = nr
	out, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func expectReject(t *testing.T, name string, bundle []byte, env *PublicKeyEnvelope, wantSubstr string) {
	t.Helper()
	_, err := VerifyPayload(bundle, env, testKind, crypto.ProfileRoot)
	if err == nil {
		t.Fatalf("%s: must reject", name)
	}
	if wantSubstr != "" && !strings.Contains(err.Error(), wantSubstr) {
		t.Fatalf("%s: rejected for the wrong reason: %v (want substring %q)", name, err, wantSubstr)
	}
}

func TestVerifyPayloadExactSignatureSet(t *testing.T) {
	s := newTestSigner(t)
	payload := []byte(`{"n":1,"who":"sigenvelope-test"}`)
	honest := s.signBundle(t, payload)

	t.Run("honest ROOT bundle verifies and returns the payload", func(t *testing.T) {
		got, err := VerifyPayload(honest, s.env, testKind, crypto.ProfileRoot)
		if err != nil {
			t.Fatalf("honest bundle must verify: %v", err)
		}
		if !bytes.Equal(got, payload) {
			t.Fatalf("returned payload differs: %s", got)
		}
	})

	t.Run("empty accepted-profile set accepts nothing", func(t *testing.T) {
		if _, err := VerifyPayload(honest, s.env, testKind); err == nil {
			t.Fatal("empty accepted-profile set must fail closed")
		}
	})

	t.Run("wrong artifact_kind rejects", func(t *testing.T) {
		if _, err := VerifyPayload(honest, s.env, "other.kind", crypto.ProfileRoot); err == nil {
			t.Fatal("artifact_kind mismatch must reject")
		}
	})

	t.Run("post-sign payload tamper rejects", func(t *testing.T) {
		tampered := bytes.Replace(honest, []byte(`"n":1`), []byte(`"n":2`), 1)
		if bytes.Equal(tampered, honest) {
			t.Fatal("tamper did not land")
		}
		expectReject(t, "tampered payload", tampered, s.env, "payload_sha256 mismatch")
	})

	t.Run("missing required algorithm rejects", func(t *testing.T) {
		missing := mutateSigs(t, honest, func(sigs []json.RawMessage) []json.RawMessage {
			return sigs[:1] // ML-DSA only, SLH-DSA dropped
		})
		expectReject(t, "missing SLH-DSA", missing, s.env, "missing "+crypto.SLHAlg+" signature")
	})

	t.Run("duplicate entry rejects even when both copies are valid", func(t *testing.T) {
		dup := mutateSigs(t, honest, func(sigs []json.RawMessage) []json.RawMessage {
			return append([]json.RawMessage{sigs[0]}, sigs...)
		})
		expectReject(t, "valid duplicate", dup, s.env, "duplicate "+crypto.SigAlg+" signature entry")
	})

	t.Run("garbage duplicate before the valid entry rejects", func(t *testing.T) {
		dup := mutateSigs(t, honest, func(sigs []json.RawMessage) []json.RawMessage {
			var e map[string]interface{}
			if err := json.Unmarshal(sigs[0], &e); err != nil {
				t.Fatal(err)
			}
			e["sig_b64"] = "AAAA"
			g, err := json.Marshal(e)
			if err != nil {
				t.Fatal(err)
			}
			return append([]json.RawMessage{g}, sigs...)
		})
		expectReject(t, "garbage duplicate", dup, s.env, "duplicate "+crypto.SigAlg+" signature entry")
	})

	t.Run("extra entry with an alg outside the profile rejects", func(t *testing.T) {
		extra := mutateSigs(t, honest, func(sigs []json.RawMessage) []json.RawMessage {
			junk := json.RawMessage(`{"alg":"ed25519","key_id":"junk","public_key_fingerprint":"sha256:00","signature_input_sha256":"sha256:00","sig_b64":"AAAA"}`)
			return append(sigs, junk)
		})
		expectReject(t, "extra unknown alg", extra, s.env, "not required by profile")
	})

	t.Run("duplicate JSON member name in the document rejects", func(t *testing.T) {
		dupName := bytes.Replace(honest,
			[]byte(`"artifact_kind":`),
			[]byte(`"canonicalization":"`+CanonicalizationV1+`","artifact_kind":`), 1)
		if bytes.Equal(dupName, honest) {
			t.Fatal("duplicate-name injection did not land")
		}
		expectReject(t, "duplicate member name", dupName, s.env, "parse bundle")
	})

	t.Run("duplicate key material in the authority envelope rejects", func(t *testing.T) {
		dupEnv := *s.env
		dupEnv.Keys = append(append([]PublicKeyMaterial(nil), s.env.Keys...), s.env.Keys[0])
		expectReject(t, "duplicate key material", honest, &dupEnv, "duplicate "+crypto.SigAlg+" key material")
	})
}

func TestValidatePublicKeyEnvelopeContract(t *testing.T) {
	s := newTestSigner(t)

	t.Run("honest envelope validates", func(t *testing.T) {
		if err := ValidatePublicKeyEnvelope(s.env, crypto.ProfileRoot); err != nil {
			t.Fatalf("honest envelope must validate: %v", err)
		}
	})

	t.Run("duplicate per-alg material rejects", func(t *testing.T) {
		dup := *s.env
		dup.Keys = append(append([]PublicKeyMaterial(nil), s.env.Keys...), s.env.Keys[0])
		err := ValidatePublicKeyEnvelope(&dup, crypto.ProfileRoot)
		if err == nil || !strings.Contains(err.Error(), "duplicate "+crypto.SigAlg+" key material") {
			t.Fatalf("duplicate material must reject as such, got %v", err)
		}
	})

	t.Run("expired envelope rejects", func(t *testing.T) {
		expired := *s.env
		expired.ExpiresAt = time.Now().UTC().Add(-time.Minute).Format(time.RFC3339)
		if err := ValidatePublicKeyEnvelope(&expired, crypto.ProfileRoot); err == nil {
			t.Fatal("expired envelope must reject")
		}
	})

	t.Run("broken fingerprint binding rejects", func(t *testing.T) {
		bad := *s.env
		bad.Keys = append([]PublicKeyMaterial(nil), s.env.Keys...)
		bad.Keys[0].PublicKeyFingerprint = SHA256Prefixed([]byte("not the binding"))
		if err := ValidatePublicKeyEnvelope(&bad, crypto.ProfileRoot); err == nil {
			t.Fatal("broken fingerprint binding must reject")
		}
	})
}
