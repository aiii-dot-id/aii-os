package witness

// The local witness-tail file: client-side truncation/fork detection.
//
// A prev-hash chain proves internal consistency, and that is exactly why
// it CANNOT prove its own completeness: truncate the file after event N
// and the survivor is a shorter, perfectly self-consistent chain — every
// hash still links, VerifyChain still passes. Detecting the cut needs a
// fact stored OUTSIDE the chain about how far the chain had provably
// reached. The witness service holds that fact durably per identity
// (ai3-witnessd persists last-tail in Postgres and refuses regressions —
// store.go:362 identity binding, :375 monotonic guard), but only the
// server consults it, and only when we next anchor. witness-tail.json is
// the same fact kept LOCALLY, one file beside the ledger, refreshed on
// every verified receipt persistence — so boot can catch a truncated or
// forked ledger immediately, offline, before extending it.
//
// Deliberately advisory-if-absent, authoritative-if-present: no file
// means first boot or a pre-feature ledger (nothing to claim), while a
// present file is a verified third-party attestation's local echo — a
// ledger that contradicts it is TRUNCATION or FORK, never a shrug.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/aiii-dot-id/aii-os/internal/ledger"
)

// TailFileName is the one file, beside the ledger.
const TailFileName = "witness-tail.json"

// LocalTail records the newest VERIFIED witness receipt's view of the
// ledger: the anchored ordinal and content hash, when the witness
// attested it, and which witness key signed the receipt.
type LocalTail struct {
	LedgerOrdinal         int64  `json:"ledger_ordinal"`
	LedgerHash            string `json:"ledger_hash"`
	WitnessedAt           string `json:"witnessed_at"`
	WitnessKeyFingerprint string `json:"witness_key_fingerprint"`
}

// writeLocalTail atomically replaces dir/witness-tail.json: temp file in
// the same directory, fsync, rename. A crash at any point leaves either
// the old complete file or the new complete file — a torn tail file
// would read as corrupt and fail the boot check it exists to serve. The
// directory fsync after rename is best-effort (not every platform
// supports it): losing the rename to a power cut degrades to file-
// absent-or-stale, which the check treats as advisory — safe.
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
	defer os.Remove(tmpName) // no-op after a successful rename
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

// CheckLocalTail is the boot check: does the ledger file still contain
// everything the newest verified witness receipt attested? dataDir is
// the directory holding witness-tail.json (normally
// filepath.Dir(lg.Path()), where the anchorer writes it).
//
//   - file absent → nil (first boot / pre-feature ledger — advisory);
//   - lg.LastSeq() < the attested ordinal → TRUNCATION (the chain is
//     shorter than a third party proved it once was — the failure class
//     VerifyChain structurally cannot see);
//   - the event AT the attested ordinal carries a different
//     content_hash → FORK (same length, different history — a reseal);
//   - file present but unreadable/torn → error, loud (a present file is
//     authoritative; an unreadable one must not soft-pass as absent).
//
// Wire it beside boot-time VerifyChain: a non-nil return is a SAFE-mode
// trigger, not a warning.
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
	events, err := ledger.ReadAll(lg.Path())
	if err != nil {
		return fmt.Errorf("witness tail check cannot read the ledger: %w", err)
	}
	for i := range events {
		if int64(events[i].Seq) == tail.LedgerOrdinal {
			if events[i].ContentHash != tail.LedgerHash {
				return fmt.Errorf("LEDGER FORK: event %d hashes to %s but the witness attested %s at %s — same ordinal, different history",
					tail.LedgerOrdinal, events[i].ContentHash, tail.LedgerHash, tail.WitnessedAt)
			}
			return nil
		}
	}
	// LastSeq claims the ordinal exists but no event carries it: a gap —
	// the same missing-history class as truncation, said precisely.
	return fmt.Errorf("LEDGER TRUNCATION: no event with seq %d exists though the ledger claims to reach %d — the witness attested %s at %s",
		tail.LedgerOrdinal, lg.LastSeq(), tail.LedgerHash, tail.WitnessedAt)
}
