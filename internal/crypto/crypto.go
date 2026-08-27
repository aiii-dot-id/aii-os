// Package crypto provides post-quantum cryptographic operations for AII OS.
//
// Uses two PQ signature algorithms matching AII OS canon (ProfileRoot):
//   - ML-DSA-87 (NIST FIPS 204) via Go 1.27 stdlib crypto/mldsa
//   - SLH-DSA-SHA2-256s (NIST FIPS 205) via Trail of Bits go-slh-dsa
//
// The identity key is ML-DSA-87. The AIII platform signs bundles with both
// algorithms (ProfileRoot = "AIII-PQ-SIGNATURE-V1-ROOT"). We verify both.
//
// The identity key (identity.sec) signs Ring 0 attestation and ledger events.
// Platform keys verify AIII bundles; R53 foregoes an operator-key tier.
//
// ML-DSA-87 key sizes:
//
//	Public key:  2,592 bytes
//	Private key: 6,432 bytes (seed-based, 32 bytes serialized)
//	Signature:   4,627 bytes
//
// SLH-DSA-SHA2-256s key sizes:
//
//	Public key:  64 bytes
//	Signature:   29,792 bytes
package crypto

import (
	"bytes"
	"crypto/mldsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/aiii-dot-id/aii-os/internal/atomicfile"
	slh "github.com/trailofbits/go-slh-dsa/slh_dsa"
)

// SigAlg is the primary signature algorithm used for identity keys.
const SigAlg = "ML-DSA-87"

// SLHAlg is the secondary signature algorithm used in ProfileRoot bundles.
const SLHAlg = "SLH-DSA-SHA2-256s"

// ProfileRoot is the AII OS canonical PQ signature profile requiring both algorithms.
const ProfileRoot = "AIII-PQ-SIGNATURE-V1-ROOT"

// slhParams is the SLH-DSA-SHA2-256s parameter set.
var slhParams = slh.SlhDsaSha2_256s()

// KeyPair holds an ML-DSA-87 keypair.
type KeyPair struct {
	Algorithm  string
	PublicKey  []byte // 2,592 bytes (serialized)
	PrivateKey *mldsa.PrivateKey
}

// Fingerprint returns SHA-256 of the public key, hex-encoded.
// Used as Author field in ledger events and SignedBy in ring content.
func (kp *KeyPair) Fingerprint() string {
	h := sha256.Sum256(kp.PublicKey)
	return hex.EncodeToString(h[:])
}

// PublicKeyB64 returns the public key base64-encoded for JSON storage.
func (kp *KeyPair) PublicKeyB64() string {
	return base64.StdEncoding.EncodeToString(kp.PublicKey)
}

// PublicKeyBytes returns the raw public key bytes (for VerifyChain's
// authorKeys map — raw bytes, not the B64 form).
func (kp *KeyPair) PublicKeyBytes() []byte {
	out := make([]byte, len(kp.PublicKey))
	copy(out, kp.PublicKey)
	return out
}

// Sign produces an ML-DSA-87 signature over the given message bytes.
func Sign(kp *KeyPair, message []byte) ([]byte, error) {
	if kp.PrivateKey == nil {
		return nil, errors.New("no private key available")
	}
	return kp.PrivateKey.Sign(nil, message, &mldsa.Options{})
}

// SignB64 signs and returns base64-encoded signature for JSON storage.
func SignB64(kp *KeyPair, message []byte) (string, error) {
	sig, err := Sign(kp, message)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(sig), nil
}

// Verify checks an ML-DSA-87 signature against a public key.
func Verify(pubKeyBytes []byte, message []byte, signature []byte) error {
	pk, err := mldsa.NewPublicKey(mldsa.MLDSA87(), pubKeyBytes)
	if err != nil {
		return fmt.Errorf("invalid public key: %w", err)
	}
	return mldsa.Verify(pk, message, signature, &mldsa.Options{})
}

// VerifyB64 verifies a base64-encoded signature.
func VerifyB64(pubKeyB64 string, message []byte, sigB64 string) error {
	pubKey, err := base64.StdEncoding.DecodeString(pubKeyB64)
	if err != nil {
		return fmt.Errorf("invalid public key encoding: %w", err)
	}
	sig, err := base64.StdEncoding.DecodeString(sigB64)
	if err != nil {
		return fmt.Errorf("invalid signature encoding: %w", err)
	}
	return Verify(pubKey, message, sig)
}

// ContentHash computes SHA-256 of payload bytes, hex-encoded.
// Embedded in every ledger event as content_hash, decoupling signing
// from JSON serialization (Go's json.Marshal ≠ cJSON_PrintUnformatted).
func ContentHash(payload []byte) string {
	h := sha256.Sum256(payload)
	return hex.EncodeToString(h[:])
}

// GenerateKeyPair creates a new ML-DSA-87 keypair.
func GenerateKeyPair() (*KeyPair, error) {
	sk, err := mldsa.GenerateKey(mldsa.MLDSA87())
	if err != nil {
		return nil, fmt.Errorf("key generation failed: %w", err)
	}
	return &KeyPair{
		Algorithm:  SigAlg,
		PublicKey:  sk.PublicKey().Bytes(),
		PrivateKey: sk,
	}, nil
}

