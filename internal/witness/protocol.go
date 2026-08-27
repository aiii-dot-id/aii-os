// Package witness implements the client half of the ai3-witnessd
// bookmark protocol: the external continuity-anchoring service.
//
// PROTOCOL SOURCE OF TRUTH: ai3-witnessd (sev_os/ai3-tools/ai3-witnessd).
// Every wire type, signature-input string, and derivation in this file
// mirrors the server's definitions, cited by file:line at each site.
// This client is written against the server that exists — the previous
// client spoke POST /anchor, a protocol nothing served (honesty review
// 2026-08-16, finding A6).
//
// WHAT A BOOKMARK PROVES (C5): the identity submits its ledger tail plus
// a range commitment; the witness returns a signed receipt. The receipt
// proves "a ledger whose event N hashed to ledger_hash existed at
// witnessed_at, attested by a third party." Combined with the ledger's
// own prev-hash chain (any event commits to its full prefix), this is
// continuity proof to a party outside the operator.
//
// RANGE SEMANTICS (client-defined; the server holds range_hash opaque):
//
//	range_start_ordinal = S, ledger_ordinal = N
//	range_hash = content_hash of ledger event S
//	ledger_hash = content_hash of ledger event N
//
// Because event N's prev-hash chain covers S..N, the pair (range_hash,
// ledger_hash) is verifiable by anyone holding the ledger file + receipt.
//
// TRUST MODEL (stated, not implied):
//  1. ALWAYS: the witness public-key envelope fetched from /witness/pubkey
//     is cross-checked against /witness/pubkey/hash (catches response
//     tampering between endpoints) — anchor is TLS + the configured domain.
//  2. BY DEFAULT: the platform-signed /witness/pubkey/manifest is fully
//     verified (dual-PQ ProfileRoot) and the served key must match the
//     manifest — the AIII platform is the anchor, same root as RING0.
//     Platform key source: operator path override
//     (witness_server.platform_pubkey_path) or the canon genesis download
//     (in-memory, per verification). Without EITHER source the key is
//     self-vouched and the log says so loudly every pass (2026-08-17).
//  3. Receipts are verified against the fetched witness key before
//     persistence: signature over the exact receipt input, key_id and
//     fingerprint match, witness_version and echoed fields match the
//     request. An unverifiable receipt is never stored.
//
// TRUNCATION & FORK DETECTION (client side; 2026-08-20 hardening):
//
// The server already detects regressions — ai3-witnessd durably
// persists each identity's last witnessed tail in Postgres and answers
// a bookmark that moves backward or rewrites an ordinal with 409
// "witness bookmark rollback or fork" (store.go:362 identity binding,
// :375 monotonic guard; body shape {"error": msg}, server.go:377-378).
// What was missing was the CLIENT side of that alarm:
//
//  1. Typed conflicts: Bookmark returns *ConflictError for every 409
//     instead of flattening it into the transport-error string. The
//     server's message is the only discriminator on the wire;
//     IsCadence() splits the one benign pacing refusal from the
//     integrity class (rollback/fork, identity mismatch, unknown —
//     unknown leans alarming by design).
//  2. The integrity latch: the Anchorer records the first integrity
//     conflict (server 409 or its own receipt-chain mismatch), fires
//     the SetOnIntegrityConflict seam once for SAFE/operator-card
//     wiring, and refuses to resubmit the same anchor point until an
//     operator resolves it — a history disagreement must never look
//     like a retryable network blip.
//  3. The one file: witness-tail.json beside the ledger (tail.go),
//     atomically rewritten at every verified receipt persistence, so
//     boot (CheckLocalTail) can prove offline that the ledger still
//     reaches the newest third-party-attested tail. A prev-hash chain
//     cannot prove its own completeness — a truncated chain is a
//     shorter VALID chain — which is exactly the gap this file closes;
//     absent it is advisory (first boot), present it is authoritative.
package witness

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/aiii-dot-id/aii-os/internal/crypto"
	"github.com/aiii-dot-id/aii-os/internal/sigenvelope"
)

