package app

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
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/crypto"
	"github.com/aiii-dot-id/aii-os/internal/dashboard"
	"github.com/aiii-dot-id/aii-os/internal/escrow"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
	"github.com/aiii-dot-id/aii-os/internal/witness"
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
// .

const newMachineMarkerName = "restore-new-machine.json"

// .
type newMachineMarker struct {
	StartedAt string `json:"started_at"`
	Identity  string `json:"identity,omitempty"`
}

func newMachineMarkerPath(cfg Config) string {
	return filepath.Join(restoreDir(cfg), newMachineMarkerName)
}

// .
// .
func restoringOnNewMachine(cfg Config) bool {
	_, err := os.Stat(newMachineMarkerPath(cfg))
	return err == nil
}

// .
func (a *App) newMachineKeys(sealed, pass []byte) (dashboard.RestoreNewKeys, error) {
	var none dashboard.RestoreNewKeys
	cfg := a.configSnapshot()
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
	// .
	// .
	// .
	// .
	// .
	// .
	keyPath, snapPath := cfg.Identity.KeyPath, a.snapshotKeyPath(cfg)
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
	marker, _ := json.MarshalIndent(newMachineMarker{StartedAt: time.Now().UTC().Format(time.RFC3339), Identity: kp.Fingerprint()}, "", "  ")
	if err := os.MkdirAll(restoreDir(cfg), 0o700); err != nil {
		return none, errors.New("This identity's directory could not be made on this machine.")
	}
	if err := writeFileDurably(newMachineMarkerPath(cfg), append(marker, '\n')); err != nil {
		logsink.Error("restore.error", "the new-machine marker could not be written: %v", err)
		return none, errors.New("Nothing could be written in this identity's directory, so the keys were not put in place.")
	}
	for _, p := range []string{keyPath, snapPath} {
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			return none, errors.New("This identity's directory could not be made on this machine.")
		}
	}
	if _, err := os.Lstat(keyPath); errors.Is(err, os.ErrNotExist) {
		if _, err := crypto.PublishKeyFile(c.IdentityKey, keyPath); err != nil {
			logsink.Error("restore.error", "the identity key could not be published durably: %v", err)
			return none, errors.New("The identity's key could not be written durably on this machine. Try again; a key already in place is checked, never written over.")
		}
		afterKeyPublished(keyPath)
	}
	// .
	if here, err := crypto.LoadKeyPair(keyPath); err != nil || here.Fingerprint() != kp.Fingerprint() {
		logsink.Error("restore.error", "the identity key does not read back as this identity's: %v", err)
		return none, errors.New("The identity's key was written and does not read back. Nothing else was done; check this machine's disk and try again.")
	}
	// .
	// .
	// .
	if c.SnapshotKey == nil {
		a.keyPair = kp
		return dashboard.RestoreNewKeys{Identity: kp.Fingerprint()}, nil
	}
	if _, err := os.Lstat(snapPath); errors.Is(err, os.ErrNotExist) {
		if _, err := escrow.PublishSecret(c.SnapshotKey, snapPath); err != nil {
			logsink.Error("restore.error", "the snapshot key could not be published durably: %v", err)
			return none, errors.New("The identity's key is in place and the snapshot key could NOT be written durably. Try again; what is in place is checked, never written over.")
		}
	}
	if err := snapshotKeyIsOursOrAbsent(snapPath, wantRecipient); err != nil {
		return none, err
	}
	if _, err := os.Lstat(snapPath); err != nil {
		return none, errors.New("The identity's key is in place and the snapshot key does not read back. Try again.")
	}
	a.keyPair = kp
	logsink.Warn("restore.decision", "a restore on a new machine has begun: the keys of identity %s are in place from an escrow file", kp.Fingerprint())
	return dashboard.RestoreNewKeys{Identity: kp.Fingerprint(), HoldsSnapshotKey: true}, nil
}

// .
// .
// .
const uploadWorkPrefix = snapshotWorkPrefix + "upload-"

// .
// .
const maxSnapshotUpload = 256 << 30

// .
var snapshotMemberRe = regexp.MustCompile(`^(ledger\.jsonl|aii\.db|RECEIPT\.json|SHA256SUMS|witness-tail\.json|segment-[0-9]+-[0-9]+\.jsonl\.gz|witness-keys/[A-Za-z0-9._-]+\.json)$`)

// .
// .
// .
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

// .
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

