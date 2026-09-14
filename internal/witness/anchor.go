package witness

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"path/filepath"
	"sync"

	"github.com/aiii-dot-id/aii-os/internal/ledger"
)

// .
// .
type ReceiptReader interface {
	LastWitnessReceipt() (int64, []byte, error)
}

// .
// .
// .
// .
type EventMinter interface {
	MintWitnessed(receipt WitnessReceipt, witnessKeyID string) (*ledger.Event, error)
}

// .
type LedgerSource interface {
	LastSeq() uint64
	LastHash() string
	Path() string
}

// .
// .
// .
// .
// .
// .
// .
// .
// .
type Anchorer struct {
	client             *Client
	ledger             LedgerSource
	key                IdentityKey
	envelopes          EnvelopeStore
	receipts           ReceiptReader
	minter             EventMinter
	platformPubkeyPath string
	sealer             Sealer
	intervalEvents     int
	floorLogged        bool

	// .
	// .
	// .
	mu              sync.Mutex
	lastAnchoredSeq uint64
	lastReceipt     *WitnessReceipt

	// .
	// .
	// .
	// .
	// .
	// .
	// .
	conflict *ConflictError
	// .
	// .
	// .
	onConflict func(*ConflictError)
}

// .
// .
type Sealer interface {
	Seal(head uint64) error
}

// .
// .
func (a *Anchorer) SetSealer(s Sealer) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.sealer = s
}

// .
// .
func NewAnchorer(client *Client, lg LedgerSource, key IdentityKey, envelopes EnvelopeStore, receipts ReceiptReader, minter EventMinter, intervalEvents int, platformPubkeyPath string) *Anchorer {
	if intervalEvents == 0 {
		// .
		// .
		// .
		// .
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

// .
// .
// .

// .
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

// .
func (a *Anchorer) LastAnchoredSeq() uint64 {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.lastAnchoredSeq
}

// .
// .
// .
func (a *Anchorer) SetOnIntegrityConflict(fn func(*ConflictError)) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.onConflict = fn
}

// .
// .
func (a *Anchorer) IntegrityConflict() *ConflictError {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.conflict
}

// .
// .
// .
// .
// .
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
	log.Printf("WITNESS: INTEGRITY CONFLICT (ROLLBACK/FORK) — anchoring latched off until operator review: %v", ce)
	if fn != nil {
		fn(ce)
	}
}

