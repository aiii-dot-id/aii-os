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

var ErrWitnessKeyKept = errors.New("the witness key kept beside the ledger was not replaced by the one the witness serves")

func persistWitnessKey(ledgerDir, keyID string, manifestRaw, keyCanonical []byte, platform *PublicKeyEnvelope) error {
	if !validKeyIDFilename(keyID) {
		return fmt.Errorf("witness key id %q is not a file name", keyID)
	}
	if err := ensureWitnessKeysDir(ledgerDir); err != nil {
		return err
	}
	doc, err := json.Marshal(persistedWitnessKey{Manifest: manifestRaw, PublicKey: keyCanonical})
	if err != nil {
		return err
	}
	doc = append(doc, '\n')
	final := filepath.Join(WitnessKeysDir(ledgerDir), keyID+".json")
	existing, err := atomicfile.ReadFile(final)
	switch {
	case err == nil && bytes.Equal(existing, doc):
		return nil
	case err == nil:
		if rerr := mayReplaceWitnessKey(existing, manifestRaw, keyCanonical, platform); rerr != nil {
			return fmt.Errorf("%w (%s): %w", ErrWitnessKeyKept, filepath.Base(final), rerr)
		}
	case !errors.Is(err, os.ErrNotExist):
		return err
	}

	published, err := atomicfile.WriteReplace(final, doc, 0o600)
	if err != nil && published {
		return fmt.Errorf("%s published but not durable: %w", filepath.Base(final), err)
	}
	return err
}

