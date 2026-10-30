package continuity

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/crypto"
	"github.com/aiii-dot-id/aii-os/internal/dashboard"
	"github.com/aiii-dot-id/aii-os/internal/escrow"
	"github.com/aiii-dot-id/aii-os/internal/ledger"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
	"github.com/aiii-dot-id/aii-os/internal/witness"
)

const newMachineMarkerName = "restore-new-machine.json"

const newMachineDirName = "restore-new-machine"

func newMachineDir(cfg Settings) string {
	return filepath.Join(restoreDir(cfg), newMachineDirName)
}

type newMachineMarker struct {
	StartedAt string `json:"started_at"`
	Identity  string `json:"identity,omitempty"`

	Wrote map[string]string `json:"wrote"`
}

const (
	wroteIdentityKey = "identity_key"
	wroteSnapshotKey = "snapshot_key"
	wroteReceipt     = "escrow_receipt"
)

func (r *Runtime) doorWrites(cfg Settings) [][2]string {
	keyPath := cfg.Identity.KeyPath
	return [][2]string{{wroteIdentityKey, keyPath}, {wroteSnapshotKey, r.snapshotKeyPath(cfg)}, {wroteReceipt, escrow.ReceiptPath(keyPath)}}
}

func newMachineMarkerPath(cfg Settings) string {
	return filepath.Join(restoreDir(cfg), newMachineMarkerName)
}

func writeNewMachineMarker(cfg Settings, m newMachineMarker) error {
	raw, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return writeFileDurably(newMachineMarkerPath(cfg), append(raw, '\n'))
}

func noteWrote(cfg Settings, m *newMachineMarker, what, path string) {
	sum, err := fileSHA256(path)
	if err == nil {
		m.Wrote[what] = sum
		err = writeNewMachineMarker(cfg, *m)
	}
	if err != nil {
		logsink.Error("restore.error", "the restore door wrote %s and could not note it (%v): withdrawing this restore will leave that file, and say so", path, err)
	}
}

func RestoringOnNewMachine(cfg Settings) bool {
	_, err := os.Stat(newMachineMarkerPath(cfg))
	return err == nil
}

func readNewMachineMarker(cfg Settings) (m newMachineMarker, found bool, err error) {
	raw, err := os.ReadFile(newMachineMarkerPath(cfg))
	if errors.Is(err, os.ErrNotExist) {
		return m, false, nil
	}
	if err != nil {
		return m, true, err
	}
	return m, true, json.Unmarshal(raw, &m)
}

