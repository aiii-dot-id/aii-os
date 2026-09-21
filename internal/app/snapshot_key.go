package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
	"os"

	"github.com/aiii-dot-id/aii-os/internal/escrow"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

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
// .
// .

func (a *App) snapshotKeyPath(cfg Config) string {
	return escrow.SnapshotKeyPath(cfg.Identity.KeyPath)
}

func (a *App) escrowReceiptPath(cfg Config) string {
	return escrow.ReceiptPath(cfg.Identity.KeyPath)
}

// .
// .
// .
func (a *App) ensureSnapshotKey(cfg Config) {
	if _, inSafe := a.SafeMode(); inSafe {
		return
	}
	path := a.snapshotKeyPath(cfg)
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

// .
// .
// .
// .
// .
// .
func (a *App) snapshotEncryption(cfg Config) (key []byte, recipient, reason, whyNot string) {
	key, err := os.ReadFile(a.snapshotKeyPath(cfg))
	if err != nil {
		return nil, "", store.UnencryptedNoKey, fmt.Sprintf("this identity has no snapshot key yet (%v); one is made at the next normal boot", err)
	}
	recipient, err = escrow.SnapshotRecipient(key)
	if err != nil {
		escrow.Wipe(key)
		return nil, "", store.UnencryptedKeyUnusable, fmt.Sprintf("the snapshot key on this host is not usable: %v", err)
	}
	raw, err := os.ReadFile(a.escrowReceiptPath(cfg))
	if err != nil {
		escrow.Wipe(key)
		return nil, "", store.UnencryptedNoEscrow, "no escrow of the snapshot key has been checked on this host. Until one has, a snapshot encrypted to it could be lost with this machine. Open Settings → Backups & Keys in the dashboard: make an escrow file, save it somewhere safe, and check it there"
	}
	var r escrow.Receipt
	if err := json.Unmarshal(raw, &r); err != nil {
		escrow.Wipe(key)
		return nil, "", store.UnencryptedReceiptUnread, fmt.Sprintf("the escrow receipt does not read: %v — check your escrow file again in Settings → Backups & Keys", err)
	}
	if a.door == nil || a.door.kp == nil {
		escrow.Wipe(key)
		return nil, "", store.UnencryptedIdentityClosed, "the identity key is not open, so the escrow receipt cannot be verified"
	}
	if err := r.Verify(a.door.kp.PublicKeyBytes(), recipient); err != nil {
		escrow.Wipe(key)
		return nil, "", store.UnencryptedReceiptMismatch, fmt.Sprintf("%v — the snapshot key on this host is not the one whose escrow was checked. Make and check a new escrow file in Settings → Backups & Keys", err)
	}
	return key, recipient, "", ""
}
