package continuity

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/crypto"
	"github.com/aiii-dot-id/aii-os/internal/escrow"
	"github.com/aiii-dot-id/aii-os/internal/ledger"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
)

func (r *Runtime) SignKeptSnapshotsAtStart(cfg Settings) {
	if !r.signingOwed(cfg) {
		return
	}
	r.w.Go(func() {
		ctx, done := r.w.UntilStopped(context.Background())
		defer done()
		r.signKeptAtStart(ctx, cfg)
	})
}

func (r *Runtime) signingOwed(cfg Settings) bool {
	if r.w.Safe() || !MaintenanceEnabled(cfg.Maintenance) || r.w.Store() == nil {
		return false
	}
	if st, ok, err := r.w.Store().ContinuityStatus(); err == nil && ok && st.Signed {
		return false
	}
	kept, _ := keptSnapshots(r.BackupsDir(cfg))
	return len(kept) > 0
}

func (r *Runtime) signKeptAtStart(ctx context.Context, cfg Settings) (signed []string, notSigned map[string]string) {
	signed, already, notSigned := r.signKept(ctx, cfg)
	var why []string
	for name, reason := range notSigned {
		why = append(why, name+" ("+reason+")")
	}
	sort.Strings(why)
	said, whyNot := strings.Join(signed, ", "), strings.Join(why, "; ")
	if said == "" {
		said = "none"
	}
	if whyNot == "" {
		whyNot = "none"
	}
	logsink.Info("snapshot.end", "snapshots kept from before snapshots were signed — signed now, as this identity's record proves them: %s; not signed: %s", said, whyNot)
	st, ok, err := r.w.Store().ContinuityStatus()
	switch {
	case err == nil && ok && st.Snapshot != "" && (contains(signed, st.Snapshot) || contains(already, st.Snapshot)):
		st.Signed = true
		if err := r.w.Store().SetContinuityStatus(st); err != nil {
			logsink.Warn("maintenance.error", "the last pass's statement could not be marked signed (%v); the next start looks again", err)
		}
		return signed, notSigned
	case err == nil && !ok && len(signed)+len(already) > 0:

		return signed, notSigned
	}
	if made, err := r.takeOnDemand(ctx, false, time.Now()); err != nil {
		logsink.Warn("snapshot.refusal", "the last pass's snapshot is not signed, and the signed snapshot this start takes was not taken (%v); the next pass makes one", err)
	} else {
		logsink.Info("snapshot.end", "%s taken and signed at this start, through record %d: the last pass's snapshot could not be signed", made.Name, made.Record)
	}
	return signed, notSigned
}

func contains(names []string, name string) bool {
	for _, n := range names {
		if n == name {
			return true
		}
	}
	return false
}

func (r *Runtime) signKept(ctx context.Context, cfg Settings) (signed, already []string, notSigned map[string]string) {
	notSigned = map[string]string{}
	r.maint.mu.Lock()
	defer r.maint.mu.Unlock()
	kp := r.w.DoorKey()
	dir := r.BackupsDir(cfg)
	kept, _ := keptSnapshots(dir)
	if kp == nil {
		for _, k := range kept {
			notSigned[k.Name] = "the identity key is not open"
		}
		return nil, nil, notSigned
	}
	var seqs []uint64
	for _, k := range kept {
		seqs = append(seqs, k.Record)
	}
	heads, err := recordHashesAt(r.w.Active(cfg.Identity).LedgerPath, seqs)
	if err != nil {
		for _, k := range kept {
			notSigned[k.Name] = "this identity's record does not read: " + err.Error()
		}
		return nil, nil, notSigned
	}
	for _, k := range kept {
		switch err := r.signOne(ctx, cfg, dir, k, kp, heads); {
		case errors.Is(err, errSignedAlready):
			already = append(already, k.Name)
		case err != nil:
			notSigned[k.Name] = err.Error()
		default:
			signed = append(signed, k.Name)
		}
	}
	return signed, already, notSigned
}

var errSignedAlready = errors.New("signed already")

