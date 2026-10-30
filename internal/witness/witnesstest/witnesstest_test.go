package witnesstest

import (
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/crypto"
	"github.com/aiii-dot-id/aii-os/internal/ledger"
	"github.com/aiii-dot-id/aii-os/internal/witness"
)

// envelopeRow is the store's one envelope row, for an identity whose
// record names none.
type envelopeRow struct{ row []byte }

func (e *envelopeRow) SaveWitnessEnvelope(j []byte) error {
	if e.row == nil {
		e.row = j
	}
	return nil
}
func (e *envelopeRow) ReplaceWitnessEnvelope(j []byte) error { e.row = j; return nil }
func (e *envelopeRow) LoadWitnessEnvelope() ([]byte, error)  { return e.row, nil }
func (e *envelopeRow) WitnessEnrollment() ([]string, time.Time, time.Time, error) {
	return nil, time.Time{}, time.Time{}, nil
}

func TestTheDoubleRefusesARollbackInTheWitnessesOwnWords(t *testing.T) {
	w := NewWitness(t, nil)
	kp, err := crypto.GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	envelopes := &envelopeRow{}
	// One identity, and a record of n records each time it is asked.
	pass := func(n int) (*witness.Anchorer, error) {
		lg, err := ledger.New(filepath.Join(t.TempDir(), "ledger.jsonl"))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { lg.Close() })
		for i := 0; i < n; i++ {
			if _, err := lg.Append(ledger.EventExperienceCreate, 3, map[string]string{"n": fmt.Sprint(i)}, kp); err != nil {
				t.Fatal(err)
			}
		}
		a := witness.NewAnchorer(witness.New(w.URL(), ""), lg, witness.AsIdentityKey(kp), envelopes, nil, nil, 1, "")
		return a, a.CheckAndAnchor()
	}
	if _, err := pass(5); err != nil {
		t.Fatalf("rig: the first bookmark: %v", err)
	}
	if at, identities := w.Holds(); at != 5 || identities != 1 {
		t.Fatalf("rig: the witness holds record %d of %d identities, want 5 of 1", at, identities)
	}

	a, err := pass(3)
	var ce *witness.ConflictError
	if !errors.As(err, &ce) {
		t.Fatalf("a bookmark behind the witness's row was not refused as a conflict: %v", err)
	}
	if !ce.IsIntegrity() || ce.Unrecognised() || ce.IsCadence() {
		t.Fatalf("THE DOUBLE'S ROLLBACK REFUSAL IS NOT ONE THE CLIENT KNOWS AS THE WITNESS'S: %v", ce)
	}
	if ce.Code != "rollback" || ce.Message != rollbackOrFork {
		t.Fatalf("THE DOUBLE DOES NOT REFUSE A ROLLBACK AS THE WITNESS DOES (code %q, words %q): want code rollback and the words %q", ce.Code, ce.Message, rollbackOrFork)
	}
	if a.IntegrityConflict() == nil {
		t.Fatal("the rollback the double refused was not latched")
	}
	if at, identities := w.Holds(); at != 5 || identities != 1 {
		t.Fatalf("the refused bookmark moved the witness's row: record %d of %d identities", at, identities)
	}

	// Another record 5, of another history: at the row, a fork.
	if _, err := pass(5); !errors.As(err, &ce) || !ce.IsIntegrity() || ce.Code != "fork" || ce.Message != rollbackOrFork {
		t.Fatalf("THE DOUBLE DOES NOT REFUSE A FORK AS THE WITNESS DOES: %v", err)
	}
}

// THE DOUBLE REFUSES IN THE WITNESS'S OWN FORM WHEN TOLD TO, with the code
// it is given: what an application test puts at the witness's address to
// meet a refusal by its code.
func TestTheDoubleRefusesWithTheCodeItIsGiven(t *testing.T) {
	w := NewWitness(t, nil)
	kp, err := crypto.GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	lg, err := ledger.New(filepath.Join(t.TempDir(), "ledger.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { lg.Close() })
	if _, err := lg.Append(ledger.EventExperienceCreate, 3, map[string]string{"n": "0"}, kp); err != nil {
		t.Fatal(err)
	}
	w.RefusesWithCode(409, "a_code_nobody_defined", "witness bookmark rollback or fork")
	a := witness.NewAnchorer(witness.New(w.URL(), ""), lg, witness.AsIdentityKey(kp), &envelopeRow{}, nil, nil, 1, "")
	var ce *witness.ConflictError
	if err := a.CheckAndAnchor(); !errors.As(err, &ce) || !ce.WitnessForm || ce.Code != "a_code_nobody_defined" || !ce.Unrecognised() {
		t.Fatalf("the double's refusal did not come back in the witness's form with its code: %v", err)
	}
	if at, identities := w.Holds(); at != -1 || identities != 0 {
		t.Fatalf("a refusal in the witness's place moved its rows: record %d of %d identities", at, identities)
	}
}
