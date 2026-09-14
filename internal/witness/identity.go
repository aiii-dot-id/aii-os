package witness

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/canonicaljson"
	"github.com/aiii-dot-id/aii-os/internal/crypto"
)

func readFileTrimmed(path string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return []byte(strings.TrimSpace(string(data))), nil
}

// .
// .
// .
// .
type EnvelopeStore interface {
	SaveWitnessEnvelope(canonicalJSON []byte) error
	LoadWitnessEnvelope() ([]byte, error)
}

// .
type IdentityKey interface {
	Fingerprint() string
	PublicKeyB64() string
	Sign(message []byte) ([]byte, error)
}

// .
type keyPairAdapter struct{ kp *crypto.KeyPair }

func (a keyPairAdapter) Fingerprint() string           { return a.kp.Fingerprint() }
func (a keyPairAdapter) PublicKeyB64() string          { return a.kp.PublicKeyB64() }
func (a keyPairAdapter) Sign(m []byte) ([]byte, error) { return crypto.Sign(a.kp, m) }

// .
func AsIdentityKey(kp *crypto.KeyPair) IdentityKey { return keyPairAdapter{kp} }

// .
// .
// .
const envelopeExpiry = 100 * 365 * 24 * time.Hour

// .
// .
// .
// .
// .
// .
// .
func EnsureIdentityEnvelope(key IdentityKey, store EnvelopeStore) (canonical []byte, env *PublicKeyEnvelope, err error) {
	if existing, err := store.LoadWitnessEnvelope(); err != nil {
		return nil, nil, fmt.Errorf("load witness envelope: %w", err)
	} else if existing != nil {
		canonical, env, err = parseIdentityEnvelope(existing)
		if err != nil {
			return nil, nil, fmt.Errorf("stored witness envelope invalid: %w", err)
		}
		return canonical, env, nil
	}

	now := time.Now().UTC()
	keyID := "aiii_identity_" + key.Fingerprint()[:16]
	env = &PublicKeyEnvelope{
		V:         1,
		Kind:      PublicKeyEnvelopeKind,
		KeyID:     keyID,
		KeyType:   "identity",
		Profile:   ProfileRoot,
		CreatedAt: now.Format(time.RFC3339),
		NotBefore: now.Add(-time.Minute).Format(time.RFC3339),
		ExpiresAt: now.Add(envelopeExpiry).Format(time.RFC3339),
		Keys: []PublicKeyMaterial{{
			Alg:                  AlgMLDSA87,
			PublicKeyB64:         key.PublicKeyB64(),
			PublicKeyFingerprint: sha256Prefixed([]byte(FingerprintMaterial(AlgMLDSA87, keyID, key.PublicKeyB64()))),
		}},
	}
	raw, err := jsonMarshal(env)
	if err != nil {
		return nil, nil, err
	}
	canonical, err = canonicaljson.CanonicalizeV1(raw)
	if err != nil {
		return nil, nil, fmt.Errorf("canonicalize identity envelope: %w", err)
	}
	if err := store.SaveWitnessEnvelope(canonical); err != nil {
		return nil, nil, fmt.Errorf("persist witness envelope: %w", err)
	}
	return canonical, env, nil
}

func parseIdentityEnvelope(canonical []byte) ([]byte, *PublicKeyEnvelope, error) {
	env := &PublicKeyEnvelope{}
	if err := jsonUnmarshalStrict(canonical, env); err != nil {
		return nil, nil, err
	}
	if env.KeyType != "identity" || env.Kind != PublicKeyEnvelopeKind {
		return nil, nil, fmt.Errorf("envelope is not an identity key envelope")
	}
	if _, ok := env.FindPublicKey(AlgMLDSA87); !ok {
		return nil, nil, fmt.Errorf("envelope has no ML-DSA-87 key")
	}
	return canonical, env, nil
}

// .
// .
// .
func DeriveIdentityID(canonicalEnvelope []byte, env *PublicKeyEnvelope) (string, error) {
	ml, ok := env.FindPublicKey(AlgMLDSA87)
	if !ok {
		return "", fmt.Errorf("identity envelope missing ML-DSA-87 key")
	}
	keyHash := sha256Prefixed(canonicalEnvelope)
	material := IdentityIDMaterial(ml.PublicKeyFingerprint, keyHash)
	return "did:aiii:identity:sha256:" + strings.TrimPrefix(sha256Prefixed([]byte(material)), HashPrefixSHA256), nil
}

// .
// .
// .
func SignRequest(key IdentityKey, env *PublicKeyEnvelope, req WitnessRequest, canonicalEnvelope []byte) (SignatureEntry, error) {
	return SignInput(key, env, RequestSignatureInput(req, canonicalEnvelope))
}

// .
// .
// .
func SignInput(key IdentityKey, env *PublicKeyEnvelope, input []byte) (SignatureEntry, error) {
	sig, err := key.Sign(input)
	if err != nil {
		return SignatureEntry{}, fmt.Errorf("sign request: %w", err)
	}
	ml, _ := env.FindPublicKey(AlgMLDSA87)
	return SignatureEntry{
		SignatureProfile:     ProfileFast,
		Alg:                  AlgMLDSA87,
		KeyID:                env.KeyID,
		PublicKeyFingerprint: ml.PublicKeyFingerprint,
		SignatureInputSHA256: sha256Prefixed(input),
		SigB64:               base64Encode(sig),
	}, nil
}
