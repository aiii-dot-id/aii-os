package ledger

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"

	"github.com/aiii-dot-id/aii-os/internal/atomicfile"
	"github.com/aiii-dot-id/aii-os/internal/crypto"
)

// Rewrap re-signs an entire ledger under the CURRENT gold envelope and
// returns the number of events rewrapped.
//
// James's ruling (2026-08-17): there are no v1/v2 systems and no era
// machinery — when the ledger format changes on the way to gold, test
// identities are re-wrapped, not migrated (Dawn's C-side ledger has
// been re-wrapped this way multiple times). Rewrap re-signs HISTORY, it
// never repairs it: the payloads must reproduce their content hashes
// and the prev_hash chain must link, or it refuses. Every event must be
// this key's own record (sig_key_id and author match its fingerprint) —
// a ledger signed by another key is someone else's history.
//
// The rewrapped chain is written to a temp file, fully verified and
// replayed, and only then atomically renamed to outPath (or over ledgerPath
// when outPath is empty). A failure before replacement leaves the destination
// untouched; replacement and durability failures are returned.
func Rewrap(ledgerPath string, kp *crypto.KeyPair, outPath string, replay func([]Event) error) (count int, retErr error) {
	if replay == nil {
		return 0, errors.New("replay validator is required")
	}
	source, err := openLedgerForRewrap(ledgerPath)
	if err != nil {
		return 0, fmt.Errorf("open ledger: %w", err)
	}
	defer func() { retErr = errors.Join(retErr, source.Close()) }()
	if err := lockLedgerFile(source); err != nil {
		return 0, fmt.Errorf("lock ledger %q: %w", ledgerPath, err)
	}
	if _, err := source.Seek(0, 0); err != nil {
		return 0, fmt.Errorf("seek ledger: %w", err)
	}
	events, err := readEvents(source)
	if err != nil {
		return 0, fmt.Errorf("read ledger: %w", err)
	}
	if len(events) == 0 {
		return 0, fmt.Errorf("ledger is empty — nothing to rewrap")
	}

	// Pre-flight: the chain's non-signature structure must already hold.
	prev := ""
	for i := range events {
		evt := &events[i]
		if evt.Seq != uint64(i+1) {
			return 0, fmt.Errorf("event %d: seq %d out of place — rewrap refuses corrupted chains", i, evt.Seq)
		}
		if evt.PrevHash != prev {
			return 0, fmt.Errorf("event %d: prev_hash linkage broken — rewrap refuses corrupted chains", i)
		}
		if crypto.ContentHash(evt.Payload) != evt.ContentHash {
			return 0, fmt.Errorf("event %d: payload does not reproduce content_hash — rewrap re-signs history, it never repairs it", i)
		}
		if !slices.Contains(CanonicalRings(evt.Type), evt.Ring) {
			return 0, fmt.Errorf("event %d: ring %d is not canonical for %q — rewrap refuses invalid history", i, evt.Ring, evt.Type)
		}
		if evt.SigKeyID != "" && evt.SigKeyID != kp.Fingerprint() {
			return 0, fmt.Errorf("event %d: signed by %s, not this key (%s) — someone else's history; regenerate instead", i, evt.SigKeyID, kp.Fingerprint())
		}
		if evt.Author != "" && evt.Author != kp.Fingerprint() {
			return 0, fmt.Errorf("event %d: author %s is not this key (%s) — rewrap does not rewrite authorship", i, evt.Author, kp.Fingerprint())
		}
		prev = evt.ContentHash
	}

	dst := outPath
	if dst == "" {
		dst = ledgerPath
	}
	f, err := os.CreateTemp(filepath.Dir(dst), "."+filepath.Base(dst)+".rewrap-*")
	if err != nil {
		return 0, fmt.Errorf("open temp: %w", err)
	}
	tmp := f.Name()
	closed := false
	defer func() {
		if !closed {
			retErr = errors.Join(retErr, f.Close())
		}
		if err := os.Remove(tmp); err != nil && !os.IsNotExist(err) {
			retErr = errors.Join(retErr, fmt.Errorf("remove temporary ledger: %w", err))
		}
	}()

	for i := range events {
		evt := &events[i]
		evt.SigAlg = crypto.SigAlg
		evt.SigKeyID = kp.Fingerprint()
		evt.Author = kp.Fingerprint()
		entrySHA, err := EntrySHA256(evt)
		if err != nil {
			return 0, fmt.Errorf("event %d: %w", i, err)
		}
		sig, err := crypto.SignB64(kp, SignatureInputGold(evt.SigAlg, evt.SigKeyID, entrySHA))
		if err != nil {
			return 0, fmt.Errorf("event %d: sign: %w", i, err)
		}
		evt.Signature = sig
		line, err := json.Marshal(evt)
		if err != nil {
			return 0, fmt.Errorf("event %d: marshal: %w", i, err)
		}
		if _, err := f.Write(append(line, '\n')); err != nil {
			return 0, fmt.Errorf("write: %w", err)
		}
	}
	if err := f.Sync(); err != nil {
		return 0, fmt.Errorf("sync: %w", err)
	}
	if err := f.Close(); err != nil {
		closed = true
		return 0, fmt.Errorf("close: %w", err)
	}
	closed = true

	// The result must verify COMPLETELY before it replaces anything.
	if err := VerifyChain(tmp, map[string][]byte{kp.Fingerprint(): kp.PublicKeyBytes()}); err != nil {
		return 0, fmt.Errorf("rewrapped chain does not verify (nothing replaced): %w", err)
	}
	candidate, err := ReadAll(tmp)
	if err != nil {
		return 0, fmt.Errorf("read verified rewrapped chain (nothing replaced): %w", err)
	}
	if err := replay(candidate); err != nil {
		return 0, fmt.Errorf("rewrapped chain does not replay (nothing replaced): %w", err)
	}

	// Lock the exact destination immediately before publication. If the
	// path does not exist, reserve it with an exclusively-created locked
	// placeholder; this closes the stat-then-rename race in which a live
	// ledger could appear at outPath and be replaced without admission.
	target, created, err := lockRewrapOutput(dst, source)
	if err != nil {
		return 0, err
	}
	if target != nil {
		defer func() {
			targetInfo, statErr := target.Stat()
			retErr = errors.Join(retErr, target.Close())
			if statErr != nil {
				retErr = errors.Join(retErr, fmt.Errorf("stat output reservation: %w", statErr))
			} else if created {
				if pathInfo, err := os.Stat(dst); err == nil && os.SameFile(targetInfo, pathInfo) {
					if err := os.Remove(dst); err != nil && !os.IsNotExist(err) {
						retErr = errors.Join(retErr, fmt.Errorf("remove output reservation: %w", err))
					}
				}
			}
		}()
	}
	published, err := atomicfile.Replace(tmp, dst)
	if err != nil {
		if published {
			created = false
			return len(events), fmt.Errorf("rewrapped ledger was published but directory durability is unconfirmed: %w", err)
		}
		return 0, fmt.Errorf("replace: %w", err)
	}
	created = false
	return len(events), nil
}