func (r *Runtime) NewMachineKeys(sealed, pass []byte) (dashboard.RestoreNewKeys, error) {
	var none dashboard.RestoreNewKeys
	r.newMachine.Lock()
	defer r.newMachine.Unlock()
	cfg := r.w.Settings()
	if err := restorable(cfg); err != nil {
		return none, err
	}
	if len(sealed) == 0 || len(sealed) > escrow.MaxSealedBytes {
		return none, errors.New("That file is not an escrow file: it is empty, or larger than any escrow.")
	}
	c, kp, err := escrow.Open(sealed, pass)
	if err != nil {
		return none, errors.New("That file did not open: the passphrase is wrong, or it is not an escrow file.")
	}
	defer c.Wipe()
	if err := escrow.Challenge(kp); err != nil {
		return none, errors.New("That escrow opens, but the key in it does not sign and verify. It cannot restore an identity.")
	}

	keyPath, snapPath := cfg.Identity.KeyPath, r.snapshotKeyPath(cfg)
	if err := identityKeyIsOursOrAbsent(keyPath, kp.Fingerprint()); err != nil {
		return none, err
	}
	var wantRecipient string
	if c.SnapshotKey != nil {
		var err error
		if wantRecipient, err = escrow.SnapshotRecipient(c.SnapshotKey); err != nil {
			return none, errors.New("That escrow opens, but the snapshot key in it is not one. It cannot open this identity's encrypted snapshots.")
		}
		if err := snapshotKeyIsOursOrAbsent(snapPath, wantRecipient); err != nil {
			return none, err
		}
	}

	marker := newMachineMarker{StartedAt: time.Now().UTC().Format(time.RFC3339), Identity: kp.Fingerprint(), Wrote: map[string]string{}}
	if was, found, err := readNewMachineMarker(cfg); found && err == nil {
		maps.Copy(marker.Wrote, was.Wrote)
	}
	if err := os.MkdirAll(restoreDir(cfg), 0o700); err != nil {
		return none, errors.New("This identity's directory could not be made on this machine.")
	}
	if err := writeNewMachineMarker(cfg, marker); err != nil {
		logsink.Error("restore.error", "the new-machine marker could not be written: %v", err)
		return none, errors.New("Nothing could be written in this identity's directory, so the keys were not put in place.")
	}
	for _, p := range []string{keyPath, snapPath} {
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			return none, errors.New("This identity's directory could not be made on this machine.")
		}
	}
	wroteKey := false
	if _, err := os.Lstat(keyPath); errors.Is(err, os.ErrNotExist) {
		if _, err := crypto.PublishKeyFile(c.IdentityKey, keyPath); err != nil {
			logsink.Error("restore.error", "the identity key could not be published durably: %v", err)
			return none, errors.New("The identity's key could not be written durably on this machine. Try again; a key already in place is checked, never written over.")
		}
		r.afterKeyPublished(keyPath)
		wroteKey = true
	}

	if here, err := crypto.LoadKeyPair(keyPath); err != nil || here.Fingerprint() != kp.Fingerprint() {
		logsink.Error("restore.error", "the identity key does not read back as this identity's: %v", err)
		return none, errors.New("The identity's key was written and does not read back. Nothing else was done; check this machine's disk and try again.")
	}
	if wroteKey {
		noteWrote(cfg, &marker, wroteIdentityKey, keyPath)
	}

	if c.SnapshotKey == nil {
		r.w.SetKey(kp)
		return dashboard.RestoreNewKeys{Identity: kp.Fingerprint()}, nil
	}
	wroteSnap := false
	if _, err := os.Lstat(snapPath); errors.Is(err, os.ErrNotExist) {
		if _, err := escrow.PublishSecret(c.SnapshotKey, snapPath); err != nil {
			logsink.Error("restore.error", "the snapshot key could not be published durably: %v", err)
			return none, errors.New("The identity's key is in place and the snapshot key could NOT be written durably. Try again; what is in place is checked, never written over.")
		}
		wroteSnap = true
	}
	if err := snapshotKeyIsOursOrAbsent(snapPath, wantRecipient); err != nil {
		return none, err
	}
	if _, err := os.Lstat(snapPath); err != nil {
		return none, errors.New("The identity's key is in place and the snapshot key does not read back. Try again.")
	}
	if wroteSnap {
		noteWrote(cfg, &marker, wroteSnapshotKey, snapPath)
	}

	if _, published, err := escrow.WriteReceipt(kp, wantRecipient, keyPath, time.Now()); err != nil && !published {
		logsink.Error("restore.error", "the keys are in place and the escrow receipt could NOT be written (%v): snapshots made here are not encrypted until the escrow file is checked in Settings → Backups & Keys", err)
	} else {
		if err != nil {
			logsink.Warn("restore.error", "the escrow receipt is in place and its write could not be confirmed durable: %v", err)
		}
		noteWrote(cfg, &marker, wroteReceipt, escrow.ReceiptPath(keyPath))
	}
	r.w.SetKey(kp)
	logsink.Warn("restore.decision", "a restore on a new machine has begun: the keys of identity %s are in place from an escrow file", kp.Fingerprint())
	return dashboard.RestoreNewKeys{Identity: kp.Fingerprint(), HoldsSnapshotKey: true}, nil
}

const uploadWorkPrefix = SnapshotWorkPrefix + "upload-"

const maxSnapshotUpload = 256 << 30

var snapshotMemberRe = regexp.MustCompile(`^(ledger\.jsonl|aii\.db|RECEIPT\.json|SHA256SUMS|SHA256SUMS\.sig|witness-tail\.json|witness-envelope\.json|witness-pending-request\.json|witness-pending-receipt\.json|witness-operation\.json|segment-[0-9]+-[0-9]+\.jsonl\.gz|witness-keys/[A-Za-z0-9._-]+\.json|witness-abandoned/[0-9a-f]{64}\.json)$`)

func identityKeyIsOursOrAbsent(keyPath, fingerprint string) error {
	_, statErr := os.Lstat(keyPath)
	switch {
	case errors.Is(statErr, os.ErrNotExist):
		return nil
	case statErr != nil:
		return errors.New("The place the identity's key goes cannot be looked at on this machine (" + fsWhy(statErr) + "). Nothing was written.")
	}
	here, err := crypto.LoadKeyPair(keyPath)
	if err != nil {
		return errors.New("There is a file where the identity's key goes, and it does not read as a key. It is never written over.")
	}
	if here.Fingerprint() != fingerprint {
		return errors.New("This machine already holds another identity's key. A key is never written over: restore on a machine, or in a home, that holds none.")
	}
	return nil
}

