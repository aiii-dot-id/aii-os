package witness

import (
	"encoding/json"
	"errors"
	"fmt"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
	"path/filepath"
	"sync"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/ledger"
)

type ReceiptReader interface {
	LastWitnessReceipt() (int64, []byte, error)
}

type EventMinter interface {
	MintWitnessed(receipt WitnessReceipt, witnessKeyID string) (*ledger.Event, error)
}

type LedgerSource interface {
	LastSeq() uint64
	Last() ledger.Boundary
	Path() string
}

type Anchorer struct {
	client             *Client
	ledger             LedgerSource
	key                IdentityKey
	envelopes          EnvelopeStore
	receipts           ReceiptReader
	minter             EventMinter
	platformPubkeyPath string
	sealer             Sealer
	pause              AppendPause
	intervalEvents     int
	floorLogged        bool

	mu              sync.Mutex
	lastAnchoredSeq uint64
	lastReceipt     *WitnessReceipt

	conflict *ConflictError

	onConflict func(*ConflictError)
}

type Sealer interface {
	Seal(head uint64) error
}

type AppendPause interface {
	PauseAppends() (release func())
}

func (a *Anchorer) SetAppendPause(p AppendPause) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.pause = p
}

func (a *Anchorer) SetSealer(s Sealer) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.sealer = s
}

func NewAnchorer(client *Client, lg LedgerSource, key IdentityKey, envelopes EnvelopeStore, receipts ReceiptReader, minter EventMinter, intervalEvents int, platformPubkeyPath string) *Anchorer {
	if intervalEvents == 0 {

		intervalEvents = 100
	}
	a := &Anchorer{
		client:             client,
		ledger:             lg,
		key:                key,
		envelopes:          envelopes,
		receipts:           receipts,
		minter:             minter,
		intervalEvents:     intervalEvents,
		platformPubkeyPath: platformPubkeyPath,
	}
	if receipts != nil {
		if seq, js, err := receipts.LastWitnessReceipt(); err == nil && seq > 0 {
			a.lastAnchoredSeq = uint64(seq)
			var r WitnessReceipt
			if json.Unmarshal(js, &r) == nil {
				a.lastReceipt = &r
			}
		}
	}
	return a
}

func (a *Anchorer) UnanchoredCount() uint64 {
	current := a.ledger.LastSeq()
	a.mu.Lock()
	anchored := a.lastAnchoredSeq
	a.mu.Unlock()
	if current < anchored {
		return 0
	}
	return current - anchored
}

func (a *Anchorer) LastAnchoredSeq() uint64 {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.lastAnchoredSeq
}

func (a *Anchorer) SetOnIntegrityConflict(fn func(*ConflictError)) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.onConflict = fn
}

func (a *Anchorer) IntegrityConflict() *ConflictError {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.conflict
}

func (a *Anchorer) recordIntegrityConflict(ce *ConflictError) {
	a.mu.Lock()
	already := a.conflict != nil
	if !already {
		a.conflict = ce
	}
	fn := a.onConflict
	a.mu.Unlock()
	if already {
		return
	}
	logsink.Error("witness.refusal", "INTEGRITY CONFLICT (ROLLBACK/FORK) — anchoring latched off until operator review: %v", ce)
	if fn != nil {
		fn(ce)
	}
}

