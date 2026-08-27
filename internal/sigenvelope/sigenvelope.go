// Package sigenvelope implements the AIII-SIGNATURE-V1 envelope grammar —
// the one signed-artifact envelope shared by every AIII authority surface
// (genesis/Ring 5/bootstrap bundles, witness manifests, plugin trust
// objects). One grammar, one implementation, many consumers (YAIAM/YAPB
// law: one source per fact); this package was extracted verbatim from
// internal/genesis/client.go, whose chain_test.go pins the grammar against
// real ai3-bundle interop vectors.
//
// The grammar (mirrored from the C-codebase ai3-bundle tool):
//
//	{artifact_kind, payload, payload_sha256, canonicalization,
//	 signature_profile, signatures[{alg, key_id, public_key_fingerprint,
//	 signature_input_sha256, sig_b64}]}
//
// Verification: reject any document AIII-CANONICAL-JSON-V1 cannot parse
// (duplicate member names at any depth reject — AIII_SERVER_KEYS.md §7),
// canonicalize the received payload, recompute and compare payload_sha256,
// reconstruct the signature input, then verify the EXACT signature set the
// declared profile requires: one entry per required algorithm, no
// duplicates, no extras, every entry verifying (the reference tool's
// validateSignatureSet; a present-but-invalid signature never soft-passes,
// TRUST_AND_SIGNING.md §8.5). A matching signature over an embedded digest
// that does not match the received payload is invalid. Callers name the
// expected artifact_kind and the accepted signature profiles; an empty
// accepted-profile set accepts nothing (fail closed).
package sigenvelope

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/canonicaljson"
	"github.com/aiii-dot-id/aii-os/internal/crypto"
)

// CanonicalizationV1 is the sole supported payload canonicalization id.
const CanonicalizationV1 = "AIII-CANONICAL-JSON-V1"

// Envelope is the signed AIII-SIGNATURE-V1 envelope for authority
// artifacts (genesis bundles, plugin trust objects, ...).
type Envelope struct {
	ArtifactKind     string           `json:"artifact_kind"`
	Payload          json.RawMessage  `json:"payload"`
	PayloadSHA256    string           `json:"payload_sha256"`
	Canonicalization string           `json:"canonicalization"`
	SignatureProfile string           `json:"signature_profile"`
	Signatures       []SignatureEntry `json:"signatures"`
}

// SignatureEntry is one algorithm's signature inside an Envelope.
type SignatureEntry struct {
	Alg                  string `json:"alg"`
	KeyID                string `json:"key_id"`
	PublicKeyFingerprint string `json:"public_key_fingerprint"`
	SignatureInputSHA256 string `json:"signature_input_sha256"`
	SigB64               string `json:"sig_b64"`
}

// PublicKeyEnvelope is the JSON envelope for an AIII public keyset
// (served at /genesis/pubkey, embedded in plugin publisher certificates,
// pinned on disk as a plugin trust root).
type PublicKeyEnvelope struct {
	V         int                 `json:"v"`
	Kind      string              `json:"kind"`
	KeyID     string              `json:"key_id"`
	KeyType   string              `json:"key_type"`
	Profile   string              `json:"profile"`
	CreatedAt string              `json:"created_at"`
	NotBefore string              `json:"not_before"`
	ExpiresAt string              `json:"expires_at"`
	Keys      []PublicKeyMaterial `json:"keys"`
}

// PublicKeyMaterial is one algorithm's public key inside a PublicKeyEnvelope.
type PublicKeyMaterial struct {
	Alg                  string `json:"alg"`
	PublicKeyB64         string `json:"public_key_b64"`
	PublicKeyFingerprint string `json:"public_key_fingerprint"`
}

// FindPublicKey returns the envelope's key material for alg.
func (e *PublicKeyEnvelope) FindPublicKey(alg string) (PublicKeyMaterial, bool) {
	for _, k := range e.Keys {
		if k.Alg == alg {
			return k, true
		}
	}
	return PublicKeyMaterial{}, false
}

// profileAccepted reports whether profile is in the accepted set. An
// empty set accepts nothing — the zero-trust default is reject.
func profileAccepted(profile string, accepted []string) bool {
	for _, p := range accepted {
		if profile == p {
			return true
		}
	}
	return false
}

