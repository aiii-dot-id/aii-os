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

// ReceiptReader restores anchoring state from the f(ledger) projection.
// *store.Store satisfies it.
type ReceiptReader interface {
	LastWitnessReceipt() (int64, []byte, error)
}

// EventMinter appends a system.witnessed event to the ledger and
// materializes it — the verified receipt enters the SIGNED chain (the
// ledger carries its own continuity proof points). cmd/aii owns the
// adapter (ledger + store + key).
type EventMinter interface {
	MintWitnessed(receipt WitnessReceipt, witnessKeyID string) (*ledger.Event, error)
}

// LedgerSource is what the anchorer needs from the ledger.
type LedgerSource interface {
	LastSeq() uint64
	LastHash() string
	Path() string
}

// Anchorer periodically anchors the ledger to the witness service.
//
// Anchoring is: read the ledger tail + range commitment → sign the
// bookmark request with the identity key → submit → VERIFY the receipt
// against the witness key → chain-check → mint system.witnessed. Only
// verified receipts enter the signed chain. Failures are
// non-blocking (the identity continues); the next pass retries — the
// server's idempotent-retry (same ordinal+hash → same receipt) makes
// retrying safe.
type Anchorer struct {
	client             *Client
	ledger             LedgerSource
	key                IdentityKey
	envelopes          EnvelopeStore
	receipts           ReceiptReader
	minter             EventMinter
	platformPubkeyPath string // optional: enables manifest verification layer
	intervalEvents     int
	floorLogged        bool // the server-floor override is said once

	// mu guards the anchoring point: the witness Every-goroutine writes
	// it while the dashboard's continuity strip reads it — unsynchronized,
	// that was a data race (finding 12, 2026-08-17 review).
	mu              sync.Mutex
	lastAnchoredSeq uint64
	lastReceipt     *WitnessReceipt // local view for chain-continuity checks

	// conflict latches the first integrity conflict (server 409
	// rollback/fork, or the local chain-continuity mismatch). Once set,
	// CheckAndAnchor refuses to resubmit the same anchor point — a
	// witness that says ROLLBACK/FORK is disagreeing about HISTORY, and
	// retrying the identical claim would only bury the alarm in log
	// noise. Set-only, like the ledger's SAFE freeze: it clears through
	// operator intervention and a restart, never by itself. Guarded by mu.
	conflict *ConflictError
	// onConflict, when set (SetOnIntegrityConflict, before the anchor
	// loop starts), fires exactly once as the latch engages — the app
	// layer's seam to SAFE/operator-card wiring.
	onConflict func(*ConflictError)
}

