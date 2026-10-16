package continuity

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/crypto"
	"github.com/aiii-dot-id/aii-os/internal/escrow"
	"github.com/aiii-dot-id/aii-os/internal/genesis"
	"github.com/aiii-dot-id/aii-os/internal/ledger"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
)

func (r *Runtime) HoldsRestoreSource(cfg Settings) bool {
	if kept, _ := keptSnapshots(r.BackupsDir(cfg)); len(kept) > 0 {
		return true
	}
	return len(setAsideSets(cfg)) > 0
}

func (r *Runtime) keyAbsent(cfg Settings) bool {
	if r.w.Key() != nil {
		return false
	}
	if _, _, err := genesis.NamedKey(cfg.Identity.LedgerPath); err == nil {
		return true
	}
	return r.recordAbsent(cfg)
}

func (r *Runtime) keyUnnamed(cfg Settings) bool {
	if r.w.Key() != nil {
		return false
	}
	artifact, err := ledger.RecordArtifact(cfg.Identity.LedgerPath)
	if err != nil || artifact == "" {
		return false
	}

	_, _, nerr := genesis.NamedKey(cfg.Identity.LedgerPath)
	return nerr != nil && !genesis.RecordUnread(nerr)
}

func (r *Runtime) recordAbsent(cfg Settings) bool {
	artifact, err := ledger.RecordArtifact(cfg.Identity.LedgerPath)
	return err == nil && artifact == "" && r.HoldsRestoreSource(cfg)
}

func (r *Runtime) putBackKey(sealed, pass []byte) error {
	cfg := r.w.Settings()
	if r.w.Key() != nil {
		return errors.New("This identity's key is on this machine and open. Nothing was written.")
	}
	if len(sealed) == 0 || len(sealed) > escrow.MaxSealedBytes {
		return errors.New("That file is not an escrow file: it is empty, or larger than any escrow.")
	}
	_, want, nerr := genesis.NamedKey(cfg.Identity.LedgerPath)
	if nerr != nil && !r.recordAbsent(cfg) {
		if genesis.RecordUnread(nerr) {
			logsink.Error("escrow.error", "the record does not read, so it cannot say whose key it needs: %v", nerr)
		} else {
			logsink.Error("escrow.error", "the record does not name its key: %v", nerr)
		}
		return errors.New("The record on this machine does not read far enough to say whose key it needs, so no key was put in place.")
	}
	c, kp, err := r.openEscrow(sealed, pass)
	if err != nil {
		return errors.New("That file did not open: the passphrase is wrong, or it is not an escrow file.")
	}
	defer c.Wipe()
	if nerr == nil {
		if kp.Fingerprint() != want {
			return errors.New("That escrow file holds another identity's keys, not the key this record was signed with. Nothing was written.")
		}
		return r.publishKeyBack(cfg, c, kp, want)
	}

	if !r.aSnapshotNames(cfg, kp.Fingerprint(), c.SnapshotKey) {
		return errors.New("No snapshot or set kept on this machine names the key in that escrow file: it holds another identity's keys. Nothing was written.")
	}
	return r.publishKeyBack(cfg, c, kp, kp.Fingerprint())
}

func (r *Runtime) openEscrow(sealed, pass []byte) (escrow.Contents, *crypto.KeyPair, error) {
	c, kp, err := escrow.Open(sealed, pass)
	if err != nil {
		return c, kp, err
	}
	if r.KeyBackHolds != nil {
		r.KeyBackHolds(c.IdentityKey)
		r.KeyBackHolds(c.SnapshotKey)
	}
	return c, kp, nil
}

