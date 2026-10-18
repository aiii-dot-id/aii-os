package continuity

import (
	"encoding/json"
	"errors"
	"fmt"
	"github.com/aiii-dot-id/aii-os/internal/atomicfile"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
	"os"

	"github.com/aiii-dot-id/aii-os/internal/escrow"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

func (r *Runtime) snapshotKeyPath(cfg Settings) string {
	return escrow.SnapshotKeyPath(r.w.Active(cfg.Identity).KeyPath)
}

func (r *Runtime) escrowReceiptPath(cfg Settings) string {
	return escrow.ReceiptPath(r.w.Active(cfg.Identity).KeyPath)
}

func (r *Runtime) EnsureSnapshotKey(cfg Settings) {
	if r.w.Safe() {
		return
	}
	path := r.snapshotKeyPath(cfg)
	if _, err := os.Stat(path); err == nil {
		return
	} else if !errors.Is(err, os.ErrNotExist) {
		logsink.Warn("snapshot.refusal", "the key could not be read (%v) — snapshots stay unencrypted", err)
		return
	}
	key, err := escrow.NewSnapshotKey()
	if err != nil {
		logsink.Warn("snapshot.refusal", "the key could not be made (%v) — snapshots stay unencrypted", err)
		return
	}
	defer escrow.Wipe(key)
	if published, err := escrow.PublishSecret(key, path); err != nil && !published {
		logsink.Warn("snapshot.refusal", "the key could not be written (%v) — snapshots stay unencrypted", err)
		return
	}
	logsink.Info("snapshot.end", "key made %s. Snapshots are encrypted to it once an escrow holding it has been checked: Settings → Backups & Keys in the dashboard.", path)
}

func (r *Runtime) snapshotEncryption(cfg Settings) (key []byte, recipient, reason, whyNot string) {
	key, err := os.ReadFile(r.snapshotKeyPath(cfg))
	if err != nil {
		return nil, "", store.UnencryptedNoKey, fmt.Sprintf("this identity has no snapshot key yet (%v); one is made at the next normal boot", err)
	}
	recipient, err = escrow.SnapshotRecipient(key)
	if err != nil {
		escrow.Wipe(key)
		return nil, "", store.UnencryptedKeyUnusable, fmt.Sprintf("the snapshot key on this host is not usable: %v", err)
	}
	raw, err := atomicfile.ReadFile(r.escrowReceiptPath(cfg))
	if err != nil {
		escrow.Wipe(key)
		if errors.Is(err, os.ErrNotExist) {
			return nil, "", store.UnencryptedNoEscrow, "no checked recovery copy of the snapshot key is recorded"
		}
		return nil, "", store.UnencryptedReceiptUnread, fmt.Sprintf("the escrow receipt could not be read: %v — check Settings → Backups & Keys", err)
	}
	var receipt escrow.Receipt
	if err := json.Unmarshal(raw, &receipt); err != nil {
		escrow.Wipe(key)
		return nil, "", store.UnencryptedReceiptUnread, fmt.Sprintf("the escrow receipt does not read: %v — check your escrow file again in Settings → Backups & Keys", err)
	}
	if r.w.DoorKey() == nil {
		escrow.Wipe(key)
		return nil, "", store.UnencryptedIdentityClosed, "the identity key is not open, so the escrow receipt cannot be verified"
	}
	if err := receipt.Verify(r.w.DoorKey().PublicKeyBytes(), recipient); err != nil {
		escrow.Wipe(key)
		return nil, "", store.UnencryptedReceiptMismatch, fmt.Sprintf("%v — the snapshot key on this host is not the one whose escrow was checked. Make and check a new escrow file in Settings → Backups & Keys", err)
	}
	return key, recipient, "", ""
}