func snapshotKeyIsOursOrAbsent(snapPath, wantRecipient string) error {
	_, statErr := os.Lstat(snapPath)
	switch {
	case errors.Is(statErr, os.ErrNotExist):
		return nil
	case statErr != nil:
		return errors.New("The place the snapshot key goes cannot be looked at on this machine (" + fsWhy(statErr) + "). Nothing was written.")
	}
	raw, err := os.ReadFile(snapPath)
	if err != nil {
		return errors.New("There is a file where the snapshot key goes, and it cannot be read (" + fsWhy(err) + "). It is never written over.")
	}
	here, herr := escrow.SnapshotRecipient(raw)
	escrow.Wipe(raw)
	if herr != nil || here != wantRecipient {
		return errors.New("This machine already holds a different snapshot key. A key is never written over.")
	}
	return nil
}

func (r *Runtime) NewMachineUpload(snapshot, member string, body io.Reader) error {
	r.newMachine.Lock()
	defer r.newMachine.Unlock()
	cfg := r.w.Settings()
	if !RestoringOnNewMachine(cfg) {
		return errors.New("Put the keys in place first: pick the escrow file above.")
	}
	m := backupSeqRe.FindStringSubmatch(snapshot)
	if m == nil {
		return errors.New("That is not a snapshot's name. A snapshot is named ledger-<date>-seq<N>, with .tar.age when it is encrypted — do not rename it.")
	}
	encrypted := m[4] != ""
	if encrypted != (member == "") {
		return errors.New("An encrypted snapshot is one file; a plaintext snapshot is a folder of files.")
	}
	if member != "" && !snapshotMemberRe.MatchString(member) {
		return fmt.Errorf("%q is not a file a snapshot holds.", member)
	}
	dir := newMachineDir(cfg)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return errors.New("The snapshot could not be written on this machine.")
	}
	work := filepath.Join(dir, uploadWorkPrefix+snapshot)
	target := work
	if member != "" {
		target = filepath.Join(work, filepath.FromSlash(member))
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			return errors.New("The snapshot's folder could not be made on this machine.")
		}
	}
	f, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return errors.New("The snapshot could not be written on this machine.")
	}
	n, err := io.Copy(f, io.LimitReader(body, maxSnapshotUpload+1))
	if serr := f.Sync(); err == nil {
		err = serr
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil || n > maxSnapshotUpload {
		os.Remove(target)
		var idle *dashboard.RestoreUploadIdle
		if errors.As(err, &idle) {
			logsink.Warn("restore.decision", "an upload of snapshot %s stopped arriving: nothing came for %s, so it was ended after %d bytes and what had arrived of it removed", snapshot, idle.Wait, n)
			wait := fmt.Sprintf("%d seconds", int(idle.Wait/time.Second))
			if idle.Wait == time.Second {
				wait = "a second"
			}
			return fmt.Errorf("No part of the snapshot arrived for %s, so this machine stopped receiving it and removed what had arrived. Send it again. (How long this machine waits is maintenance.restore_upload_idle_seconds in its config.json.)", wait)
		}
		return errors.New("The snapshot did not arrive whole. Send it again.")
	}

	if err := admitWitnessMember(target, member); err != nil {
		os.Remove(target)
		return fmt.Errorf("%q is not a witness operation a snapshot can hold: %v", member, err)
	}
	if encrypted {
		if err := r.publishArrived(dir, work, snapshot); err != nil {
			logsink.Error("restore.error", "an uploaded snapshot could not take its name: %v", err)
			return errors.New("The snapshot arrived and could not be kept on this machine. Send it again.")
		}
	}
	return nil
}

func (r *Runtime) NewMachineUploadIdle() time.Duration {
	return RestoreUploadIdle(r.w.Settings().Maintenance)
}

func (r *Runtime) publishArrived(dir, work, name string) error {
	final := filepath.Join(dir, name)
	if err := os.RemoveAll(final); err != nil {
		return err
	}
	if err := os.Rename(work, final); err != nil {
		return err
	}
	return r.syncDir(dir)
}

