package witness

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/atomicfile"
)

const ConflictFileName = "witness-conflict.json"

func ConflictPath(ledgerDir string) string { return filepath.Join(ledgerDir, ConflictFileName) }

type KeptConflict struct {
	WitnessURL string `json:"witness_url"`
	Local      bool   `json:"local"`
	Answer     string `json:"answer"`
	Record     uint64 `json:"record"`
	KeptAt     string `json:"kept_at"`
}

func keepConflict(ledgerDir string, kc KeptConflict) error {
	raw, err := json.Marshal(kc)
	if err != nil {
		return err
	}
	published, err := atomicfile.WriteReplace(ConflictPath(ledgerDir), raw, 0o600)
	if err != nil && published {
		return fmt.Errorf("%s published but not durable: %w", ConflictFileName, err)
	}
	return err
}

var ErrKeptConflictUnread = errors.New("the witness's answer kept beside the ledger does not read")

func ReadKeptConflict(ledgerDir string) (*KeptConflict, error) {
	raw, err := atomicfile.ReadFile(ConflictPath(ledgerDir))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("%s: %w", ConflictFileName, err)
	}
	var kc KeptConflict
	if err := json.Unmarshal(raw, &kc); err != nil || kc.WitnessURL == "" || kc.Answer == "" {
		return nil, fmt.Errorf("%w: %s (%v)", ErrKeptConflictUnread, ConflictFileName, err)
	}
	return &kc, nil
}

func (kc *KeptConflict) Of(witnessURL string) bool {
	return witnessURL != "" && kc.WitnessURL == trimTrailingSlash(witnessURL)
}

func keptConflictOf(witnessURL string, ce *ConflictError, record uint64, now time.Time) KeptConflict {
	return KeptConflict{WitnessURL: witnessURL, Local: ce.Local, Answer: ce.Error(), Record: record, KeptAt: now.UTC().Format(time.RFC3339)}
}

func (a *Anchorer) AskWhileHeld(ctx context.Context) (cleared bool, err error) {
	dir := filepath.Dir(a.ledger.Path())
	if p, perr := readPendingReceipt(dir); perr != nil || p != nil {
		return false, fmt.Errorf("a receipt is pending beside the ledger (%v), and only a running identity settles it: the witness is not asked while it stands", perr)
	}
	a.mu.Lock()
	a.askOnly, a.conflict = true, nil
	a.mu.Unlock()
	err = a.AnchorPass(ctx, nil)
	a.mu.Lock()
	ce := a.conflict
	a.mu.Unlock()
	switch {
	case err == nil:
		return true, nil
	case ce != nil && ce.WitnessSide:
		return true, nil
	}
	return false, err
}

const ClearedPrefix = "witness-conflict.cleared-"

func SetKeptConflictAside(ledgerDir string, now time.Time) (string, error) {
	name := ClearedPrefix + now.UTC().Format("20060102T150405Z") + ".json"
	published, err := atomicfile.Replace(ConflictPath(ledgerDir), filepath.Join(ledgerDir, name))
	if err != nil && published {
		return name, fmt.Errorf("%s set aside as %s but not durably: %w", ConflictFileName, name, err)
	}
	return name, err
}
