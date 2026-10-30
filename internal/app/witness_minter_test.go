package app

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/witness"
)

// THE MINTER REFUSES A RECEIPT IN A LATER VERSION'S FORM. Its head would
// carry a context digest and want the context beside it: a record the
// version before this one refuses at boot, and one this version has no
// context for. No path reaches the minter with one; the minter is the last
// door, and the record is left as it was.
func TestTheMinterRefusesAReceiptInALaterVersionsForm(t *testing.T) {
	f := newSealedFixture(t)
	a := New(f.cfg)
	if err := startLiveForTest(a); err != nil {
		t.Fatal(err)
	}
	defer stopApp(t, a)
	before := a.door.LastSeq()
	r := witness.WitnessReceipt{IdentityID: "did:aiii:identity:sha256:later", PreviousWitnessedLedgerOrdinal: -1, PreviousWitnessedLedgerHash: witness.ZeroLedgerHash,
		LedgerOrdinal: int64(before), LedgerHash: strings.Repeat("a", 64), WitnessedAt: time.Now().UTC().Format(time.RFC3339),
		ContextSHA256: "sha256:" + strings.Repeat("c", 64)}
	if _, err := (witnessMinter{door: a.door}).MintWitnessed(r, "aiii_witness_later"); !errors.Is(err, witness.ErrLaterReceiptForm) {
		t.Fatalf("the minter took a receipt in a later version's form: %v", err)
	}
	if a.door.LastSeq() != before {
		t.Fatalf("the record moved: %d → %d", before, a.door.LastSeq())
	}
}
