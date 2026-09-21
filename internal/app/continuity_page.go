package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/cognitive"
	"github.com/aiii-dot-id/aii-os/internal/dashboard"
	"github.com/aiii-dot-id/aii-os/internal/escrow"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

// .
// .
// .
// .
// .
// .

// .
func (a *App) continuityHooks() *dashboard.ContinuityHooks {
	return &dashboard.ContinuityHooks{
		// .
		// .
		// .
		Background: a.runBackground,
		View:       a.continuityView,
		Take: func(ctx context.Context) (dashboard.ContinuityView, error) {
			ctx, done := a.untilStopped(ctx)
			defer done()
			return a.personTake(ctx)
		},
		Verify: func(ctx context.Context, name string) (string, error) {
			ctx, done := a.untilStopped(ctx)
			defer done()
			return a.personVerify(ctx, name)
		},
		EscrowCreate: a.escrowCreateHere,
		EscrowCheck:  a.escrowCheckHere,
		RestorePlan: func(source, name string) (dashboard.RestorePlan, error) {
			p, err := a.restorePlan(source, name)
			return dashboard.RestorePlan{Source: p.Source, Name: p.Name, At: p.At, Encrypted: p.Encrypted, RestoredTo: p.RestoredTo,
				LiveThrough: p.LiveThrough, SetAside: p.SetAside, Witnessed: p.Witnessed, WitnessedSetAside: p.WitnessedSetAside,
				WitnessHash: p.WitnessHash, WitnessURL: p.WitnessURL, Identity: p.Identity, ConfirmText: p.ConfirmText, AsideUnder: p.AsideUnder}, err
		},
		Restore: a.requestRestore,
	}
}

// .
// .
func (a *App) continuityView() dashboard.ContinuityView {
	cfg := a.configSnapshot()
	now := time.Now()
	v := dashboard.ContinuityView{
		Enabled:    maintenanceEnabled(cfg),
		BackupKeep: maintenanceKeep(cfg),
		Chain:      a.bootChain,
	}
	_, v.Safe = a.SafeMode()
	if v.Enabled && !v.Safe {
		v.NextPass = time.UnixMilli(cognitive.NextLocalDaily(now, maintenanceHourLocal, 0)).UTC().Format(time.RFC3339)
	}
	kept, _ := keptSnapshots(a.backupsDir(cfg))
	for i := len(kept) - 1; i >= 0; i-- {
		k := kept[i]
		v.Snapshots = append(v.Snapshots, dashboard.ContinuitySnapshot{
			Name: k.Name, At: k.at().UTC().Format(time.RFC3339), Record: k.Record, OnDemand: k.OnDemand, Encrypted: k.Encrypted,
		})
	}
	if a.store != nil {
		if st, ok, err := a.store.ContinuityStatus(); err == nil && ok {
			// .
			v.LastPass = &dashboard.ContinuityPass{At: st.At, Outcome: st.Outcome, Detail: st.Detail, Snapshot: st.Snapshot, FailingSince: st.FailingSince}
		}
	}
	key, _, reason, _ := a.snapshotEncryption(cfg)
	escrow.Wipe(key)
	v.Encrypting = reason == ""
	v.Unencrypted = reason
	v.UnencryptedText = unencryptedForPerson(reason)
	if raw, err := os.ReadFile(a.escrowReceiptPath(cfg)); err == nil {
		var r escrow.Receipt
		if json.Unmarshal(raw, &r) == nil {
			v.EscrowCheckedAt = r.CheckedAt
		}
	}
	v.EscrowCovers = reason == ""
	for _, set := range setAsideSets(cfg) {
		v.SetAside = append(v.SetAside, dashboard.ContinuitySetAside{Name: set.Name, Path: set.Path, Bytes: set.Bytes,
			SetAsideAt: set.Record.SetAsideAt, WasThrough: set.Record.WasThrough, RestoredFrom: set.Record.RestoredFrom, RestoredTo: set.Record.RestoredTo, PutBackAt: set.Record.PutBackAt})
	}
	if f := lastRestoreFailure(cfg); f != nil {
		v.RestoreFailed = "A restore from " + f.Request.Name + " was asked for and NOT performed: " + f.Why + ". Nothing was touched."
	}
	return v
}

// .
// .
func unencryptedForPerson(reason string) string {
	switch reason {
	case "":
		return ""
	case store.UnencryptedNoKey:
		return "This identity has no snapshot key yet. One is made at the next normal start."
	case store.UnencryptedKeyUnusable:
		return "The snapshot key on this machine does not read as a key. Snapshots stay unencrypted until it is replaced."
	case store.UnencryptedNoEscrow:
		return "No escrow of the keys has been checked. A snapshot encrypted to a key that exists only on this machine would be lost with the machine, so snapshots stay unencrypted until an escrow file has been made and checked below."
	case store.UnencryptedReceiptUnread:
		return "The record of the last escrow check does not read. Check your escrow file again below."
	case store.UnencryptedReceiptMismatch:
		return "The snapshot key on this machine is not the one your checked escrow holds. Make a new escrow file below and check it."
	case store.UnencryptedIdentityClosed:
		return "The identity's key is not open in this start, so the escrow check cannot be confirmed."
	}
	return "The reason was not recorded."
}

// .
func (a *App) personTake(ctx context.Context) (dashboard.ContinuityView, error) {
	if _, err := a.takeOnDemand(ctx, false, time.Now()); err != nil {
		return a.continuityView(), personError(err)
	}
	return a.continuityView(), nil
}

// .
func (a *App) personVerify(ctx context.Context, name string) (string, error) {
	out, err := a.verifyKept(ctx, name, false, time.Now())
	return out, personError(err)
}

