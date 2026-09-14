package witness

// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/aiii-dot-id/aii-os/internal/ledger"
)

// .
const TailFileName = "witness-tail.json"

// .
// .
// .
type LocalTail struct {
	LedgerOrdinal         int64  `json:"ledger_ordinal"`
	LedgerHash            string `json:"ledger_hash"`
	WitnessedAt           string `json:"witnessed_at"`
	WitnessKeyFingerprint string `json:"witness_key_fingerprint"`
}

// .
// .
// .
// .
// .
// .
// .
func writeLocalTail(dir string, tail LocalTail) error {
	raw, err := json.Marshal(tail)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, TailFileName+".tmp-")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(raw); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, filepath.Join(dir, TailFileName)); err != nil {
		return err
	}
	if d, err := os.Open(dir); err == nil {
		_ = d.Sync()
		_ = d.Close()
	}
	return nil
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
// .
// .
// .
// .
// .
// .
// .
func CheckLocalTail(dataDir string, lg LedgerSource) error {
	raw, err := os.ReadFile(filepath.Join(dataDir, TailFileName))
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("witness tail file unreadable: %w", err)
	}
	var tail LocalTail
	if err := json.Unmarshal(raw, &tail); err != nil {
		return fmt.Errorf("witness tail file corrupt: %w", err)
	}
	if tail.LedgerOrdinal < 1 || tail.LedgerHash == "" {
		return fmt.Errorf("witness tail file malformed: ordinal=%d hash=%q", tail.LedgerOrdinal, tail.LedgerHash)
	}
	if int64(lg.LastSeq()) < tail.LedgerOrdinal {
		return fmt.Errorf("LEDGER TRUNCATION: ledger ends at seq %d but the witness attested event %d (%s) at %s — events the witness proved existed are gone",
			lg.LastSeq(), tail.LedgerOrdinal, tail.LedgerHash, tail.WitnessedAt)
	}
	// .
	found := false
	var forkErr error
	if err := ledger.Stream(lg.Path(), func(evt *ledger.Event) error {
		if int64(evt.Seq) != tail.LedgerOrdinal {
			return nil
		}
		found = true
		if got := evt.EntryHash(); got != tail.LedgerHash {
			forkErr = fmt.Errorf("LEDGER FORK: event %d hashes to %s but the witness attested %s at %s — same ordinal, different history",
				tail.LedgerOrdinal, got, tail.LedgerHash, tail.WitnessedAt)
		}
		return ledger.ErrStop
	}); err != nil {
		return fmt.Errorf("witness tail check cannot read the ledger: %w", err)
	}
	if forkErr != nil {
		return forkErr
	}
	if found {
		return nil
	}
	// .
	// .
	return fmt.Errorf("LEDGER TRUNCATION: no event with seq %d exists though the ledger claims to reach %d — the witness attested %s at %s",
		tail.LedgerOrdinal, lg.LastSeq(), tail.LedgerHash, tail.WitnessedAt)
}
