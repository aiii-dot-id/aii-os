package app

import (
	"bytes"
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
// .
// .
// .
// .
// .
// .
// .
// .

const (
	restoreRequestName = "restore-request.json"
	restoreJournalName = "restore-journal.json"
	restoreFailedName  = "restore-failed.json"
	restoreRecordName  = "RESTORE.json"
	setAsideDirName    = "set-aside"
	restoreStagePrefix = ".restore-staging-"

	sourceSnapshot = "snapshot"
	sourceSetAside = "set-aside"
)

// .
type restoreRequest struct {
	Source      string `json:"source"`
	Name        string `json:"name"`
	RequestedAt string `json:"requested_at"`
	Identity    string `json:"identity"`
	// .
	// .
	// .
	Label string `json:"label,omitempty"`
}

// .
// .
// .
// .
func setName(cfg Config, live string) string {
	db, lg := filepath.Base(cfg.Identity.DBPath), filepath.Base(cfg.Identity.LedgerPath)
	switch {
	case live == lg:
		return "ledger.jsonl"
	case live == db:
		return snapshotDB
	case strings.HasPrefix(live, db+"-") && isDatabaseSidecar(snapshotDB+strings.TrimPrefix(live, db)):
		return snapshotDB + strings.TrimPrefix(live, db)
	}
	return live
}

// .
// .
// .
// .
// .
// .
var databaseSidecars = []string{"-wal", "-shm", "-journal"}

func isDatabaseSidecar(rel string) bool {
	for _, side := range databaseSidecars {
		if rel == snapshotDB+side {
			return true
		}
	}
	return false
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
// .
// .
// .
// .
// .
// .
// .
type restoreMove struct {
	From string `json:"from"`
	To   string `json:"to"`
	Kind string `json:"kind"`
}

// .
// .
// .
const (
	movePreserve = "preserve"
	moveCopy     = "copy"
	moveInstall  = "install"
)

func (m restoreMove) kind(aside string) string {
	if m.Kind != "" {
		return m.Kind
	}
	if strings.HasPrefix(m.To, aside) {
		return movePreserve
	}
	return moveInstall
}

// .
type restoreJournal struct {
	Request  restoreRequest `json:"request"`
	Aside    string         `json:"aside"`
	Staging  string         `json:"staging"`
	Moves    []restoreMove  `json:"moves"`
	Restored RestoreRecord  `json:"restored"`
}

// .
// .
type RestoreRecord struct {
	SetAsideAt   string `json:"set_aside_at"`
	Identity     string `json:"identity"`
	WasThrough   uint64 `json:"was_through"`
	RestoredFrom string `json:"restored_from"`
	RestoredTo   uint64 `json:"restored_to"`
	Witnessed    int64  `json:"witnessed"`
	// .
	// .
	// .
	PutBackAt string `json:"put_back_at,omitempty"`
}

// .
// .
type restoreFailure struct {
	At      string         `json:"at"`
	Request restoreRequest `json:"request"`
	Why     string         `json:"why"`
}

var setAsideNameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,80}-[0-9]{8}T[0-9]{6}Z-before-restore-to-seq[0-9]+$`)

func restoreDir(cfg Config) string         { return filepath.Dir(cfg.Identity.LedgerPath) }
func setAsideRoot(cfg Config) string       { return filepath.Join(restoreDir(cfg), setAsideDirName) }
func restoreRequestPath(cfg Config) string { return filepath.Join(restoreDir(cfg), restoreRequestName) }

// .
// .
func restorable(cfg Config) error {
	if filepath.Dir(cfg.Identity.DBPath) != filepath.Dir(cfg.Identity.LedgerPath) {
		return errors.New("This identity keeps its database and its record in different directories, which a restore from the page does not handle. Restore by the procedure in the maintenance guide.")
	}
	return nil
}

// .
// .
// .
func (a *App) resolveRestoreSource(cfg Config, source, name string) (path string, snap *keptSnapshot, err error) {
	switch source {
	case sourceSnapshot:
		kept, _ := keptSnapshots(a.backupsDir(cfg))
		for i := range kept {
			if kept[i].Name == name {
				return filepath.Join(a.backupsDir(cfg), kept[i].Name), &kept[i], nil
			}
		}
		return "", nil, fmt.Errorf("%q is not one of the snapshots kept here", name)
	case sourceSetAside:
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

// .
// .
func (a *App) performPendingRestore(ctx context.Context, cfg Config, kp *crypto.KeyPair) error {
	dir := restoreDir(cfg)
	// .
	// .
	// .
	// .
	// .
	resumed, found, jerr := readRestoreJournal(cfg)
	if jerr != nil {
		return fmt.Errorf("%w — what it describes may be half in place, so the record is not opened; it is in %s", jerr, dir)
	}
	if found {
		logsink.Warn("restore.decision", "a restore was interrupted after it was journalled — rolling it forward (everything it moves was proved before the journal was written)")
		if ferr := a.finishRestore(cfg, resumed); ferr != nil {
			logsink.Error("restore.error", "the interrupted restore did not finish: %v — the journal stays and the next start goes on from here", ferr)
			return fmt.Errorf("an interrupted restore did not finish: %w", ferr)
		}
		return nil
	}
	raw, err := os.ReadFile(restoreRequestPath(cfg))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	var req restoreRequest
	withdraw := func(why string) error {
		logsink.Error("restore.refusal", "the restore that was asked for was NOT performed: %s — nothing was touched", why)
		f, _ := json.MarshalIndent(restoreFailure{At: time.Now().UTC().Format(time.RFC3339), Request: req, Why: why}, "", "  ")
		_ = writeFileDurably(filepath.Join(dir, restoreFailedName), append(f, '\n'))
		os.Remove(restoreRequestPath(cfg))
		sweepRestoreStaging(dir)
		return nil
	}
	if err != nil {
		// .
		// .
		// .
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
	srcPath, snap, err := a.resolveRestoreSource(cfg, req.Source, req.Name)
	if err != nil {
		return withdraw(err.Error())
	}
	sweepRestoreStaging(dir)
	staging, err := os.MkdirTemp(dir, restoreStagePrefix+"*")
	if err != nil {
		return withdraw("a staging directory could not be made: " + err.Error())
	}
	set := filepath.Join(staging, "set")
	through, err := a.stageAndProve(ctx, cfg, kp, srcPath, snap, set, filepath.Join(staging, "proof"))
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
	if err := writeFileDurably(filepath.Join(dir, restoreJournalName), append(jr, '\n')); err != nil {
		os.RemoveAll(staging)
		return withdraw("the journal could not be written: " + err.Error())
	}
	if err := syncDir(dir); err != nil {
		os.RemoveAll(staging)
		return withdraw("the journal is not durable: " + err.Error())
	}
	restoreStep("journalled")
	if err := a.finishRestore(cfg, j); err != nil {
		logsink.Error("restore.error", "the restore did not finish: %v — the journal stays and the next start goes on from here", err)
		return fmt.Errorf("the restore did not finish: %w", err)
	}
	return nil
}

// .
// .
// .
func journalledRestore(cfg Config) bool {
	_, found, err := readRestoreJournal(cfg)
	return found && err == nil
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
func readRestoreJournal(cfg Config) (restoreJournal, bool, error) {
	var j restoreJournal
	dir := restoreDir(cfg)
	raw, err := os.ReadFile(filepath.Join(dir, restoreJournalName))
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
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	switch j.Request.Source {
	case sourceSnapshot:
		if !backupSeqRe.MatchString(j.Request.Name) {
			return j, false, fmt.Errorf("a restore journal is here and %q is not a snapshot's name", j.Request.Name)
		}
	case sourceSetAside:
		if !setAsideNameRe.MatchString(j.Request.Name) {
			return j, false, fmt.Errorf("a restore journal is here and %q is not a set-aside set's name", j.Request.Name)
		}
	default:
		return j, false, fmt.Errorf("a restore journal is here and is from neither a snapshot nor a set kept aside, but from %q", j.Request.Source)
	}
	asideOK := within(j.Aside, setAsideRoot(cfg)) && filepath.Dir(filepath.Clean(j.Aside)) == filepath.Clean(setAsideRoot(cfg))
	stagingOK := j.Staging == "" || (filepath.Dir(filepath.Clean(j.Staging)) == filepath.Clean(dir) && strings.HasPrefix(filepath.Base(j.Staging), restoreStagePrefix))
	if !asideOK || !stagingOK {
		return j, false, fmt.Errorf("a restore journal is here and names places that are not this restore's (aside %q, staging %q)", j.Aside, j.Staging)
	}
	// .
	// .
	// .
	// .
	mine := func(path, root string) (string, bool) {
		rel, err := filepath.Rel(root, filepath.Clean(path))
		if err != nil || !within(path, root) {
			return "", false
		}
		rel = filepath.ToSlash(rel)
		switch {
		case rel == filepath.Base(cfg.Identity.DBPath), rel == filepath.Base(cfg.Identity.LedgerPath), rel == witness.TailFileName:
		case strings.HasPrefix(rel, filepath.Base(cfg.Identity.DBPath)+"-") && isDatabaseSidecar(snapshotDB+strings.TrimPrefix(rel, filepath.Base(cfg.Identity.DBPath))):
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
		case movePreserve, moveCopy:
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

// .
// .
var restoreStep = func(step string) {}

// .
// .
func (a *App) stageAndProve(ctx context.Context, cfg Config, kp *crypto.KeyPair, srcPath string, snap *keptSnapshot, set, scratch string) (uint64, error) {
	if snap != nil && snap.Encrypted {
		key, err := os.ReadFile(a.snapshotKeyPath(cfg))
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
		if err := a.proveSnapshotIn(ctx, cfg, set, want, scratch); err != nil {
			return 0, err
		}
		return want.LastSeq, nil
	}
	// .
	// .
	// .
	// .
	beside, err := a.besideFor(cfg, set)
	if err != nil {
		return 0, err
	}
	n, fp, err := genesis.VerifyHeld(filepath.Join(set, "ledger.jsonl"), beside, nil)
	if err != nil {
		return 0, fmt.Errorf("%w; witness: %s", err, beside.Summary())
	}
	if fp != kp.Fingerprint() {
		return 0, fmt.Errorf("it is identity %s's, and the key on this machine is %s's", fp, kp.Fingerprint())
	}
	head, _, err := store.ProveRestore(ctx, filepath.Join(set, snapshotDB), filepath.Join(set, "ledger.jsonl"), scratch)
	if err != nil {
		return 0, fmt.Errorf("it does not restore: %w", err)
	}
	if head.Seq != uint64(n) {
		return 0, fmt.Errorf("restored, its database ends at record %d and its record at %d", head.Seq, n)
	}
	return uint64(n), nil
}

// .
// .
// .
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

// .
// .
func planRestoreMoves(cfg Config, req restoreRequest, set, staging string, through uint64, fp, label string) (restoreJournal, error) {
	dir := restoreDir(cfg)
	now := time.Now().UTC()
	aside := filepath.Join(setAsideRoot(cfg), fmt.Sprintf("%s-%s-before-restore-to-seq%d", label, now.Format("20060102T150405Z"), through))
	j := restoreJournal{Request: req, Aside: aside, Staging: staging,
		Restored: RestoreRecord{SetAsideAt: now.Format(time.RFC3339), Identity: fp, RestoredFrom: req.Name, RestoredTo: through, Witnessed: -1}}

	staged, err := filesUnder(set)
	if err != nil {
		return j, err
	}
	// .
	if tail, err := witness.ReadLocalTail(dir); err == nil && tail != nil {
		j.Restored.Witnessed = tail.LedgerOrdinal
	}
	if last, err := lastSeqOfTail(cfg.Identity.LedgerPath); err == nil {
		j.Restored.WasThrough = last
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
	// .
	// .
	// .
	// .
	// .
	// .
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
		j.Moves = append(j.Moves, restoreMove{Kind: movePreserve,
			From: filepath.Join(dir, name), To: filepath.Join(aside, setName(cfg, name))})
	}
	if keys, err := os.ReadDir(witness.WitnessKeysDir(dir)); err == nil {
		for _, e := range keys {
			n := e.Name()
			if e.IsDir() || strings.HasPrefix(n, ".") || !strings.HasSuffix(n, ".json") {
				continue
			}
			j.Moves = append(j.Moves, restoreMove{Kind: moveCopy,
				From: filepath.Join(witness.WitnessKeysDir(dir), n), To: filepath.Join(aside, witness.WitnessKeysDirName, n)})
		}
	}
	for _, rel := range staged {
		var to string
		switch {
		case rel == snapshotDB:
			to = cfg.Identity.DBPath
		case rel == "ledger.jsonl":
			to = cfg.Identity.LedgerPath
		case rel == witness.TailFileName:
			// .
			// .
			// .
			// .
			// .
			// .
			to = filepath.Join(dir, witness.TailFileName)
		case segmentNameRe.MatchString(rel):
			to = filepath.Join(dir, rel)
		case isDatabaseSidecar(rel):
			// .
			// .
			// .
			// .
			// .
			// .
			// .
			to = cfg.Identity.DBPath + strings.TrimPrefix(rel, snapshotDB)
		case strings.HasPrefix(rel, witness.WitnessKeysDirName+"/"):
			to = filepath.Join(dir, filepath.FromSlash(rel))
		default:
			continue
		}
		j.Moves = append(j.Moves, restoreMove{Kind: moveInstall,
			From: filepath.Join(set, filepath.FromSlash(rel)), To: to})
	}
	return j, nil
}

var segmentNameRe = regexp.MustCompile(`^segment-[0-9]+-[0-9]+\.jsonl\.gz$`)

// .
// .
// .
// .
// .
// .
func (a *App) finishRestore(cfg Config, j restoreJournal) error {
	dir := restoreDir(cfg)
	if err := os.MkdirAll(j.Aside, 0o700); err != nil {
		return fmt.Errorf("the set-aside directory could not be made: %w", err)
	}
	for i, m := range j.Moves {
		kind := m.kind(j.Aside)
		_, toErr := os.Lstat(m.To)
		_, fromErr := os.Lstat(m.From)
		switch kind {
		case movePreserve, moveCopy:
			// .
			// .
			if toErr == nil {
				continue
			}
			if errors.Is(fromErr, os.ErrNotExist) {
				// .
				// .
				return fmt.Errorf("move %d: %s is neither where it was nor where it was to be kept", i, filepath.Base(m.From))
			}
			if err := os.MkdirAll(filepath.Dir(m.To), 0o700); err != nil {
				return fmt.Errorf("move %d: %w", i, err)
			}
			if kind == moveCopy {
				if err := copyFile(m.From, m.To); err != nil {
					return fmt.Errorf("move %d (%s): %w", i, filepath.Base(m.From), err)
				}
			} else if err := os.Rename(m.From, m.To); err != nil {
				return fmt.Errorf("move %d (%s): %w", i, filepath.Base(m.From), err)
			}
		default:
			if errors.Is(fromErr, os.ErrNotExist) {
				// .
				if toErr == nil {
					continue
				}
				return fmt.Errorf("move %d: %s was to be put in place and is neither here nor staged — the restore is incomplete", i, filepath.Base(m.To))
			}
			if toErr == nil {
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
		restoreStep(fmt.Sprintf("moved:%d", i))
	}
	if err := syncDir(dir); err != nil {
		return fmt.Errorf("the restored directory is not durable: %w", err)
	}
	if err := syncDir(j.Aside); err != nil {
		return fmt.Errorf("what was set aside is not durable: %w", err)
	}
	restoreStep("moved")

	// .
	// .
	// .
	if held, _ := os.ReadDir(j.Aside); len(held) == 0 {
		os.Remove(j.Aside)
		j.Aside = "(nothing was here to set aside)"
	} else {
		rec, _ := json.MarshalIndent(j.Restored, "", "  ")
		if err := writeFileDurably(filepath.Join(j.Aside, restoreRecordName), append(rec, '\n')); err != nil {
			return fmt.Errorf("the set-aside set's own record could not be written: %w", err)
		}
	}
	if j.Request.Source == sourceSetAside {
		src := filepath.Join(setAsideRoot(cfg), j.Request.Name, restoreRecordName)
		var was RestoreRecord
		if raw, err := os.ReadFile(src); err == nil && json.Unmarshal(raw, &was) == nil {
			was.PutBackAt = j.Restored.SetAsideAt
			if upd, merr := json.MarshalIndent(was, "", "  "); merr == nil {
				_ = writeFileDurably(src, append(upd, '\n'))
			}
		}
	}
	restoreStep("cleanup")
	// .
	// .
	// .
	for _, gone := range []string{restoreRequestPath(cfg), filepath.Join(dir, restoreFailedName), newMachineMarkerPath(cfg), filepath.Join(dir, restoreJournalName)} {
		if err := os.Remove(gone); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("%s outlived the restore it belongs to: %w", filepath.Base(gone), err)
		}
	}
	if j.Staging != "" && strings.HasPrefix(filepath.Base(j.Staging), restoreStagePrefix) {
		os.RemoveAll(j.Staging)
	}
	if err := syncDir(dir); err != nil {
		return fmt.Errorf("the restored directory is not durable: %w", err)
	}
	logsink.Warn("restore.end", "RESTORED from %s: the record now ends at %d. What was live (through record %d) is set aside, whole, in %s", j.Request.Name, j.Restored.RestoredTo, j.Restored.WasThrough, j.Aside)
	return nil
}

// .
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

// .
// .
func sweepRestoreStaging(dir string) {
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if e.IsDir() && strings.HasPrefix(e.Name(), restoreStagePrefix) {
			os.RemoveAll(filepath.Join(dir, e.Name()))
		}
	}
}

// .
// .
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

// .
// .
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

// .

// .
func setAsideSets(cfg Config) []SetAside {
	entries, _ := os.ReadDir(setAsideRoot(cfg))
	var out []SetAside
	for _, e := range entries {
		if !e.IsDir() || !setAsideNameRe.MatchString(e.Name()) {
			continue
		}
		path := filepath.Join(setAsideRoot(cfg), e.Name())
		s := SetAside{Name: e.Name(), Path: path, Bytes: dirBytes(path)}
		if raw, err := os.ReadFile(filepath.Join(path, restoreRecordName)); err == nil {
			_ = json.Unmarshal(raw, &s.Record)
		}
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Record.SetAsideAt > out[j].Record.SetAsideAt })
	return out
}

// .
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

// .

// .
// .
type RestorePlan struct {
	Source, Name      string
	At                string
	RestoredTo        uint64
	LiveThrough       int64
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

// .
func (a *App) restorePlan(source, name string) (RestorePlan, error) {
	cfg := a.configSnapshot()
	if err := restorable(cfg); err != nil {
		return RestorePlan{}, err
	}
	path, snap, err := a.resolveRestoreSource(cfg, source, name)
	if err != nil {
		return RestorePlan{}, errors.New("That is not in the list any more. Reload the page.")
	}
	p := RestorePlan{Source: source, Name: name, LiveThrough: -1, SetAside: -1, Witnessed: -1,
		WitnessURL: cfg.Witness.URL, ConfirmText: a.restoreConfirmText(), AsideUnder: setAsideRoot(cfg)}
	if a.keyPair != nil {
		p.Identity = a.keyPair.Fingerprint()
	}
	if snap != nil {
		p.At, p.RestoredTo, p.Encrypted = snap.at().UTC().Format(time.RFC3339), snap.Record, snap.Encrypted
	} else {
		var rec RestoreRecord
		if raw, rerr := os.ReadFile(filepath.Join(path, restoreRecordName)); rerr == nil {
			_ = json.Unmarshal(raw, &rec)
		}
		p.At, p.RestoredTo = rec.SetAsideAt, rec.WasThrough
		if last, lerr := lastSeqOfTail(filepath.Join(path, "ledger.jsonl")); lerr == nil {
			p.RestoredTo = last
		}
	}
	if a.ledger != nil {
		p.LiveThrough = int64(a.ledger.LastSeq())
	} else if last, lerr := lastSeqOfTail(cfg.Identity.LedgerPath); lerr == nil {
		p.LiveThrough = int64(last)
	}
	if p.LiveThrough >= 0 {
		p.SetAside = max(0, p.LiveThrough-int64(p.RestoredTo))
	}
	// .
	// .
	if tail, terr := witness.ReadLocalTail(restoreDir(cfg)); terr == nil && tail != nil {
		p.Witnessed, p.WitnessHash = tail.LedgerOrdinal, tail.LedgerHash
	} else if a.bootAttested >= 0 {
		p.Witnessed, p.WitnessHash = a.bootAttested, a.bootAttestedHash
	}
	if p.Witnessed >= 0 {
		p.WitnessedSetAside = max(0, p.Witnessed-int64(p.RestoredTo))
	}
	return p, nil
}

// .
// .
// .
func (a *App) restoreConfirmText() string {
	if name := strings.TrimSpace(a.resolveDisplayName()); name != "" {
		return name
	}
	return "RESTORE"
}

// .
// .
// .
func (a *App) requestRestore(source, name, typed string) error {
	cfg := a.configSnapshot()
	if err := restorable(cfg); err != nil {
		return err
	}
	if strings.TrimSpace(typed) != a.restoreConfirmText() {
		return fmt.Errorf("To confirm, type %q exactly.", a.restoreConfirmText())
	}
	if _, _, err := a.resolveRestoreSource(cfg, source, name); err != nil {
		return errors.New("That is not in the list any more. Reload the page.")
	}
	if a.keyPair == nil {
		return errors.New("The identity's key is not loaded in this start, so a restore cannot be held to it.")
	}
	req := restoreRequest{Source: source, Name: name, RequestedAt: time.Now().UTC().Format(time.RFC3339), Identity: a.keyPair.Fingerprint(), Label: a.resolveDisplayName()}
	raw, _ := json.MarshalIndent(req, "", "  ")
	if err := writeFileDurably(restoreRequestPath(cfg), append(raw, '\n')); err != nil {
		logsink.Error("restore.error", "the restore request could not be written: %v", err)
		return errors.New("The request could not be written on this machine, so nothing will be restored.")
	}
	_ = syncDir(restoreDir(cfg))
	logsink.Warn("restore.decision", "a person asked for a restore from %s %s; the next start performs it before the record is opened", source, name)
	return nil
}

// .
// .
func lastRestoreFailure(cfg Config) *restoreFailure {
	raw, err := os.ReadFile(filepath.Join(restoreDir(cfg), restoreFailedName))
	if err != nil {
		return nil
	}
	var f restoreFailure
	if json.Unmarshal(raw, &f) != nil {
		return nil
	}
	return &f
}