// .
// .
func personError(err error) error {
	var r *ContinuityRefusal
	if !errors.As(err, &r) {
		return err
	}
	switch r.Reason {
	case "safe":
		return errors.New("This start is SAFE: it verifies and reports and makes no files. Restart normally first.")
	case "disabled":
		return errors.New("The maintenance pass is switched off. Switch it on above first.")
	case "busy":
		return errors.New("A maintenance pass is running right now. Try again when it is done.")
	case "none":
		return errors.New("There is no snapshot yet. Press Back up now to make the first.")
	case "unknown":
		return errors.New("That snapshot is not in the list any more. Reload the list.")
	}
	return err
}

// .
// .
// .
// .
// .
func (a *App) escrowCreateHere(pass []byte) (file []byte, name string, err error) {
	if _, inSafe := a.SafeMode(); inSafe {
		return nil, "", errors.New("This start is SAFE. An escrow is made from a normal start.")
	}
	if len(pass) < escrowMinPassphrase {
		return nil, "", fmt.Errorf("The passphrase is shorter than %d characters.", escrowMinPassphrase)
	}
	cfg := a.configSnapshot()
	var c escrow.Contents
	defer c.Wipe()
	if c.IdentityKey, err = os.ReadFile(cfg.Identity.KeyPath); err != nil {
		logsink.Error("escrow.error", "the identity key does not read: %v", err)
		return nil, "", errors.New("The identity's key could not be read on this machine.")
	}
	if c.SnapshotKey, err = os.ReadFile(a.snapshotKeyPath(cfg)); errors.Is(err, os.ErrNotExist) {
		return nil, "", errors.New("This identity has no snapshot key yet. One is made at the next normal start; make the escrow after it.")
	} else if err != nil {
		logsink.Error("escrow.error", "the snapshot key does not read: %v", err)
		return nil, "", errors.New("The snapshot key could not be read on this machine.")
	}
	sealed, err := escrow.Seal(c, pass)
	if err != nil {
		logsink.Error("escrow.error", "seal: %v", err)
		return nil, "", errors.New("The escrow could not be sealed.")
	}
	fp := a.door.kp.Fingerprint()
	logsink.Info("escrow.end", "an escrow file was made for the person to save; nothing was written here and no receipt is signed until it is checked")
	return sealed, fmt.Sprintf("escrow-%s-%s.age", fp[:12], time.Now().UTC().Format("20060102")), nil
}

// .
// .
// .
const escrowMinPassphrase = 12

// .
// .
// .
// .
// .
func (a *App) escrowCheckHere(sealed, pass []byte) (dashboard.ContinuityView, error) {
	fail := func(msg string) (dashboard.ContinuityView, error) { return a.continuityView(), errors.New(msg) }
	if _, inSafe := a.SafeMode(); inSafe {
		return fail("This start is SAFE. An escrow is checked from a normal start.")
	}
	if a.door == nil || a.door.kp == nil {
		return fail("The identity's key is not open in this start.")
	}
	if len(sealed) == 0 || len(sealed) > escrow.MaxSealedBytes {
		return fail("That file is not an escrow file: it is empty, or larger than any escrow.")
	}
	cfg := a.configSnapshot()
	c, kp, recipient, err := escrow.OpenFor(sealed, pass, a.door.kp.Fingerprint())
	var other *escrow.AnotherIdentity
	switch {
	case errors.As(err, &other):
		return fail("That escrow file holds another identity's keys, not this one's.")
	case err != nil:
		return fail("That file did not open: the passphrase is wrong, or it is not an escrow file.")
	}
	defer c.Wipe()
	hostRecipient, hostErr := "", error(nil)
	if raw, rerr := os.ReadFile(a.snapshotKeyPath(cfg)); rerr == nil {
		hostRecipient, hostErr = escrow.SnapshotRecipient(raw)
		escrow.Wipe(raw)
	} else if !errors.Is(rerr, os.ErrNotExist) {
		hostErr = rerr
	}
	var nc *escrow.NotCovered
	if err := escrow.Covers(recipient, hostRecipient, hostErr); errors.As(err, &nc) {
		switch nc.Why {
		case escrow.NoSnapshotKeyInEscrow:
			return fail("That escrow file opens and holds the identity's key, but no snapshot key — it was made before snapshots were encrypted. Make a new escrow file.")
		case escrow.NoSnapshotKeyOnHost:
			return fail("That escrow file opens, but this machine has no snapshot key for it to cover. One is made at the next normal start; make a new escrow file after it.")
		case escrow.HostSnapshotKeyUnreadable:
			return fail("That escrow file opens, but the snapshot key on this machine does not read.")
		default:
			return fail("That escrow file opens, but it holds an OLDER snapshot key than the one on this machine, so it could not open snapshots made here. Make a new escrow file and check that one.")
		}
	}
	if _, published, err := escrow.WriteReceipt(kp, recipient, cfg.Identity.KeyPath, time.Now()); err != nil && !published {
		logsink.Error("escrow.error", "checked, and the receipt could not be written: %v", err)
		return fail("The escrow file checked out, but the record of the check could not be written on this machine, so snapshots stay unencrypted.")
	}
	logsink.Info("escrow.end", "an escrow file was checked from the dashboard: it holds this identity and this host's snapshot key; the receipt is signed")
	return a.continuityView(), nil
}

// .
// .
func (a *App) untilStopped(ctx context.Context) (context.Context, func()) {
	ctx, cancel := context.WithCancel(ctx)
	if a.bgCtx == nil {
		return ctx, cancel
	}
	stop := context.AfterFunc(a.bgCtx, cancel)
	return ctx, func() { stop(); cancel() }
}
