package app

import (
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/crypto"
	"github.com/aiii-dot-id/aii-os/internal/ledger"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
	"github.com/aiii-dot-id/aii-os/internal/witness"
)

const envelopeRowDisagreesSaid = "This identity's database holds a witness envelope that does not name the identity at the witness that its record names, so it was not kept beside the record (" + witness.EnvelopeFileName + "). " +
	"The record decides: the envelope it names is rebuilt from the record when the witness is next asked, and kept beside the record then. The identity keeps running. The detail: "

func (a *App) keepWitnessEnvelope(kp *crypto.KeyPair, lg *ledger.Ledger) {
	var guard witness.FileGuard = witness.Unguarded{}
	if a.door != nil {
		guard = a.door
	}
	kept, err := witness.KeepEnvelopeBeside(guard, witness.AsIdentityKey(kp), a.store, filepath.Dir(lg.Path()))
	switch {
	case errors.Is(err, witness.ErrEnvelopeRowDisagrees):
	case err != nil:
		logsink.Error("witness.error", "the witness envelope could not be kept beside the ledger at this start (the next use of the envelope tries again): %v", err)
		return
	case kept:
		logsink.Info("witness.decision", "the witness envelope is kept beside the ledger (%s), from the store, where it names the identity the record names", witness.EnvelopeFileName)
		return
	default:
		return
	}
	logsink.Error("witness.refusal", "the witness envelope is not kept beside the ledger: %v", err)
	id := "witness_envelope_" + witnessNoticeNow().UTC().Format("20060102")
	if told, lerr := a.store.OutboxIDsUnder(id); lerr == nil && slices.Contains(told, id) {
		return
	}
	if _, werr := a.store.AddOutboxNoticeOnce(id, "operator", "", envelopeRowDisagreesSaid+err.Error(), nil); werr != nil {
		logsink.Warn("witness.error", "the operator's notice that the witness envelope is not kept beside the ledger could not be written: %v", werr)
	}
}

const witnessKeyKeptSaid = "The witness at %s now serves a key proof that may not replace the one kept beside this identity's record: it names another key under the same key id, it is valid for less time than the one kept, or it does not verify as this program and the version before it check one. " +
	"The kept proof was left as it was, so every witness head already in the record still verifies. New records are not witnessed while the witness serves that proof; the identity keeps running, and each anchor pass asks again. " +
	"Ask whoever runs the witness about its key. This is not told again today. The detail: "

func (a *App) tellWitnessKeyKept(url string, err error, now time.Time) {
	id := "witness_key_kept_" + now.UTC().Format("20060102")
	if told, lerr := a.store.OutboxIDsUnder(id); lerr == nil && slices.Contains(told, id) {
		return
	}
	if _, werr := a.store.AddOutboxNoticeOnce(id, "operator", "", fmt.Sprintf(witnessKeyKeptSaid, url)+err.Error(), nil); werr != nil {
		logsink.Warn("witness.error", "the operator's notice that the witness key kept beside the ledger was not replaced could not be written: %v", werr)
	}
}

const witnessParkedSaid = "Beside this identity's record stands witness work that this version cannot carry out: an operation a later version of this program wrote (" + witness.OperationFileName + "), a kept bookmark (" + witness.PendingRequestFileName + ") this version cannot read, or a file whose presence cannot be told. " +
	"Until a version that can carry it out runs, this version changes nothing at the witness: no new record is witnessed and no segment is sealed. Nothing is lost: what stands is kept as it is and travels in backups, and the identity keeps running. " +
	"Run the version that wrote it to finish it. This is not told again today. The detail: "

func (a *App) tellWitnessParked(err error, now time.Time) {
	id := "witness_parked_" + now.UTC().Format("20060102")
	if told, lerr := a.store.OutboxIDsUnder(id); lerr == nil && slices.Contains(told, id) {
		return
	}
	if _, werr := a.store.AddOutboxNoticeOnce(id, "operator", "", witnessParkedSaid+err.Error(), nil); werr != nil {
		logsink.Warn("witness.error", "the operator's notice that witness work is parked could not be written: %v", werr)
	}
}

const witnessContextRequiredSaid = "The witness at %s enrols a new identity only from a newer version of this program, and this version cannot send what it asks for. " +
	"This identity is not enrolled there: no new record is witnessed and no segment is sealed until a newer version runs. The identity keeps running. " +
	"This version does not ask the witness again until it is started again, and this is not told again today. The detail: "

const witnessKeyEnrolledSaid = "The witness at %s holds this identity's signing key under another witness identity, and that enrolment was not taken as this identity's. " +
	"No new record is witnessed and no segment is sealed until it is put right; the identity keeps running. " +
	"This version does not ask the witness again until it is started again, and this is not told again today. Why, and the detail: "

const witnessEnrolmentAdoptedSaid = "The witness at %s already held this identity's signing key, enrolled as %s. " +
	"That enrolment's envelope holds this identity's key, its receipt verified, and the record names no other witness identity, so this identity now presents that envelope to the witness. " +
	"Its receipt is recorded only where the rule every witness head is held to accepts it. The identity keeps running. This is not told again today."

func (a *App) tellWitnessRefusalStands(url string, ce *witness.ConflictError, why string, now time.Time) {
	id, said := "witness_key_enrolled_", fmt.Sprintf(witnessKeyEnrolledSaid, url)+why+". "+ce.Error()
	if ce.IsContextRequired() {
		id, said = "witness_context_required_", fmt.Sprintf(witnessContextRequiredSaid, url)+ce.Error()
	}
	id += now.UTC().Format("20060102")
	if told, lerr := a.store.OutboxIDsUnder(id); lerr == nil && slices.Contains(told, id) {
		return
	}
	if _, werr := a.store.AddOutboxNoticeOnce(id, "operator", "", said, nil); werr != nil {
		logsink.Warn("witness.error", "the operator's notice that the witness's refusal stands could not be written: %v", werr)
	}
}

func (a *App) tellWitnessEnrolmentAdopted(url, registeredID string, now time.Time) {
	id := "witness_enrolment_adopted_" + now.UTC().Format("20060102")
	if told, lerr := a.store.OutboxIDsUnder(id); lerr == nil && slices.Contains(told, id) {
		return
	}
	if _, werr := a.store.AddOutboxNoticeOnce(id, "operator", "", fmt.Sprintf(witnessEnrolmentAdoptedSaid, url, registeredID), nil); werr != nil {
		logsink.Warn("witness.error", "the operator's notice that the witness's enrolment was adopted could not be written: %v", werr)
	}
}