func lockRewrapOutput(path string, source *os.File) (*os.File, bool, error) {
	sourceInfo, err := source.Stat()
	if err != nil {
		return nil, false, fmt.Errorf("stat source ledger: %w", err)
	}
	for {
		target, err := openLedgerForRewrap(path)
		created := false
		if os.IsNotExist(err) {
			target, err = createLedgerForRewrap(path)
			if os.IsExist(err) {
				continue
			}
			created = err == nil
		}
		if err != nil {
			return nil, false, fmt.Errorf("open output ledger: %w", err)
		}
		targetInfo, err := target.Stat()
		if err != nil {
			return nil, false, errors.Join(fmt.Errorf("stat output ledger: %w", err), target.Close())
		}
		if os.SameFile(sourceInfo, targetInfo) {
			if err := target.Close(); err != nil {
				return nil, false, fmt.Errorf("close duplicate source handle: %w", err)
			}
			return nil, false, nil
		}
		if err := lockLedgerFile(target); err != nil {
			lockErr := fmt.Errorf("lock output ledger %q: %w", path, err)
			lockErr = errors.Join(lockErr, target.Close())
			if created {
				if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
					lockErr = errors.Join(lockErr, fmt.Errorf("remove output reservation: %w", err))
				}
			}
			return nil, false, lockErr
		}
		return target, created, nil
	}
}
