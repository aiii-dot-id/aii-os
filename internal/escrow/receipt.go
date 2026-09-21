package escrow

import (
	"encoding/base64"
	"errors"
	"fmt"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/crypto"
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
// .
type Receipt struct {
	Identity          string `json:"identity"`
	SnapshotRecipient string `json:"snapshot_recipient"`
	CheckedAt         string `json:"checked_at"`
	Sig               string `json:"sig"`
}

// .
// .
// .
const (
	SnapshotKeyFileName = "snapshot.key"
	ReceiptFileName     = "escrow-receipt.json"
)

// .
var ErrReceipt = errors.New("escrow receipt does not verify")

// .
// .
func receiptInput(identity, recipient, checkedAt string) []byte {
	return []byte("AII-ESCROW-RECEIPT-V1\nidentity:" + identity + "\nsnapshot_recipient:" + recipient + "\nchecked_at:" + checkedAt + "\n")
}

// .
// .
// .
func SignReceipt(kp *crypto.KeyPair, snapshotRecipient string, at time.Time) (Receipt, error) {
	if snapshotRecipient == "" {
		return Receipt{}, fmt.Errorf("%w: no snapshot recipient to attest", ErrReceipt)
	}
	r := Receipt{Identity: kp.Fingerprint(), SnapshotRecipient: snapshotRecipient, CheckedAt: at.UTC().Format(time.RFC3339)}
	sig, err := crypto.Sign(kp, receiptInput(r.Identity, r.SnapshotRecipient, r.CheckedAt))
	if err != nil {
		return Receipt{}, fmt.Errorf("sign receipt: %w", err)
	}
	r.Sig = base64.StdEncoding.EncodeToString(sig)
	return r, nil
}

// .
// .
// .
func (r Receipt) Verify(identityPub []byte, snapshotRecipient string) error {
	if r.Identity != crypto.PublicKeyFingerprint(identityPub) {
		return fmt.Errorf("%w: it is identity %s's", ErrReceipt, r.Identity)
	}
	if snapshotRecipient == "" || r.SnapshotRecipient != snapshotRecipient {
		return fmt.Errorf("%w: it attests another snapshot key", ErrReceipt)
	}
	if _, err := time.Parse(time.RFC3339, r.CheckedAt); err != nil {
		return fmt.Errorf("%w: checked_at: %w", ErrReceipt, err)
	}
	sig, err := base64.StdEncoding.DecodeString(r.Sig)
	if err != nil {
		return fmt.Errorf("%w: signature: %w", ErrReceipt, err)
	}
	if err := crypto.Verify(identityPub, receiptInput(r.Identity, r.SnapshotRecipient, r.CheckedAt), sig); err != nil {
		return fmt.Errorf("%w: %w", ErrReceipt, err)
	}
	return nil
}
