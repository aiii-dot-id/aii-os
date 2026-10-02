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
	raw, err := os.ReadFile(PendingReceiptPath(ledgerDir))
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
	platform, err := LoadPlatformEnvelope(a.platformPubkeyPath)
	if err != nil {
		return err
	}
	heads, err := LoadHeadVerifier(dir, platform)
	if err != nil {
		return fmt.Errorf("witness keys beside the ledger: %w", err)
	}
	if last != nil {
		heads.prevOrdinal, heads.prevHash, heads.identityID = last.LedgerOrdinal, last.LedgerHash, last.IdentityID
	}

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
	err = heads.verifyReceipt(place, headReceipt{
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
	if errors.Is(err, ledger.ErrWitnessKeyUnknown) {
		return nil
	}
	return err
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