// Wire constants — witnessd/types.go:12-30. The grammar constants are
// the same facts the AIII envelope doctrine defines (AIII_SERVER_KEYS.md
// §6-§7), so they are sourced from the one Go definition
// (internal/sigenvelope, internal/crypto) and cannot drift per consumer.
const (
	WitnessVersion = "1.0"

	HashPrefixSHA256 = "sha256:"
	ZeroSHA256Hash   = HashPrefixSHA256 + "0000000000000000000000000000000000000000000000000000000000000000"

	CanonicalizationV1 = sigenvelope.CanonicalizationV1
	ProfileRoot        = crypto.ProfileRoot
	AlgMLDSA87         = crypto.SigAlg
	AlgSLHDSASHA2256   = crypto.SLHAlg

	// ProfileFast — witnessd-only online-signing profile (bookmark
	// receipts); not part of the ROOT envelope grammar.
	ProfileFast = "AIII-PQ-SIGNATURE-V1-FAST"

	// PublicKeyEnvelopeKind — witnessd validatePublicEnvelope ("kind must be...").
	PublicKeyEnvelopeKind = "aiii.server_key.public"

	// Status codes the bookmark endpoint returns.
	StatusCreated  = 201 // first bookmark for an identity
	StatusOK       = 200 // advance or idempotent retry
	StatusConflict = 409 // rollback/fork/cadence violation
)

// PublicKeyMaterial — witnessd/types.go PublicKeyMaterial; field-for-field
// the AIII keyset entry (AIII_SERVER_KEYS.md §5), so it IS the shared
// grammar type (consolidation 2026-08-19, one source per fact).
type PublicKeyMaterial = sigenvelope.PublicKeyMaterial

// PublicKeyEnvelope — witnessd/types.go PublicKeyEnvelope. The identity
// presents one of these (key_type "identity") alongside every bookmark.
// Field-for-field the AIII public keyset envelope (AIII_SERVER_KEYS.md
// §5), so it aliases the shared grammar type; FindPublicKey rides the
// alias from sigenvelope.
type PublicKeyEnvelope = sigenvelope.PublicKeyEnvelope

// SignatureEntry — witnessd/types.go SignatureEntry. NOT the shared
// envelope entry (sigenvelope.SignatureEntry): witnessd request/receipt
// signatures carry signature_profile PER ENTRY (ProfileFast receipts),
// while the ROOT envelope grammar carries the profile at envelope level
// only — a different wire fact, deliberately not unified.
type SignatureEntry struct {
	SignatureProfile     string `json:"signature_profile,omitempty"`
	Alg                  string `json:"alg"`
	KeyID                string `json:"key_id"`
	PublicKeyFingerprint string `json:"public_key_fingerprint"`
	SignatureInputSHA256 string `json:"signature_input_sha256"`
	SigB64               string `json:"sig_b64"`
}

// WitnessRequest — witnessd/types.go WitnessRequest. The body of
// POST /witness/bookmark; identity-signed.
type WitnessRequest struct {
	IdentityID        string          `json:"identity_id"`
	IdentityPublicKey json.RawMessage `json:"identity_public_key"`
	LedgerOrdinal     int64           `json:"ledger_ordinal"`
	LedgerHash        string          `json:"ledger_hash"`
	RangeStartOrdinal int64           `json:"range_start_ordinal"`
	RangeHash         string          `json:"range_hash"`
	IdentitySignature SignatureEntry  `json:"identity_signature"`
}

// WitnessReceipt — witnessd/types.go WitnessReceipt. Witness-signed.
type WitnessReceipt struct {
	WitnessVersion                 string         `json:"witness_version"`
	ReceiptKind                    string         `json:"receipt_kind,omitempty"`
	IdentityID                     string         `json:"identity_id"`
	IdentityPublicKeyHash          string         `json:"identity_public_key_hash,omitempty"`
	PreviousWitnessedLedgerOrdinal int64          `json:"previous_witnessed_ledger_ordinal"`
	PreviousWitnessedLedgerHash    string         `json:"previous_witnessed_ledger_hash"`
	LedgerOrdinal                  int64          `json:"ledger_ordinal"`
	LedgerHash                     string         `json:"ledger_hash"`
	RangeStartOrdinal              int64          `json:"range_start_ordinal"`
	RangeHash                      string         `json:"range_hash"`
	WitnessedAt                    string         `json:"witnessed_at"`
	WitnessSignature               SignatureEntry `json:"witness_signature"`
}

// WitnessStatus — GET /status fields the client uses (window sizing and
// cadence gating: the hosted witness rejects anchors closer together than
// min_periodic_cadence events with 409).
type WitnessStatus struct {
	MaxRangeEntries    int64 `json:"max_range_entries"`
	MinPeriodicCadence int64 `json:"min_periodic_cadence"`
}

