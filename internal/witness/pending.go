package witness

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/atomicfile"
	"github.com/aiii-dot-id/aii-os/internal/ledger"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
)

const PendingReceiptFileName = "witness-pending-receipt.json"

func PendingReceiptPath(ledgerDir string) string {
	return filepath.Join(ledgerDir, PendingReceiptFileName)
}

var ErrPendingReceiptRefused = errors.New("the witness receipt pending beside the ledger cannot be minted")

type pendingReceipt struct {
	IdentityID    string         `json:"identity_id"`
	LedgerOrdinal int64          `json:"ledger_ordinal"`
	LedgerHash    string         `json:"ledger_hash"`
	Receipt       WitnessReceipt `json:"receipt"`
	WrittenAt     string         `json:"written_at"`
	writtenAt     time.Time
}

func writePendingReceipt(ledgerDir string, r WitnessReceipt, now time.Time) error {
	raw, err := json.Marshal(pendingReceipt{
		IdentityID:    r.IdentityID,
		LedgerOrdinal: r.LedgerOrdinal,
		LedgerHash:    r.LedgerHash,
		Receipt:       r,
		WrittenAt:     now.UTC().Format(time.RFC3339Nano),
	})
	if err != nil {
		return err
	}
	published, err := atomicfile.WriteReplace(PendingReceiptPath(ledgerDir), raw, 0o600)
	if err != nil && published {
		return fmt.Errorf("%s published but not durable: %w", PendingReceiptFileName, err)
	}
	return err
}

func readPendingReceipt(ledgerDir string) (*pendingReceipt, error) {
	raw, err := atomicfile.ReadFile(PendingReceiptPath(ledgerDir))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("%s unreadable: %w", PendingReceiptFileName, err)
	}
	var p pendingReceipt
	if err := jsonUnmarshalStrict(raw, &p); err != nil {
		return nil, fmt.Errorf("%s corrupt: %w", PendingReceiptFileName, err)
	}
	r := p.Receipt
	if p.IdentityID == "" || p.LedgerOrdinal < 1 || p.LedgerHash == "" ||
		p.IdentityID != r.IdentityID || p.LedgerOrdinal != r.LedgerOrdinal || p.LedgerHash != r.LedgerHash {
		return nil, fmt.Errorf("%s malformed: it names %s at %d (%s), its receipt %s at %d (%s)",
			PendingReceiptFileName, p.IdentityID, p.LedgerOrdinal, p.LedgerHash, r.IdentityID, r.LedgerOrdinal, r.LedgerHash)
	}
	if p.writtenAt, err = time.Parse(time.RFC3339Nano, p.WrittenAt); err != nil {
		return nil, fmt.Errorf("%s malformed: written_at: %w", PendingReceiptFileName, err)
	}
	return &p, nil
}

func retirePendingReceipt(ledgerDir string) error {
	if err := os.Remove(PendingReceiptPath(ledgerDir)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return atomicfile.SyncDir(ledgerDir)
}

func sameReceipt(a, b WitnessReceipt) bool {
	return a.IdentityID == b.IdentityID && a.LedgerOrdinal == b.LedgerOrdinal && a.LedgerHash == b.LedgerHash &&
		a.PreviousWitnessedLedgerOrdinal == b.PreviousWitnessedLedgerOrdinal &&
		a.PreviousWitnessedLedgerHash == b.PreviousWitnessedLedgerHash && a.WitnessedAt == b.WitnessedAt
}

func (a *Anchorer) SettlePending() error {
	if a.minter == nil {
		return nil
	}
	dir := filepath.Dir(a.ledger.Path())
	p, err := readPendingReceipt(dir)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrPendingReceiptRefused, err)
	}
	if p == nil {
		return nil
	}
	r := p.Receipt
	a.mu.Lock()
	last := a.lastReceipt
	a.mu.Unlock()
	if last != nil && sameReceipt(*last, r) {
		if err := retirePendingReceipt(dir); err != nil {
			return fmt.Errorf("the receipt for record %d is in the record and its copy beside the ledger could not be retired: %w", r.LedgerOrdinal, err)
		}
		logsink.Info("witness.decision", "the receipt pending beside the ledger (record %d) is in the record — retired", r.LedgerOrdinal)
		return nil
	}
	if err := a.standsAsNextHead(dir, last, r); err != nil {
		return fmt.Errorf("%w (identity %s, record %d, kept %s): %w", ErrPendingReceiptRefused, r.IdentityID, r.LedgerOrdinal, p.WrittenAt, err)
	}
	head, err := a.minter.MintWitnessed(r, r.WitnessSignature.KeyID)
	if err != nil {
		return fmt.Errorf("mint the receipt pending beside the ledger (record %d): %w", r.LedgerOrdinal, err)
	}
	a.anchored(r)
	if err := retirePendingReceipt(dir); err != nil {
		return fmt.Errorf("the receipt for record %d is in the record and its copy beside the ledger could not be retired: %w", r.LedgerOrdinal, err)
	}
	at := uint64(0)
	if head != nil {
		at = head.Seq
	}
	logsink.Info("witness.end", "the receipt pending beside the ledger is in the record: head %d attests record %d, witnessed %s", at, r.LedgerOrdinal, r.WitnessedAt)
	return nil
}