func (a *Anchorer) CheckAndAnchor() error {

	a.mu.Lock()
	latched := a.conflict
	a.mu.Unlock()
	if latched != nil {
		logsink.Warn("witness.refusal", "anchoring remains latched off by an unresolved ROLLBACK/FORK conflict: %v", latched)
		return fmt.Errorf("anchoring latched off: %w", latched)
	}

	if err := a.SettlePending(); err != nil {
		if errors.Is(err, ErrPendingReceiptRefused) {
			logsink.Error("witness.refusal", "anchoring refused: %v", err)
		} else {
			logsink.Error("witness.error", "anchoring refused until the receipt pending beside the ledger is settled: %v", err)
		}
		return err
	}

	current := a.ledger.LastSeq()
	needed := int64(a.intervalEvents)
	if st, err := a.client.Status(); err == nil && st.MinPeriodicCadence > needed {

		a.mu.Lock()
		if !a.floorLogged {
			a.floorLogged = true
			logsink.Info("witness.decision", "server minimum periodic cadence %d events overrides configured interval %d — anchors follow the floor", st.MinPeriodicCadence, needed)
		}
		a.mu.Unlock()
		needed = st.MinPeriodicCadence
	}
	a.mu.Lock()
	anchored := a.lastAnchoredSeq
	a.mu.Unlock()
	if current == 0 || int64(current-anchored) < needed {
		return nil
	}

	witnessKey, err := a.client.FetchWitnessKey()
	if err != nil {
		return fmt.Errorf("witness key: %w", err)
	}

	manifestVerified := false
	if a.platformPubkeyPath != "" || a.client.HasGenesisURL() {
		manifestRaw, err := a.client.VerifyManifest(witnessKey, a.platformPubkeyPath)
		if err != nil {
			logsink.Error("witness.refusal", "manifest verification failed — anchoring refused this pass: %v", err)
			return fmt.Errorf("witness manifest: %w", err)
		}

		keyCanonical, err := canonicalEnvelopeBytes(witnessKey)
		if err != nil {
			return fmt.Errorf("witness key: %w", err)
		}
		if err := persistWitnessKey(filepath.Dir(a.ledger.Path()), witnessKey.KeyID, manifestRaw, keyCanonical); err != nil {
			logsink.Warn("witness.error", "could not persist the verified witness key beside the ledger — anchoring refused this pass: %v", err)
			return fmt.Errorf("persist witness key: %w", err)
		}
		manifestVerified = true
	} else {

		logsink.Warn("witness.refusal", "NO platform key source — witness key is SELF-vouched (no manifest verification possible)")
	}

	canonicalEnvelope, env, err := EnsureIdentityEnvelope(a.key, a.envelopes, filepath.Dir(a.ledger.Path()))
	if err != nil {
		return fmt.Errorf("identity envelope: %w", err)
	}
	identityID, err := DeriveIdentityID(canonicalEnvelope, env)
	if err != nil {
		return fmt.Errorf("identity id: %w", err)
	}

	if a.lastReceipt != nil && a.lastReceipt.IdentityID != "" && a.lastReceipt.IdentityID != identityID {
		logsink.Error("witness.refusal", "anchoring refused: the record's last receipt names %s, the envelope names %s", a.lastReceipt.IdentityID, identityID)
		return fmt.Errorf("%w: the record's last receipt names %s, the envelope names %s", ErrNotTheRecordedIdentity, a.lastReceipt.IdentityID, identityID)
	}

	release := func() {}
	a.mu.Lock()
	if a.pause != nil {
		release = a.pause.PauseAppends()
	}
	a.mu.Unlock()
	defer release()

	req, err := a.buildRequest(identityID, canonicalEnvelope, env)
	if err != nil {
		return err
	}

	result, err := a.client.Bookmark(req)
	if err != nil {

		var ce *ConflictError
		if errors.As(err, &ce) {
			if ce.IsCadence() {

				logsink.Info("witness.decision", "anchor paced off by server cadence (not an integrity signal): %v", ce)
				return fmt.Errorf("anchor refused by cadence gate: %w", ce)
			}

			a.recordIntegrityConflict(ce)
			return fmt.Errorf("witness integrity conflict: %w", ce)
		}
		return fmt.Errorf("anchor failed (non-blocking): %w", err)
	}

	if err := VerifyReceipt(result.Receipt, req, witnessKey); err != nil {
		logsink.Error("witness.refusal", "receipt FAILED verification — discarded, not persisted, anchor point not advanced: %v", err)
		return fmt.Errorf("receipt verification: %w", err)
	}

	if a.lastReceipt != nil && !result.First {
		if result.Receipt.PreviousWitnessedLedgerOrdinal != a.lastReceipt.LedgerOrdinal ||
			result.Receipt.PreviousWitnessedLedgerHash != a.lastReceipt.LedgerHash {
			ce := &ConflictError{Local: true, Message: fmt.Sprintf(
				"witness state fork: receipt chains (%d,%s...) but local last receipt is (%d,%s...) — refusing to anchor over the divergence",
				result.Receipt.PreviousWitnessedLedgerOrdinal, result.Receipt.PreviousWitnessedLedgerHash[:20],
				a.lastReceipt.LedgerOrdinal, a.lastReceipt.LedgerHash[:20])}
			a.recordIntegrityConflict(ce)
			return ce
		}
	}

	if a.minter != nil {

		dir := filepath.Dir(a.ledger.Path())
		kept := "kept beside the ledger; the next pass settles it first"
		if err := writePendingReceipt(dir, result.Receipt, time.Now()); err != nil {
			kept = "NOT kept beside the ledger"
			logsink.Error("witness.error", "the receipt for record %d could not be kept beside the ledger before its mint — minting it anyway: %v", result.Receipt.LedgerOrdinal, err)
		}
		head, err := a.minter.MintWitnessed(result.Receipt, witnessKey.KeyID)
		release()
		if err != nil {
			logsink.Error("witness.error", "system.witnessed mint failed — receipt verified but NOT in the chain (%s), anchor point not advanced: %v", kept, err)
			return fmt.Errorf("mint system.witnessed: %w", err)
		}
		if err := retirePendingReceipt(dir); err != nil {
			logsink.Warn("witness.error", "the receipt for record %d is in the record and its copy beside the ledger could not be retired — the next pass retires it: %v", result.Receipt.LedgerOrdinal, err)
		}

		if a.sealer != nil && manifestVerified && head != nil {
			if !onTime(head, result.Receipt) {
				logsink.Info("witness.decision", "head %d attests record %d: records landed while the witness answered, so it is late — in the record, and the next head on time seals over it", head.Seq, result.Receipt.LedgerOrdinal)
			} else if err := a.sealer.Seal(head.Seq); err != nil {
				logsink.Warn("witness.error", "sealing through record %d failed — ledger containers retained: %v", head.Seq, err)
			}
		}
	}
	a.anchored(result.Receipt)
	logsink.Info("witness.end", "anchored at seq %d (first=%v, witnessed %s) — receipt in ledger",
		result.Receipt.LedgerOrdinal, result.First, result.Receipt.WitnessedAt)
	return nil
}

