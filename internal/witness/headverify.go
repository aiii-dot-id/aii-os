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
	"github.com/aiii-dot-id/aii-os/internal/genesis"
	"github.com/aiii-dot-id/aii-os/internal/ledger"
	"github.com/aiii-dot-id/aii-os/internal/sigenvelope"
)

const WitnessKeysDirName = "witness-keys"

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

	published, err := atomicfile.WriteReplace(final, doc, 0o600)
	if err != nil && published {
		return fmt.Errorf("%s published but not durable: %w", filepath.Base(final), err)
	}
	return err
}

type WitnessKeyMaterial struct {
	KeyID       string
	Fingerprint string
	PublicKey   []byte
	NotBefore   time.Time
	ExpiresAt   time.Time
	envelope    PublicKeyEnvelope
}

func LoadPlatformEnvelope(path string) (*PublicKeyEnvelope, error) {
	if path == "" {

		root := genesis.PinnedRoot()
		if err := sigenvelope.ValidWindow(root, time.Now()); err != nil {
			return nil, fmt.Errorf("platform pubkey (the shipped root): %w", err)
		}
		return root, nil
	}
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

	if err := sigenvelope.ValidatePublicKeyEnvelopeShape(&env, ProfileRoot); err != nil {
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
	km := &WitnessKeyMaterial{KeyID: keyID, Fingerprint: wm.PublicKeyFingerprint, PublicKey: pub, envelope: env}
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

type HeadVerifier struct {
	keys        map[string]*WitnessKeyMaterial
	prevOrdinal int64
	prevHash    string
	identityID  string
	verified    int
	unverified  int
	footnotes   int

	attestedOrdinal int64
	attestedHash    string
}

func NewHeadVerifier(keys map[string]*WitnessKeyMaterial) *HeadVerifier {
	if keys == nil {
		keys = map[string]*WitnessKeyMaterial{}
	}

	return &HeadVerifier{keys: keys, prevOrdinal: -1, prevHash: ZeroLedgerHash, attestedOrdinal: -1}
}

func LoadHeadVerifier(ledgerDir string, platform *PublicKeyEnvelope) (*HeadVerifier, error) {
	keys, err := LoadWitnessKeys(ledgerDir, platform)
	if err != nil {
		return nil, err
	}
	return NewHeadVerifier(keys), nil
}

func (h *HeadVerifier) Verified() int { return h.verified }

func (h *HeadVerifier) Unverified() int { return h.unverified }

func (h *HeadVerifier) Footnotes() int { return h.footnotes }

func (h *HeadVerifier) Keys() int { return len(h.keys) }

func (h *HeadVerifier) Attested() (seq uint64, hash string, ok bool) {
	if h.attestedOrdinal < 0 {
		return 0, "", false
	}
	return uint64(h.attestedOrdinal), h.attestedHash, true
}

func (h *HeadVerifier) Summary() string {
	var reach string
	if seq, hash, ok := h.Attested(); ok {
		if len(hash) > 12 {
			hash = hash[:12]
		}
		reach = fmt.Sprintf("witnessed through record %d (%s)", seq, hash)
	} else {
		reach = "no record witnessed"
	}
	line := fmt.Sprintf("%d witness heads verified under %d persisted keys, %s; %d tail heads unverified",
		h.verified, len(h.keys), reach, h.unverified)

	if h.footnotes > 0 {
		line += fmt.Sprintf("; %d footnote heads accepted with no receipt (re-wrapped: they stand on the identity's proof alone)", h.footnotes)
	}
	return line
}

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

func (h *HeadVerifier) VerifyHead(evt *ledger.Event, hashAt func(seq uint64) (string, bool)) (uint64, error) {
	if evt.Type != ledger.EventSystemWitnessed {
		return h.chainAt(), fmt.Errorf("record %d is %s, not a witness head", evt.Seq, evt.Type)
	}
	var payload struct {
		Receipt      json.RawMessage `json:"receipt"`
		BeforeRewrap json.RawMessage `json:"receipt_before_rewrap"`
	}
	if err := json.Unmarshal(evt.Payload, &payload); err != nil {
		return h.chainAt(), fmt.Errorf("record %d: payload: %w", evt.Seq, err)
	}
	if len(payload.Receipt) == 0 {
		if len(payload.BeforeRewrap) > 0 {

			h.footnotes++
			return h.chainAt(), nil
		}
		return h.chainAt(), fmt.Errorf("record %d: payload carries no receipt", evt.Seq)
	}
	dec := json.NewDecoder(bytes.NewReader(payload.Receipt))
	dec.DisallowUnknownFields()
	var r headReceipt
	if err := dec.Decode(&r); err != nil {
		return h.chainAt(), fmt.Errorf("record %d: receipt: %w", evt.Seq, err)
	}
	err := h.verifyReceipt(headPlace{seq: evt.Seq, prev: evt.Prev, sealed: evt.Sealed(), closing: evt.Closing(), hashAt: hashAt}, r)
	return h.chainAt(), err
}

func (h *HeadVerifier) chainAt() uint64 {
	if h.prevOrdinal < 0 {
		return 0
	}
	return uint64(h.prevOrdinal)
}

type headPlace struct {
	seq             uint64
	prev            string
	sealed, closing bool
	hashAt          func(seq uint64) (string, bool)
}

func (h *HeadVerifier) verifyReceipt(place headPlace, r headReceipt) error {
	seq := place.seq
	switch {
	case seq > 0 && r.LedgerOrdinal == int64(seq)-1:
		if r.LedgerHash != place.prev {
			return fmt.Errorf("record %d: receipt attests hash %s, the record before the head is %s", seq, r.LedgerHash, place.prev)
		}
	case place.closing:
		return fmt.Errorf("record %d: closes its segment and its receipt attests record %d, not the record before the head: a late head never closes a segment", seq, r.LedgerOrdinal)
	case r.LedgerOrdinal <= h.prevOrdinal || r.LedgerOrdinal >= int64(seq):
		return fmt.Errorf("record %d: receipt attests ordinal %d, neither the record before the head nor a record after the one the previous head attested (%d)", seq, r.LedgerOrdinal, h.prevOrdinal)
	default:
		var hash string
		ok := false
		if place.hashAt != nil {
			hash, ok = place.hashAt(uint64(r.LedgerOrdinal))
		}
		if !ok {
			return fmt.Errorf("record %d: receipt attests record %d, whose entry hash is not at hand to check it against", seq, r.LedgerOrdinal)
		}
		if r.LedgerHash != hash {
			return fmt.Errorf("record %d: receipt attests hash %s for record %d, which is %s", seq, r.LedgerHash, r.LedgerOrdinal, hash)
		}
	}
	if h.identityID == "" {
		h.identityID = r.IdentityID
	} else if r.IdentityID != h.identityID {
		return fmt.Errorf("record %d: receipt names identity %s, earlier heads name %s", seq, r.IdentityID, h.identityID)
	}
	if r.PreviousWitnessedLedgerOrdinal != h.prevOrdinal || r.PreviousWitnessedLedgerHash != h.prevHash {
		return fmt.Errorf("record %d: receipt continues from (%d, %s), the previous head attested (%d, %s)", seq,
			r.PreviousWitnessedLedgerOrdinal, r.PreviousWitnessedLedgerHash, h.prevOrdinal, h.prevHash)
	}
	h.prevOrdinal, h.prevHash = r.LedgerOrdinal, r.LedgerHash
	km := h.keys[r.WitnessKeyID]
	if km == nil {

		if !place.sealed {
			h.unverified++
		}
		return fmt.Errorf("record %d: %w (%s)", seq, ledger.ErrWitnessKeyUnknown, r.WitnessKeyID)
	}
	at, err := time.Parse(time.RFC3339, r.WitnessedAt)
	if err != nil {
		return fmt.Errorf("record %d: witnessed_at: %w", seq, err)
	}
	if !km.NotBefore.IsZero() && at.Before(km.NotBefore) {
		return fmt.Errorf("record %d: witnessed at %s, before key %s was valid", seq, r.WitnessedAt, km.KeyID)
	}
	if !at.Before(km.ExpiresAt) {
		return fmt.Errorf("record %d: witnessed at %s, after key %s expired", seq, r.WitnessedAt, km.KeyID)
	}
	if err := sigenvelope.ValidWindow(&km.envelope, at); err != nil {
		return fmt.Errorf("record %d: witnessed at %s, outside key %s's own envelope: %w", seq, r.WitnessedAt, km.KeyID, err)
	}
	sig, err := base64.StdEncoding.DecodeString(r.WitnessSigB64)
	if err != nil {
		return fmt.Errorf("record %d: witness signature: %w", seq, err)
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
		return fmt.Errorf("record %d: witness signature under %s: %w", seq, km.KeyID, err)
	}
	h.verified++
	h.attestedOrdinal, h.attestedHash = r.LedgerOrdinal, r.LedgerHash
	return nil
}
