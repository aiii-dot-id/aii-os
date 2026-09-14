package witness

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/atomicfile"
	"github.com/aiii-dot-id/aii-os/internal/canonicaljson"
	"github.com/aiii-dot-id/aii-os/internal/crypto"
	"github.com/aiii-dot-id/aii-os/internal/ledger"
	"github.com/aiii-dot-id/aii-os/internal/sigenvelope"
)

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
const WitnessKeysDirName = "witness-keys"

// .
func WitnessKeysDir(ledgerDir string) string { return filepath.Join(ledgerDir, WitnessKeysDirName) }

type persistedWitnessKey struct {
	Manifest  json.RawMessage `json:"public_key_manifest"`
	PublicKey json.RawMessage `json:"public_key"`
}

func validKeyIDFilename(keyID string) bool {
	if keyID == "" || len(keyID) > 200 || strings.HasPrefix(keyID, ".") {
		return false
	}
	for _, r := range keyID {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '-', r == '.':
		default:
			return false
		}
	}
	return true
}

// .
// .
// .
func persistWitnessKey(ledgerDir, keyID string, manifestRaw, keyCanonical []byte) error {
	if !validKeyIDFilename(keyID) {
		return fmt.Errorf("witness key id %q is not a file name", keyID)
	}
	dir := WitnessKeysDir(ledgerDir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	doc, err := json.Marshal(persistedWitnessKey{Manifest: manifestRaw, PublicKey: keyCanonical})
	if err != nil {
		return err
	}
	doc = append(doc, '\n')
	final := filepath.Join(dir, keyID+".json")
	if existing, err := os.ReadFile(final); err == nil && bytes.Equal(existing, doc) {
		return nil
	}
	tmp := filepath.Join(dir, "."+keyID+".tmp")
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(doc); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	// .
	// .
	if published, err := atomicfile.Replace(tmp, final); err != nil {
		if !published {
			os.Remove(tmp)
		}
		return err
	}
	return nil
}

// .
// .
type WitnessKeyMaterial struct {
	KeyID       string
	Fingerprint string
	PublicKey   []byte
	NotBefore   time.Time
	ExpiresAt   time.Time
}

// .
// .
func LoadPlatformEnvelope(path string) (*PublicKeyEnvelope, error) {
	raw, err := readFileTrimmed(path)
	if err != nil {
		return nil, fmt.Errorf("platform pubkey: %w", err)
	}
	var env PublicKeyEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, fmt.Errorf("parse platform pubkey envelope: %w", err)
	}
	if err := sigenvelope.ValidatePublicKeyEnvelope(&env, ProfileRoot); err != nil {
		return nil, fmt.Errorf("platform pubkey envelope: %w", err)
	}
	return &env, nil
}

// .
// .
// .
func LoadWitnessKeys(ledgerDir string, platform *PublicKeyEnvelope) (map[string]*WitnessKeyMaterial, error) {
	keys := map[string]*WitnessKeyMaterial{}
	dir := WitnessKeysDir(ledgerDir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return keys, nil
		}
		return nil, err
	}
	if platform == nil {
		return nil, errors.New("no platform root to verify persisted witness keys under")
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".json") || strings.HasPrefix(name, ".") {
			continue
		}
		keyID := strings.TrimSuffix(name, ".json")
		km, err := loadWitnessKeyFile(filepath.Join(dir, name), keyID, platform)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", filepath.Join(WitnessKeysDirName, name), err)
		}
		keys[keyID] = km
	}
	return keys, nil
}