// .
// .
// .
// .
func (a *App) newMachineUpload(snapshot, member string, body io.Reader) error {
	cfg := a.configSnapshot()
	if !restoringOnNewMachine(cfg) {
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
	dir := a.backupsDir(cfg)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return errors.New("The backups directory could not be made on this machine.")
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
		return errors.New("The snapshot did not arrive whole. Send it again.")
	}
	if encrypted {
		if err := publishArrived(dir, work, snapshot); err != nil {
			logsink.Error("restore.error", "an uploaded snapshot could not be put with the backups: %v", err)
			return errors.New("The snapshot arrived and could not be put with the backups.")
		}
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
func publishArrived(dir, work, name string) error {
	final := filepath.Join(dir, name)
	if _, err := os.Lstat(final); errors.Is(err, os.ErrNotExist) {
		if err := os.Rename(work, final); err != nil {
			return err
		}
		return syncDir(dir)
	} else if err != nil {
		return err
	}
	same, err := sameTree(work, final)
	if err != nil {
		return err
	}
	if same {
		return os.RemoveAll(work)
	}
	aside := final + ".replaced-" + time.Now().UTC().Format("20060102T150405.000000000Z")
	logsink.Warn("restore.decision", "a snapshot named %s was already here and is NOT the one that just arrived — it is kept as %s, and the arrival takes the name", name, filepath.Base(aside))
	if err := os.Rename(final, aside); err != nil {
		return err
	}
	if err := os.Rename(work, final); err != nil {
		return err
	}
	return syncDir(dir)
}

// .
// .
func sameTree(a, b string) (bool, error) {
	sa, err := os.Lstat(a)
	if err != nil {
		return false, err
	}
	sb, err := os.Lstat(b)
	if err != nil {
		return false, err
	}
	if sa.IsDir() != sb.IsDir() {
		return false, nil
	}
	if !sa.IsDir() {
		if sa.Size() != sb.Size() {
			return false, nil
		}
		ha, err := fileSHA256(a)
		if err != nil {
			return false, err
		}
		hb, err := fileSHA256(b)
		return ha == hb, err
	}
	fa, err := filesUnder(a)
	if err != nil {
		return false, err
	}
	fb, err := filesUnder(b)
	if err != nil {
		return false, err
	}
	if len(fa) != len(fb) {
		return false, nil
	}
	sort.Strings(fa)
	sort.Strings(fb)
	for i := range fa {
		if fa[i] != fb[i] {
			return false, nil
		}
		same, err := sameTree(filepath.Join(a, filepath.FromSlash(fa[i])), filepath.Join(b, filepath.FromSlash(fb[i])))
		if err != nil || !same {
			return false, err
		}
	}
	return true, nil
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

// .
func (a *App) newMachineRestore(ctx context.Context, snapshot string, provider *dashboard.GenesisRequest) (dashboard.RestoreNewDone, error) {
	var none dashboard.RestoreNewDone
	cfg := a.configSnapshot()
	if !restoringOnNewMachine(cfg) {
		return none, errors.New("Put the keys in place first: pick the escrow file above.")
	}
	kp, err := crypto.LoadKeyPair(cfg.Identity.KeyPath)
	if err != nil {
		return none, errors.New("The identity's key is not in place on this machine. Pick the escrow file again.")
	}
	a.keyPair = kp
	if m := backupSeqRe.FindStringSubmatch(snapshot); m == nil {
		return none, errors.New("That is not a snapshot's name.")
	} else if m[4] == "" {
		// .
		// .
		dir := a.backupsDir(cfg)
		work := filepath.Join(dir, uploadWorkPrefix+snapshot)
		_, arrived := os.Lstat(work)
		_, published := os.Lstat(filepath.Join(dir, snapshot))
		// .
		// .
		// .
		if arrived == nil {
			if _, serr := os.Stat(filepath.Join(work, snapshotSums)); serr != nil {
				return none, errors.New("The snapshot's folder did not arrive whole: its checksum list is missing. Pick the folder again.")
			}
			if err := publishArrived(dir, work, snapshot); err != nil {
				logsink.Error("restore.error", "an uploaded snapshot folder could not be put with the backups: %v", err)
				return none, errors.New("The snapshot arrived and could not be put with the backups.")
			}
		} else if published != nil {
			return none, errors.New("The snapshot has not arrived on this machine yet. Send it first.")
		}
	}
	if _, _, err := a.resolveRestoreSource(cfg, sourceSnapshot, snapshot); err != nil {
		return none, errors.New("The snapshot has not arrived on this machine yet. Send it first.")
	}
	// .
	// .
	if provider != nil && provider.Provider != "" {
		if _, err := a.upsertBirthProvider(provider); err != nil {
			return none, fmt.Errorf("The model could not be set up: %v", err)
		}
		cfg = a.configSnapshot()
	}
	req := restoreRequest{Source: sourceSnapshot, Name: snapshot, RequestedAt: time.Now().UTC().Format(time.RFC3339), Identity: kp.Fingerprint()}
	raw, _ := json.MarshalIndent(req, "", "  ")
	if err := writeFileDurably(restoreRequestPath(cfg), append(raw, '\n')); err != nil {
		return none, errors.New("The restore could not be written down on this machine, so nothing was restored.")
	}
	if err := a.performPendingRestore(ctx, cfg, kp); err != nil {
		return none, fmt.Errorf("The restore did not finish: %v. Start this identity again and it goes on from where it stopped.", err)
	}
	if f := lastRestoreFailure(cfg); f != nil {
		os.Remove(filepath.Join(restoreDir(cfg), restoreFailedName))
		return none, fmt.Errorf("The snapshot was NOT restored: %s. Nothing was put in place; the keys stay where they are, and you can try another snapshot.", personWhy(f.Why))
	}
	if !fileExists(cfg.Identity.LedgerPath) {
		return none, errors.New("The restore did not finish. Start this identity again and it goes on from where it stopped.")
	}
	os.Remove(newMachineMarkerPath(cfg))
	if err := a.startLive(); err != nil {
		return none, fmt.Errorf("The identity is restored, and its start failed: %v. Start it again.", err)
	}
	a.dashboard.SwapHandler(a.buildLiveHandler())
	done := dashboard.RestoreNewDone{Identity: kp.Fingerprint(), Witnessed: -1}
	if a.ledger != nil {
		done.RestoredTo = a.ledger.LastSeq()
	}
	if tail, err := witness.ReadLocalTail(restoreDir(cfg)); err == nil && tail != nil {
		done.Witnessed = tail.LedgerOrdinal
	}
	logsink.Warn("restore.end", "RESTORED ON A NEW MACHINE: identity %s, through record %d — the handler is swapped, as after a birth", kp.Fingerprint(), done.RestoredTo)
	return done, nil
}

// .
// .
func personWhy(why string) string {
	return strings.TrimPrefix(why, "the source does not prove: ")
}

// .
// .
// .
// .
var afterKeyPublished = func(string) {}

// .
// .
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
