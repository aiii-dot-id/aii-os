// .
// .
// .
// .
package genesis

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/aiii-dot-id/aii-os/internal/crypto"
	"github.com/aiii-dot-id/aii-os/internal/ledger"
	"github.com/aiii-dot-id/aii-os/internal/ring"
	"github.com/aiii-dot-id/aii-os/internal/sigenvelope"
)

// .
type BirthConfig struct {
	Name string

	// .
	// .
	Ring0Bundle []byte
	Root        *sigenvelope.PublicKeyEnvelope

	KeyPath    string
	LedgerPath string
	DBPath     string
	ModelID    string
}

// .
type BirthResult struct {
	Name        string
	Fingerprint string
	KeyPair     *crypto.KeyPair
	Ledger      *ledger.Ledger
	BirthEvent  *ledger.Event
}

// .
// .
// .
func Birth(cfg *BirthConfig) (*BirthResult, error) {
	// .
	if cfg == nil {
		return nil, fmt.Errorf("birth configuration is required")
	}
	if cfg.Name == "" {
		return nil, fmt.Errorf("identity name is required")
	}
	if len(cfg.Ring0Bundle) == 0 {
		return nil, fmt.Errorf("signed Ring 0 platform bundle is required")
	}
	if cfg.Root == nil {
		return nil, fmt.Errorf("trust root is required to verify the Ring 0 bundle at the mint")
	}
	if cfg.KeyPath == "" || cfg.LedgerPath == "" || cfg.DBPath == "" {
		return nil, fmt.Errorf("key, ledger, and db paths are required")
	}

	// .
	// .
	// .
	// .
	// .
	for _, p := range []string{cfg.LedgerPath, cfg.KeyPath, cfg.DBPath} {
		if _, err := os.Stat(p); err == nil {
			return nil, fmt.Errorf("refusing to birth over existing artifact %s — an identity already lives here (or a partial birth needs cleanup by hand)", p)
		} else if !errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("cannot inspect birth path %s: %w", p, err)
		}
	}

	// .
	// .
	// .
	// .
	var created []string
	defer func() {
		if r := recover(); r != nil {
			for _, p := range created {
				os.Remove(p)
			}
			panic(r)
		}
	}()
	cleanup := func(primary error) error {
		for _, p := range created {
			if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
				primary = errors.Join(primary, fmt.Errorf("remove partial birth artifact %s: %w", p, err))
			}
		}
		return primary
	}

	// .
	// .
	// .
	// .
	// .
	// .
	// .
	ring0Text, err := verifyBundle(cfg.Ring0Bundle, cfg.Root, "ring0.bundle")
	if err != nil {
		return nil, fmt.Errorf("Ring 0 bundle verification failed — refusing to birth under an unsigned or forged constitution: %w", err)
	}
	// .
	// .
	// .
	var envMeta struct {
		PayloadSHA256 string `json:"payload_sha256"`
	}
	if err := json.Unmarshal(cfg.Ring0Bundle, &envMeta); err != nil {
		return nil, fmt.Errorf("read verified Ring 0 envelope metadata: %w", err)
	}
	ring0Provenance := "platform_bundle"
	ring0BundleB64 := base64.StdEncoding.EncodeToString(cfg.Ring0Bundle)
	ring0BundleSHA := envMeta.PayloadSHA256

	// .
	kp, err := crypto.GenerateKeyPair()
	if err != nil {
		return nil, fmt.Errorf("keypair generation failed: %w", err)
	}

	// .
	// .
	// .
	identityRing0Sig, err := crypto.SignB64(kp, []byte(ring0Text))
	if err != nil {
		return nil, fmt.Errorf("Ring 0 identity signing failed: %w", err)
	}
	for _, p := range []string{cfg.KeyPath, cfg.LedgerPath, cfg.DBPath} {
		if err := os.MkdirAll(filepath.Dir(p), 0750); err != nil {
			return nil, fmt.Errorf("cannot create directory for %s: %w", p, err)
		}
	}

	// .
	// .
	// .
	published, err := crypto.SaveKeyPair(kp, cfg.KeyPath)
	if published {
		created = append(created, cfg.KeyPath)
	}
	if err != nil {
		return nil, cleanup(fmt.Errorf("key save failed: %w", err))
	}

	// .
	l, err := ledger.New(cfg.LedgerPath)
	if err != nil {
		return nil, cleanup(fmt.Errorf("ledger creation failed: %w", err))
	}
	created = append(created, cfg.LedgerPath)
	failLedger := func(primary error) error {
		if err := l.Close(); err != nil {
			primary = errors.Join(primary, fmt.Errorf("close partial ledger: %w", err))
		}
		return cleanup(primary)
	}
	if cfg.ModelID != "" {
		l.SetModelID(cfg.ModelID)
	}

	// .
	payload := BirthAttestationPayload{
		Name:                  cfg.Name,
		Ring0Content:          ring0Text,
		Ring0Provenance:       ring0Provenance,
		Ring0BundleB64:        ring0BundleB64,
		Ring0BundlePayloadSHA: ring0BundleSHA,
		Ring0IdentitySig:      identityRing0Sig,
		Ring0SigAlg:           crypto.SigAlg,
		PublicKey:             kp.PublicKeyB64(),
		PublicKeyAlg:          crypto.SigAlg,
		Fingerprint:           kp.Fingerprint(),
	}

	evt, err := l.Append(
		ledger.EventRing0Genesis,
		kp.Fingerprint(),
		0,
		payload,
		kp,
	)
	if err != nil {
		return nil, failLedger(fmt.Errorf("birth attestation failed: %w", err))
	}

	return &BirthResult{
		Name:        cfg.Name,
		Fingerprint: kp.Fingerprint(),
		KeyPair:     kp,
		Ledger:      l,
		BirthEvent:  evt,
	}, nil
}

