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
// .
// .
// .
package witness

import (
	"encoding/json"
	"fmt"

	"github.com/aiii-dot-id/aii-os/internal/crypto"
	"github.com/aiii-dot-id/aii-os/internal/sigenvelope"
)

// .
// .
// .
// .
const (
	// .
	// .
	HashPrefixSHA256 = "sha256:"
	// .
	ZeroLedgerHash = "0000000000000000000000000000000000000000000000000000000000000000"

	CanonicalizationV1 = sigenvelope.CanonicalizationV1
	ProfileRoot        = crypto.ProfileRoot
	AlgMLDSA87         = crypto.SigAlg
	AlgSLHDSASHA2256   = crypto.SLHAlg

	// .
	// .
	ProfileFast = "AIII-PQ-SIGNATURE-V1-FAST"

	// .
	PublicKeyEnvelopeKind = "aiii.server_key.public"

	// .
	StatusCreated  = 201
	StatusOK       = 200
	StatusConflict = 409
)

// .
// .
// .
type PublicKeyMaterial = sigenvelope.PublicKeyMaterial

// .
// .
// .
// .
// .
type PublicKeyEnvelope = sigenvelope.PublicKeyEnvelope

// .
// .
// .
// .
// .
type SignatureEntry struct {
	SignatureProfile     string `json:"signature_profile,omitempty"`
	Alg                  string `json:"alg"`
	KeyID                string `json:"key_id"`
	PublicKeyFingerprint string `json:"public_key_fingerprint"`
	SignatureInputSHA256 string `json:"signature_input_sha256"`
	SigB64               string `json:"sig_b64"`
}

// .
// .
// .
type WitnessRequest struct {
	IdentityID        string          `json:"identity_id"`
	IdentityPublicKey json.RawMessage `json:"identity_public_key"`
	LedgerOrdinal     int64           `json:"ledger_ordinal"`
	LedgerHash        string          `json:"ledger_hash"`
	IdentitySignature SignatureEntry  `json:"identity_signature"`
}

// .
// .
type WitnessReceipt struct {
	IdentityID                     string         `json:"identity_id"`
	PreviousWitnessedLedgerOrdinal int64          `json:"previous_witnessed_ledger_ordinal"`
	PreviousWitnessedLedgerHash    string         `json:"previous_witnessed_ledger_hash"`
	LedgerOrdinal                  int64          `json:"ledger_ordinal"`
	LedgerHash                     string         `json:"ledger_hash"`
	WitnessedAt                    string         `json:"witnessed_at"`
	WitnessSignature               SignatureEntry `json:"witness_signature"`
}

// .
// .
// .
type WitnessStatus struct {
	MinPeriodicCadence int64 `json:"min_periodic_cadence"`
}

// .
// .
func RequestSignatureInput(req WitnessRequest, canonicalPublicKey []byte) []byte {
	return []byte(fmt.Sprintf("AIII-WITNESS-REQUEST\nidentity_id:%s\nidentity_public_key:%s\nledger_ordinal:%d\nledger_hash:%s\n",
		req.IdentityID,
		string(canonicalPublicKey),
		req.LedgerOrdinal,
		req.LedgerHash))
}

// .
// .
// .
func ReceiptSignatureInput(receipt WitnessReceipt) []byte {
	return []byte(fmt.Sprintf("AIII-WITNESS-RECEIPT\nidentity_id:%s\nprevious_witnessed_ledger_ordinal:%d\nprevious_witnessed_ledger_hash:%s\nledger_ordinal:%d\nledger_hash:%s\nwitnessed_at:%s\n",
		receipt.IdentityID,
		receipt.PreviousWitnessedLedgerOrdinal,
		receipt.PreviousWitnessedLedgerHash,
		receipt.LedgerOrdinal,
		receipt.LedgerHash,
		receipt.WitnessedAt))
}

// .
// .
// .
// .
func IdentityIDMaterial(mlDsaFingerprint, canonicalKeyHash string) string {
	return fmt.Sprintf("AIII-WITNESS-IDENTITY-ID\nidentity_public_key_fingerprint:%s\nidentity_public_key_hash:%s\n",
		mlDsaFingerprint, canonicalKeyHash)
}

// .
// .
func FingerprintMaterial(alg, keyID, publicKeyB64 string) string {
	return fmt.Sprintf("AIII-PUBLIC-KEY-FINGERPRINT-V1\nalg:%s\nkey_id:%s\npublic_key_b64:%s\n", alg, keyID, publicKeyB64)
}
