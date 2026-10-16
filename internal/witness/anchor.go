package witness

import (
	"context"
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

	onUnrecognised   func(*ConflictError)
	toldUnrecognised map[ReplyKind]bool
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

func (a *Anchorer) SetOnUnrecognisedConflict(fn func(*ConflictError)) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.onUnrecognised = fn
}

func (a *Anchorer) refuseUnrecognised(ce *ConflictError) {
	kind := ce.Kind()
	a.mu.Lock()
	again := a.toldUnrecognised[kind]
	if a.toldUnrecognised == nil {
		a.toldUnrecognised = map[ReplyKind]bool{}
	}
	a.toldUnrecognised[kind] = true
	fn := a.onUnrecognised
	a.mu.Unlock()
	if again {
		return
	}
	logsink.Warn("witness.refusal", "the witness's address answered this bookmark with a 409 this program does not recognise — NOT taken as evidence against the record (nothing in it is signed); this pass is skipped, nothing is latched, and the next pass asks again; another reply of this kind (%s) is not told again until the witness answers: %v", ce.Kind(), ce)
	if fn != nil {
		fn(ce)
	}
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
	if ce.WitnessSide {
		logsink.Error("witness.refusal", "THE WITNESS'S ROW DOES NOT CONTINUE THIS RECORD — no head is minted and anchoring is off until the witness's side is put right; the record is intact and the identity runs: %v", ce)
	} else {
		logsink.Error("witness.refusal", "INTEGRITY CONFLICT (ROLLBACK/FORK) — anchoring latched off until operator review: %v", ce)
	}
	if fn != nil {
		fn(ce)
	}
}

func (a *Anchorer) witnessSideOnly(dir string, last *WitnessReceipt, r WitnessReceipt) bool {
	p, hp := r.PreviousWitnessedLedgerOrdinal, r.PreviousWitnessedLedgerHash
	switch {
	case p == -1 && hp == ZeroLedgerHash:

	case p < 0:
		return false
	case last != nil && p == last.LedgerOrdinal && hp != last.LedgerHash:
		return false
	default:
		hash, err := entryHashAt(a.ledger.Path(), uint64(p))
		if err != nil || hash != hp {
			return false
		}
	}
	heads, err := a.headRule(dir, nil)
	if err != nil {
		return false
	}
	heads.prevOrdinal, heads.prevHash = p, hp
	if last != nil {
		heads.identityID = last.IdentityID
	}
	return a.standsUnder(heads, r) == nil
}

func (a *Anchorer) CheckAndAnchor() error {
	return a.AnchorPass(context.Background(), nil)
}