// RequestSignatureInput — witnessd/crypto.go requestSignatureInput,
// byte-for-byte. The identity signs this string.
func RequestSignatureInput(req WitnessRequest, canonicalPublicKey []byte) []byte {
	return []byte(fmt.Sprintf("AIII-WITNESS-REQUEST\nidentity_id:%s\nidentity_public_key:%s\nledger_ordinal:%d\nledger_hash:%s\nrange_start_ordinal:%d\nrange_hash:%s\n",
		req.IdentityID,
		string(canonicalPublicKey),
		req.LedgerOrdinal,
		req.LedgerHash,
		req.RangeStartOrdinal,
		req.RangeHash))
}

// ReceiptSignatureInput — witnessd/crypto.go receiptSignatureInput
// (bookmark kind), byte-for-byte. The witness signs this string; the
// client verifies against it.
func ReceiptSignatureInput(receipt WitnessReceipt) []byte {
	return []byte(fmt.Sprintf("AIII-WITNESS-RECEIPT\nwitness_version:%s\nidentity_id:%s\nprevious_witnessed_ledger_ordinal:%d\nprevious_witnessed_ledger_hash:%s\nledger_ordinal:%d\nledger_hash:%s\nrange_start_ordinal:%d\nrange_hash:%s\nwitnessed_at:%s\n",
		receipt.WitnessVersion,
		receipt.IdentityID,
		receipt.PreviousWitnessedLedgerOrdinal,
		receipt.PreviousWitnessedLedgerHash,
		receipt.LedgerOrdinal,
		receipt.LedgerHash,
		receipt.RangeStartOrdinal,
		receipt.RangeHash,
		receipt.WitnessedAt))
}

// IdentityIDMaterial — witnessd/crypto.go deriveWitnessIdentityID:
// "did:aiii:identity:sha256:<hex>" over the ML-DSA fingerprint and the
// canonical envelope hash. Stable only if the envelope bytes are stable —
// the envelope is therefore synthesized once and persisted.
func IdentityIDMaterial(mlDsaFingerprint, canonicalKeyHash string) string {
	return fmt.Sprintf("AIII-WITNESS-IDENTITY-ID\nidentity_public_key_fingerprint:%s\nidentity_public_key_hash:%s\n",
		mlDsaFingerprint, canonicalKeyHash)
}

// FingerprintMaterial — witnessd/crypto.go publicKeyFingerprint,
// byte-for-byte (identical to the genesis client's derivation).
func FingerprintMaterial(alg, keyID, publicKeyB64 string) string {
	return fmt.Sprintf("AIII-PUBLIC-KEY-FINGERPRINT-V1\nalg:%s\nkey_id:%s\npublic_key_b64:%s\n", alg, keyID, publicKeyB64)
}

// RangeHashMaterial builds the canon §6 range-hash input:
//
//	AIII-WITNESS-RANGE-HASH
//	range_start_ordinal:<S>
//	ledger_ordinal:<N>
//	<ordinal>:<line-hash>   (for every event S..N, each line newline-terminated)
//	...
//
// lineHashes must carry the content hash of every event from
// range_start_ordinal through ledger_ordinal IN ORDER.
func RangeHashMaterial(startOrdinal, ledgerOrdinal int64, lineHashes []string) []byte {
	var sb strings.Builder
	sb.WriteString("AIII-WITNESS-RANGE-HASH\n")
	fmt.Fprintf(&sb, "range_start_ordinal:%d\n", startOrdinal)
	fmt.Fprintf(&sb, "ledger_ordinal:%d\n", ledgerOrdinal)
	for i, h := range lineHashes {
		fmt.Fprintf(&sb, "%d:%s\n", startOrdinal+int64(i), h)
	}
	return []byte(sb.String())
}

// TrimHashPrefix strips "sha256:" for identity-id hex composition
// (witnessd deriveWitnessIdentityID uses strings.TrimPrefix).
func TrimHashPrefix(h string) string { return strings.TrimPrefix(h, HashPrefixSHA256) }

// PrefixHash is the wire form of a ledger hash.
//
// LEDGER_GOLD_FORMAT.md §2 is explicit and the two differ on purpose:
// content_hash is "lowercase hex SHA-256" with NO prefix, while
// entry_sha256 is "sha256:" + lowercase-hex. The witness protocol wants
// the prefixed form (witnessd validateSHA256 rejects anything else with
// 400 "invalid hash field"), so a ledger hash is prefixed AT THE WIRE
// and nowhere else. Local state stays in ledger form.
//
// Idempotent: prefixing an already-prefixed hash is a no-op, so this is
// safe to apply at any boundary without knowing what came before.
func PrefixHash(h string) string {
	if h == "" || strings.HasPrefix(h, HashPrefixSHA256) {
		return h
	}
	return HashPrefixSHA256 + h
}