// VerifyPayload verifies a PQ-signed envelope against pubkey and returns
// the raw payload. expectedKind names the artifact_kind the caller is
// willing to accept (a wire constant, never derived from the bundle);
// acceptedProfiles names the signature profiles the caller accepts.
func VerifyPayload(bundleBytes []byte, pubkey *PublicKeyEnvelope, expectedKind string, acceptedProfiles ...string) (json.RawMessage, error) {
	// Grammar gate first, exactly like the reference verifier (ai3-bundle
	// strictUnmarshal, main.go:391-400: CanonicalizeV1, then decode):
	// duplicate member names at any depth, invalid UTF-8, and trailing
	// tokens reject before any field is trusted (AIII_SERVER_KEYS.md §7).
	// Unknown member NAMES stay ignored on both stacks — the signature
	// input covers exactly the named fields, so extras cannot smuggle
	// authority.
	if _, err := canonicaljson.CanonicalizeV1(bundleBytes); err != nil {
		return nil, fmt.Errorf("parse bundle: %w", err)
	}
	var bundle Envelope
	if err := json.Unmarshal(bundleBytes, &bundle); err != nil {
		return nil, fmt.Errorf("parse bundle: %w", err)
	}

	// Validate bundle shape
	if bundle.ArtifactKind != expectedKind {
		return nil, fmt.Errorf("artifact_kind must be %q, got %q", expectedKind, bundle.ArtifactKind)
	}
	if bundle.Canonicalization != CanonicalizationV1 {
		return nil, fmt.Errorf("unsupported canonicalization %q", bundle.Canonicalization)
	}
	if !profileAccepted(bundle.SignatureProfile, acceptedProfiles) {
		return nil, fmt.Errorf("signature_profile must be %s", strings.Join(acceptedProfiles, " or "))
	}
	if len(bundle.Signatures) == 0 {
		return nil, fmt.Errorf("no signatures in bundle")
	}

	// Validate payload_sha256
	if err := ValidatePayloadSHA256(bundle.PayloadSHA256); err != nil {
		return nil, err
	}

	// Verify payload hash matches declared hash
	canonicalPayload, err := canonicaljson.CanonicalizeV1(bundle.Payload)
	if err != nil {
		return nil, fmt.Errorf("canonicalize payload: %w", err)
	}
	if SHA256Prefixed(canonicalPayload) != bundle.PayloadSHA256 {
		return nil, fmt.Errorf("payload_sha256 mismatch")
	}

	// Get required algorithms for this profile
	required, ok := crypto.RequiredAlgorithms(bundle.SignatureProfile)
	if !ok {
		return nil, fmt.Errorf("unknown signature profile %q", bundle.SignatureProfile)
	}

	// Build signature lookup. The set law is EXACT (reference:
	// ai3-bundle validateSignatureSet): one entry per required
	// algorithm, no duplicates, no extras. A present entry that is
	// never verified is evidence soft-passing — forbidden
	// (TRUST_AND_SIGNING.md §8.5).
	sigsByAlg := make(map[string]SignatureEntry, len(bundle.Signatures))
	for _, sig := range bundle.Signatures {
		if _, dup := sigsByAlg[sig.Alg]; dup {
			return nil, fmt.Errorf("duplicate %s signature entry", sig.Alg)
		}
		sigsByAlg[sig.Alg] = sig
	}
	requiredSet := make(map[string]bool, len(required))
	for _, alg := range required {
		requiredSet[alg] = true
	}
	for _, sig := range bundle.Signatures {
		if !requiredSet[sig.Alg] {
			return nil, fmt.Errorf("signature alg %q is not required by profile %s", sig.Alg, bundle.SignatureProfile)
		}
	}

	// Build pubkey lookup. Duplicate per-alg material rejects here even
	// when the caller pre-validated the envelope: two materials for one
	// algorithm would make the key binding depend on lookup order.
	pubKeysByAlg := make(map[string]PublicKeyMaterial, len(pubkey.Keys))
	for _, key := range pubkey.Keys {
		if _, dup := pubKeysByAlg[key.Alg]; dup {
			return nil, fmt.Errorf("duplicate %s key material in authority envelope", key.Alg)
		}
		pubKeysByAlg[key.Alg] = key
	}

	// Verify each required algorithm
	for _, alg := range required {
		sig, ok := sigsByAlg[alg]
		if !ok {
			return nil, fmt.Errorf("missing %s signature", alg)
		}

		pubKey, ok := pubKeysByAlg[alg]
		if !ok {
			return nil, fmt.Errorf("missing %s public key in authority envelope", alg)
		}

		// Verify key_id matches
		if sig.KeyID != pubkey.KeyID {
			return nil, fmt.Errorf("signature key_id %q does not match pubkey key_id %q", sig.KeyID, pubkey.KeyID)
		}

		// Verify fingerprint matches
		if sig.PublicKeyFingerprint != pubKey.PublicKeyFingerprint {
			return nil, fmt.Errorf("signature fingerprint does not match public key fingerprint for %s", alg)
		}

		// Construct signature input string
		sigInput := SignatureInput(
			bundle.ArtifactKind,
			bundle.SignatureProfile,
			sig.Alg,
			sig.KeyID,
			sig.PublicKeyFingerprint,
			bundle.PayloadSHA256,
		)

		// Verify signature_input_sha256
		if SHA256Prefixed([]byte(sigInput)) != sig.SignatureInputSHA256 {
			return nil, fmt.Errorf("signature_input_sha256 mismatch for %s", alg)
		}

		// Decode public key and signature
		pubKeyBytes, err := base64.StdEncoding.DecodeString(pubKey.PublicKeyB64)
		if err != nil {
			return nil, fmt.Errorf("decode %s public key: %w", alg, err)
		}
		sigBytes, err := base64.StdEncoding.DecodeString(sig.SigB64)
		if err != nil {
			return nil, fmt.Errorf("decode %s signature: %w", alg, err)
		}

		// Verify signature
		switch alg {
		case crypto.SigAlg:
			if err := crypto.Verify(pubKeyBytes, []byte(sigInput), sigBytes); err != nil {
				return nil, fmt.Errorf("ML-DSA-87 verification failed: %w", err)
			}
		case crypto.SLHAlg:
			if err := crypto.VerifySLH(pubKeyBytes, []byte(sigInput), sigBytes); err != nil {
				return nil, fmt.Errorf("SLH-DSA verification failed: %w", err)
			}
		default:
			return nil, fmt.Errorf("unsupported algorithm %q", alg)
		}
	}

	return bundle.Payload, nil
}