// .
// .
// .
// .
// .
// .
// .
// .
func (a *Anchorer) CheckAndAnchor() error {
	// .
	// .
	// .
	// .
	a.mu.Lock()
	latched := a.conflict
	a.mu.Unlock()
	if latched != nil {
		log.Printf("WITNESS: anchoring remains latched off by an unresolved ROLLBACK/FORK conflict: %v", latched)
		return fmt.Errorf("anchoring latched off: %w", latched)
	}

	current := a.ledger.LastSeq()
	needed := int64(a.intervalEvents)
	if st, err := a.client.Status(); err == nil && st.MinPeriodicCadence > needed {
		// .
		// .
		// .
		// .
		a.mu.Lock()
		if !a.floorLogged {
			a.floorLogged = true
			log.Printf("WITNESS: server minimum periodic cadence %d events overrides configured interval %d — anchors follow the floor", st.MinPeriodicCadence, needed)
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

	// .
	// .
	witnessKey, err := a.client.FetchWitnessKey()
	if err != nil {
		return fmt.Errorf("witness key: %w", err)
	}
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	manifestVerified := false
	if a.platformPubkeyPath != "" || a.client.HasGenesisURL() {
		manifestRaw, err := a.client.VerifyManifest(witnessKey, a.platformPubkeyPath)
		if err != nil {
			log.Printf("WITNESS: manifest verification failed — anchoring refused this pass: %v", err)
			return fmt.Errorf("witness manifest: %w", err)
		}
		// .
		// .
		// .
		// .
		keyCanonical, err := canonicalEnvelopeBytes(witnessKey)
		if err != nil {
			return fmt.Errorf("witness key: %w", err)
		}
		if err := persistWitnessKey(filepath.Dir(a.ledger.Path()), witnessKey.KeyID, manifestRaw, keyCanonical); err != nil {
			log.Printf("WITNESS: could not persist the verified witness key beside the ledger — anchoring refused this pass: %v", err)
			return fmt.Errorf("persist witness key: %w", err)
		}
		manifestVerified = true
	} else {
		// .
		// .
		// .
		log.Printf("WITNESS: NO platform key source — witness key is SELF-vouched (no manifest verification possible)")
	}
	// .
	// .

	// .
	canonicalEnvelope, env, err := EnsureIdentityEnvelope(a.key, a.envelopes)
	if err != nil {
		return fmt.Errorf("identity envelope: %w", err)
	}
	identityID, err := DeriveIdentityID(canonicalEnvelope, env)
	if err != nil {
		return fmt.Errorf("identity id: %w", err)
	}

	req, err := a.buildRequest(identityID, canonicalEnvelope, env)
	if err != nil {
		return err
	}

	result, err := a.client.Bookmark(req)
	if err != nil {
		// .
		// .
		// .
		var ce *ConflictError
		if errors.As(err, &ce) {
			if ce.IsCadence() {
				// .
				// .
				// .
				// .
				// .
				log.Printf("WITNESS: anchor paced off by server cadence (not an integrity signal): %v", ce)
				return fmt.Errorf("anchor refused by cadence gate: %w", ce)
			}
			// .
			// .
			// .
			// .
			// .
			a.recordIntegrityConflict(ce)
			return fmt.Errorf("witness integrity conflict: %w", ce)
		}
		return fmt.Errorf("anchor failed (non-blocking): %w", err)
	}

	if err := VerifyReceipt(result.Receipt, req, witnessKey); err != nil {
		log.Printf("WITNESS: receipt FAILED verification — discarded, not persisted, anchor point not advanced: %v", err)
		return fmt.Errorf("receipt verification: %w", err)
	}

	// .
	// .
	// .
	// .
	// .
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

	// .
	// .
	// .
	if a.minter != nil {
		head, err := a.minter.MintWitnessed(result.Receipt, witnessKey.KeyID)
		if err != nil {
			log.Printf("WITNESS: system.witnessed mint failed — receipt verified but NOT in the chain, anchor point not advanced: %v", err)
			return fmt.Errorf("mint system.witnessed: %w", err)
		}
		// .
		// .
		// .
		// .
		// .
		if a.sealer != nil && manifestVerified && head != nil {
			if err := a.sealer.Seal(head.Seq); err != nil {
				log.Printf("WITNESS: sealing through record %d failed — records stay in the tail with their proofs; the next head seals them: %v", head.Seq, err)
			}
		}
	}
	a.mu.Lock()
	a.lastAnchoredSeq = uint64(req.LedgerOrdinal)
	a.lastReceipt = &result.Receipt
	a.mu.Unlock()

	// .
	// .
	// .
	// .
	// .
	// .
	fp := ""
	if wm, ok := witnessKey.FindPublicKey(AlgMLDSA87); ok {
		fp = wm.PublicKeyFingerprint
	}
	if err := writeLocalTail(filepath.Dir(a.ledger.Path()), LocalTail{
		LedgerOrdinal: result.Receipt.LedgerOrdinal,
		// .
		// .
		// .
		LedgerHash:            result.Receipt.LedgerHash,
		WitnessedAt:           result.Receipt.WitnessedAt,
		WitnessKeyFingerprint: fp,
	}); err != nil {
		log.Printf("WITNESS: witness-tail.json write failed (boot truncation check will lag one anchor): %v", err)
	}

	log.Printf("WITNESS: anchored at seq %d (first=%v, witnessed %s) — receipt in ledger",
		result.Receipt.LedgerOrdinal, result.First, result.Receipt.WitnessedAt)
	return nil
}

// .
// .
// .
func (a *Anchorer) buildRequest(identityID string, canonicalEnvelope []byte, env *PublicKeyEnvelope) (WitnessRequest, error) {
	lastSeq := a.ledger.LastSeq()
	if lastSeq == 0 {
		return WitnessRequest{}, fmt.Errorf("ledger is empty — nothing to anchor")
	}
	req := WitnessRequest{
		IdentityID:        identityID,
		IdentityPublicKey: canonicalEnvelope,
		LedgerOrdinal:     int64(lastSeq),
		LedgerHash:        a.ledger.LastHash(),
	}
	sig, err := SignRequest(a.key, env, req, canonicalEnvelope)
	if err != nil {
		return WitnessRequest{}, err
	}
	req.IdentitySignature = sig
	return req, nil
}
