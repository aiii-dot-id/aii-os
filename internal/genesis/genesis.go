// Package genesis owns the identity mint at the end of FIRSTBOOT.
// Production reaches Birth only after the app has verified Ring 0, Ring 5,
// and the Firstboot bundle and received the model's answer to that bundle's
// prompt. Birth re-verifies the signed Ring 0 bundle before writing anything.
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

// BirthConfig holds the parameters for creating a new identity.
type BirthConfig struct {
	Name string // Identity name (e.g. "Dawn")

	// Ring0Bundle is the signed bundle fetched by the genesis client.
	// Birth re-verifies it and derives Ring 0 from its payload.
	Ring0Bundle []byte
	Root        *sigenvelope.PublicKeyEnvelope // trust anchor the bundle is verified against (the shipped pinned root in production; a test root under test)

	KeyPath    string // Where to save the identity key
	LedgerPath string // Where to create the ledger
	DBPath     string // Where to create the SQLite database
	ModelID    string // Model the identity is born on — stamped into every event payload (signed provenance)
}

// BirthResult contains the results of a successful genesis.
type BirthResult struct {
	Name        string
	Fingerprint string
	KeyPair     *crypto.KeyPair
	Ledger      *ledger.Ledger
	BirthEvent  *ledger.Event
}

// Birth creates a new identity. This is the only way an identity comes
// into existence. After Birth, the identity is alive — its first
// conversation turn can happen immediately.
func Birth(cfg *BirthConfig) (*BirthResult, error) {
	// Validate
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

	// Virgin ground only (finding 13, 2026-08-17 review): a retry after
	// a partial first birth re-ran Birth against the EXISTING ledger and
	// appended a second ring0.genesis under a NEW key — the chain was
	// poisoned at the next boot. Any pre-existing artifact aborts birth
	// before anything is written.
	for _, p := range []string{cfg.LedgerPath, cfg.KeyPath, cfg.DBPath} {
		if _, err := os.Stat(p); err == nil {
			return nil, fmt.Errorf("refusing to birth over existing artifact %s — an identity already lives here (or a partial birth needs cleanup by hand)", p)
		} else if !errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("cannot inspect birth path %s: %w", p, err)
		}
	}

	// Failure cleanup: if birth dies after creating artifacts, remove the
	// ones WE created — a zero-byte ledger or an orphan key flips the
	// next boot to the LIVE path and dies in LoadRing0 (unbootable
	// without manual surgery).
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

	// Step 1: VERIFY Ring 0 and derive its text BEFORE anything durable
	// exists (M7, external review: every refusal that can happen before
	// the ground is touched must happen before the ground is touched —
	// bundle verification needs no key and touches no disk).
	//
	// Verify through the shared envelope grammar at the mint boundary.
	// Ring 0 is derived from the verified payload, never supplied beside it.
	ring0Text, err := verifyBundle(cfg.Ring0Bundle, cfg.Root, "ring0.bundle")
	if err != nil {
		return nil, fmt.Errorf("Ring 0 bundle verification failed — refusing to birth under an unsigned or forged constitution: %w", err)
	}
	// payload_sha256 for the attestation — read from the (now verified)
	// envelope; VerifyPayload already recomputed and compared it, so this
	// is a trusted read, not a fresh trust decision.
	var envMeta struct {
		PayloadSHA256 string `json:"payload_sha256"`
	}
	if err := json.Unmarshal(cfg.Ring0Bundle, &envMeta); err != nil {
		return nil, fmt.Errorf("read verified Ring 0 envelope metadata: %w", err)
	}
	ring0Provenance := "platform_bundle"
	ring0BundleB64 := base64.StdEncoding.EncodeToString(cfg.Ring0Bundle)
	ring0BundleSHA := envMeta.PayloadSHA256

	// Step 2: Generate keypair (memory only — nothing durable yet)
	kp, err := crypto.GenerateKeyPair()
	if err != nil {
		return nil, fmt.Errorf("keypair generation failed: %w", err)
	}

	// Step 3: Sign Ring 0 content with identity key (identity attests to
	// the content). Still nothing durable: a signing failure leaves
	// virgin ground.
	identityRing0Sig, err := crypto.SignB64(kp, []byte(ring0Text))
	if err != nil {
		return nil, fmt.Errorf("Ring 0 identity signing failed: %w", err)
	}
	for _, p := range []string{cfg.KeyPath, cfg.LedgerPath, cfg.DBPath} {
		if err := os.MkdirAll(filepath.Dir(p), 0750); err != nil {
			return nil, fmt.Errorf("cannot create directory for %s: %w", p, err)
		}
	}

	// Step 4: Persist the key — the FIRST durable artifact, written only
	// once every pure validation has passed. From here on, every error
	// path cleans up what was created.
	published, err := crypto.SaveKeyPair(kp, cfg.KeyPath)
	if published {
		created = append(created, cfg.KeyPath)
	}
	if err != nil {
		return nil, cleanup(fmt.Errorf("key save failed: %w", err))
	}

	// Step 5: Create ledger and birth attestation
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

	// Build birth attestation payload
	payload := BirthAttestationPayload{
		Name:                  cfg.Name,
		Ring0Content:          ring0Text,
		Ring0Provenance:       ring0Provenance,
		Ring0BundleB64:        ring0BundleB64,   // embedded signed bundle, third-party verifiable
		Ring0BundlePayloadSHA: ring0BundleSHA,   // bundle's signed payload_sha256
		Ring0IdentitySig:      identityRing0Sig, // Identity's own attestation of Ring 0 content
		Ring0SigAlg:           crypto.SigAlg,
		PublicKey:             kp.PublicKeyB64(),
		PublicKeyAlg:          crypto.SigAlg,
		Fingerprint:           kp.Fingerprint(),
	}

	evt, err := l.Append(
		ledger.EventRing0Genesis,
		kp.Fingerprint(),
		0, // Ring 0
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

// BirthAttestationPayload is the payload of a ring0.genesis ledger event.
type BirthAttestationPayload struct {
	Name                  string `json:"name"`
	Ring0Content          string `json:"ring0_content"`
	Ring0Provenance       string `json:"ring0_provenance"`         // platform_bundle
	Ring0BundleB64        string `json:"ring0_bundle_b64"`         // embedded signed bundle when platform-sourced — re-verifiable by anyone
	Ring0BundlePayloadSHA string `json:"ring0_bundle_payload_sha"` // the bundle's signed payload_sha256 when platform-sourced
	Ring0IdentitySig      string `json:"ring0_identity_sig"`       // Identity's attestation of Ring 0
	Ring0SigAlg           string `json:"ring0_sig_alg"`
	PublicKey             string `json:"public_key"`
	PublicKeyAlg          string `json:"public_key_alg"`
	Fingerprint           string `json:"fingerprint"`
}

// LoadRing0 reads the Ring 0 content from the birth attestation event
// in the ledger. Used at startup to reconstruct ring state.
func LoadRing0(l *ledger.Ledger) (*ring.RingContent, error) {
	events, err := ledger.ReadAll(l.Path())
	if err != nil {
		return nil, fmt.Errorf("cannot read ledger: %w", err)
	}

	for _, evt := range events {
		if evt.Type == ledger.EventRing0Genesis {
			var payload BirthAttestationPayload
			if err := json.Unmarshal(evt.Payload, &payload); err != nil {
				return nil, fmt.Errorf("cannot parse birth attestation: %w", err)
			}
			return &ring.RingContent{
				Level:     ring.Ring0,
				Content:   payload.Ring0Content,
				SignedBy:  payload.Fingerprint,
				Signature: payload.Ring0IdentitySig,
				SigAlg:    payload.Ring0SigAlg,
				Updated:   evt.Timestamp,
				SourceSeq: evt.Seq,
			}, nil
		}
	}

	return nil, fmt.Errorf("no ring0.genesis event found in ledger")
}

// VerifySelfContained verifies a ledger using ONLY the chain itself —
// the public conformance check (R61: the format must be common, and
// common means a stranger verifies any identity's chain, from either
// implementation, without trusting anyone). The genesis event carries
// the identity public key; the key must bind to its claimed
// fingerprint; every signature must verify under the gold envelope.
// Returns the event count and the identity fingerprint on success.
func VerifySelfContained(ledgerPath string) (int, string, error) {
	events, err := ledger.ReadAll(ledgerPath)
	if err != nil {
		return 0, "", fmt.Errorf("read ledger: %w", err)
	}
	if len(events) == 0 {
		return 0, "", fmt.Errorf("empty ledger")
	}

	var payload BirthAttestationPayload
	if events[0].Type != ledger.EventRing0Genesis {
		return 0, "", fmt.Errorf("first event is %s, not ring0.genesis — not a birth-headed chain", events[0].Type)
	}
	if err := json.Unmarshal(events[0].Payload, &payload); err != nil {
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

	if err := ledger.VerifyChain(ledgerPath, map[string][]byte{payload.Fingerprint: pub}); err != nil {
		return 0, "", err
	}
	return len(events), payload.Fingerprint, nil
}