// NewAnchorer creates the anchorer. State restore happens from the
// receipt projection at construction (local reads, no network).
func NewAnchorer(client *Client, lg LedgerSource, key IdentityKey, envelopes EnvelopeStore, receipts ReceiptReader, minter EventMinter, intervalEvents int, platformPubkeyPath string) *Anchorer {
	if intervalEvents == 0 {
		intervalEvents = 50
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

// snapshotLocked-style accessors: every read/write of the anchoring
// point goes through mu. Construction happens before sharing, so the
// constructor's direct writes are safe.

// UnanchoredCount is restart-stable (restored from the receipt projection).
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

// LastAnchoredSeq exposes the current anchoring point.
func (a *Anchorer) LastAnchoredSeq() uint64 {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.lastAnchoredSeq
}

// SetOnIntegrityConflict wires the integrity-alarm seam: fn fires once,
// as the latch engages, with the conflict that engaged it. Call before
// the anchor loop starts (the app's boot wiring, next to NewAnchorer).
func (a *Anchorer) SetOnIntegrityConflict(fn func(*ConflictError)) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.onConflict = fn
}

// IntegrityConflict returns the latched conflict (nil = none) — the
// poll surface for dashboards beside the callback seam.
func (a *Anchorer) IntegrityConflict() *ConflictError {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.conflict
}

// recordIntegrityConflict latches ce (first wins) and fires the wiring
// exactly once. The log line carries the ROLLBACK/FORK words because
// this is the one witness outcome that must never read like a network
// blip: a third party with durable state claims our history moved
// backward or split.
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

// CheckAndAnchor anchors if enough events have passed — at least the
// configured interval AND the server's minimum cadence (the hosted witness
// rejects too-frequent anchors with 409; gating locally avoids both the
// wasted request and the fork-vs-cadence ambiguity of post-hoc 409s).
// Every step fails soft (logged, retried next pass) except receipt
// verification: a receipt that does not verify is DISCARDED loudly, and
// the anchor point does not advance — an unverifiable "proof" is worse
// than none.
func (a *Anchorer) CheckAndAnchor() error {
	// The integrity latch outranks the cadence math: once a
	// rollback/fork conflict is recorded, resubmitting the same anchor
	// point would be a silent retry of a claim a third party already
	// called divergent. Loud, every pass, until the operator resolves it.
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
		// The server's floor silently overrode the configured cadence
		// for weeks (config said 50, anchors landed every 100). Say it
		// once per process: the operator reads cadence from config and
		// deserves to know which number is actually in charge.
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

	// Witness key: fetch + cross-check (and platform-manifest-verify when
	// configured) at least once per anchor attempt.
	witnessKey, err := a.client.FetchWitnessKey()
	if err != nil {
		return fmt.Errorf("witness key: %w", err)
	}
	// Witness key: fetch + cross-check, then manifest-verify against the
	// platform root — by DEFAULT (genesis download), or via an operator
	// path override. A witness that vouches for itself is not verified:
	// the hash cross-check is transport integrity, not trust (2026-08-17
	// review — the genesis branch existed but was unreachable without a
	// configured path, so default deployments silently skipped the
	// platform chain the code claimed to enforce).
	if a.platformPubkeyPath != "" || a.client.HasGenesisURL() {
		if err := a.client.VerifyManifest(witnessKey, a.platformPubkeyPath); err != nil {
			log.Printf("WITNESS: manifest verification failed — anchoring refused this pass: %v", err)
			return fmt.Errorf("witness manifest: %w", err)
		}
	} else {
		// Degenerate config (witness without genesis source): the key is
		// self-vouched. Loud, every pass — this is not a verified trust
		// chain, and the log must not pretend otherwise.
		log.Printf("WITNESS: NO platform key source — witness key is SELF-vouched (no manifest verification possible)")
	}
	// (the cached-witnessKey field is gone — it was write-only state;
	// every pass fetches and verifies fresh, which is the trust model.)

	// Stable identity envelope (synthesized once, persisted).
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
		// A 409 is not a transport blip — it is the server's durable
		// state disagreeing with ours, and it used to drown in the same
		// "anchor failed (non-blocking)" wrapper as a dropped connection.
		var ce *ConflictError
		if errors.As(err, &ce) {
			if ce.IsCadence() {
				// The one non-integrity 409. The local cadence gate above
				// (MinPeriodicCadence folded into needed) avoids these
				// whenever /status is reachable, so a surviving cadence
				// 409 means /status failed this pass — pacing, self-heals
				// as events accumulate; retrying next pass is correct.
				log.Printf("WITNESS: anchor paced off by server cadence (not an integrity signal): %v", ce)
				return fmt.Errorf("anchor refused by cadence gate: %w", ce)
			}
			// Any other conflict — rollback/fork, identity mismatch, or a
			// message this client cannot classify — is presumed INTEGRITY:
			// the local cadence gate already prevents the benign 409
			// class, so what survives is the server claiming our history
			// moved backward, split, or changed identity.
			a.recordIntegrityConflict(ce)
			return fmt.Errorf("witness integrity conflict: %w", ce)
		}
		return fmt.Errorf("anchor failed (non-blocking): %w", err)
	}

	if err := VerifyReceipt(result.Receipt, req, witnessKey); err != nil {
		log.Printf("WITNESS: receipt FAILED verification — discarded, not persisted, anchor point not advanced: %v", err)
		return fmt.Errorf("receipt verification: %w", err)
	}

	// Chain continuity: when both sides hold a previous anchor, the
	// receipt's previous_witnessed_* must match our local last receipt —
	// a mismatch means the witness's state forked from ours (or ours from
	// its). The same integrity class as a server 409: latch it, so the
	// next pass does not quietly re-anchor over the divergence.
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

	// The verified receipt enters the SIGNED ledger — system.witnessed is
	// the meta-layer anchor; the chain carries its own proof points and
	// replay rebuilds the receipt projection.
	if a.minter != nil {
		if _, err := a.minter.MintWitnessed(result.Receipt, witnessKey.KeyID); err != nil {
			log.Printf("WITNESS: system.witnessed mint failed — receipt verified but NOT in the chain, anchor point not advanced: %v", err)
			return fmt.Errorf("mint system.witnessed: %w", err)
		}
	}
	a.mu.Lock()
	a.lastAnchoredSeq = uint64(req.LedgerOrdinal)
	a.lastReceipt = &result.Receipt
	a.mu.Unlock()

	// The operator's "one file": every verified receipt persistence also
	// refreshes witness-tail.json beside the ledger, so the NEXT boot can
	// prove the file it opens has not been truncated or forked behind
	// the newest third-party attestation (CheckLocalTail). Advisory
	// plumbing — a write failure is loud but must not undo an anchor the
	// ledger already carries.
	fp := ""
	if wm, ok := witnessKey.FindPublicKey(AlgMLDSA87); ok {
		fp = wm.PublicKeyFingerprint
	}
	if err := writeLocalTail(filepath.Dir(a.ledger.Path()), LocalTail{
		LedgerOrdinal: result.Receipt.LedgerOrdinal,
		// Back to ledger form. CheckLocalTail compares this against a
		// raw event ContentHash, so storing the receipt's prefixed hash
		// would make every boot report a truncation that did not happen
		// — a false "acked loss" straight into SAFE. The bug was latent
		// only because no anchor had ever succeeded to write a tail.
		LedgerHash:            TrimHashPrefix(result.Receipt.LedgerHash),
		WitnessedAt:           result.Receipt.WitnessedAt,
		WitnessKeyFingerprint: fp,
	}); err != nil {
		log.Printf("WITNESS: witness-tail.json write failed (boot truncation check will lag one anchor): %v", err)
	}

	log.Printf("WITNESS: anchored at seq %d (range %d..%d, first=%v, witnessed %s) — receipt in ledger",
		result.Receipt.LedgerOrdinal, result.Receipt.RangeStartOrdinal, result.Receipt.LedgerOrdinal, result.First, result.Receipt.WitnessedAt)
	return nil
}

