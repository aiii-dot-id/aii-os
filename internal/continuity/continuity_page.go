package continuity

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/aiii-dot-id/aii-os/internal/atomicfile"
	"os"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/cognitive"
	"github.com/aiii-dot-id/aii-os/internal/crypto"
	"github.com/aiii-dot-id/aii-os/internal/dashboard"
	"github.com/aiii-dot-id/aii-os/internal/escrow"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

func (r *Runtime) WireHooks(h *dashboard.WSHandler) {
	h.Continuity = r.continuityHooks()
}

func (r *Runtime) continuityHooks() *dashboard.ContinuityHooks {
	return &dashboard.ContinuityHooks{

		Background: r.w.Go,
		View:       r.continuityView,
		Take: func(ctx context.Context) (dashboard.ContinuityView, error) {
			ctx, done := r.w.UntilStopped(ctx)
			defer done()
			return r.personTake(ctx)
		},
		Verify: func(ctx context.Context, name string) (string, error) {
			ctx, done := r.w.UntilStopped(ctx)
			defer done()
			return r.personVerify(ctx, name)
		},
		EscrowCreate: r.escrowCreateHere,
		EscrowCheck:  r.escrowCheckHere,
		RestorePlan: func(source, name string) (dashboard.RestorePlan, error) {
			p, err := r.restorePlan(source, name)
			return dashboard.RestorePlan{Source: p.Source, Name: p.Name, At: p.At, Encrypted: p.Encrypted, RestoredTo: p.RestoredTo,
				LiveThrough: p.LiveThrough, NoLiveRecord: p.NoLiveRecord, SetAside: p.SetAside, Witnessed: p.Witnessed, WitnessedSetAside: p.WitnessedSetAside,
				WitnessHash: p.WitnessHash, WitnessURL: p.WitnessURL, Identity: p.Identity, ConfirmText: p.ConfirmText, AsideUnder: p.AsideUnder}, err
		},
		Restore:         r.requestRestore,
		KeyBack:         r.putBackKey,
		SnapshotKeyBack: r.putBackSnapshotKey,
	}
}

func (r *Runtime) continuityView() dashboard.ContinuityView {
	cfg := r.w.Settings()
	now := time.Now()
	v := dashboard.ContinuityView{
		Enabled:    MaintenanceEnabled(cfg.Maintenance),
		BackupKeep: MaintenanceKeep(cfg.Maintenance),
		Chain:      r.w.BootChain(),
	}
	v.Safe = r.w.Safe()
	v.KeyAbsent = v.Safe && r.keyAbsent(cfg)
	v.RecordAbsent = v.KeyAbsent && r.recordAbsent(cfg)
	v.KeyUnnamed = v.Safe && !v.KeyAbsent && r.keyUnnamed(cfg)
	if v.Safe && !v.KeyAbsent && r.w.Key() != nil {
		v.SnapshotKeyMismatch, v.SnapshotKeyAbsent = r.snapshotKeyMismatch(cfg)
	}
	if v.Enabled && !v.Safe {
		v.NextPass = time.UnixMilli(cognitive.NextLocalDaily(now, maintenanceHourLocal, 0)).UTC().Format(time.RFC3339)
	}
	kept, _ := keptSnapshots(r.BackupsDir(cfg))
	for i := len(kept) - 1; i >= 0; i-- {
		k := kept[i]
		v.Snapshots = append(v.Snapshots, dashboard.ContinuitySnapshot{
			Name: k.Name, At: k.at().UTC().Format(time.RFC3339), Record: k.Record, OnDemand: k.OnDemand, Encrypted: k.Encrypted,
		})
	}
	if r.w.Store() != nil {
		if st, ok, err := r.w.Store().ContinuityStatus(); err == nil && ok {

			v.LastPass = &dashboard.ContinuityPass{At: st.At, Outcome: st.Outcome, Detail: st.Detail, Snapshot: st.Snapshot, FailingSince: st.FailingSince}
		}
	}
	key, _, reason, _ := r.snapshotEncryption(cfg)
	escrow.Wipe(key)
	v.Encrypting = reason == ""
	v.Unencrypted = reason
	v.UnencryptedText = unencryptedForPerson(reason)
	if raw, err := atomicfile.ReadFile(r.escrowReceiptPath(cfg)); err == nil {
		var receipt escrow.Receipt
		if json.Unmarshal(raw, &receipt) == nil {
			v.EscrowCheckedAt = receipt.CheckedAt
		}
	}
	v.EscrowCovers = reason == ""
	for _, set := range setAsideSets(cfg) {
		v.SetAside = append(v.SetAside, dashboard.ContinuitySetAside{Name: set.Name, Path: set.Path, Bytes: set.Bytes,
			SetAsideAt: set.Record.SetAsideAt, WasThrough: set.Record.WasThrough, RestoredFrom: set.Record.RestoredFrom, RestoredTo: set.Record.RestoredTo, PutBackAt: set.Record.PutBackAt})
	}
	if f := lastRestoreFailure(cfg); f != nil {
		v.RestoreFailed = "A restore from " + f.Request.Name + " was asked for and NOT performed: " + f.Why + "."
	}
	if r.w.Store() != nil {
		if last, ok, err := r.w.Store().LastRestore(); err == nil && ok {
			v.Restored = restoredForPerson(last)
		}
	}
	return v
}