func (r *Runtime) NewMachineRestore(ctx context.Context, snapshot string, provider *dashboard.GenesisRequest) (dashboard.RestoreNewDone, error) {
	var none dashboard.RestoreNewDone
	r.newMachine.Lock()
	defer r.newMachine.Unlock()
	cfg := r.w.Settings()
	if !RestoringOnNewMachine(cfg) {
		return none, errors.New("Put the keys in place first: pick the escrow file above.")
	}
	kp, err := crypto.LoadKeyPair(cfg.Identity.KeyPath)
	if err != nil {
		return none, errors.New("The identity's key is not in place on this machine. Pick the escrow file again.")
	}
	r.w.SetKey(kp)
	if m := backupSeqRe.FindStringSubmatch(snapshot); m == nil {
		return none, errors.New("That is not a snapshot's name.")
	} else if m[4] == "" {

		dir := newMachineDir(cfg)
		work := filepath.Join(dir, uploadWorkPrefix+snapshot)
		_, arrived := os.Lstat(work)
		_, published := os.Lstat(filepath.Join(dir, snapshot))

		if arrived == nil {
			if _, serr := os.Stat(filepath.Join(work, SnapshotSums)); serr != nil {
				return none, errors.New("The snapshot's folder did not arrive whole: its checksum list is missing. Pick the folder again.")
			}
			if err := r.publishArrived(dir, work, snapshot); err != nil {
				logsink.Error("restore.error", "an uploaded snapshot folder could not take its name: %v", err)
				return none, errors.New("The snapshot arrived and could not be kept on this machine. Pick the folder again.")
			}
		} else if published != nil {
			return none, errors.New("The snapshot has not arrived on this machine yet. Send it first.")
		}
	}
	if _, _, err := r.resolveRestoreSource(cfg, SourceSnapshot, snapshot); err != nil {
		return none, errors.New("The snapshot has not arrived on this machine yet. Send it first.")
	}

	if provider != nil && provider.Provider != "" {
		if err := r.w.BirthProvider(provider); err != nil {
			return none, fmt.Errorf("The model could not be set up: %v", err)
		}
		cfg = r.w.Settings()
	}
	req := RestoreRequest{Source: SourceSnapshot, Name: snapshot, RequestedAt: time.Now().UTC().Format(time.RFC3339), Identity: kp.Fingerprint()}
	raw, _ := json.MarshalIndent(req, "", "  ")
	if err := writeFileDurably(restoreRequestPath(cfg), append(raw, '\n')); err != nil {
		return none, errors.New("The restore could not be written down on this machine, so nothing was restored.")
	}
	if err := r.PerformPendingRestore(ctx, cfg, kp); err != nil {
		return none, fmt.Errorf("The restore did not finish: %v. Start this identity again and it goes on from where it stopped.", err)
	}
	if f := lastRestoreFailure(cfg); f != nil {
		os.Remove(filepath.Join(restoreDir(cfg), RestoreFailedName))
		return none, fmt.Errorf("The snapshot was NOT restored: %s. Nothing was put in place; the keys stay where they are, and you can try another snapshot.", personWhy(f.Why))
	}
	if _, err := os.Stat(cfg.Identity.LedgerPath); err != nil {
		return none, errors.New("The restore did not finish. Start this identity again and it goes on from where it stopped.")
	}
	r.endNewMachine(cfg)
	if err := r.w.StartLive(); err != nil {
		return none, fmt.Errorf("The identity is restored, and its start failed: %v. Start it again.", err)
	}
	r.w.SwapToLive()
	done := dashboard.RestoreNewDone{Identity: kp.Fingerprint(), Witnessed: -1}
	if through, ok := r.w.LastSeq(); ok {
		done.RestoredTo = through
	} else if r.w.Safe() {

		logsink.Warn("restore.end", "RESTORED ON A NEW MACHINE: identity %s — its start stopped in SAFE with the record closed; the handler is swapped and the page says why", kp.Fingerprint())
		return none, errors.New("The identity is restored, and its start stopped in SAFE. Reload this page: it is the identity's own now, and it says what stopped the start and what to put right.")
	}
	if tail, err := witness.ReadLocalTail(restoreDir(cfg)); err == nil && tail != nil {
		done.Witnessed = tail.LedgerOrdinal
	}
	logsink.Warn("restore.end", "RESTORED ON A NEW MACHINE: identity %s, through record %d — the handler is swapped, as after a birth", kp.Fingerprint(), done.RestoredTo)
	return done, nil
}