// buildRequest computes the bookmark from the ledger: ledger_hash = last
// event's content_hash; range window = the events since the previous
// anchor (first anchor: from event 1), capped to the server's hosted
// maximum; range_hash = the CANON §6 aggregate over the ordered content
// hashes S..N (RangeHashMaterial — what ai3-reconstruct and any canon
// verifier compute).
func (a *Anchorer) buildRequest(identityID string, canonicalEnvelope []byte, env *PublicKeyEnvelope) (WitnessRequest, error) {
	events, err := ledger.ReadAll(a.ledger.Path())
	if err != nil {
		return WitnessRequest{}, fmt.Errorf("read ledger: %w", err)
	}
	if len(events) == 0 {
		return WitnessRequest{}, fmt.Errorf("ledger is empty — nothing to anchor")
	}
	last := events[len(events)-1]

	a.mu.Lock()
	lastAnchored := a.lastAnchoredSeq
	a.mu.Unlock()
	startSeq := lastAnchored + 1 // first anchor: 1
	maxEntries := int64(1024)    // conservative default before /status is known
	if st, err := a.client.Status(); err == nil && st.MaxRangeEntries > 0 {
		maxEntries = st.MaxRangeEntries
	}
	if int64(last.Seq)-int64(startSeq)+1 > maxEntries {
		startSeq = uint64(int64(last.Seq) - maxEntries + 1)
	}

	startIdx := -1
	for i := range events {
		if events[i].Seq == startSeq {
			startIdx = i
			break
		}
	}
	if startIdx < 0 {
		return WitnessRequest{}, fmt.Errorf("ledger event %d (range start) not found — chain broken", startSeq)
	}

	// Canon §6 aggregate: every content hash S..N, in order
	lineHashes := make([]string, 0, len(events)-startIdx)
	for i := startIdx; i < len(events); i++ {
		lineHashes = append(lineHashes, events[i].ContentHash)
	}
	rangeHash := sha256Prefixed(RangeHashMaterial(int64(startSeq), int64(last.Seq), lineHashes))

	req := WitnessRequest{
		IdentityID:        identityID,
		IdentityPublicKey: canonicalEnvelope,
		LedgerOrdinal:     int64(last.Seq),
		// Prefixed at the wire: the ledger stores bare hex per
		// LEDGER_GOLD_FORMAT §2, the witness requires "sha256:"+hex.
		// Sending it raw earned 400 "invalid hash field" on every anchor
		// attempt, every 30s, so this identity was never witnessed — note
		// that RangeHash below was already prefixed, by sha256Prefixed, so
		// one request carried both forms.
		LedgerHash:        PrefixHash(last.ContentHash),
		RangeStartOrdinal: int64(startSeq),
		RangeHash:         rangeHash,
	}
	sig, err := SignRequest(a.key, env, req, canonicalEnvelope)
	if err != nil {
		return WitnessRequest{}, err
	}
	req.IdentitySignature = sig
	return req, nil
}