// .
type BirthAttestationPayload struct {
	Name                  string `json:"name"`
	Ring0Content          string `json:"ring0_content"`
	Ring0Provenance       string `json:"ring0_provenance"`
	Ring0BundleB64        string `json:"ring0_bundle_b64"`
	Ring0BundlePayloadSHA string `json:"ring0_bundle_payload_sha"`
	Ring0IdentitySig      string `json:"ring0_identity_sig"`
	Ring0SigAlg           string `json:"ring0_sig_alg"`
	PublicKey             string `json:"public_key"`
	PublicKeyAlg          string `json:"public_key_alg"`
	Fingerprint           string `json:"fingerprint"`
}

// .
// .
func LoadRing0(l *ledger.Ledger) (*ring.RingContent, error) {
	var found *ring.RingContent
	var parseErr error
	if err := ledger.Stream(l.Path(), func(evt *ledger.Event) error {
		if evt.Type != ledger.EventRing0Genesis {
			return nil
		}
		var payload BirthAttestationPayload
		if err := json.Unmarshal(evt.Payload, &payload); err != nil {
			parseErr = fmt.Errorf("cannot parse birth attestation: %w", err)
			return ledger.ErrStop
		}
		found = &ring.RingContent{
			Level:     ring.Ring0,
			Content:   payload.Ring0Content,
			SignedBy:  payload.Fingerprint,
			Signature: payload.Ring0IdentitySig,
			SigAlg:    payload.Ring0SigAlg,
			Updated:   evt.Timestamp,
			SourceSeq: evt.Seq,
		}
		return ledger.ErrStop
	}); err != nil {
		return nil, fmt.Errorf("cannot read ledger: %w", err)
	}
	if parseErr != nil {
		return nil, parseErr
	}
	if found == nil {
		return nil, fmt.Errorf("no ring0.genesis event found in ledger")
	}
	return found, nil
}

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
func VerifySelfContained(ledgerPath string) (int, string, error) {
	return VerifySelfContainedWith(ledgerPath, nil)
}

// .
// .
func VerifySelfContainedWith(ledgerPath string, heads ledger.HeadVerifier) (int, string, error) {
	// .
	var first *ledger.Event
	if err := ledger.Stream(ledgerPath, func(evt *ledger.Event) error {
		e := *evt
		first = &e
		return ledger.ErrStop
	}); err != nil {
		return 0, "", fmt.Errorf("read ledger: %w", err)
	}
	if first == nil {
		return 0, "", fmt.Errorf("empty ledger")
	}

	var payload BirthAttestationPayload
	if first.Type != ledger.EventRing0Genesis {
		return 0, "", fmt.Errorf("first event is %s, not ring0.genesis — not a birth-headed chain", first.Type)
	}
	if err := json.Unmarshal(first.Payload, &payload); err != nil {
		return 0, "", fmt.Errorf("parse birth attestation: %w", err)
	}
	if payload.PublicKey == "" {
		return 0, "", fmt.Errorf("genesis carries no public key — chain is not self-contained")
	}
	pub, err := base64.StdEncoding.DecodeString(payload.PublicKey)
	if err != nil {
		return 0, "", fmt.Errorf("genesis public key: %w", err)
	}
	if !crypto.VerifyFingerprint(pub, payload.Fingerprint) {
		return 0, "", fmt.Errorf("genesis public key does not match its claimed fingerprint %s", payload.Fingerprint)
	}

	n, err := ledger.VerifyChain(ledgerPath, pub, heads)
	if err != nil {
		return 0, "", err
	}
	return n, payload.Fingerprint, nil
}
