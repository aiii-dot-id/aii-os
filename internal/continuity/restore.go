package continuity

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/crypto"
	"github.com/aiii-dot-id/aii-os/internal/escrow"
	"github.com/aiii-dot-id/aii-os/internal/genesis"
	"github.com/aiii-dot-id/aii-os/internal/ledger"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
	"github.com/aiii-dot-id/aii-os/internal/store"
	"github.com/aiii-dot-id/aii-os/internal/witness"
)

const (
	RestoreRequestName = "restore-request.json"
	RestoreJournalName = "restore-journal.json"
	RestoreFailedName  = "restore-failed.json"
	RestoreSettleName  = "restore-settle.json"
	RestoreRecordName  = "RESTORE.json"
	setAsideDirName    = "set-aside"
	RestoreStagePrefix = ".restore-staging-"

	SourceSnapshot = "snapshot"
	SourceSetAside = "set-aside"
)

type RestoreRequest struct {
	Source      string `json:"source"`
	Name        string `json:"name"`
	RequestedAt string `json:"requested_at"`
	Identity    string `json:"identity"`

	Label string `json:"label,omitempty"`
}

func setName(cfg Settings, live string) string {
	db, lg := filepath.Base(cfg.Identity.DBPath), filepath.Base(cfg.Identity.LedgerPath)
	switch {
	case live == lg:
		return "ledger.jsonl"
	case live == db:
		return SnapshotDB
	case strings.HasPrefix(live, db+"-") && isDatabaseSidecar(SnapshotDB+strings.TrimPrefix(live, db)):
		return SnapshotDB + strings.TrimPrefix(live, db)
	}
	return live
}

var databaseSidecars = []string{"-wal", "-shm", "-journal"}

func isDatabaseSidecar(rel string) bool {
	for _, side := range databaseSidecars {
		if rel == SnapshotDB+side {
			return true
		}
	}
	return false
}

type RestoreMove struct {
	From string `json:"from"`
	To   string `json:"to"`
	Kind string `json:"kind"`
}

const (
	MovePreserve = "preserve"
	MoveCopy     = "copy"
	MoveInstall  = "install"
)

func (m RestoreMove) kind(aside string) string {
	if m.Kind != "" {
		return m.Kind
	}
	if strings.HasPrefix(m.To, aside) {
		return MovePreserve
	}
	return MoveInstall
}

type RestoreJournal struct {
	Request  RestoreRequest `json:"request"`
	Aside    string         `json:"aside"`
	Staging  string         `json:"staging"`
	Moves    []RestoreMove  `json:"moves"`
	Restored RestoreRecord  `json:"restored"`
}

type RestoreRecord struct {
	SetAsideAt   string `json:"set_aside_at"`
	Identity     string `json:"identity"`
	WasThrough   uint64 `json:"was_through"`
	RestoredFrom string `json:"restored_from"`
	RestoredTo   uint64 `json:"restored_to"`
	Witnessed    int64  `json:"witnessed"`

	PutBackAt string `json:"put_back_at,omitempty"`
}

type restoreFailure struct {
	At      string         `json:"at"`
	Request RestoreRequest `json:"request"`
	Why     string         `json:"why"`
}

func restoreFailureJSON(req RestoreRequest, why string) []byte {
	f, _ := json.MarshalIndent(restoreFailure{At: time.Now().UTC().Format(time.RFC3339), Request: req, Why: why}, "", "  ")
	return append(f, '\n')
}

func recordRestoreFailure(cfg Settings, req RestoreRequest, why string) {
	logsink.Error("restore.refusal", "the restore that was asked for was NOT performed: %s", why)
	if err := writeFileDurably(filepath.Join(restoreDir(cfg), RestoreFailedName), restoreFailureJSON(req, why)); err != nil {
		logsink.Error("restore.error", "the record of the refused restore was not written (%v); the log line above is the only account of it", err)
	}
}

var setAsideNameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,80}-([0-9]{8}T[0-9]{6}Z)-before-restore-to-seq[0-9]+$`)

func restoreDir(cfg Settings) string { return filepath.Dir(cfg.Identity.LedgerPath) }
func setAsideRoot(cfg Settings) string {
	return filepath.Join(restoreDir(cfg), setAsideDirName)
}
func restoreRequestPath(cfg Settings) string {
	return filepath.Join(restoreDir(cfg), RestoreRequestName)
}

func restorable(cfg Settings) error {
	if filepath.Dir(cfg.Identity.DBPath) != filepath.Dir(cfg.Identity.LedgerPath) {
		return errors.New("This identity keeps its database and its record in different directories, which a restore from the page does not handle. Restore by the procedure in the maintenance guide.")
	}
	return nil
}

func (r *Runtime) resolveRestoreSource(cfg Settings, source, name string) (path string, snap *keptSnapshot, err error) {
	switch source {
	case SourceSnapshot:
		kept, _ := keptSnapshots(r.BackupsDir(cfg))
		for i := range kept {
			if kept[i].Name == name {
				return filepath.Join(r.BackupsDir(cfg), kept[i].Name), &kept[i], nil
			}
		}
		return "", nil, fmt.Errorf("%q is not one of the snapshots kept here", name)
	case SourceSetAside:
		if !setAsideNameRe.MatchString(name) {
			return "", nil, fmt.Errorf("%q is not a set-aside set's name", name)
		}
		entries, _ := os.ReadDir(setAsideRoot(cfg))
		for _, e := range entries {
			if e.IsDir() && e.Name() == name {
				return filepath.Join(setAsideRoot(cfg), name), nil, nil
			}
		}
		return "", nil, fmt.Errorf("%q is not one of the sets kept aside here", name)
	}
	return "", nil, fmt.Errorf("a restore is from a snapshot or from a set kept aside, not from %q", source)
}

func (r *Runtime) PerformPendingRestore(ctx context.Context, cfg Settings, kp *crypto.KeyPair) error {
	dir := restoreDir(cfg)

	resumed, found, jerr := readRestoreJournal(cfg)
	if jerr != nil {
		return fmt.Errorf("%w — what it describes may be half in place, so the record is not opened; it is in %s", jerr, dir)
	}
	if found {
		logsink.Warn("restore.decision", "a restore was interrupted after it was journalled — rolling it forward (everything it moves was proved before the journal was written)")
		if ferr := r.finishRestore(cfg, resumed); ferr != nil {
			logsink.Error("restore.error", "the interrupted restore did not finish: %v — the journal stays and the next start goes on from here", ferr)
			return fmt.Errorf("an interrupted restore did not finish: %w", ferr)
		}
		return nil
	}
	raw, err := os.ReadFile(restoreRequestPath(cfg))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	var req RestoreRequest
	withdraw := func(why string) error {
		recordRestoreFailure(cfg, req, why+". Nothing was touched")
		os.Remove(restoreRequestPath(cfg))
		sweepRestoreStaging(dir)
		return nil
	}
	if err != nil {

		return withdraw("the request is here and cannot be read: " + err.Error())
	}
	if err := json.Unmarshal(raw, &req); err != nil {
		return withdraw("the request does not read")
	}
	if err := restorable(cfg); err != nil {
		return withdraw(err.Error())
	}
	if req.Identity != kp.Fingerprint() {
		return withdraw("the request was made for another identity than the one whose key is on this machine")
	}
	srcPath, snap, err := r.resolveRestoreSource(cfg, req.Source, req.Name)
	if err != nil {
		return withdraw(err.Error())
	}
	sweepRestoreStaging(dir)
	staging, err := os.MkdirTemp(dir, RestoreStagePrefix+"*")
	if err != nil {
		return withdraw("a staging directory could not be made: " + err.Error())
	}
	set := filepath.Join(staging, "set")
	through, err := r.stageAndProve(ctx, cfg, kp, srcPath, snap, set, filepath.Join(staging, "proof"))
	if err != nil {
		os.RemoveAll(staging)
		return withdraw("the source does not prove: " + err.Error())
	}
	j, err := planRestoreMoves(cfg, req, set, staging, through, kp.Fingerprint(), setAsideLabel(req.Label, kp.Fingerprint()))
	if err != nil {
		os.RemoveAll(staging)
		return withdraw(err.Error())
	}
	jr, _ := json.MarshalIndent(j, "", "  ")
	if err := writeFileDurably(filepath.Join(dir, RestoreJournalName), append(jr, '\n')); err != nil {
		os.RemoveAll(staging)
		return withdraw("the journal could not be written: " + err.Error())
	}
	if err := r.syncDir(dir); err != nil {
		os.RemoveAll(staging)
		return withdraw("the journal is not durable: " + err.Error())
	}
	r.restoreStep("journalled")
	if err := r.finishRestore(cfg, j); err != nil {
		logsink.Error("restore.error", "the restore did not finish: %v — the journal stays and the next start goes on from here", err)
		return fmt.Errorf("the restore did not finish: %w", err)
	}
	return nil
}

func JournalledRestore(cfg Settings) bool {
	_, found, err := readRestoreJournal(cfg)
	return found && err == nil
}

func readRestoreJournal(cfg Settings) (RestoreJournal, bool, error) {
	var j RestoreJournal
	dir := restoreDir(cfg)
	raw, err := os.ReadFile(filepath.Join(dir, RestoreJournalName))
	if errors.Is(err, os.ErrNotExist) {
		return j, false, nil
	}
	if err != nil {
		return j, false, fmt.Errorf("a restore journal is here and cannot be read (%w)", err)
	}
	if err := json.Unmarshal(raw, &j); err != nil {
		return j, false, fmt.Errorf("a restore journal is here and does not read (%w)", err)
	}
	if len(j.Moves) == 0 {
		return j, false, errors.New("a restore journal is here and lists nothing to do")
	}
	within := func(path, root string) bool {
		rel, err := filepath.Rel(root, filepath.Clean(path))
		return err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
	}

	switch j.Request.Source {
	case SourceSnapshot:
		if !backupSeqRe.MatchString(j.Request.Name) {
			return j, false, fmt.Errorf("a restore journal is here and %q is not a snapshot's name", j.Request.Name)
		}
	case SourceSetAside:
		if !setAsideNameRe.MatchString(j.Request.Name) {
			return j, false, fmt.Errorf("a restore journal is here and %q is not a set-aside set's name", j.Request.Name)
		}
	default:
		return j, false, fmt.Errorf("a restore journal is here and is from neither a snapshot nor a set kept aside, but from %q", j.Request.Source)
	}
	asideOK := within(j.Aside, setAsideRoot(cfg)) && filepath.Dir(filepath.Clean(j.Aside)) == filepath.Clean(setAsideRoot(cfg))
	stagingOK := j.Staging == "" || (filepath.Dir(filepath.Clean(j.Staging)) == filepath.Clean(dir) && strings.HasPrefix(filepath.Base(j.Staging), RestoreStagePrefix))
	if !asideOK || !stagingOK {
		return j, false, fmt.Errorf("a restore journal is here and names places that are not this restore's (aside %q, staging %q)", j.Aside, j.Staging)
	}

	mine := func(path, root string) (string, bool) {
		rel, err := filepath.Rel(root, filepath.Clean(path))
		if err != nil || !within(path, root) {
			return "", false
		}
		rel = filepath.ToSlash(rel)
		switch {
		case rel == filepath.Base(cfg.Identity.DBPath), rel == filepath.Base(cfg.Identity.LedgerPath), rel == witness.TailFileName:
		case strings.HasPrefix(rel, filepath.Base(cfg.Identity.DBPath)+"-") && isDatabaseSidecar(SnapshotDB+strings.TrimPrefix(rel, filepath.Base(cfg.Identity.DBPath))):
		case segmentNameRe.MatchString(rel):
		case strings.HasPrefix(rel, witness.WitnessKeysDirName+"/") && strings.HasSuffix(rel, ".json") && strings.Count(rel, "/") == 1:
		default:
			return "", false
		}
		return rel, true
	}
	for i, m := range j.Moves {
		var ok bool
		switch m.kind(j.Aside) {
		case MovePreserve, MoveCopy:
			from, fromOK := mine(m.From, dir)
			rel, relErr := filepath.Rel(j.Aside, filepath.Clean(m.To))
			ok = fromOK && relErr == nil && filepath.ToSlash(rel) == setName(cfg, from)
		default:
			_, toOK := mine(m.To, dir)
			ok = toOK && j.Staging != "" && within(m.From, filepath.Join(j.Staging, "set"))
		}
		if !ok {
			return j, false, fmt.Errorf("a restore journal is here and its move %d runs outside this restore (%q to %q)", i, m.From, m.To)
		}
	}
	return j, true, nil
}

func (r *Runtime) stageAndProve(ctx context.Context, cfg Settings, kp *crypto.KeyPair, srcPath string, snap *keptSnapshot, set, scratch string) (uint64, error) {
	if snap != nil && snap.Encrypted {
		key, err := os.ReadFile(r.snapshotKeyPath(cfg))
		if err != nil {
			return 0, fmt.Errorf("it is encrypted and the snapshot key on this machine does not read: %w", err)
		}
		defer escrow.Wipe(key)
		present, err := escrow.OpenSnapshot(srcPath, key, set)
		if err != nil {
			return 0, fmt.Errorf("it does not open: %w", err)
		}
		if err := escrow.CheckSums(set, present); err != nil {
			return 0, err
		}
	} else {
		if err := copySetTree(srcPath, set); err != nil {
			return 0, fmt.Errorf("copy: %w", err)
		}
		if snap != nil {
			present, err := filesUnder(set)
			if err != nil {
				return 0, err
			}
			if err := escrow.CheckSums(set, present); err != nil {
				return 0, err
			}
		}
	}
	if snap != nil {
		raw, err := os.ReadFile(filepath.Join(set, snapshotReceiptName))
		if err != nil {
			return 0, fmt.Errorf("it holds no receipt, so it holds no database to restore with its record: %w", err)
		}
		var want snapshotReceipt
		if err := json.Unmarshal(raw, &want); err != nil {
			return 0, fmt.Errorf("its receipt does not read: %w", err)
		}
		if want.Identity != kp.Fingerprint() {
			return 0, fmt.Errorf("it is identity %s's, and the key on this machine is %s's", want.Identity, kp.Fingerprint())
		}
		if err := r.proveSnapshotIn(ctx, cfg, set, want, scratch); err != nil {
			return 0, err
		}
		return want.LastSeq, nil
	}

	beside, err := besideFor(cfg, set)
	if err != nil {
		return 0, err
	}
	var verified store.MirrorHead
	n, fp, err := genesis.VerifyHeld(filepath.Join(set, "ledger.jsonl"), beside, func(string) func(*ledger.Event) error {
		return func(evt *ledger.Event) error {
			verified = store.MirrorHead{Seq: evt.Seq, Hash: evt.EntryHash()}
			return nil
		}
	})
	if err != nil {
		return 0, fmt.Errorf("%w; witness: %s", err, beside.Summary())
	}
	if fp != kp.Fingerprint() {
		return 0, fmt.Errorf("it is identity %s's, and the key on this machine is %s's", fp, kp.Fingerprint())
	}
	head, _, err := store.ProveRestore(ctx, filepath.Join(set, SnapshotDB), filepath.Join(set, "ledger.jsonl"), scratch, verified)
	if err != nil {
		return 0, fmt.Errorf("it does not restore: %w", err)
	}
	if head.Seq != uint64(n) {
		return 0, fmt.Errorf("restored, its database ends at record %d and its record at %d", head.Seq, n)
	}
	return uint64(n), nil
}

func setAsideLabel(name, fingerprint string) string {
	var b strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == ' ', r == '-', r == '_':
			b.WriteRune('-')
		}
	}
	label := strings.Trim(b.String(), "-")
	if label == "" {
		label = "identity-" + fingerprint[:12]
	}
	if len(label) > 40 {
		label = label[:40]
	}
	return label
}

func planRestoreMoves(cfg Settings, req RestoreRequest, set, staging string, through uint64, fp, label string) (RestoreJournal, error) {
	dir := restoreDir(cfg)
	now := time.Now().UTC()
	aside := filepath.Join(setAsideRoot(cfg), fmt.Sprintf("%s-%s-before-restore-to-seq%d", label, now.Format("20060102T150405Z"), through))
	j := RestoreJournal{Request: req, Aside: aside, Staging: staging,
		Restored: RestoreRecord{SetAsideAt: now.Format(time.RFC3339), Identity: fp, RestoredFrom: req.Name, RestoredTo: through, Witnessed: -1}}

	staged, err := filesUnder(set)
	if err != nil {
		return j, err
	}

	if tail, err := witness.ReadLocalTail(dir); err == nil && tail != nil {
		j.Restored.Witnessed = tail.LedgerOrdinal
	}
	if last, err := lastSeqOfTail(cfg.Identity.LedgerPath); err == nil {
		j.Restored.WasThrough = last
	}

	live := []string{filepath.Base(cfg.Identity.DBPath)}
	for _, side := range databaseSidecars {
		live = append(live, filepath.Base(cfg.Identity.DBPath)+side)
	}
	live = append(live, filepath.Base(cfg.Identity.LedgerPath), witness.TailFileName)
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if !e.IsDir() && segmentNameRe.MatchString(e.Name()) {
			live = append(live, e.Name())
		}
	}
	for _, name := range live {
		if _, err := os.Lstat(filepath.Join(dir, name)); err != nil {
			continue
		}
		j.Moves = append(j.Moves, RestoreMove{Kind: MovePreserve,
			From: filepath.Join(dir, name), To: filepath.Join(aside, setName(cfg, name))})
	}
	if keys, err := os.ReadDir(witness.WitnessKeysDir(dir)); err == nil {
		for _, e := range keys {
			n := e.Name()
			if e.IsDir() || strings.HasPrefix(n, ".") || !strings.HasSuffix(n, ".json") {
				continue
			}
			j.Moves = append(j.Moves, RestoreMove{Kind: MoveCopy,
				From: filepath.Join(witness.WitnessKeysDir(dir), n), To: filepath.Join(aside, witness.WitnessKeysDirName, n)})
		}
	}
	for _, rel := range staged {
		var to string
		switch {
		case rel == SnapshotDB:
			to = cfg.Identity.DBPath
		case rel == "ledger.jsonl":
			to = cfg.Identity.LedgerPath
		case rel == witness.TailFileName:

			to = filepath.Join(dir, witness.TailFileName)
		case segmentNameRe.MatchString(rel):
			to = filepath.Join(dir, rel)
		case isDatabaseSidecar(rel):

			to = cfg.Identity.DBPath + strings.TrimPrefix(rel, SnapshotDB)
		case strings.HasPrefix(rel, witness.WitnessKeysDirName+"/"):
			to = filepath.Join(dir, filepath.FromSlash(rel))
		default:
			continue
		}
		j.Moves = append(j.Moves, RestoreMove{Kind: MoveInstall,
			From: filepath.Join(set, filepath.FromSlash(rel)), To: to})
	}
	return j, nil
}

var segmentNameRe = regexp.MustCompile(`^segment-[0-9]+-[0-9]+\.jsonl\.gz$`)

func (r *Runtime) finishRestore(cfg Settings, j RestoreJournal) error {
	dir := restoreDir(cfg)

	made, performable, err := journalMade(j)
	if err != nil {
		return err
	}
	if !performable {
		return r.withdrawJournal(cfg, j, made)
	}
	if err := os.MkdirAll(j.Aside, 0o700); err != nil {
		return fmt.Errorf("the set-aside directory could not be made: %w", err)
	}
	for i, m := range j.Moves {
		kind := m.kind(j.Aside)
		_, toErr := os.Lstat(m.To)
		_, fromErr := os.Lstat(m.From)
		switch kind {
		case MovePreserve, MoveCopy:

			if toErr == nil {
				continue
			}
			if errors.Is(fromErr, os.ErrNotExist) {

				return fmt.Errorf("move %d: %s is neither where it was nor where it was to be kept", i, filepath.Base(m.From))
			}
			if err := os.MkdirAll(filepath.Dir(m.To), 0o700); err != nil {
				return fmt.Errorf("move %d: %w", i, err)
			}
			if kind == MoveCopy {
				if err := copyFile(m.From, m.To); err != nil {
					return fmt.Errorf("move %d (%s): %w", i, filepath.Base(m.From), err)
				}
			} else if err := os.Rename(m.From, m.To); err != nil {
				return fmt.Errorf("move %d (%s): %w", i, filepath.Base(m.From), err)
			}
		default:
			if errors.Is(fromErr, os.ErrNotExist) {

				if toErr == nil {
					continue
				}
				return fmt.Errorf("move %d: %s was to be put in place and is neither here nor staged — the restore is incomplete", i, filepath.Base(m.To))
			}
			if toErr == nil {

				if same, _ := sameFile(m.From, m.To); !same {
					return fmt.Errorf("move %d: %s is in place already and is not what this restore would put there", i, filepath.Base(m.To))
				}
				continue
			}
			if err := os.MkdirAll(filepath.Dir(m.To), 0o700); err != nil {
				return fmt.Errorf("move %d: %w", i, err)
			}
			if err := os.Rename(m.From, m.To); err != nil {
				return fmt.Errorf("move %d (%s): %w", i, filepath.Base(m.From), err)
			}
		}
		r.restoreStep(fmt.Sprintf("moved:%d", i))
	}
	if err := r.syncDir(dir); err != nil {
		return fmt.Errorf("the restored directory is not durable: %w", err)
	}
	if err := r.syncDir(j.Aside); err != nil {
		return fmt.Errorf("what was set aside is not durable: %w", err)
	}
	r.restoreStep("moved")

	if held, _ := os.ReadDir(j.Aside); len(held) == 0 {
		os.Remove(j.Aside)
		j.Aside = "(nothing was here to set aside)"
	} else {
		rec, _ := json.MarshalIndent(j.Restored, "", "  ")
		if err := writeFileDurably(filepath.Join(j.Aside, RestoreRecordName), append(rec, '\n')); err != nil {
			return fmt.Errorf("the set-aside set's own record could not be written: %w", err)
		}
	}
	if j.Request.Source == SourceSetAside {
		src := filepath.Join(setAsideRoot(cfg), j.Request.Name, RestoreRecordName)
		var was RestoreRecord
		if raw, err := os.ReadFile(src); err == nil && json.Unmarshal(raw, &was) == nil {
			was.PutBackAt = j.Restored.SetAsideAt
			if upd, merr := json.MarshalIndent(was, "", "  "); merr == nil {
				if err := writeFileDurably(src, append(upd, '\n')); err != nil {
					logsink.Error("restore.error", "the set-aside record %s does not say it was put back: %v", src, err)
				}
			}
		}
	}

	settle, _ := json.MarshalIndent(RestoreSettlement{At: time.Now().UTC(), Source: j.Request.Source, Name: j.Request.Name, Earlier: earlierSettlement(cfg, j)}, "", "  ")
	if err := writeFileDurably(restoreSettlePath(cfg), append(settle, '\n')); err != nil {
		return fmt.Errorf("what the next start owes the messages this restore brought back could not be written: %w", err)
	}
	r.restoreStep("cleanup")

	for _, gone := range []string{restoreRequestPath(cfg), filepath.Join(dir, RestoreFailedName), newMachineMarkerPath(cfg), filepath.Join(dir, RestoreJournalName)} {
		if err := os.Remove(gone); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("%s outlived the restore it belongs to: %w", filepath.Base(gone), err)
		}
	}
	if j.Staging != "" && strings.HasPrefix(filepath.Base(j.Staging), RestoreStagePrefix) {
		os.RemoveAll(j.Staging)
	}
	if err := r.syncDir(dir); err != nil {
		return fmt.Errorf("the restored directory is not durable: %w", err)
	}
	logsink.Warn("restore.end", "RESTORED from %s: the record now ends at %d. What was live (through record %d) is set aside, whole, in %s", j.Request.Name, j.Restored.RestoredTo, j.Restored.WasThrough, j.Aside)
	return nil
}

const notCompletedDirName = "not-completed"

func journalMade(j RestoreJournal) (made []bool, performable bool, err error) {
	present := func(path string) (bool, error) {
		_, err := os.Lstat(path)
		if err == nil || errors.Is(err, os.ErrNotExist) {
			return err == nil, nil
		}
		return false, fmt.Errorf("whether %s is in place cannot be told (%w), so nothing of the restore was moved", filepath.Base(path), err)
	}
	made = make([]bool, len(j.Moves))
	keptFrom := map[string]int{}
	for i, m := range j.Moves {
		if m.kind(j.Aside) == MoveInstall {
			continue
		}
		keptFrom[filepath.Clean(m.From)] = i
		if made[i], err = present(m.To); err != nil {
			return nil, false, err
		}
	}
	performable = true
	for i, m := range j.Moves {
		if m.kind(j.Aside) != MoveInstall {
			continue
		}
		staged, err := present(m.From)
		if err != nil {
			return nil, false, err
		}
		if staged {
			continue
		}
		there, err := present(m.To)
		if err != nil {
			return nil, false, err
		}
		if k, kept := keptFrom[filepath.Clean(m.To)]; kept {
			made[i] = there && j.Moves[k].kind(j.Aside) == MovePreserve && made[k]
		} else {
			made[i] = there
		}
		performable = performable && made[i]
	}
	return made, performable, nil
}

func (r *Runtime) withdrawJournal(cfg Settings, j RestoreJournal, made []bool) error {
	dir := restoreDir(cfg)
	notCompleted := filepath.Join(j.Aside, notCompletedDirName)
	cannot := func(format string, args ...any) error {
		return fmt.Errorf("the restore cannot be completed — the files it staged are gone from %s — and it cannot be put back: %s; nothing more was moved", j.Staging, fmt.Sprintf(format, args...))
	}
	free := func(path string) error {
		_, err := os.Lstat(path)
		switch {
		case err == nil:
			return errors.New("its place is taken by something this restore did not put there")
		case errors.Is(err, os.ErrNotExist):
			return nil
		}
		return fmt.Errorf("whether its place is free cannot be told (%w)", err)
	}
	type rename struct{ from, to string }
	var back []rename
	vacated, claimed := map[string]bool{}, map[string]bool{}
	for i := len(j.Moves) - 1; i >= 0; i-- {
		m := j.Moves[i]
		kind := m.kind(j.Aside)
		if !made[i] {
			if kind == MoveInstall {
				continue
			}
			if _, err := os.Lstat(m.From); errors.Is(err, os.ErrNotExist) {
				return cannot("move %d: %s is neither where it was nor where it was to be kept", i, filepath.Base(m.From))
			} else if err != nil {
				return cannot("move %d: whether %s is where it was cannot be told (%v)", i, filepath.Base(m.From), err)
			}
			continue
		}
		var to string
		if kind == MovePreserve {
			to = filepath.Clean(m.From)
			if err := free(to); err != nil && !vacated[to] {
				return cannot("move %d: %s was set aside, and %v", i, filepath.Base(m.From), err)
			}
		} else {
			base := dir
			if kind == MoveCopy {
				base = j.Aside
			}
			rel, err := filepath.Rel(base, filepath.Clean(m.To))
			if err != nil {
				return cannot("move %d: %v", i, err)
			}
			to = filepath.Join(notCompleted, rel)
			if err := free(to); err != nil || claimed[to] {
				return cannot("move %d: %s cannot be kept in %s: %v", i, filepath.Base(m.To), notCompleted, cmp.Or(err, errors.New("another file of this restore is to be kept under that name")))
			}
			vacated[filepath.Clean(m.To)] = true
		}
		claimed[to] = true
		back = append(back, rename{m.To, to})
	}

	touched := map[string]bool{}
	for _, b := range back {
		if err := os.MkdirAll(filepath.Dir(b.to), 0o700); err != nil {
			return fmt.Errorf("putting back a restore that cannot be completed: %w — the journal stays and the next start goes on from here", err)
		}
		if err := os.Rename(b.from, b.to); err != nil {
			return fmt.Errorf("putting back a restore that cannot be completed: %w — the journal stays and the next start goes on from here", err)
		}
		touched[filepath.Dir(b.from)] = true

		for d := filepath.Dir(b.to); ; d = filepath.Dir(d) {
			touched[d] = true
			if d == filepath.Clean(j.Aside) || !strings.HasPrefix(d, filepath.Clean(j.Aside)+string(filepath.Separator)) {
				break
			}
		}
	}
	dirs := make([]string, 0, len(touched))
	for d := range touched {
		dirs = append(dirs, d)
	}
	sort.Strings(dirs)
	for _, d := range dirs {
		if err := r.syncDir(d); err != nil {
			return fmt.Errorf("what was put back is not durable (%s): %w — the journal stays and the next start goes on from here", d, err)
		}
	}

	if err := r.retireFinishedMarks(cfg, j); err != nil {
		return err
	}

	os.Remove(filepath.Join(j.Aside, witness.WitnessKeysDirName))
	os.Remove(j.Aside)

	why := "the files it had staged and proved are gone from " + j.Staging
	_, kerr := os.Stat(notCompleted)
	kept := kerr == nil
	if len(back) == 0 && !kept {
		why += ". Nothing was touched"
	} else {
		why += ", and it had begun: it was rolled back, and everything it had set aside is back in its place"
		if kept {
			why += "; what it had already put in place is kept, not deleted, in " + notCompleted
		}
	}
	if _, err := os.Stat(j.Aside); err == nil {
		if err := writeFileDurably(filepath.Join(j.Aside, RestoreFailedName), restoreFailureJSON(j.Request, why)); err != nil {
			return fmt.Errorf("the record of what %s holds could not be written: %w — the journal stays and the next start goes on from here", j.Aside, err)
		}
	}
	recordRestoreFailure(cfg, j.Request, why)
	for _, gone := range []string{restoreRequestPath(cfg), filepath.Join(dir, RestoreJournalName)} {
		if err := os.Remove(gone); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("%s outlived the restore it belongs to, which was withdrawn: %w", filepath.Base(gone), err)
		}
	}
	if err := r.syncDir(dir); err != nil {
		return fmt.Errorf("the withdrawal of the restore is not durable: %w", err)
	}
	sweepRestoreStaging(dir)
	return nil
}

func (r *Runtime) retireFinishedMarks(cfg Settings, j RestoreJournal) error {
	stays := func(what string, err error) error {
		return fmt.Errorf("%s of a restore that was rolled back could not be taken back: %w — the journal stays and the next start goes on from here", what, err)
	}
	if err := os.Remove(filepath.Join(j.Aside, RestoreRecordName)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return stays("the set-aside folder's record", err)
	}
	if j.Request.Source == SourceSetAside {
		src := filepath.Join(setAsideRoot(cfg), j.Request.Name, RestoreRecordName)
		var was RestoreRecord
		if raw, err := os.ReadFile(src); err == nil && json.Unmarshal(raw, &was) == nil && was.PutBackAt != "" && was.PutBackAt == j.Restored.SetAsideAt {
			was.PutBackAt = ""
			upd, err := json.MarshalIndent(was, "", "  ")
			if err == nil {
				err = writeFileDurably(src, append(upd, '\n'))
			}
			if err != nil {
				return stays("the mark on the set it was to be put back from", err)
			}
		}
	}
	s, ok, err := ReadRestoreSettlement(cfg)
	if err != nil || !ok || !ownSettlement(s, j) {
		return nil
	}
	if s.Earlier != "" {

		if err := writeFileDurably(restoreSettlePath(cfg), []byte(s.Earlier)); err != nil {
			return stays("the settlement, which an earlier restore's goes back in place of,", err)
		}
		return nil
	}
	if err := os.Remove(restoreSettlePath(cfg)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return stays("the settlement", err)
	}

	if err := r.syncDir(restoreDir(cfg)); err != nil {
		return stays("the settlement", err)
	}
	return nil
}

type RestoreSettlement struct {
	At     time.Time `json:"at"`
	Source string    `json:"source"`
	Name   string    `json:"name"`

	Earlier string `json:"earlier,omitempty"`
}

func ownSettlement(s RestoreSettlement, j RestoreJournal) bool {
	planned, _ := time.Parse(time.RFC3339, j.Restored.SetAsideAt)
	return s.Source == j.Request.Source && s.Name == j.Request.Name && !s.At.Before(planned)
}

func earlierSettlement(cfg Settings, j RestoreJournal) string {
	raw, err := os.ReadFile(restoreSettlePath(cfg))
	if err != nil {
		return ""
	}
	var s RestoreSettlement
	if json.Unmarshal(raw, &s) == nil && ownSettlement(s, j) {
		return s.Earlier
	}
	return string(raw)
}

func restoreSettlePath(cfg Settings) string {
	return filepath.Join(restoreDir(cfg), RestoreSettleName)
}

func ReadRestoreSettlement(cfg Settings) (RestoreSettlement, bool, error) {
	var s RestoreSettlement
	raw, err := os.ReadFile(restoreSettlePath(cfg))
	if errors.Is(err, os.ErrNotExist) {
		return s, false, nil
	}
	if err != nil {
		return s, false, fmt.Errorf("a restore's settlement is here and cannot be read (%w), so the messages it brought back are not sent", err)
	}
	if err := json.Unmarshal(raw, &s); err != nil {
		return s, false, fmt.Errorf("a restore's settlement is here and does not read (%w), so the messages it brought back are not sent", err)
	}
	if s.At.IsZero() {
		return s, false, errors.New("a restore's settlement is here and names no instant, so the messages it brought back are not sent")
	}
	return s, true, nil
}

func (r *Runtime) SettleRestore(cfg Settings, s RestoreSettlement) error {
	if r.w.Safe() {
		return nil
	}
	r.restoreStep("settle")
	restore := store.Restore{At: s.At, Source: s.Source, SourceAt: restoreSourceAt(s.Source, s.Name)}
	n, err := r.w.Store().SettleRestore(restore, RestoredFrom(restore)+", in which it was still waiting to be sent — it may already have been sent before the restore")
	if err != nil {
		return fmt.Errorf("the messages a restore brought back could not be settled, so nothing that could send them is started: %w", err)
	}
	r.restoreStep("settled")
	if err := os.Remove(restoreSettlePath(cfg)); err != nil && !errors.Is(err, os.ErrNotExist) {
		logsink.Warn("restore.error", "%s outlived its settlement (%v) — the next start finds the restore recorded and changes nothing", RestoreSettleName, err)
	} else {
		_ = r.syncDir(restoreDir(cfg))
	}
	logsink.Warn("restore.end", "%s: %d message(s) to people that were waiting in it are parked as possibly sent, not sent again, and the identity is told", RestoredFrom(restore), n)
	return nil
}

func restoreSourceAt(source, name string) time.Time {
	var m []string
	switch source {
	case SourceSnapshot:
		m = backupSeqRe.FindStringSubmatch(name)
	case SourceSetAside:
		m = setAsideNameRe.FindStringSubmatch(name)
	}
	if m == nil {
		return time.Time{}
	}
	at, _ := time.Parse("20060102T150405Z", m[1])
	return at
}

func RestoredFrom(r store.Restore) string {
	what, when := "a snapshot", " of "
	if r.Source == SourceSetAside {
		what, when = "a set kept aside", " at "
	}
	if r.SourceAt.IsZero() {
		return "restored from " + what
	}
	return "restored from " + what + when + r.SourceAt.UTC().Format("2006-01-02 15:04 MST")
}

func sameFile(a, b string) (bool, error) {
	x, err := os.ReadFile(a)
	if err != nil {
		return false, err
	}
	y, err := os.ReadFile(b)
	if err != nil {
		return false, err
	}
	return bytes.Equal(x, y), nil
}

func sweepRestoreStaging(dir string) {
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if e.IsDir() && strings.HasPrefix(e.Name(), RestoreStagePrefix) {
			os.RemoveAll(filepath.Join(dir, e.Name()))
		}
	}
}

func lastSeqOfTail(path string) (uint64, error) {
	var last uint64
	err := ledger.Stream(path, func(evt *ledger.Event) error {
		last = evt.Seq
		return nil
	})
	if err != nil {
		return 0, err
	}
	if last == 0 {
		return 0, errors.New("the record holds nothing")
	}
	return last, nil
}

func copySetTree(src, dst string) error {
	files, err := filesUnder(src)
	if err != nil {
		return err
	}
	if _, err := os.Stat(filepath.Join(src, escrow.SumsName)); err == nil {
		files = append(files, escrow.SumsName)
	}
	for _, rel := range files {
		to := filepath.Join(dst, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(to), 0o700); err != nil {
			return err
		}
		if err := copyFile(filepath.Join(src, filepath.FromSlash(rel)), to); err != nil {
			return err
		}
	}
	return nil
}

func SetAsideIn(dir string) (string, error) {
	root := filepath.Join(dir, setAsideDirName)
	entries, err := os.ReadDir(root)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("could not list the sets kept aside at %s: %w", root, err)
	}
	for _, e := range entries {
		if e.IsDir() && setAsideNameRe.MatchString(e.Name()) {
			return filepath.Join(root, e.Name()), nil
		}
	}
	return "", nil
}

func setAsideSets(cfg Settings) []SetAside {
	entries, _ := os.ReadDir(setAsideRoot(cfg))
	var out []SetAside
	for _, e := range entries {
		if !e.IsDir() || !setAsideNameRe.MatchString(e.Name()) {
			continue
		}
		path := filepath.Join(setAsideRoot(cfg), e.Name())

		if _, err := os.Stat(filepath.Join(path, RestoreRecordName)); errors.Is(err, os.ErrNotExist) {
			if _, err := os.Stat(filepath.Join(path, RestoreFailedName)); err == nil {
				continue
			}
		}
		s := SetAside{Name: e.Name(), Path: path, Bytes: dirBytes(path)}
		if raw, err := os.ReadFile(filepath.Join(path, RestoreRecordName)); err == nil {
			_ = json.Unmarshal(raw, &s.Record)
		}
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Record.SetAsideAt > out[j].Record.SetAsideAt })
	return out
}

type SetAside struct {
	Name   string
	Path   string
	Bytes  int64
	Record RestoreRecord
}

func dirBytes(root string) (n int64) {
	filepath.WalkDir(root, func(_ string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			if info, ierr := d.Info(); ierr == nil {
				n += info.Size()
			}
		}
		return nil
	})
	return n
}

type RestorePlan struct {
	Source, Name      string
	At                string
	RestoredTo        uint64
	LiveThrough       int64
	NoLiveRecord      bool
	SetAside          int64
	Witnessed         int64
	WitnessHash       string
	WitnessedSetAside int64
	WitnessURL        string
	Identity          string
	ConfirmText       string
	AsideUnder        string
	Encrypted         bool
}

func (r *Runtime) restorePlan(source, name string) (RestorePlan, error) {
	cfg := r.w.Settings()
	if err := restorable(cfg); err != nil {
		return RestorePlan{}, err
	}
	path, snap, err := r.resolveRestoreSource(cfg, source, name)
	if err != nil {
		return RestorePlan{}, errors.New("That is not in the list any more. Reload the page.")
	}
	p := RestorePlan{Source: source, Name: name, LiveThrough: -1, SetAside: -1, Witnessed: -1,
		WitnessURL: cfg.WitnessURL, ConfirmText: r.restoreConfirmText(), AsideUnder: setAsideRoot(cfg)}
	if r.w.Key() != nil {
		p.Identity = r.w.Key().Fingerprint()
	}
	if snap != nil {
		p.At, p.RestoredTo, p.Encrypted = snap.at().UTC().Format(time.RFC3339), snap.Record, snap.Encrypted
	} else {
		var rec RestoreRecord
		if raw, rerr := os.ReadFile(filepath.Join(path, RestoreRecordName)); rerr == nil {
			_ = json.Unmarshal(raw, &rec)
		}
		p.At, p.RestoredTo = rec.SetAsideAt, rec.WasThrough
		if last, lerr := lastSeqOfTail(filepath.Join(path, "ledger.jsonl")); lerr == nil {
			p.RestoredTo = last
		}
	}
	if live, ok := r.w.LastSeq(); ok {
		p.LiveThrough = int64(live)
	} else if last, lerr := lastSeqOfTail(cfg.Identity.LedgerPath); lerr == nil {
		p.LiveThrough = int64(last)
	}
	if p.LiveThrough >= 0 {
		p.SetAside = max(0, p.LiveThrough-int64(p.RestoredTo))
	} else if artifact, aerr := ledger.RecordArtifact(cfg.Identity.LedgerPath); aerr == nil && artifact == "" {

		p.NoLiveRecord, p.SetAside = true, 0
	}

	if tail, terr := witness.ReadLocalTail(restoreDir(cfg)); terr == nil && tail != nil {
		p.Witnessed, p.WitnessHash = tail.LedgerOrdinal, tail.LedgerHash
	} else if attested, hash := r.w.BootAttested(); attested >= 0 {
		p.Witnessed, p.WitnessHash = attested, hash
	}
	if p.Witnessed >= 0 {
		p.WitnessedSetAside = max(0, p.Witnessed-int64(p.RestoredTo))
	}
	return p, nil
}

func (r *Runtime) restoreConfirmText() string {
	if name := strings.TrimSpace(r.w.DisplayName()); name != "" {
		return name
	}
	return "RESTORE"
}

func (r *Runtime) requestRestore(source, name, typed string) error {
	cfg := r.w.Settings()
	if err := restorable(cfg); err != nil {
		return err
	}
	if strings.TrimSpace(typed) != r.restoreConfirmText() {
		return fmt.Errorf("To confirm, type %q exactly.", r.restoreConfirmText())
	}
	if _, _, err := r.resolveRestoreSource(cfg, source, name); err != nil {
		return errors.New("That is not in the list any more. Reload the page.")
	}
	if r.w.Key() == nil {
		return errors.New("The identity's key is not loaded in this start, so a restore cannot be held to it.")
	}
	req := RestoreRequest{Source: source, Name: name, RequestedAt: time.Now().UTC().Format(time.RFC3339), Identity: r.w.Key().Fingerprint(), Label: r.w.DisplayName()}
	raw, _ := json.MarshalIndent(req, "", "  ")
	if err := writeFileDurably(restoreRequestPath(cfg), append(raw, '\n')); err != nil {
		logsink.Error("restore.error", "the restore request could not be written: %v", err)
		return errors.New("The request could not be written on this machine, so nothing will be restored.")
	}
	_ = r.syncDir(restoreDir(cfg))
	logsink.Warn("restore.decision", "a person asked for a restore from %s %s; the next start performs it before the record is opened", source, name)
	return nil
}

func lastRestoreFailure(cfg Settings) *restoreFailure {
	raw, err := os.ReadFile(filepath.Join(restoreDir(cfg), RestoreFailedName))
	if err != nil {
		return nil
	}
	var f restoreFailure
	if json.Unmarshal(raw, &f) != nil {
		return nil
	}
	return &f
}