func loadWitnessKeyFile(path, keyID string, platform *PublicKeyEnvelope) (*WitnessKeyMaterial, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var doc persistedWitnessKey
	if err := dec.Decode(&doc); err != nil {
		return nil, fmt.Errorf("parse: %w", err)
	}
	if len(doc.Manifest) == 0 || len(doc.PublicKey) == 0 {
		return nil, errors.New("file carries no manifest or no public key")
	}
	canonical, err := canonicaljson.CanonicalizeV1(doc.PublicKey)
	if err != nil {
		return nil, fmt.Errorf("public key: %w", err)
	}
	var env PublicKeyEnvelope
	if err := json.Unmarshal(canonical, &env); err != nil {
		return nil, fmt.Errorf("public key: %w", err)
	}
	if env.KeyID != keyID {
		return nil, fmt.Errorf("file is named %s but holds key %s", keyID, env.KeyID)
	}
	if err := sigenvelope.ValidatePublicKeyEnvelope(&env, ProfileRoot); err != nil {
		return nil, fmt.Errorf("public key envelope: %w", err)
	}
	payload, err := verifyManifestBundle(doc.Manifest, platform, &env, false)
	if err != nil {
		return nil, fmt.Errorf("manifest: %w", err)
	}
	wm, ok := env.FindPublicKey(AlgMLDSA87)
	if !ok {
		return nil, errors.New("witness key has no ML-DSA-87 material")
	}
	pub, err := base64.StdEncoding.DecodeString(wm.PublicKeyB64)
	if err != nil {
		return nil, fmt.Errorf("witness key decode: %w", err)
	}
	km := &WitnessKeyMaterial{KeyID: keyID, Fingerprint: wm.PublicKeyFingerprint, PublicKey: pub}
	if payload.NotBefore != "" {
		nb, err := time.Parse(time.RFC3339, payload.NotBefore)
		if err != nil {
			return nil, fmt.Errorf("manifest not_before: %w", err)
		}
		km.NotBefore = nb
	}
	exp, err := time.Parse(time.RFC3339, payload.ExpiresAt)
	if err != nil {
		return nil, fmt.Errorf("manifest expires_at: %w", err)
	}
	km.ExpiresAt = exp
	return km, nil
}

// .
// .
// .
// .
// .
type HeadVerifier struct {
	keys        map[string]*WitnessKeyMaterial
	prevOrdinal int64
	prevHash    string
	identityID  string
	verified    int
	unverified  int
	footnotes   int
}

// .
func NewHeadVerifier(keys map[string]*WitnessKeyMaterial) *HeadVerifier {
	if keys == nil {
		keys = map[string]*WitnessKeyMaterial{}
	}
	// .
	// .
	// .
	return &HeadVerifier{keys: keys, prevOrdinal: -1, prevHash: ZeroLedgerHash}
}

// .
// .
func LoadHeadVerifier(ledgerDir string, platform *PublicKeyEnvelope) (*HeadVerifier, error) {
	keys, err := LoadWitnessKeys(ledgerDir, platform)
	if err != nil {
		return nil, err
	}
	return NewHeadVerifier(keys), nil
}

// .
func (h *HeadVerifier) Verified() int { return h.verified }

// .
// .
func (h *HeadVerifier) Unverified() int { return h.unverified }

// .
// .
// .
func (h *HeadVerifier) Footnotes() int { return h.footnotes }

// .
func (h *HeadVerifier) Keys() int { return len(h.keys) }

type headReceipt struct {
	IdentityID                     string `json:"identity_id"`
	PreviousWitnessedLedgerOrdinal int64  `json:"previous_witnessed_ledger_ordinal"`
	PreviousWitnessedLedgerHash    string `json:"previous_witnessed_ledger_hash"`
	LedgerOrdinal                  int64  `json:"ledger_ordinal"`
	LedgerHash                     string `json:"ledger_hash"`
	WitnessedAt                    string `json:"witnessed_at"`
	WitnessKeyID                   string `json:"witness_key_id"`
	WitnessSigB64                  string `json:"witness_sig_b64"`
}