func (r *Runtime) endNewMachine(cfg Settings) {
	dir, backups := newMachineDir(cfg), r.BackupsDir(cfg)
	for _, name := range receivedSnapshots(dir) {
		if _, err := os.Lstat(filepath.Join(backups, name)); !errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err := os.MkdirAll(backups, 0o700); err != nil {
			logsink.Warn("restore.error", "the snapshot %s the restore door received is not kept: the backups directory could not be made (%v)", name, err)
			break
		}
		if err := os.Rename(filepath.Join(dir, name), filepath.Join(backups, name)); err != nil {
			logsink.Warn("restore.error", "the snapshot %s the restore door received is not kept: %v", name, err)
		}
	}
	_ = r.syncDir(backups)
	for _, gone := range []string{dir, newMachineMarkerPath(cfg)} {
		if err := os.RemoveAll(gone); err != nil {
			logsink.Warn("restore.error", "%s outlived the restore it served (%v); it holds nothing the identity needs", gone, err)
		}
	}
	_ = r.syncDir(restoreDir(cfg))
}

func receivedSnapshots(dir string) []string {
	entries, _ := os.ReadDir(dir)
	var names []string
	for _, e := range entries {
		if backupSeqRe.MatchString(e.Name()) && snapshotComplete(dir, e.Name()) {
			names = append(names, e.Name())
		}
	}
	return names
}

func (r *Runtime) NewMachineBegun() *dashboard.RestoreNewBegun {
	cfg := r.w.Settings()
	m, found, _ := readNewMachineMarker(cfg)
	if !found {
		return nil
	}
	return &dashboard.RestoreNewBegun{Identity: m.Identity, Received: receivedSnapshots(newMachineDir(cfg))}
}

func (r *Runtime) NewMachineRefusesBirth() error {
	cfg := r.w.Settings()
	m, found, _ := readNewMachineMarker(cfg)
	if !found {
		return nil
	}
	whose := "an identity"
	if m.Identity != "" {
		whose = "identity " + shortIdentity(m.Identity)
	}
	if here, err := crypto.LoadKeyPair(cfg.Identity.KeyPath); err == nil && m.Identity != "" && here.Fingerprint() == m.Identity {
		return refuseDoor(dashboard.RestoreNewStands, "Nothing was sent, and no identity was born. The keys of %s are on this machine: they were put here from an escrow file at \"Restore an identity from a backup instead\", and a new identity is not born over them. Open that door below, then either restore that identity from a snapshot, or press \"Withdraw these keys\" and press Birth again.", whose)
	}
	return refuseDoor(dashboard.RestoreNewStands, "Nothing was sent, and no identity was born. A restore of %s was begun on this machine at \"Restore an identity from a backup instead\" and has been neither finished nor withdrawn. Open that door below, then either go on with the restore, or press \"Withdraw these keys\" and press Birth again.", whose)
}

func (r *Runtime) NewMachineWithdraw() (snapshots []string, err error) {
	r.newMachine.Lock()
	defer r.newMachine.Unlock()
	cfg := r.w.Settings()
	m, found, _ := readNewMachineMarker(cfg)
	if !found {
		return nil, refuseDoor(dashboard.RestoreNewNone, "No restore is begun on this machine, so there is nothing here to withdraw.")
	}
	if err := r.onlyTheDoorsHere(cfg); err != nil {
		return nil, err
	}
	keyPath, snapPath := cfg.Identity.KeyPath, r.snapshotKeyPath(cfg)
	if m.Wrote == nil {
		return nil, refuseDoor(dashboard.RestoreNewNotItsKey, "Nothing was withdrawn: this restore was begun by an earlier version of AII OS, which kept no note of what it put here, so this page cannot tell its keys from keys that were here before. By hand: %s is this page's own note of the restore, and can be removed; %s and %s are key files: remove one only if your escrow file holds it.", newMachineMarkerPath(cfg), keyPath, snapPath)
	}
	for _, f := range r.doorWrites(cfg) {
		what, path := f[0], f[1]
		if _, err := os.Lstat(path); errors.Is(err, os.ErrNotExist) {
			continue
		} else if err != nil {
			return nil, refuseDoor(dashboard.RestoreNewNotItsKey, "Nothing was withdrawn: whether %s is what this page wrote there cannot be told (%s), and only what this page wrote is removed here.", path, fsWhy(err))
		}
		if sum, err := fileSHA256(path); err != nil || sum != m.Wrote[what] {
			return nil, refuseDoor(dashboard.RestoreNewNotItsKey, "Nothing was withdrawn: %s is not what this page wrote there, and only what this page wrote is removed here. It is left as it is: move it off this machine by hand (keep it if your escrow file does not hold it), then press \"Withdraw these keys\" again.", path)
		}
	}
	received := receivedSnapshots(newMachineDir(cfg))
	for _, gone := range []string{newMachineDir(cfg), snapPath, escrow.ReceiptPath(keyPath), keyPath, restoreRequestPath(cfg), newMachineMarkerPath(cfg)} {
		if err := os.RemoveAll(gone); err != nil {
			return nil, r.withdrawStopped(gone, err)
		}
		if err := r.syncDir(filepath.Dir(gone)); err != nil {
			return nil, r.withdrawStopped(gone, err)
		}
	}
	sweepRestoreStaging(restoreDir(cfg))
	r.w.SetKey(nil)
	logsink.Warn("restore.decision", "the restore of identity %s begun on this machine from an escrow file was withdrawn at the page: its keys, the %d snapshots it received and its marker are removed, and this machine is first boot's again", m.Identity, len(received))
	return received, nil
}