func (a *Anchorer) AnchorPass(ctx context.Context, st *WitnessStatus) error {

	a.mu.Lock()
	latched := a.conflict
	a.mu.Unlock()
	if latched != nil {
		if latched.WitnessSide {
			logsink.Warn("witness.refusal", "anchoring remains off: the witness's row does not continue this record, and that is put right on the witness's side: %v", latched)
		} else {
			logsink.Warn("witness.refusal", "anchoring remains latched off by an unresolved ROLLBACK/FORK conflict: %v", latched)
		}
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

	ledgerDir := filepath.Dir(a.ledger.Path())
	resend := a.requestToSendAgain(ledgerDir)

	current := a.ledger.LastSeq()
	needed := int64(a.intervalEvents)
	if st == nil {
		if asked, err := a.client.Status(); err == nil {
			st = asked
		}
	}
	if st != nil && st.MinPeriodicCadence > needed {

		a.mu.Lock()
		if !a.floorLogged {
			a.floorLogged = true
			logsink.Info("witness.decision", "server minimum periodic cadence %d events overrides configured interval %d — anchors follow the floor", st.MinPeriodicCadence, needed)
		}
		a.mu.Unlock()
		needed = st.MinPeriodicCadence
	}

	a.mu.Lock()
	anchored, last := a.lastAnchoredSeq, a.lastReceipt
	a.mu.Unlock()
	if resend == nil && (current == 0 || int64(current-anchored) < needed) {
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

	if last != nil && last.IdentityID != "" && last.IdentityID != identityID {
		logsink.Error("witness.refusal", "anchoring refused: the record's last receipt names %s, the envelope names %s", last.IdentityID, identityID)
		return fmt.Errorf("%w: the record's last receipt names %s, the envelope names %s", ErrNotTheRecordedIdentity, last.IdentityID, identityID)
	}

	var heads *HeadVerifier
	if a.minter != nil {
		heads, err = a.headRule(filepath.Dir(a.ledger.Path()), last)
		if err != nil {
			logsink.Error("witness.refusal", "the rule the next boot holds a head to did not load — anchoring refused this pass, the witness was not asked: %v", err)
			return fmt.Errorf("head rule beside the ledger: %w", err)
		}
	}

	if err := ctx.Err(); err != nil {
		return fmt.Errorf("anchor pass ended before its bookmark: %w", err)
	}

	release := func() {}
	a.mu.Lock()
	if a.pause != nil {
		release = a.pause.PauseAppends()
	}
	a.mu.Unlock()
	defer release()

	if resend != nil && resend.IdentityID != identityID {

		logsink.Error("witness.refusal", "the bookmark kept beside the ledger names %s and the envelope names %s: it is not sent again", resend.IdentityID, identityID)
		a.retireRequest(ledgerDir)
		return fmt.Errorf("%w: the bookmark kept beside the ledger names %s, the envelope names %s", ErrNotTheRecordedIdentity, resend.IdentityID, identityID)
	}
	var req WitnessRequest
	if resend != nil {
		req = resend.Request
		logsink.Info("witness.decision", "sending again the bookmark for record %d, kept beside the ledger since %s: its answer was never kept here", req.LedgerOrdinal, resend.WrittenAt)
	} else {
		req, err = a.buildRequest(identityID, canonicalEnvelope, env)
		if err != nil {
			return err
		}

		if werr := writePendingRequest(ledgerDir, req, time.Now()); werr != nil {
			logsink.Warn("witness.error", "the bookmark for record %d could not be kept beside the ledger before it left (%v): if its answer is lost, the next pass cannot send it again", req.LedgerOrdinal, werr)
		}
	}

	result, err := a.client.Bookmark(req)
	if err != nil {

		var ce *ConflictError
		if errors.As(err, &ce) {
			if ce.IsCadence() {

				logsink.Info("witness.decision", "anchor paced off by server cadence (not an integrity signal): %v", ce)

				a.retireRequest(ledgerDir)
				return fmt.Errorf("anchor refused by cadence gate: %w", ce)
			}
			if ce.Unrecognised() {

				a.refuseUnrecognised(ce)
				return fmt.Errorf("anchor refused by a 409 this program does not recognise (not an integrity signal; nothing latched): %w", ce)
			}

			a.retireRequest(ledgerDir)
			a.recordIntegrityConflict(ce)
			return fmt.Errorf("witness integrity conflict: %w", ce)
		}
		return fmt.Errorf("anchor failed (non-blocking): %w", err)
	}

	if err := VerifyReceipt(result.Receipt, req, witnessKey); err != nil {

		if resend != nil {
			a.retireRequest(ledgerDir)
			logsink.Error("witness.refusal", "the receipt for the bookmark sent again (record %d) FAILED verification — discarded, not persisted, anchor point not advanced; the bookmark is no longer kept, and the next pass bookmarks the record's end: %v", req.LedgerOrdinal, err)
		} else {
			logsink.Error("witness.refusal", "receipt FAILED verification — discarded, not persisted, anchor point not advanced: %v", err)
		}
		return fmt.Errorf("receipt verification: %w", err)
	}

	if heads == nil && last != nil && !result.First {
		if result.Receipt.PreviousWitnessedLedgerOrdinal != last.LedgerOrdinal ||
			result.Receipt.PreviousWitnessedLedgerHash != last.LedgerHash {
			ce := &ConflictError{Local: true, Message: fmt.Sprintf(
				"witness state fork: receipt chains (%d,%s...) but local last receipt is (%d,%s...) — refusing to anchor over the divergence",
				result.Receipt.PreviousWitnessedLedgerOrdinal, result.Receipt.PreviousWitnessedLedgerHash[:20],
				last.LedgerOrdinal, last.LedgerHash[:20])}
			a.retireRequest(ledgerDir)
			a.recordIntegrityConflict(ce)
			return ce
		}
	}

	if heads != nil {
		if err := a.standsUnder(heads, result.Receipt); err != nil {
			var no *cannotStand
			if errors.As(err, &no) {

				ce := &ConflictError{Local: true}
				if a.witnessSideOnly(filepath.Dir(a.ledger.Path()), last, result.Receipt) {
					ce.WitnessSide = true
					ce.Message = fmt.Sprintf(
						"the witness's row for this identity does not continue this record: the receipt for record %d (first=%v) cannot stand as its next head: %v — nothing in the answer contradicts the record, which stands; no head is minted",
						result.Receipt.LedgerOrdinal, result.First, err)
				} else {
					ce.Message = fmt.Sprintf(
						"witness state fork: the receipt for record %d (first=%v) cannot stand as the record's next head: %v — refusing to anchor over the divergence",
						result.Receipt.LedgerOrdinal, result.First, err)
				}
				a.retireRequest(ledgerDir)
				a.recordIntegrityConflict(ce)
				return ce
			}

			kept := "kept beside the ledger; the next pass settles it first"
			if werr := writePendingReceipt(filepath.Dir(a.ledger.Path()), result.Receipt, time.Now()); werr != nil {
				kept = fmt.Sprintf("NOT kept beside the ledger: %v", werr)
			} else {
				a.retireRequest(ledgerDir)
			}
			logsink.Error("witness.error", "the receipt for record %d could not be held to the head rule and is not minted (%s): %v", result.Receipt.LedgerOrdinal, kept, err)
			return fmt.Errorf("receipt for record %d not minted (%s): %w", result.Receipt.LedgerOrdinal, kept, err)
		}
	}

	if a.minter != nil {

		dir := filepath.Dir(a.ledger.Path())
		kept := "kept beside the ledger; the next pass settles it first"
		if err := writePendingReceipt(dir, result.Receipt, time.Now()); err != nil {
			kept = "NOT kept beside the ledger"
			logsink.Error("witness.error", "the receipt for record %d could not be kept beside the ledger before its mint — minting it anyway: %v", result.Receipt.LedgerOrdinal, err)
		} else {
			a.retireRequest(ledgerDir)
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
	a.retireRequest(ledgerDir)
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
	a.toldUnrecognised = nil
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