func restoredForPerson(r store.Restore) string {
	from := RestoredFrom(r)
	return "Last " + from + ", on " + r.At.UTC().Format("2006-01-02 15:04 MST") + ". " +
		"Messages to people that were still waiting to be sent in it were not sent again, because they may already have been sent before the restore: each is marked so, and the identity was told. " +
		"An alarm that was due before the restore fires after it, marked the same way."
}

func unencryptedForPerson(reason string) string {
	switch reason {
	case "":
		return ""
	case store.UnencryptedNoKey:
		return "This identity has no snapshot key yet. One is made at the next normal start."
	case store.UnencryptedKeyUnusable:
		return "The snapshot key on this machine does not read as a key. Snapshots stay unencrypted until it is replaced."
	case store.UnencryptedNoEscrow:
		return "Snapshots are not encrypted: no checked recovery copy of the snapshot key is recorded. To encrypt them, make an escrow file below and check it."
	case store.UnencryptedReceiptUnread:
		return "The record of the last escrow check does not read. Check your escrow file again below."
	case store.UnencryptedReceiptMismatch:
		return "The snapshot key on this machine is not the one your checked escrow holds. Make a new escrow file below and check it."
	case store.UnencryptedIdentityClosed:
		return "The identity's key is not open in this start, so the escrow check cannot be confirmed."
	}
	return "The reason was not recorded."
}

func (r *Runtime) personTake(ctx context.Context) (dashboard.ContinuityView, error) {
	if _, err := r.takeOnDemand(ctx, false, time.Now()); err != nil {
		return r.continuityView(), personError(err)
	}
	return r.continuityView(), nil
}

func (r *Runtime) personVerify(ctx context.Context, name string) (string, error) {
	out, err := r.verifyKept(ctx, name, false, time.Now())
	return out, personError(err)
}

func personError(err error) error {
	var r *Refusal
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

func (r *Runtime) escrowCreateHere(pass []byte) (file []byte, name string, err error) {
	if r.w.Safe() {
		return nil, "", errors.New("This start is SAFE. An escrow is made from a normal start.")
	}
	if len(pass) < escrowMinPassphrase {
		return nil, "", fmt.Errorf("The passphrase is shorter than %d characters.", escrowMinPassphrase)
	}
	cfg := r.w.Settings()
	var c escrow.Contents
	defer c.Wipe()

	if c.IdentityKey, err = os.ReadFile(r.w.Active(cfg.Identity).KeyPath); err != nil {
		logsink.Error("escrow.error", "the identity key does not read: %v", err)
		return nil, "", errors.New("The identity's key could not be read on this machine.")
	}
	if held, perr := crypto.ParseKeyPair(c.IdentityKey); perr != nil || r.w.DoorKey() == nil ||
		held.Fingerprint() != r.w.DoorKey().Fingerprint() {
		logsink.Error("escrow.error", "the key file read is not the running identity's key (%v); nothing was sealed", perr)
		return nil, "", errors.New("The key file on this machine is not this running identity's key, so no escrow was made.")
	}
	if c.SnapshotKey, err = os.ReadFile(r.snapshotKeyPath(cfg)); errors.Is(err, os.ErrNotExist) {
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
	fp := r.w.DoorKey().Fingerprint()
	logsink.Info("escrow.end", "an escrow file was made for the person to save; nothing was written here and no receipt is signed until it is checked")
	return sealed, fmt.Sprintf("escrow-%s-%s.age", fp[:12], time.Now().UTC().Format("20060102")), nil
}

const escrowMinPassphrase = 12

func (r *Runtime) escrowCheckHere(sealed, pass []byte) (dashboard.ContinuityView, error) {
	fail := func(msg string) (dashboard.ContinuityView, error) { return r.continuityView(), errors.New(msg) }
	if r.w.Safe() {
		return fail("This start is SAFE. An escrow is checked from a normal start.")
	}
	if r.w.DoorKey() == nil {
		return fail("The identity's key is not open in this start.")
	}
	if len(sealed) == 0 || len(sealed) > escrow.MaxSealedBytes {
		return fail("That file is not an escrow file: it is empty, or larger than any escrow.")
	}
	cfg := r.w.Settings()
	c, kp, recipient, err := escrow.OpenFor(sealed, pass, r.w.DoorKey().Fingerprint())
	var other *escrow.AnotherIdentity
	switch {
	case errors.As(err, &other):
		return fail("That escrow file holds another identity's keys, not this one's.")
	case err != nil:
		return fail("That file did not open: the passphrase is wrong, or it is not an escrow file.")
	}
	defer c.Wipe()
	hostRecipient, hostErr := "", error(nil)
	if raw, rerr := os.ReadFile(r.snapshotKeyPath(cfg)); rerr == nil {
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
	if _, published, err := escrow.WriteReceipt(kp, recipient, r.w.Active(cfg.Identity).KeyPath, time.Now()); err != nil && !published {
		logsink.Error("escrow.error", "checked, and the receipt could not be written: %v", err)
		return fail("The escrow file checked out, but the record of the check could not be written on this machine, so snapshots stay unencrypted.")
	}
	logsink.Info("escrow.end", "an escrow file was checked from the dashboard: it holds this identity and this host's snapshot key; the receipt is signed")
	return r.continuityView(), nil
}
