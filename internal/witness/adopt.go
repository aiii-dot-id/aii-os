package witness

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"

	"github.com/aiii-dot-id/aii-os/internal/logsink"
)

func (a *Anchorer) adoptRegistered(dir string, ce *ConflictError, witnessKey *PublicKeyEnvelope, last *WitnessReceipt, heads *HeadVerifier, release func()) error {
	canonical, receipt, err := a.registeredEvidence(ce, witnessKey)
	if err != nil {
		a.stand(ce, "the witness holds this identity's signing key under another enrolment, which was not adopted: "+err.Error())
		return fmt.Errorf("anchor refused: the witness holds this identity's key under another enrolment, not adopted (%v): %w", err, ce)
	}
	if err := a.parked(dir); err != nil {
		return err
	}
	if err := a.guard().WitnessFiles(func() error { return writeEnvelopeFile(dir, a.key, canonical) }); err != nil {
		return fmt.Errorf("the registered witness envelope could not be kept beside the ledger (the next pass asks again): %w", err)
	}
	cacheEnvelope(a.envelopes, canonical)
	logsink.Info("witness.decision", "the witness's registered enrolment of this identity's key is adopted: %s is this identity at the witness, its envelope kept beside the ledger; its receipt for record %d is held to the head rule", receipt.IdentityID, receipt.LedgerOrdinal)
	a.mu.Lock()
	fn := a.onAdopted
	a.mu.Unlock()
	if fn != nil {
		fn(receipt.IdentityID)
	}
	if heads == nil {

		return nil
	}

	if err := a.standsUnder(heads, receipt); err != nil {
		var no *cannotStand
		if errors.As(err, &no) {
			conflict := &ConflictError{Local: true}
			if a.witnessSideOnly(dir, last, receipt) {
				conflict.WitnessSide = true
				conflict.Message = fmt.Sprintf("the witness's row for the adopted enrolment %s does not continue this record: its receipt for record %d cannot stand as the record's next head: %v — nothing in it contradicts the record, which stands; no head is minted", receipt.IdentityID, receipt.LedgerOrdinal, err)
			} else {
				conflict.Message = fmt.Sprintf("witness state fork: the receipt of the adopted enrolment %s for record %d cannot stand as the record's next head: %v", receipt.IdentityID, receipt.LedgerOrdinal, err)
			}
			a.recordIntegrityConflict(conflict)
			return conflict
		}
		kept := "kept beside the ledger; the next pass settles it first"
		if werr := a.keepReceipt(dir, receipt); werr != nil {
			kept = fmt.Sprintf("NOT kept beside the ledger: %v", werr)
		}
		return fmt.Errorf("the adopted enrolment's receipt for record %d could not be held to the head rule and is not minted (%s): %w", receipt.LedgerOrdinal, kept, err)
	}
	if err := a.keepReceipt(dir, receipt); err != nil {
		return fmt.Errorf("the adopted enrolment's receipt for record %d could not be kept beside the ledger before its mint (the next pass asks again): %w", receipt.LedgerOrdinal, err)
	}
	head, err := a.minter.MintWitnessed(receipt, receipt.WitnessSignature.KeyID)
	release()
	if err != nil {
		return fmt.Errorf("mint the adopted enrolment's receipt for record %d (kept beside the ledger; the next pass settles it first): %w", receipt.LedgerOrdinal, err)
	}
	a.anchored(receipt)
	if err := a.retireReceipt(dir, receipt); err != nil {
		logsink.Warn("witness.error", "the receipt for record %d is in the record and its copy beside the ledger could not be retired — the next pass retires it: %v", receipt.LedgerOrdinal, err)
	}
	at := uint64(0)
	if head != nil {
		at = head.Seq
	}
	logsink.Info("witness.end", "the adopted enrolment's receipt is in the record: head %d attests record %d, witnessed %s", at, receipt.LedgerOrdinal, receipt.WitnessedAt)
	return nil
}

func (a *Anchorer) registeredEvidence(ce *ConflictError, witnessKey *PublicKeyEnvelope) ([]byte, WitnessReceipt, error) {
	ev := ce.Evidence
	switch {
	case ev == nil || len(ev.RegisteredIdentityPublicKey) == 0:
		return nil, WitnessReceipt{}, errors.New("the refusal carries no registered envelope, and an identity id alone is an unsigned hint")
	case ev.Unreadable != "":
		return nil, WitnessReceipt{}, fmt.Errorf("the refusal's evidence does not read (%s)", ev.Unreadable)
	}
	canonical, env, err := parseIdentityEnvelope(ev.RegisteredIdentityPublicKey)
	if err != nil {
		return nil, WitnessReceipt{}, fmt.Errorf("the registered envelope is not an identity envelope: %w", err)
	}
	if !sameSigningKey(env, a.key) {
		return nil, WitnessReceipt{}, errors.New("the registered envelope holds another signing key than this identity's")
	}
	id, err := DeriveIdentityID(canonical, env)
	if err != nil {
		return nil, WitnessReceipt{}, fmt.Errorf("the registered envelope: %w", err)
	}
	if id != ev.RegisteredIdentityID {
		return nil, WitnessReceipt{}, fmt.Errorf("the registered envelope derives %s, and the refusal names %s", id, ev.RegisteredIdentityID)
	}
	r := ev.Receipt
	switch {
	case r == nil:
		return nil, WitnessReceipt{}, errors.New("the refusal carries no receipt for the registered enrolment")
	case r.IdentityID != id:
		return nil, WitnessReceipt{}, fmt.Errorf("the refusal's receipt names %s, the registered enrolment %s", r.IdentityID, id)
	case r.ContextSHA256 != "":
		return nil, WitnessReceipt{}, fmt.Errorf("the refusal's receipt: %w", ErrLaterReceiptForm)
	}
	if err := verifyReceiptSignature(*r, witnessKey); err != nil {
		return nil, WitnessReceipt{}, fmt.Errorf("the refusal's receipt does not verify under the witness key: %w", err)
	}
	ids, _, _, err := a.envelopes.WitnessEnrollment()
	if err != nil {
		return nil, WitnessReceipt{}, fmt.Errorf("the record's witness enrollment could not be read: %w", err)
	}
	if len(ids) > 1 || (len(ids) == 1 && ids[0] != id) {
		return nil, WitnessReceipt{}, fmt.Errorf("the record names witness identity %s, not the registered %s", ids[0], id)
	}
	return canonical, *r, nil
}

func sameSigningKey(env *PublicKeyEnvelope, key IdentityKey) bool {
	ml, ok := env.FindPublicKey(AlgMLDSA87)
	if !ok {
		return false
	}
	theirs, err1 := base64.StdEncoding.DecodeString(ml.PublicKeyB64)
	ours, err2 := base64.StdEncoding.DecodeString(key.PublicKeyB64())
	return err1 == nil && err2 == nil && len(ours) > 0 && bytes.Equal(theirs, ours)
}