func (r *Runtime) onlyTheDoorsHere(cfg Settings) error {
	cannot := func(what string, err error) error {
		return refuseDoor(dashboard.RestoreNewNotFirstBoot, "Nothing was withdrawn: whether %s is on this machine cannot be told (%s).", what, fsWhy(err))
	}
	lives := func(what, path string) error {
		return refuseDoor(dashboard.RestoreNewNotFirstBoot, "Nothing was withdrawn: %s is on this machine (%s), and keys beside it are that identity's own.", what, path)
	}
	kept := func(what, path string) error {
		return refuseDoor(dashboard.RestoreNewNotFirstBoot, "Nothing was withdrawn: %s is on this machine (%s), and without these keys the next start would open that identity's own page, not first boot. Restore from it, or move it off this machine and press \"Withdraw these keys\" again.", what, path)
	}
	if artifact, err := ledger.RecordArtifact(cfg.Identity.LedgerPath); err != nil {
		return cannot("an identity's record", err)
	} else if artifact != "" {
		return lives("an identity's record", artifact)
	}
	if _, err := os.Lstat(cfg.Identity.DBPath); err == nil {
		return lives("an identity's database", cfg.Identity.DBPath)
	} else if !errors.Is(err, os.ErrNotExist) {
		return cannot("an identity's database", err)
	}
	backups := r.BackupsDir(cfg)
	entries, err := os.ReadDir(backups)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return cannot("a snapshot", err)
	}
	for _, e := range entries {
		if IsBackupEvidence(e.Name()) {
			return kept("a snapshot", filepath.Join(backups, e.Name()))
		}
	}
	if sets := setAsideSets(cfg); len(sets) > 0 {
		return kept("a set an earlier restore kept aside", sets[0].Path)
	}
	return nil
}

func (r *Runtime) withdrawStopped(path string, err error) error {
	logsink.Error("restore.error", "withdrawing a restore begun on this machine stopped at %s: %v", path, err)
	return fmt.Errorf("Withdrawing stopped at %s (%s). Press \"Withdraw these keys\" again.", path, fsWhy(err))
}

func refuseDoor(reason, format string, args ...any) error {
	return &dashboard.RestoreNewRefusal{Reason: reason, Text: fmt.Sprintf(format, args...)}
}

func shortIdentity(fingerprint string) string {
	if len(fingerprint) > 16 {
		return fingerprint[:16] + "…"
	}
	return fingerprint
}

func personWhy(why string) string {
	return strings.TrimSuffix(strings.TrimPrefix(why, "the source does not prove: "), ". Nothing was touched")
}

func fsWhy(err error) string {
	switch {
	case errors.Is(err, os.ErrPermission):
		return "permission denied"
	case errors.Is(err, os.ErrNotExist):
		return "it is not there"
	}
	var pe *os.PathError
	if errors.As(err, &pe) {
		return pe.Err.Error()
	}
	return err.Error()
}

func admitWitnessMember(path, member string) error {
	switch {
	case member == witness.OperationFileName:
		info, err := os.Stat(path)
		if err != nil {
			return err
		}
		return witness.CheckOperationSize(info.Size())
	case strings.HasPrefix(member, witness.AbandonedDirName+"/"):
		info, err := os.Stat(path)
		if err != nil {
			return err
		}
		if err := witness.CheckOperationSize(info.Size()); err != nil {
			return err
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return witness.CheckArchive(strings.TrimPrefix(member, witness.AbandonedDirName+"/"), raw)
	}
	return nil
}