func (r *Runtime) signOne(ctx context.Context, cfg Settings, dir string, k keptSnapshot, kp *crypto.KeyPair, heads map[uint64]string) error {
	work, err := os.MkdirTemp(dir, SnapshotWorkPrefix+"sign-*.tmp")
	if err != nil {
		return fmt.Errorf("working set: %w", err)
	}
	defer func() {
		if rmErr := os.RemoveAll(work); rmErr != nil {
			logsink.Error("maintenance.error", "a signing's plaintext working set could not be removed: %v — it stands in %s until the next normal boot sweeps it", rmErr, dir)
		}
	}()
	set := filepath.Join(dir, k.Name)
	var present []string
	var key []byte
	if k.Encrypted {
		if key, err = os.ReadFile(r.snapshotKeyPath(cfg)); err != nil {
			return fmt.Errorf("it is encrypted and the snapshot key does not read: %w", err)
		}
		defer escrow.Wipe(key)
		set = filepath.Join(work, "set")
		if present, err = escrow.OpenSnapshot(filepath.Join(dir, k.Name), key, set); err != nil {
			return fmt.Errorf("it does not open: %w", err)
		}
	} else if present, err = filesUnder(set); err != nil {
		return err
	}
	if sums, err := os.ReadFile(filepath.Join(set, SnapshotSums)); err == nil {
		if _, named := escrow.SignedPart(sums); named {
			return errSignedAlready
		}
	}

	if i := slices.Index(present, escrow.SumsSigName); i >= 0 {
		if err := os.Remove(filepath.Join(set, escrow.SumsSigName)); err != nil {
			return err
		}
		present = slices.Delete(present, i, i+1)
	}
	if err := escrow.CheckSums(set, present); err != nil {
		return fmt.Errorf("its files do not match its checksum list: %w", err)
	}
	raw, err := os.ReadFile(filepath.Join(set, snapshotReceiptName))
	if err != nil {
		return fmt.Errorf("it holds no receipt: %w", err)
	}
	var want snapshotReceipt
	if err := json.Unmarshal(raw, &want); err != nil {
		return fmt.Errorf("its receipt does not read: %w", err)
	}
	switch {
	case want.Identity != kp.Fingerprint():
		return fmt.Errorf("its receipt is identity %s's", want.Identity)
	case want.LastSeq != k.Record:
		return fmt.Errorf("it is named for record %d and its receipt says %d", k.Record, want.LastSeq)
	case heads[want.LastSeq] == "":
		return fmt.Errorf("this identity's record holds no record %d", want.LastSeq)
	case heads[want.LastSeq] != want.LastHash:
		return fmt.Errorf("its receipt names record %d as %.12s, and this identity's record holds %.12s there", want.LastSeq, want.LastHash, heads[want.LastSeq])
	}
	if err := r.proveSnapshotIn(ctx, cfg, set, want, filepath.Join(work, "proof")); err != nil {
		return err
	}
	if err := signSums(set, kp, want); err != nil {
		return fmt.Errorf("sign: %w", err)
	}
	if !k.Encrypted {
		return r.syncDir(set)
	}

	members := append(append([]string{}, present...), escrow.SumsSigName)
	sums := map[string]string{}
	for _, n := range members {
		h, err := fileSHA256(filepath.Join(set, filepath.FromSlash(n)))
		if err != nil {
			return err
		}
		sums[n] = h
	}
	recipient, err := escrow.SnapshotRecipient(key)
	if err != nil {
		return err
	}
	sealed := filepath.Join(work, "sealed"+escrow.SnapshotSuffix)
	if err := escrow.EncryptSnapshot(set, members, recipient, sealed); err != nil {
		return err
	}
	if err := escrow.VerifySnapshot(sealed, key, members, sums); err != nil {
		return fmt.Errorf("the encrypted snapshot does not verify — the kept one is untouched: %w", err)
	}
	if err := os.Rename(sealed, filepath.Join(dir, k.Name)); err != nil {
		return err
	}
	return r.syncDir(dir)
}

func recordHashesAt(ledgerPath string, seqs []uint64) (map[uint64]string, error) {
	want := map[uint64]bool{}
	var top uint64
	for _, s := range seqs {
		want[s] = true
		if s > top {
			top = s
		}
	}
	out := map[uint64]string{}
	err := ledger.Stream(ledgerPath, func(evt *ledger.Event) error {
		if want[evt.Seq] {
			out[evt.Seq] = evt.EntryHash()
		}
		if evt.Seq >= top {
			return ledger.ErrStop
		}
		return nil
	})
	return out, err
}

func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