func ensureWitnessKeysDir(ledgerDir string) error {
	dir := WitnessKeysDir(ledgerDir)
	if _, err := os.Stat(dir); err == nil {
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.Mkdir(dir, 0o700); err != nil {
		return err
	}
	if err := atomicfile.SyncDir(ledgerDir); err != nil {
		os.Remove(dir)
		return fmt.Errorf("%s made but its entry is not durable: %w", WitnessKeysDirName, err)
	}
	return nil
}

func mayReplaceWitnessKey(existing, manifestRaw, keyCanonical []byte, platform *PublicKeyEnvelope) error {
	dec := json.NewDecoder(bytes.NewReader(existing))
	dec.DisallowUnknownFields()
	var kept persistedWitnessKey
	if err := dec.Decode(&kept); err != nil {
		return fmt.Errorf("the kept file does not read: %w", err)
	}
	keptCanonical, err := canonicaljson.CanonicalizeV1(kept.PublicKey)
	if err != nil {
		return fmt.Errorf("the kept file's public key: %w", err)
	}
	if !bytes.Equal(keptCanonical, keyCanonical) {
		return errors.New("the witness serves another public key envelope under the same key id")
	}
	if platform == nil {
		return errors.New("no platform root to verify the refreshed manifest under")
	}
	var keptEnv, env PublicKeyEnvelope
	if err := json.Unmarshal(keptCanonical, &keptEnv); err != nil {
		return fmt.Errorf("the kept file's public key: %w", err)
	}
	if err := json.Unmarshal(keyCanonical, &env); err != nil {
		return fmt.Errorf("public key: %w", err)
	}
	was, err := verifyPersistedManifest(kept.Manifest, platform, &keptEnv)
	if err != nil {
		return fmt.Errorf("the kept manifest does not verify: %w", err)
	}
	now, err := verifyManifestBundle(manifestRaw, platform, &env, false)
	if err != nil {
		return fmt.Errorf("the refreshed manifest does not verify as a predecessor reads it: %w", err)
	}
	return windowCovers(was, now)
}

func windowCovers(was, now *manifestPayload) error {
	if now.NotBefore != "" {
		if was.NotBefore == "" {
			return fmt.Errorf("the refreshed manifest begins at %s; the kept one has no beginning", now.NotBefore)
		}
		wasFrom, err1 := time.Parse(time.RFC3339, was.NotBefore)
		nowFrom, err2 := time.Parse(time.RFC3339, now.NotBefore)
		if err1 != nil || err2 != nil || nowFrom.After(wasFrom) {
			return fmt.Errorf("the refreshed manifest begins at %s, after the kept one (%s)", now.NotBefore, was.NotBefore)
		}
	}
	wasTo, err1 := time.Parse(time.RFC3339, was.ExpiresAt)
	nowTo, err2 := time.Parse(time.RFC3339, now.ExpiresAt)
	if err1 != nil || err2 != nil || nowTo.Before(wasTo) {
		return fmt.Errorf("the refreshed manifest ends at %s, before the kept one (%s)", now.ExpiresAt, was.ExpiresAt)
	}
	return nil
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
	raw, err := atomicfile.ReadFile(path)
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
	return witnessKeyMaterial(doc.Manifest, doc.PublicKey, func(env *PublicKeyEnvelope) error {
		if env.KeyID != keyID {
			return fmt.Errorf("file is named %s but holds key %s", keyID, env.KeyID)
		}
		return nil
	}, func(manifest []byte, env *PublicKeyEnvelope) (*manifestPayload, error) {
		return verifyPersistedManifest(manifest, platform, env)
	})
}

type manifestProof func(manifest []byte, env *PublicKeyEnvelope) (*manifestPayload, error)

func witnessKeyMaterial(manifest, publicKey []byte, named func(*PublicKeyEnvelope) error, proof manifestProof) (*WitnessKeyMaterial, error) {
	canonical, err := canonicaljson.CanonicalizeV1(publicKey)
	if err != nil {
		return nil, fmt.Errorf("public key: %w", err)
	}
	var env PublicKeyEnvelope
	if err := json.Unmarshal(canonical, &env); err != nil {
		return nil, fmt.Errorf("public key: %w", err)
	}
	if err := named(&env); err != nil {
		return nil, err
	}

	if err := sigenvelope.ValidatePublicKeyEnvelopeShape(&env, ProfileRoot); err != nil {
		return nil, fmt.Errorf("public key envelope: %w", err)
	}
	payload, err := proof(manifest, &env)
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
	km := &WitnessKeyMaterial{KeyID: env.KeyID, Fingerprint: wm.PublicKeyFingerprint, PublicKey: pub, envelope: env}
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

var shippedRoots = genesis.PinnedRoots

func verifyPersistedManifest(raw []byte, platform, witnessKey *PublicKeyEnvelope) (*manifestPayload, error) {
	payload, err := verifyManifestBundle(raw, platform, witnessKey, false)
	if err == nil {
		return payload, nil
	}
	roots := shippedRoots()
	if !roots.IsCurrent(platform) {
		return nil, err
	}
	signer, ok := roots.Signer(raw)
	if !ok || signer.RetiredAt.IsZero() {
		return nil, err
	}
	payload, herr := verifyManifestUnder(raw, signer.Env, witnessKey, false)
	if herr != nil {
		return nil, fmt.Errorf("%w; under %s, the retired root it names: %w", err, signer.Env.KeyID, herr)
	}
	created, perr := time.Parse(time.RFC3339, payload.CreatedAt)
	if perr != nil || !signer.Covers(created) {
		return nil, fmt.Errorf("signed by %s, a root retired at %s, and created at %q, a time that root did not cover", signer.Env.KeyID, signer.RetiredAt.Format(time.RFC3339), payload.CreatedAt)
	}
	return payload, nil
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

	proof manifestProof
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
	h := NewHeadVerifier(keys)
	if platform != nil {
		h.proof = func(manifest []byte, env *PublicKeyEnvelope) (*manifestPayload, error) {
			return verifyPersistedManifest(manifest, platform, env)
		}
	}
	return h, nil
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
	ContextSHA256                  string `json:"context_sha256,omitempty"`
}

func (h *HeadVerifier) VerifyHead(evt *ledger.Event, hashAt func(seq uint64) (string, bool)) (uint64, error) {
	if evt.Type != ledger.EventSystemWitnessed {
		return h.chainAt(), fmt.Errorf("record %d is %s, not a witness head", evt.Seq, evt.Type)
	}
	var payload struct {
		Receipt      json.RawMessage `json:"receipt"`
		BeforeRewrap json.RawMessage `json:"receipt_before_rewrap"`
		Context      json.RawMessage `json:"context"`
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
	ctx, err := headContext(payload.Receipt, r, payload.Context)
	if err != nil {
		return h.chainAt(), fmt.Errorf("record %d: %w", evt.Seq, err)
	}
	err = h.verifyReceipt(headPlace{seq: evt.Seq, prev: evt.Prev, sealed: evt.Sealed(), closing: evt.Closing(), hashAt: hashAt}, r, ctx)
	return h.chainAt(), err
}

func headContext(receiptRaw json.RawMessage, r headReceipt, contextRaw json.RawMessage) (*witnessContext, error) {
	if err := checkContextDigestMember(receiptRaw, r.ContextSHA256); err != nil {
		return nil, fmt.Errorf("receipt: %w", err)
	}
	switch {
	case r.ContextSHA256 == "" && len(contextRaw) > 0:
		return nil, errors.New("a context stands beside a receipt that signs none")
	case r.ContextSHA256 == "":
		return nil, nil
	case len(contextRaw) == 0 || isNull(contextRaw):
		return nil, fmt.Errorf("the receipt signs context %s and the record carries no context beside it", r.ContextSHA256)
	}
	ctx, err := readContext(contextRaw)
	if err != nil {
		return nil, fmt.Errorf("context: %w", err)
	}
	if ctx.digest != r.ContextSHA256 {
		return nil, fmt.Errorf("the receipt signs context %s, and the context beside it digests to %s", r.ContextSHA256, ctx.digest)
	}
	return ctx, nil
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

func (h *HeadVerifier) verifyReceipt(place headPlace, r headReceipt, ctx *witnessContext) error {
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
	switch {
	case r.ContextSHA256 != "" && ctx == nil:
		return fmt.Errorf("record %d: the receipt signs context %s, and no context is held for it", seq, r.ContextSHA256)
	case ctx != nil:
		if err := h.continuesAsItsContextSays(r, ctx); err != nil {
			return fmt.Errorf("record %d: %w", seq, err)
		}
	}
	h.prevOrdinal, h.prevHash = r.LedgerOrdinal, r.LedgerHash
	km := h.keys[r.WitnessKeyID]
	if km == nil {

		if !place.sealed {
			h.unverified++
		}
		return fmt.Errorf("record %d: %w (%s)", seq, ledger.ErrWitnessKeyUnknown, r.WitnessKeyID)
	}
	if err := km.coversAt(r.WitnessedAt); err != nil {
		return fmt.Errorf("record %d: %w", seq, err)
	}
	if ctx != nil {

		target, err := canonicalEnvelopeBytes(&km.envelope)
		if err != nil {
			return fmt.Errorf("record %d: key %s: %w", seq, km.KeyID, err)
		}
		if got := sha256Prefixed(target); got != ctx.targetKeyHash {
			return fmt.Errorf("record %d: the context targets witness key %s, and the receipt is signed by %s (%s)", seq, ctx.targetKeyHash, km.KeyID, got)
		}
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
		ContextSHA256:                  r.ContextSHA256,
	})
	if err := crypto.Verify(km.PublicKey, input, sig); err != nil {
		return fmt.Errorf("record %d: witness signature under %s: %w", seq, km.KeyID, err)
	}
	h.verified++
	h.attestedOrdinal, h.attestedHash = r.LedgerOrdinal, r.LedgerHash
	return nil
}

func (km *WitnessKeyMaterial) coversAt(witnessedAt string) error {
	at, err := time.Parse(time.RFC3339, witnessedAt)
	if err != nil {
		return fmt.Errorf("witnessed_at: %w", err)
	}
	if !km.NotBefore.IsZero() && at.Before(km.NotBefore) {
		return fmt.Errorf("witnessed at %s, before key %s was valid", witnessedAt, km.KeyID)
	}
	if !at.Before(km.ExpiresAt) {
		return fmt.Errorf("witnessed at %s, after key %s expired", witnessedAt, km.KeyID)
	}
	if err := sigenvelope.ValidWindow(&km.envelope, at); err != nil {
		return fmt.Errorf("witnessed at %s, outside key %s's own envelope: %w", witnessedAt, km.KeyID, err)
	}
	return nil
}

func (h *HeadVerifier) continuesAsItsContextSays(r headReceipt, ctx *witnessContext) error {
	prev := headTuple{Ordinal: r.PreviousWitnessedLedgerOrdinal, Hash: r.PreviousWitnessedLedgerHash}
	if ctx.predecessor == nil {
		want := headTuple{Ordinal: -1, Hash: ZeroLedgerHash}
		if ctx.expectedTuple != nil {
			want = *ctx.expectedTuple
		}
		if prev != want {
			return fmt.Errorf("the receipt continues from (%d, %s), and its context's operation from (%d, %s)", prev.Ordinal, prev.Hash, want.Ordinal, want.Hash)
		}
		return nil
	}
	p, err := h.verifiedPredecessor(ctx.predecessor)
	if err != nil {
		return fmt.Errorf("the handover's predecessor: %w", err)
	}
	pt := headTuple{Ordinal: p.LedgerOrdinal, Hash: p.LedgerHash}
	switch {
	case p.IdentityID != r.IdentityID:
		return fmt.Errorf("the handover's predecessor names identity %s, the receipt %s", p.IdentityID, r.IdentityID)
	case prev != pt:
		return fmt.Errorf("the receipt continues from (%d, %s), and its handover's predecessor stands at (%d, %s)", prev.Ordinal, prev.Hash, pt.Ordinal, pt.Hash)
	case r.LedgerOrdinal <= pt.Ordinal:
		return fmt.Errorf("the receipt attests record %d, not after its handover's predecessor (%d)", r.LedgerOrdinal, pt.Ordinal)
	}
	if d := ctx.expectedTuple; d != nil {
		if d.Ordinal > pt.Ordinal || (d.Ordinal == pt.Ordinal && d.Hash != pt.Hash) {
			return fmt.Errorf("the handover expected the destination at (%d, %s), past or beside its predecessor (%d, %s)", d.Ordinal, d.Hash, pt.Ordinal, pt.Hash)
		}
	}
	return nil
}

func (h *HeadVerifier) verifiedPredecessor(p *contextPredecessor) (WitnessReceipt, error) {
	if h.proof == nil {
		return WitnessReceipt{}, errors.New("no platform root to verify its proof under")
	}
	receipt, err := DecodeReceipt(p.Receipt)
	if err != nil {
		return WitnessReceipt{}, fmt.Errorf("receipt: %w", err)
	}
	km, err := witnessKeyMaterial(p.SignedManifest, p.PublicKey, func(*PublicKeyEnvelope) error { return nil }, h.proof)
	if err != nil {
		return WitnessReceipt{}, fmt.Errorf("key: %w", err)
	}
	if err := km.coversAt(receipt.WitnessedAt); err != nil {
		return WitnessReceipt{}, err
	}
	if err := verifyReceiptSignature(receipt, &km.envelope); err != nil {
		return WitnessReceipt{}, err
	}
	return receipt, nil
}