func (a *Anchorer) standsAsNextHead(dir string, last *WitnessReceipt, r WitnessReceipt) error {
	heads, err := a.headRule(dir, last)
	if err != nil {
		return err
	}
	return a.standsUnder(heads, r)
}

func (a *Anchorer) headRule(dir string, last *WitnessReceipt) (*HeadVerifier, error) {
	platform, err := LoadPlatformEnvelope(a.platformPubkeyPath)
	if err != nil {
		return nil, err
	}
	heads, err := LoadHeadVerifier(dir, platform)
	if err != nil {
		return nil, fmt.Errorf("witness keys beside the ledger: %w", err)
	}
	if last != nil {
		heads.prevOrdinal, heads.prevHash, heads.identityID = last.LedgerOrdinal, last.LedgerHash, last.IdentityID
	}
	return heads, nil
}

type cannotStand struct{ err error }

func (e *cannotStand) Error() string { return e.err.Error() }
func (e *cannotStand) Unwrap() error { return e.err }

func (a *Anchorer) standsUnder(heads *HeadVerifier, r WitnessReceipt) error {

	var readErr error
	hashAt := func(seq uint64) (string, bool) {
		hash, err := entryHashAt(a.ledger.Path(), seq)
		if err != nil {
			readErr = err
			return "", false
		}
		return hash, true
	}
	end := a.ledger.Last()
	place := headPlace{seq: end.Seq + 1, prev: end.Hash, hashAt: hashAt}
	err := heads.verifyReceipt(place, headReceipt{
		IdentityID:                     r.IdentityID,
		PreviousWitnessedLedgerOrdinal: r.PreviousWitnessedLedgerOrdinal,
		PreviousWitnessedLedgerHash:    r.PreviousWitnessedLedgerHash,
		LedgerOrdinal:                  r.LedgerOrdinal,
		LedgerHash:                     r.LedgerHash,
		WitnessedAt:                    r.WitnessedAt,
		WitnessKeyID:                   r.WitnessSignature.KeyID,
		WitnessSigB64:                  r.WitnessSignature.SigB64,
	})
	if readErr != nil {
		return fmt.Errorf("record %d, which the receipt attests, does not read back: %w", r.LedgerOrdinal, readErr)
	}
	if err == nil || errors.Is(err, ledger.ErrWitnessKeyUnknown) {
		return nil
	}
	return &cannotStand{err}
}

