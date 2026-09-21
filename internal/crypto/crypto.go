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
// .
// .
// .
// .
// .
// .
// .
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
	"github.com/aiii-dot-id/aii-os/internal/fileperm"
	slh "github.com/trailofbits/go-slh-dsa/slh_dsa"
)

// .
const SigAlg = "ML-DSA-87"

// .
const SLHAlg = "SLH-DSA-SHA2-256s"

// .
const ProfileRoot = "AIII-PQ-SIGNATURE-V1-ROOT"

// .
var slhParams = slh.SlhDsaSha2_256s()

// .
type KeyPair struct {
	Algorithm  string
	PublicKey  []byte
	PrivateKey *mldsa.PrivateKey
}

// .
// .
func (kp *KeyPair) Fingerprint() string {
	h := sha256.Sum256(kp.PublicKey)
	return hex.EncodeToString(h[:])
}

// .
func (kp *KeyPair) PublicKeyB64() string {
	return base64.StdEncoding.EncodeToString(kp.PublicKey)
}

// .
// .
func (kp *KeyPair) PublicKeyBytes() []byte {
	out := make([]byte, len(kp.PublicKey))
	copy(out, kp.PublicKey)
	return out
}

// .
func Sign(kp *KeyPair, message []byte) ([]byte, error) {
	if kp.PrivateKey == nil {
		return nil, errors.New("no private key available")
	}
	return kp.PrivateKey.Sign(nil, message, &mldsa.Options{})
}

// .
func SignB64(kp *KeyPair, message []byte) (string, error) {
	sig, err := Sign(kp, message)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(sig), nil
}

// .
func Verify(pubKeyBytes []byte, message []byte, signature []byte) error {
	pk, err := mldsa.NewPublicKey(mldsa.MLDSA87(), pubKeyBytes)
	if err != nil {
		return fmt.Errorf("invalid public key: %w", err)
	}
	return mldsa.Verify(pk, message, signature, &mldsa.Options{})
}

// .
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

// .
// .
// .
func ContentHash(payload []byte) string {
	h := sha256.Sum256(payload)
	return hex.EncodeToString(h[:])
}

// .
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

// .
// .
// .
// .
// .
// .
func SaveKeyPair(kp *KeyPair, path string) (published bool, retErr error) {
	if kp == nil || kp.PrivateKey == nil {
		return false, errors.New("complete keypair is required")
	}
	// .
	privBytes := kp.PrivateKey.Bytes()
	pubBytes := kp.PublicKey

	// .
	data := make([]byte, 0, 4+len(privBytes)+len(pubBytes))
	data = append(data, byte(len(privBytes)>>24), byte(len(privBytes)>>16), byte(len(privBytes)>>8), byte(len(privBytes)))
	data = append(data, privBytes...)
	data = append(data, pubBytes...)
	return PublishKeyFile(data, path)
}

// .
// .
// .
// .
// .
// .
func PublishKeyFile(data []byte, path string) (published bool, retErr error) {
	if path == "" {
		return false, errors.New("key path is required")
	}
	if _, err := ParseKeyPair(data); err != nil {
		return false, fmt.Errorf("refusing to publish: %w", err)
	}
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
	// .
	// .
	if err := fileperm.RestrictToOwner(f); err != nil {
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

// .
func LoadKeyPair(path string) (*KeyPair, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("cannot read key file: %w", err)
	}
	return ParseKeyPair(data)
}

// .
// .
// .
// .
func ParseKeyPair(data []byte) (*KeyPair, error) {
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

	// .
	// .
	// .
	// .
	// .
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

// .
// .
// .
// .
func PublicKeyFingerprint(pubKeyBytes []byte) string {
	h := sha256.Sum256(pubKeyBytes)
	return hex.EncodeToString(h[:])
}

// .
func VerifyFingerprint(pubKeyBytes []byte, fingerprint string) bool {
	h := sha256.Sum256(pubKeyBytes)
	return hex.EncodeToString(h[:]) == fingerprint
}

// .

// .
// .
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

// .
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

// .
// .
func RequiredAlgorithms(profile string) ([]string, bool) {
	switch profile {
	case ProfileRoot:
		return []string{SigAlg, SLHAlg}, true
	default:
		return nil, false
	}
}