// ValidatePublicKeyEnvelope validates a public-key envelope's own
// contract: version, accepted profile, key id, validity window, and the
// fingerprint binding of every key entry.
func ValidatePublicKeyEnvelope(env *PublicKeyEnvelope, acceptedProfiles ...string) error {
	if env.V != 1 {
		return fmt.Errorf("v must be 1, got %d", env.V)
	}
	if !profileAccepted(env.Profile, acceptedProfiles) {
		return fmt.Errorf("profile must be %s", strings.Join(acceptedProfiles, " or "))
	}
	if env.KeyID == "" {
		return fmt.Errorf("key_id is required")
	}
	if len(env.Keys) == 0 {
		return fmt.Errorf("no keys in envelope")
	}
	// Validity window (finding 9, 2026-08-17 review): an expired or
	// not-yet-valid platform key envelope was accepted — the window
	// fields were parsed and never checked.
	now := time.Now().UTC()
	if env.NotBefore != "" {
		nb, err := time.Parse(time.RFC3339, env.NotBefore)
		if err != nil {
			return fmt.Errorf("not_before unparseable: %w", err)
		}
		if now.Before(nb) {
			return fmt.Errorf("key envelope not yet valid (not_before %s)", env.NotBefore)
		}
	}
	if env.ExpiresAt != "" {
		exp, err := time.Parse(time.RFC3339, env.ExpiresAt)
		if err != nil {
			return fmt.Errorf("expires_at unparseable: %w", err)
		}
		if !now.Before(exp) {
			return fmt.Errorf("key envelope expired at %s", env.ExpiresAt)
		}
	}
	// Check each key entry; at most one material per algorithm (a
	// duplicate would make key binding lookup-order-defined).
	seenAlg := make(map[string]bool, len(env.Keys))
	for _, key := range env.Keys {
		if seenAlg[key.Alg] {
			return fmt.Errorf("duplicate %s key material in envelope", key.Alg)
		}
		seenAlg[key.Alg] = true
		if key.Alg == "" || key.PublicKeyB64 == "" || key.PublicKeyFingerprint == "" {
			return fmt.Errorf("key entry missing required fields")
		}
		// Verify fingerprint
		expected := PublicKeyFingerprint(key.Alg, env.KeyID, key.PublicKeyB64)
		if key.PublicKeyFingerprint != expected {
			return fmt.Errorf("public_key_fingerprint mismatch for %s", key.Alg)
		}
	}
	return nil
}

// ValidatePayloadSHA256 checks the sha256:<64-hex> shape of a payload hash.
func ValidatePayloadSHA256(value string) error {
	if !strings.HasPrefix(value, "sha256:") {
		return fmt.Errorf("payload_sha256 must start with sha256:")
	}
	raw := strings.TrimPrefix(value, "sha256:")
	if len(raw) != 64 {
		return fmt.Errorf("payload_sha256 must be 64 hex chars")
	}
	if _, err := hex.DecodeString(raw); err != nil {
		return fmt.Errorf("payload_sha256 is not valid hex: %w", err)
	}
	return nil
}

// PublicKeyFingerprint computes the AIII-PUBLIC-KEY-FINGERPRINT-V1 binding
// of an algorithm + key id + public key.
func PublicKeyFingerprint(alg, keyID, publicKeyB64 string) string {
	input := fmt.Sprintf("AIII-PUBLIC-KEY-FINGERPRINT-V1\nalg:%s\nkey_id:%s\npublic_key_b64:%s\n", alg, keyID, publicKeyB64)
	return SHA256Prefixed([]byte(input))
}

// SignatureInput reconstructs the exact signed input string. The
// AIII-SIGNATURE-V1 domain tag keeps artifact classes distinct — a
// package must never be mistakable for a ledger line (LEDGER_GOLD_FORMAT
// §3 makes the domain tag the separator).
func SignatureInput(artifactKind, profile, alg, keyID, pubKeyFingerprint, payloadSHA256 string) string {
	return fmt.Sprintf("AIII-SIGNATURE-V1\nartifact_kind:%s\ncanonicalization:%s\nsignature_profile:%s\nalg:%s\nkey_id:%s\npublic_key_fingerprint:%s\npayload_sha256:%s\n",
		artifactKind, CanonicalizationV1, profile, alg, keyID, pubKeyFingerprint, payloadSHA256)
}

// SHA256Prefixed returns sha256:<hex> over data.
func SHA256Prefixed(data []byte) string {
	sum := sha256.Sum256(data)
	return fmt.Sprintf("sha256:%x", sum[:])
}