// SaveKeyPair saves a keypair to disk.
// The private key is stored unencrypted with 0600 permissions; filesystem
// custody is the protection boundary. The public key is stored alongside it.
//
// The write is atomic and refuses an existing key. A crash cannot expose a
// partial key, and concurrent births cannot replace one another's identity.
func SaveKeyPair(kp *KeyPair, path string) (published bool, retErr error) {
	if kp == nil || kp.PrivateKey == nil {
		return false, errors.New("complete keypair is required")
	}
	if path == "" {
		return false, errors.New("key path is required")
	}
	// Store private key bytes and public key bytes
	privBytes := kp.PrivateKey.Bytes()
	pubBytes := kp.PublicKey

	// Format: simple binary file — 4 bytes priv len + priv + pub.
	data := make([]byte, 0, 4+len(privBytes)+len(pubBytes))
	data = append(data, byte(len(privBytes)>>24), byte(len(privBytes)>>16), byte(len(privBytes)>>8), byte(len(privBytes)))
	data = append(data, privBytes...)
	data = append(data, pubBytes...)

	f, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return false, fmt.Errorf("key temp create: %w", err)
	}
	tmp := f.Name()
	closed := false
	defer func() {
		if !closed {
			retErr = errors.Join(retErr, f.Close())
		}
		if err := os.Remove(tmp); err != nil && !errors.Is(err, os.ErrNotExist) {
			retErr = errors.Join(retErr, fmt.Errorf("remove key temp: %w", err))
		}
	}()
	if err := f.Chmod(0600); err != nil {
		return false, fmt.Errorf("key temp permissions: %w", err)
	}
	if _, err := f.Write(data); err != nil {
		return false, fmt.Errorf("key temp write: %w", err)
	}
	if err := f.Sync(); err != nil {
		return false, fmt.Errorf("key temp sync: %w", err)
	}
	if err := f.Close(); err != nil {
		closed = true
		return false, fmt.Errorf("key temp close: %w", err)
	}
	closed = true
	published, err = atomicfile.PublishNew(tmp, path)
	if err == nil {
		return true, nil
	}
	if published {
		return true, fmt.Errorf("key published but directory durability is unconfirmed: %w", err)
	}
	return false, fmt.Errorf("key publish: %w", err)
}

// LoadKeyPair loads a keypair from disk.
func LoadKeyPair(path string) (*KeyPair, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("cannot read key file: %w", err)
	}
	if len(data) < 4 {
		return nil, errors.New("key file too short")
	}
	privLen := uint64(binary.BigEndian.Uint32(data[:4]))
	if privLen > uint64(len(data)-4) {
		return nil, errors.New("key file truncated")
	}
	privEnd := 4 + int(privLen)
	privBytes := data[4:privEnd]
	pubBytes := data[privEnd:]

	sk, err := mldsa.NewPrivateKey(mldsa.MLDSA87(), privBytes)
	if err != nil {
		return nil, fmt.Errorf("cannot reconstruct private key: %w", err)
	}

	// The stored public bytes must match the private key's derived public
	// key (finding 18): trusting the stored bytes let a swapped pub
	// section coexist with the real private key — every append signed
	// with one identity while claiming another's fingerprint. Fail
	// closed; the derived key is the truth.
	derived := sk.PublicKey().Bytes()
	if !bytes.Equal(derived, pubBytes) {
		return nil, fmt.Errorf("key file corrupt: stored public key does not match the private key")
	}

	return &KeyPair{
		Algorithm:  SigAlg,
		PublicKey:  pubBytes,
		PrivateKey: sk,
	}, nil
}

// VerifyFingerprint checks that a public key matches a given fingerprint.
func VerifyFingerprint(pubKeyBytes []byte, fingerprint string) bool {
	h := sha256.Sum256(pubKeyBytes)
	return hex.EncodeToString(h[:]) == fingerprint
}

// --- SLH-DSA-SHA2-256s ---

// VerifySLH verifies an SLH-DSA-SHA2-256s signature.
// Used for the secondary signature in ProfileRoot bundles.
func VerifySLH(pubKeyBytes []byte, message []byte, signature []byte) error {
	pk, err := slh.LoadPublicKey(slhParams, pubKeyBytes)
	if err != nil {
		return fmt.Errorf("invalid SLH-DSA public key: %w", err)
	}
	sig, err := slh.LoadSignature(slhParams, signature)
	if err != nil {
		return fmt.Errorf("invalid SLH-DSA signature: %w", err)
	}
	if !pk.Verify(sig, message, []byte{}) {
		return errors.New("SLH-DSA signature verification failed")
	}
	return nil
}

// VerifySLHB64 verifies a base64-encoded SLH-DSA signature.
func VerifySLHB64(pubKeyB64 string, message []byte, sigB64 string) error {
	pubKey, err := base64.StdEncoding.DecodeString(pubKeyB64)
	if err != nil {
		return fmt.Errorf("invalid public key encoding: %w", err)
	}
	sig, err := base64.StdEncoding.DecodeString(sigB64)
	if err != nil {
		return fmt.Errorf("invalid signature encoding: %w", err)
	}
	return VerifySLH(pubKey, message, sig)
}

// RequiredAlgorithms returns the algorithms required by a signature profile.
// ProfileRoot requires both ML-DSA-87 and SLH-DSA-SHA2-256s.
func RequiredAlgorithms(profile string) ([]string, bool) {
	switch profile {
	case ProfileRoot:
		return []string{SigAlg, SLHAlg}, true
	default:
		return nil, false
	}
}