func entryHashAt(path string, seq uint64) (string, error) {
	var hash string
	err := ledger.Stream(path, func(evt *ledger.Event) error {
		if evt.Seq == seq {
			hash = evt.EntryHash()
			return ledger.ErrStop
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	if hash == "" {
		return "", fmt.Errorf("the ledger holds no record %d", seq)
	}
	return hash, nil
}

const PendingRequestFileName = "witness-pending-request.json"

func PendingRequestPath(ledgerDir string) string {
	return filepath.Join(ledgerDir, PendingRequestFileName)
}

type pendingRequest struct {
	IdentityID    string         `json:"identity_id"`
	LedgerOrdinal int64          `json:"ledger_ordinal"`
	LedgerHash    string         `json:"ledger_hash"`
	Request       WitnessRequest `json:"request"`
	WrittenAt     string         `json:"written_at"`
}

func writePendingRequest(ledgerDir string, req WitnessRequest, now time.Time) error {
	raw, err := json.Marshal(pendingRequest{
		IdentityID:    req.IdentityID,
		LedgerOrdinal: req.LedgerOrdinal,
		LedgerHash:    req.LedgerHash,
		Request:       req,
		WrittenAt:     now.UTC().Format(time.RFC3339Nano),
	})
	if err != nil {
		return err
	}
	published, err := atomicfile.WriteReplace(PendingRequestPath(ledgerDir), raw, 0o600)
	if err != nil && published {
		return fmt.Errorf("%s published but not durable: %w", PendingRequestFileName, err)
	}
	return err
}

func readPendingRequest(ledgerDir string) (*pendingRequest, error) {
	raw, err := atomicfile.ReadFile(PendingRequestPath(ledgerDir))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("%s unreadable: %w", PendingRequestFileName, err)
	}
	var p pendingRequest
	if err := jsonUnmarshalStrict(raw, &p); err != nil {
		return nil, fmt.Errorf("%s corrupt: %w", PendingRequestFileName, err)
	}
	r := p.Request
	if p.IdentityID == "" || p.LedgerOrdinal < 1 || p.LedgerHash == "" ||
		p.IdentityID != r.IdentityID || p.LedgerOrdinal != r.LedgerOrdinal || p.LedgerHash != r.LedgerHash {
		return nil, fmt.Errorf("%s malformed: it names %s at %d (%s), its request %s at %d (%s)",
			PendingRequestFileName, p.IdentityID, p.LedgerOrdinal, p.LedgerHash, r.IdentityID, r.LedgerOrdinal, r.LedgerHash)
	}
	return &p, nil
}

func retirePendingRequest(ledgerDir string) error {
	if err := os.Remove(PendingRequestPath(ledgerDir)); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	return atomicfile.SyncDir(ledgerDir)
}

func (a *Anchorer) requestToSendAgain(dir string) *pendingRequest {
	p, err := readPendingRequest(dir)
	if err != nil {
		aside := PendingRequestPath(dir) + ".unreadable"
		if rerr := os.Rename(PendingRequestPath(dir), aside); rerr != nil {
			logsink.Error("witness.error", "the bookmark kept beside the ledger does not read (%v) and could not be set aside (%v): this pass bookmarks the record's end without it", err, rerr)
			return nil
		}
		logsink.Error("witness.error", "the bookmark kept beside the ledger does not read (%v): set aside as %s. Which bookmark the witness last took is not known here, and this pass bookmarks the record's end", err, filepath.Base(aside))
		return nil
	}
	if p == nil {
		return nil
	}
	a.mu.Lock()
	anchored := a.lastAnchoredSeq
	a.mu.Unlock()
	end := a.ledger.Last()
	at := uint64(p.LedgerOrdinal)
	switch {
	case at <= anchored:

	case at > end.Seq || !a.holdsAsSent(p, end):

		logsink.Warn("witness.decision", "the bookmark kept beside the ledger is for record %d, which this record no longer holds as it was sent (the record ends at %d): it is not sent again, and this pass asks the witness about the record as it is", p.LedgerOrdinal, end.Seq)
	default:
		return p
	}
	a.retireRequest(dir)
	return nil
}

func (a *Anchorer) holdsAsSent(p *pendingRequest, end ledger.Boundary) bool {
	if uint64(p.LedgerOrdinal) == end.Seq {
		return p.LedgerHash == end.Hash
	}
	hash, err := entryHashAt(a.ledger.Path(), uint64(p.LedgerOrdinal))
	return err != nil || hash == p.LedgerHash
}

func (a *Anchorer) retireRequest(dir string) {
	if err := retirePendingRequest(dir); err != nil {
		logsink.Warn("witness.error", "the bookmark kept beside the ledger could not be retired (the next pass sends it again, and the witness answers it as before): %v", err)
	}
}