// .
func (h *HeadVerifier) VerifyHead(evt *ledger.Event) error {
	if evt.Type != ledger.EventSystemWitnessed {
		return fmt.Errorf("record %d is %s, not a witness head", evt.Seq, evt.Type)
	}
	var payload struct {
		Receipt      json.RawMessage `json:"receipt"`
		BeforeRewrap json.RawMessage `json:"receipt_before_rewrap"`
	}
	if err := json.Unmarshal(evt.Payload, &payload); err != nil {
		return fmt.Errorf("record %d: payload: %w", evt.Seq, err)
	}
	if len(payload.Receipt) == 0 {
		if len(payload.BeforeRewrap) > 0 {
			// .
			// .
			// .
			// .
			h.footnotes++
			return nil
		}
		return fmt.Errorf("record %d: payload carries no receipt", evt.Seq)
	}
	dec := json.NewDecoder(bytes.NewReader(payload.Receipt))
	dec.DisallowUnknownFields()
	var r headReceipt
	if err := dec.Decode(&r); err != nil {
		return fmt.Errorf("record %d: receipt: %w", evt.Seq, err)
	}
	if r.LedgerOrdinal != int64(evt.Seq)-1 || evt.Seq == 0 {
		return fmt.Errorf("record %d: receipt attests ordinal %d, not the record before the head", evt.Seq, r.LedgerOrdinal)
	}
	if r.LedgerHash != evt.Prev {
		return fmt.Errorf("record %d: receipt attests hash %s, the record before the head is %s", evt.Seq, r.LedgerHash, evt.Prev)
	}
	if h.identityID == "" {
		h.identityID = r.IdentityID
	} else if r.IdentityID != h.identityID {
		return fmt.Errorf("record %d: receipt names identity %s, earlier heads name %s", evt.Seq, r.IdentityID, h.identityID)
	}
	if r.PreviousWitnessedLedgerOrdinal != h.prevOrdinal || r.PreviousWitnessedLedgerHash != h.prevHash {
		return fmt.Errorf("record %d: receipt continues from (%d, %s), the previous head attested (%d, %s)", evt.Seq,
			r.PreviousWitnessedLedgerOrdinal, r.PreviousWitnessedLedgerHash, h.prevOrdinal, h.prevHash)
	}
	h.prevOrdinal, h.prevHash = r.LedgerOrdinal, r.LedgerHash
	km := h.keys[r.WitnessKeyID]
	if km == nil {
		h.unverified++
		return fmt.Errorf("record %d: %w (%s)", evt.Seq, ledger.ErrWitnessKeyUnknown, r.WitnessKeyID)
	}
	at, err := time.Parse(time.RFC3339, r.WitnessedAt)
	if err != nil {
		return fmt.Errorf("record %d: witnessed_at: %w", evt.Seq, err)
	}
	if !km.NotBefore.IsZero() && at.Before(km.NotBefore) {
		return fmt.Errorf("record %d: witnessed at %s, before key %s was valid", evt.Seq, r.WitnessedAt, km.KeyID)
	}
	if !at.Before(km.ExpiresAt) {
		return fmt.Errorf("record %d: witnessed at %s, after key %s expired", evt.Seq, r.WitnessedAt, km.KeyID)
	}
	sig, err := base64.StdEncoding.DecodeString(r.WitnessSigB64)
	if err != nil {
		return fmt.Errorf("record %d: witness signature: %w", evt.Seq, err)
	}
	input := ReceiptSignatureInput(WitnessReceipt{
		IdentityID:                     r.IdentityID,
		PreviousWitnessedLedgerOrdinal: r.PreviousWitnessedLedgerOrdinal,
		PreviousWitnessedLedgerHash:    r.PreviousWitnessedLedgerHash,
		LedgerOrdinal:                  r.LedgerOrdinal,
		LedgerHash:                     r.LedgerHash,
		WitnessedAt:                    r.WitnessedAt,
	})
	if err := crypto.Verify(km.PublicKey, input, sig); err != nil {
		return fmt.Errorf("record %d: witness signature under %s: %w", evt.Seq, km.KeyID, err)
	}
	h.verified++
	return nil
}
