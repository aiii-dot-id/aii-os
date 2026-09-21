package escrow

import (
	"encoding/json"
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
type NotCovered struct {
	Why string
	Err error
}

// .
const (
	NoSnapshotKeyInEscrow     = "no_snapshot_key_in_escrow"
	NoSnapshotKeyOnHost       = "no_snapshot_key_on_host"
	HostSnapshotKeyUnreadable = "host_snapshot_key_unreadable"
	AnotherSnapshotKey        = "another_snapshot_key"
)

func (e *NotCovered) Error() string {
	return "the escrow does not cover this host's snapshots: " + e.Why
}
func (e *NotCovered) Unwrap() error { return e.Err }

// .
type AnotherIdentity struct{ Holds, Want string }

func (e *AnotherIdentity) Error() string {
	return fmt.Sprintf("the escrow holds identity %s, not %s", e.Holds, e.Want)
}

// .
// .
// .
// .
func OpenFor(sealed, passphrase []byte, wantFingerprint string) (c Contents, kp *crypto.KeyPair, recipient string, err error) {
	c, kp, err = Open(sealed, passphrase)
	if err != nil {
		return Contents{}, nil, "", err
	}
	if kp.Fingerprint() != wantFingerprint {
		c.Wipe()
		return Contents{}, nil, "", &AnotherIdentity{Holds: kp.Fingerprint(), Want: wantFingerprint}
	}
	if err := Challenge(kp); err != nil {
		c.Wipe()
		return Contents{}, nil, "", err
	}
	if c.SnapshotKey != nil {
		if recipient, err = SnapshotRecipient(c.SnapshotKey); err != nil {
			c.Wipe()
			return Contents{}, nil, "", err
		}
	}
	return c, kp, recipient, nil
}

// .
// .
// .
func Covers(escrowRecipient, hostRecipient string, hostErr error) error {
	switch {
	case escrowRecipient == "":
		return &NotCovered{Why: NoSnapshotKeyInEscrow}
	case hostErr != nil:
		return &NotCovered{Why: HostSnapshotKeyUnreadable, Err: hostErr}
	case hostRecipient == "":
		return &NotCovered{Why: NoSnapshotKeyOnHost}
	case hostRecipient != escrowRecipient:
		return &NotCovered{Why: AnotherSnapshotKey}
	}
	return nil
}

// .
// .
// .
func WriteReceipt(kp *crypto.KeyPair, recipient, identityKeyPath string, at time.Time) (r Receipt, published bool, err error) {
	r, err = SignReceipt(kp, recipient, at)
	if err != nil {
		return Receipt{}, false, err
	}
	raw, _ := json.MarshalIndent(r, "", "  ")
	published, err = PublishReplacing(append(raw, '\n'), ReceiptPath(identityKeyPath))
	return r, published, err
}