func (r *Runtime) publishKeyBack(cfg Settings, c escrow.Contents, kp *crypto.KeyPair, want string) error {

	keyPath := cfg.Identity.KeyPath
	if err := identityKeyIsOursOrAbsent(keyPath, want); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(keyPath), 0o700); err != nil {
		return errors.New("The directory the identity's key goes in could not be made on this machine.")
	}
	if _, err := os.Lstat(keyPath); errors.Is(err, os.ErrNotExist) {
		if _, err := crypto.PublishKeyFile(c.IdentityKey, keyPath); err != nil {
			logsink.Error("escrow.error", "the identity key could not be published durably: %v", err)
			return errors.New("The identity's key could not be written durably on this machine. Try again; a key already in place is checked, never written over.")
		}
		r.afterKeyPublished(keyPath)
	}

	here, err := os.ReadFile(keyPath)
	if r.KeyBackHolds != nil {
		r.KeyBackHolds(here)
	}
	same := err == nil && bytes.Equal(here, c.IdentityKey)
	escrow.Wipe(here)
	if !same {
		logsink.Error("escrow.error", "the identity key does not read back as this identity's: %v", err)
		return errors.New("The identity's key was written and does not read back. Check this machine's disk and try again.")
	}

	if snapPath := r.snapshotKeyPath(cfg); c.SnapshotKey != nil {
		if _, err := os.Lstat(snapPath); errors.Is(err, os.ErrNotExist) {
			if _, err := escrow.PublishSecret(c.SnapshotKey, snapPath); err != nil {
				logsink.Warn("escrow.error", "the identity key is in place; the snapshot key could not be published: %v", err)
			}
		} else {
			logsink.Info("escrow.decision", "a snapshot key stands here and is left as it is; if it does not open the snapshots kept here, the page says so and offers the escrow's in its place")
		}
	}
	logsink.Warn("restore.decision", "the key of identity %s was put back from an escrow file, held to what the record or a kept snapshot names; the next start verifies the record under it, or offers the restore where there is no record", kp.Fingerprint())
	return nil
}

func (r *Runtime) snapshotKeyMismatch(cfg Settings) (snapshot string, absent bool) {
	dir := r.BackupsDir(cfg)
	kept, _ := keptSnapshots(dir)
	for i := len(kept) - 1; i >= 0; i-- {
		if kept[i].Encrypted {
			snapshot = kept[i].Name
			break
		}
	}
	if snapshot == "" {
		return "", false
	}
	key, err := os.ReadFile(r.snapshotKeyPath(cfg))
	if errors.Is(err, os.ErrNotExist) {
		return snapshot, true
	}
	defer escrow.Wipe(key)
	if err == nil && escrow.SnapshotKeyOpens(filepath.Join(dir, snapshot), key) == nil {
		return "", false
	}
	return snapshot, false
}

func (r *Runtime) putBackSnapshotKey(sealed, pass []byte) (string, error) {
	cfg := r.w.Settings()
	if !r.w.Safe() {
		return "", errors.New("The snapshot key is put in place from a SAFE start only: a running identity's passes encrypt to the one here. Nothing was written.")
	}
	held := r.w.Key()
	if held == nil {
		return "", errors.New("Put this identity's key back first; its snapshot key goes back with it. Nothing was written.")
	}
	snapshot, absent := r.snapshotKeyMismatch(cfg)
	if snapshot == "" {
		return "", errors.New("The snapshot key on this machine opens the snapshots kept here. Nothing was written.")
	}
	if len(sealed) == 0 || len(sealed) > escrow.MaxSealedBytes {
		return "", errors.New("That file is not an escrow file: it is empty, or larger than any escrow.")
	}
	c, kp, err := r.openEscrow(sealed, pass)
	if err != nil {
		return "", errors.New("That file did not open: the passphrase is wrong, or it is not an escrow file.")
	}
	defer c.Wipe()
	switch {
	case kp.Fingerprint() != held.Fingerprint():
		return "", errors.New("That escrow file holds another identity's keys. Nothing was written.")
	case c.SnapshotKey == nil:
		return "", errors.New("That escrow file holds no snapshot key. Nothing was written.")
	}
	if err := escrow.SnapshotKeyOpens(filepath.Join(r.BackupsDir(cfg), snapshot), c.SnapshotKey); err != nil {
		return "", errors.New("The snapshot key in that escrow file does not open the snapshots kept here either. Nothing was written.")
	}
	snapPath := r.snapshotKeyPath(cfg)
	aside := ""
	if !absent {
		aside = snapPath + ".set-aside-" + time.Now().UTC().Format("20060102T150405Z")
		if err := os.Rename(snapPath, aside); err != nil {
			logsink.Error("escrow.error", "the snapshot key that does not open the snapshots could not be moved aside: %v", err)
			return "", errors.New("The snapshot key on this machine could not be moved aside, so nothing was written.")
		}
		logsink.Warn("escrow.decision", "the snapshot key that does not open the snapshots kept here is moved aside to %s, not deleted", aside)
	}
	if _, err := escrow.PublishSecret(c.SnapshotKey, snapPath); err != nil {
		logsink.Error("escrow.error", "the escrow's snapshot key could not be published: %v", err)
		return "", errors.New("The escrow's snapshot key could not be written durably on this machine. Try again.")
	}
	here, err := os.ReadFile(snapPath)
	if r.KeyBackHolds != nil {
		r.KeyBackHolds(here)
	}
	same := err == nil && bytes.Equal(here, c.SnapshotKey)
	escrow.Wipe(here)
	if !same {
		logsink.Error("escrow.error", "the snapshot key does not read back as the escrow's: %v", err)
		return "", errors.New("The snapshot key was written and does not read back. Check this machine's disk and try again.")
	}
	if recipient, rerr := escrow.SnapshotRecipient(c.SnapshotKey); rerr == nil {
		if _, published, werr := escrow.WriteReceipt(kp, recipient, r.w.Active(cfg.Identity).KeyPath, time.Now()); werr != nil && !published {
			logsink.Error("escrow.error", "the snapshot key is in place and the escrow receipt could NOT be written (%v): snapshots made here are not encrypted until the escrow file is checked in Settings → Backups & Keys", werr)
		}
	}
	logsink.Warn("restore.decision", "the escrow's snapshot key of identity %s is in place: it opens %s", kp.Fingerprint(), snapshot)
	said := "The snapshot key from your escrow file is in place and opens the snapshots kept here. A restore can be asked for now."
	if aside != "" {
		said = "The snapshot key from your escrow file is in place and opens the snapshots kept here; the one that stood here is kept beside it, as " + filepath.Base(aside) + ". A restore can be asked for now."
	}
	return said, nil
}