func onTime(head *ledger.Event, r WitnessReceipt) bool {
	return r.LedgerOrdinal >= 0 && head.Seq == uint64(r.LedgerOrdinal)+1 && head.Prev == r.LedgerHash
}

func (a *Anchorer) anchored(r WitnessReceipt) {
	a.mu.Lock()
	a.lastAnchoredSeq = uint64(r.LedgerOrdinal)
	a.lastReceipt = &r
	a.mu.Unlock()
	if err := writeLocalTail(filepath.Dir(a.ledger.Path()), LocalTail{
		LedgerOrdinal: r.LedgerOrdinal,

		LedgerHash:            r.LedgerHash,
		WitnessedAt:           r.WitnessedAt,
		WitnessKeyFingerprint: r.WitnessSignature.PublicKeyFingerprint,
	}); err != nil {
		logsink.Warn("witness.error", "witness-tail.json write failed (boot truncation check will lag one anchor): %v", err)
	}
}

func (a *Anchorer) buildRequest(identityID string, canonicalEnvelope []byte, env *PublicKeyEnvelope) (WitnessRequest, error) {
	last := a.ledger.Last()
	if last.Seq == 0 {
		return WitnessRequest{}, fmt.Errorf("ledger is empty — nothing to anchor")
	}
	req := WitnessRequest{
		IdentityID:        identityID,
		IdentityPublicKey: canonicalEnvelope,
		LedgerOrdinal:     int64(last.Seq),
		LedgerHash:        last.Hash,
	}
	sig, err := SignRequest(a.key, env, req, canonicalEnvelope)
	if err != nil {
		return WitnessRequest{}, err
	}
	req.IdentitySignature = sig
	return req, nil
}