func (r *Runtime) aSnapshotNames(cfg Settings, want string, snapshotKey []byte) bool {
	dir := r.BackupsDir(cfg)
	kept, _ := keptSnapshots(dir)
	for i := len(kept) - 1; i >= 0; i-- {
		k := kept[i]
		set := filepath.Join(dir, k.Name)
		if k.Encrypted {
			if len(snapshotKey) != 0 && encryptedSnapshotNames(cfg, filepath.Join(dir, k.Name), snapshotKey, want) {
				return true
			}
			continue
		}
		if setNames(set, want) {
			return true
		}
	}
	for _, s := range setAsideSets(cfg) {
		if setNames(s.Path, want) {
			return true
		}
	}
	return false
}

func encryptedSnapshotNames(cfg Settings, src string, snapshotKey []byte, want string) bool {
	root := keyCheckScratch(cfg)
	if err := os.MkdirAll(root, 0o700); err != nil {
		return false
	}
	if err := os.Chmod(root, 0o700); err != nil {
		return false
	}
	work, err := os.MkdirTemp(root, "open-*")
	if err != nil {
		return false
	}
	defer os.RemoveAll(work)
	set := filepath.Join(work, "set")
	if _, err := escrow.OpenSnapshot(src, snapshotKey, set); err != nil {
		return false
	}
	return setNames(set, want)
}

func keyCheckScratch(cfg Settings) string {
	return filepath.Join(restoreDir(cfg), keyCheckScratchName)
}

const keyCheckScratchName = ".key-check"

func (r *Runtime) SweepKeyCheckScratch(cfg Settings) {
	root := keyCheckScratch(cfg)
	entries, err := os.ReadDir(root)
	if errors.Is(err, os.ErrNotExist) {
		return
	}
	if err == nil && len(entries) == 0 {
		return
	}
	if rmErr := os.RemoveAll(root); rmErr != nil {
		logsink.Error("escrow.error", "a key check's decrypted scratch could not be removed (%v) — remove %s by hand", rmErr, root)
		return
	}
	logsink.Warn("escrow.decision", "a key check that never returned left a decrypted snapshot in %s; it is removed", root)
}

func setNames(set, want string) bool {
	if raw, err := os.ReadFile(filepath.Join(set, snapshotReceiptName)); err == nil {
		var rec snapshotReceipt
		if json.Unmarshal(raw, &rec) == nil && rec.Identity != "" {
			return rec.Identity == want
		}
	}
	_, named, err := genesis.NamedKey(filepath.Join(set, "ledger.jsonl"))
	return err == nil && named == want
}
