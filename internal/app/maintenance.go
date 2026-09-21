package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/atomicfile"
	"github.com/aiii-dot-id/aii-os/internal/cognitive"
	"github.com/aiii-dot-id/aii-os/internal/escrow"
	"github.com/aiii-dot-id/aii-os/internal/genesis"
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
	// .
	// .
	// .
	// .
	maintenanceAlarmID   = "maintenance.daily"
	maintenanceOwnerName = "maintenance"

	// .
	// .
	// .
	// .
	// .
	// .
	// .
	backupsDirName = "backups"

	// .
	// .
	// .
	maintenanceHourLocal = 4

	// .
	// .
	// .
	// .
	defaultBackupKeep = 8
)

// .
// .
// .
type maintenanceOwner struct{ a *App }

func (o maintenanceOwner) Name() string { return maintenanceOwnerName }

func (o maintenanceOwner) OnAlarm(ctx context.Context, _ string, _ string, _ int64, _ string) cognitive.AlarmResult {
	o.a.runMaintenance(ctx)
	next := cognitive.NextLocalDaily(time.Now(), maintenanceHourLocal, 0)
	return cognitive.AlarmResult{Accepted: true, NextDeadline: &next}
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
// .
// .
// .
// .
func armMaintenanceAlarm(t *cognitive.TIME, now time.Time) error {
	next := cognitive.NextLocalDaily(now, maintenanceHourLocal, 0)
	return t.SetAlarm(maintenanceAlarmID, maintenanceOwnerName, "wall", next, nil, "")
}

// .
// .
// .
// .
func maintenanceEnabled(cfg Config) bool {
	if cfg.Maintenance.Enabled == nil {
		return true
	}
	return *cfg.Maintenance.Enabled
}

func maintenanceKeep(cfg Config) int {
	if cfg.Maintenance.BackupKeep <= 0 {
		return defaultBackupKeep
	}
	return cfg.Maintenance.BackupKeep
}

func (a *App) backupsDir(cfg Config) string {
	return filepath.Join(filepath.Dir(cfg.Identity.DBPath), backupsDirName)
}

// .
// .
// .
func (a *App) runMaintenance(ctx context.Context) {
	// .
	// .
	// .
	// .
	// .
	// .
	a.maintMu.Lock()
	defer a.maintMu.Unlock()
	cfg := a.configSnapshot()
	if !maintenanceEnabled(cfg) {
		logsink.Info("maintenance.refusal", "disabled by config; nothing verified, nothing copied")
		a.recordPass(store.ContinuityStatus{Outcome: store.ContinuityDisabled})
		return
	}
	start := time.Now()
	_, inSafe := a.SafeMode()

	// .
	// .
	// .
	// .
	// .
	// .
	// .
	logsink.PruneCaptures()

	// .
	// .
	// .
	// .
	// .
	dbNote := "quick_check ok"
	if a.store != nil {
		databaseHealthy := true
		if err := a.store.QuickCheck(); err != nil {
			databaseHealthy = false
			dbNote = "quick_check FAILED"
			a.maintenanceAlert("db-quick-check", fmt.Sprintf("%v — %s", err, restoreAdvice))
		}
		// .
		// .
		// .
		// .
		if err := a.store.ForeignKeyCheck(); err != nil {
			databaseHealthy = false
			dbNote += ", foreign keys FAILED"
			a.maintenanceAlert("db-foreign-keys", fmt.Sprintf("%v — %s", err, restoreAdvice))
		} else {
			dbNote += ", foreign keys ok"
		}
		// .
		// .
		// .
		if inSafe || !databaseHealthy {
			dbNote += ", write maintenance skipped"
		} else if note, err := a.store.HousekeepContext(ctx); err != nil {
			dbNote += ", housekeeping failed: " + err.Error()
			logsink.Warn("maintenance.error", "housekeeping: %v", err)
		} else {
			dbNote += ", " + note
		}
	}

	dir := a.backupsDir(cfg)

	if inSafe {
		// .
		// .
		n, err := a.verifyLiveLedger(cfg)
		if err != nil {
			a.maintenanceAlert("ledger-chain", fmt.Sprintf("SAFE-mode verification failed: %v", err))
			logsink.Error("maintenance.error", "SAFE; chain FAILED; %s; no copy considered (%.1fs)", dbNote, time.Since(start).Seconds())
			a.recordPass(store.ContinuityStatus{Outcome: store.ContinuityFailed, Detail: "the live chain did not verify: " + err.Error()})
			return
		}
		logsink.Info("maintenance.refusal", "SAFE; chain ok (%d events); %s; no copy in SAFE (%.1fs)", n, dbNote, time.Since(start).Seconds())
		a.recordPass(store.ContinuityStatus{Outcome: store.ContinuitySafe, Record: uint64(n)})
		return
	}

	// .
	// .
	// .
	// .
	// .
	made, err := a.takeSnapshot(ctx, cfg, dir, false)
	if err != nil {
		a.maintenanceAlert("backup", err.Error())
		logsink.Error("maintenance.error", "chain/copy FAILED (%v); %s; older copies protected (%.1fs)", err, dbNote, time.Since(start).Seconds())
		a.recordPass(store.ContinuityStatus{Outcome: store.ContinuityFailed, Detail: err.Error()})
		return
	}
	removed := pruneBackups(dir, maintenanceKeep(cfg))
	logsink.Info("maintenance.end", "verified, copied and restore-proved %s (%d events); pruned %d; %s (%.1fs)",
		made.Name, made.Record, removed, dbNote, time.Since(start).Seconds())
	a.recordPass(passMade(made, false))
}

// .
func passMade(made publishedSnapshot, onDemand bool) store.ContinuityStatus {
	return store.ContinuityStatus{Outcome: store.ContinuityOK, Snapshot: made.Name, Record: made.Record,
		OnDemand: onDemand, Encrypted: made.Encrypted, Unencrypted: made.Unencrypted}
}

// .
// .
// .
// .
// .
// .
func (a *App) recordPass(st store.ContinuityStatus) {
	if a.store == nil {
		return
	}
	if err := a.store.SetContinuityStatus(st); err != nil {
		logsink.Warn("maintenance.error", "the pass's result could not be recorded (%v) — the log line is its only record today", err)
	}
}

// .
// .
const restoreAdvice = "stop the identity, move the database aside — never delete it, it holds every conversation and the lived clock — and restore aii.db with its ledger from the newest snapshot under data/backups (the Restore procedure in the maintenance contract)"

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
func (a *App) maintenanceBackup(ctx context.Context, cfg Config, dir string) (string, uint64, error) {
	p, err := a.takeSnapshot(ctx, cfg, dir, false)
	return p.Name, p.Record, err
}

// .
type publishedSnapshot struct {
	Name        string
	Record      uint64
	Encrypted   bool
	Unencrypted string
}

// .
// .
// .
// .
func (a *App) takeSnapshot(ctx context.Context, cfg Config, dir string, onDemand bool) (publishedSnapshot, error) {
	none := func(err error) (publishedSnapshot, error) { return publishedSnapshot{}, err }
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return none(fmt.Errorf("backups dir: %w", err))
	}
	// .
	// .
	// .
	// .
	// .
	tmp, err := os.MkdirTemp(dir, snapshotWorkPrefix+"*.tmp")
	if err != nil {
		return none(fmt.Errorf("copy: %w", err))
	}
	// .
	// .
	// .
	// .
	// .
	// .
	defer func() {
		if rmErr := os.RemoveAll(tmp); rmErr != nil {
			logsink.Error("maintenance.error", "the pass's plaintext working set could not be removed: %v — it stands in %s until the next normal boot sweeps it", rmErr, dir)
			a.maintenanceAlert("snapshot-debris", "a snapshot pass could not remove its plaintext working set ("+rmErr.Error()+") — remove "+tmp+" by hand, or restart the identity")
		}
	}()

	// .
	// .
	// .
	// .
	// .
	// .
	// .
	srcDir := filepath.Dir(cfg.Identity.LedgerPath)
	tailRaw, tailErr := os.ReadFile(witness.TailPath(srcDir))
	if tailErr != nil && !errors.Is(tailErr, os.ErrNotExist) {
		return none(fmt.Errorf("copy the witness tail: %w", tailErr))
	}

	at, pin, err := a.door.capture(ctx, filepath.Join(tmp, "ledger.jsonl"))
	if err != nil {
		return none(fmt.Errorf("capture: %w", err))
	}
	// .
	// .
	// .
	defer pin.Release()
	stamp := time.Now().UTC()
	snapshotStep("captured")
	names := []string{"ledger.jsonl"}

	// .
	// .
	dbCopy := filepath.Join(tmp, snapshotDB)
	if err := pin.BackupTo(ctx, dbCopy); err != nil {
		return none(fmt.Errorf("copy the database: %w", err))
	}
	facts, err := pin.Facts(ctx)
	pin.Release()
	if err != nil {
		return none(fmt.Errorf("capture: %w", err))
	}
	names = append(names, snapshotDB)

	// .
	// .
	for _, seg := range at.Segments {
		n := filepath.Base(seg)
		if err := copyFile(seg, filepath.Join(tmp, n)); err != nil {
			return none(fmt.Errorf("copy %s: %w", n, err))
		}
		names = append(names, n)
	}
	// .
	// .
	// .
	if keys, err := os.ReadDir(witness.WitnessKeysDir(srcDir)); err == nil {
		sub := filepath.Join(tmp, witness.WitnessKeysDirName)
		if err := os.MkdirAll(sub, 0o700); err != nil {
			return none(fmt.Errorf("copy witness keys: %w", err))
		}
		for _, e := range keys {
			n := e.Name()
			if e.IsDir() || strings.HasPrefix(n, ".") || !strings.HasSuffix(n, ".json") {
				continue
			}
			if err := copyFile(filepath.Join(srcDir, witness.WitnessKeysDirName, n), filepath.Join(sub, n)); err != nil {
				return none(fmt.Errorf("copy witness key %s: %w", n, err))
			}
			names = append(names, witness.WitnessKeysDirName+"/"+n)
		}
	}
	// .
	// .
	// .
	if tailErr == nil {
		if err := writeFileDurably(filepath.Join(tmp, witness.TailFileName), tailRaw); err != nil {
			return none(fmt.Errorf("copy the witness tail: %w", err))
		}
		names = append(names, witness.TailFileName)
	}

	// .
	// .
	// .
	// .
	receipt := snapshotReceipt{
		Identity:   a.door.kp.Fingerprint(),
		CapturedAt: stamp.Format(time.RFC3339),
		LastSeq:    at.LastSeq,
		LastHash:   at.LastHash,
		SealedSeq:  at.SealedSeq,
		Runtime:    facts,
	}
	if err := a.proveSnapshot(ctx, cfg, tmp, receipt); err != nil {
		return none(err)
	}
	if err := writeReceipt(tmp, receipt); err != nil {
		return none(fmt.Errorf("receipt: %w", err))
	}
	names = append(names, snapshotReceiptName)
	sums, err := writeSums(tmp, names)
	if err != nil {
		return none(fmt.Errorf("checksum list: %w", err))
	}
	name := fmt.Sprintf("ledger-%s-seq%d", stamp.Format("20060102T150405Z"), at.LastSeq)
	if onDemand {
		name += onDemandMark
		// .
		// .
		// .
		// .
		// .
		// .
		for _, taken := range []string{name, name + escrow.SnapshotSuffix} {
			if _, err := os.Lstat(filepath.Join(dir, taken)); err == nil {
				aside, err := os.MkdirTemp(dir, snapshotWorkPrefix+"replaced-*.tmp")
				if err != nil {
					return none(fmt.Errorf("publish: %w", err))
				}
				defer os.RemoveAll(aside)
				if err := os.Rename(filepath.Join(dir, taken), filepath.Join(aside, taken)); err != nil {
					return none(fmt.Errorf("publish: %w", err))
				}
			}
		}
	}

	// .
	// .
	// .
	// .
	// .
	key, recipient, reason, whyNot := a.snapshotEncryption(cfg)
	if whyNot != "" {
		a.maintenanceAlert("snapshot-unencrypted", "today's snapshot is NOT encrypted: "+whyNot)
		// .
		// .
		// .
		if err := syncTree(tmp); err != nil {
			return none(fmt.Errorf("publish: %w", err))
		}
		if err := os.Rename(tmp, filepath.Join(dir, name)); err != nil {
			return none(fmt.Errorf("publish: %w", err))
		}
	} else {
		defer escrow.Wipe(key)
		members := append(append([]string{}, names...), snapshotSums)
		sealed, err := os.CreateTemp(dir, snapshotWorkPrefix+"*"+escrow.SnapshotSuffix+".tmp")
		if err != nil {
			return none(fmt.Errorf("encrypt: %w", err))
		}
		sealedPath := sealed.Name()
		sealed.Close()
		os.Remove(sealedPath)
		defer os.Remove(sealedPath)
		if err := escrow.EncryptSnapshot(tmp, members, recipient, sealedPath); err != nil {
			return none(fmt.Errorf("encrypt — not published: %w", err))
		}
		snapshotStep("encrypted:" + sealedPath)
		sumsRaw, err := os.ReadFile(filepath.Join(tmp, snapshotSums))
		if err != nil {
			return none(fmt.Errorf("encrypt — not published: %w", err))
		}
		sumsHash := sha256.Sum256(sumsRaw)
		sums[snapshotSums] = hex.EncodeToString(sumsHash[:])
		if err := escrow.VerifySnapshot(sealedPath, key, members, sums); err != nil {
			return none(fmt.Errorf("the encrypted snapshot does not verify — not published: %w", err))
		}
		name += escrow.SnapshotSuffix
		if err := os.Rename(sealedPath, filepath.Join(dir, name)); err != nil {
			return none(fmt.Errorf("publish: %w", err))
		}
	}
	if err := syncDir(dir); err != nil {
		return none(fmt.Errorf("publish: %w", err))
	}
	logsink.Info("maintenance.end", "identity %s; captured at record %d with %d lived ticks, last turn %d",
		receipt.Identity, at.LastSeq, facts.LifetimeTicks, facts.LastTurnSeq)
	return publishedSnapshot{Name: name, Record: at.LastSeq, Encrypted: reason == "", Unencrypted: reason}, nil
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
func (a *App) proveSnapshot(ctx context.Context, cfg Config, dir string, want snapshotReceipt) error {
	return a.proveSnapshotIn(ctx, cfg, dir, want, filepath.Join(dir, ".restore-proof"))
}

// .
// .
// .
// .
func (a *App) proveSnapshotIn(ctx context.Context, cfg Config, dir string, want snapshotReceipt, scratch string) error {
	// .
	// .
	beside, err := a.besideFor(cfg, dir)
	if err != nil {
		return fmt.Errorf("copy does not verify — not published: %w", err)
	}
	ledgerCopy, dbCopy := filepath.Join(dir, "ledger.jsonl"), filepath.Join(dir, snapshotDB)
	n, fp, err := genesis.VerifyHeld(ledgerCopy, beside, nil)
	if err != nil {
		return fmt.Errorf("copy does not verify — not published: %w; witness: %s", err, beside.Summary())
	}
	if fp != want.Identity {
		return fmt.Errorf("copy does not verify — not published: the record copy is identity %s, captured from %s", fp, want.Identity)
	}
	if uint64(n) != want.LastSeq {
		return fmt.Errorf("copy does not verify — not published: the record copy holds %d records, captured at %d", n, want.LastSeq)
	}
	head, copied, err := store.InspectCopy(ctx, dbCopy)
	if err != nil {
		return fmt.Errorf("copy does not verify — not published: database: %w", err)
	}
	if head.Hash != want.LastHash {
		return fmt.Errorf("copy does not verify — not published: the database copy ends at record %d, captured at %d", head.Seq, want.LastSeq)
	}
	if d := want.Runtime.Differs(copied); d != "" {
		return fmt.Errorf("copy does not verify — not published: the database copy is not the captured state: %s", d)
	}
	head, restored, err := store.ProveRestore(ctx, dbCopy, ledgerCopy, scratch)
	if err != nil {
		return fmt.Errorf("copy does not restore — not published: %w", err)
	}
	if head.Hash != want.LastHash {
		return fmt.Errorf("copy does not restore — not published: replayed over the database copy, the record copy ends at record %d (%.12s), captured at %d (%.12s)",
			head.Seq, head.Hash, want.LastSeq, want.LastHash)
	}
	if d := want.Runtime.Differs(restored); d != "" {
		return fmt.Errorf("copy does not restore — not published: %s", d)
	}
	return nil
}

// .
// .
// .
// .
var snapshotStep = func(step string) {}

// .
// .
const snapshotDB = "aii.db"

// .
const snapshotReceiptName = "RECEIPT.json"

// .
// .
// .
// .
type snapshotReceipt struct {
	Identity   string             `json:"identity"`
	CapturedAt string             `json:"captured_at"`
	LastSeq    uint64             `json:"last_seq"`
	LastHash   string             `json:"last_hash"`
	SealedSeq  uint64             `json:"sealed_seq"`
	Runtime    store.RuntimeFacts `json:"runtime"`
}

func writeReceipt(dir string, r snapshotReceipt) error {
	raw, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	return writeFileDurably(filepath.Join(dir, snapshotReceiptName), append(raw, '\n'))
}

// .
// .
var syncDir = atomicfile.SyncDir

// .
func syncTree(root string) error {
	return filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return syncDir(path)
		}
		return nil
	})
}

// .
// .
// .
// .
// .
// .
func writeFileDurably(path string, data []byte) error {
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	published := false
	defer func() {
		if !published {
			os.Remove(tmp.Name())
		}
	}()
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	published, err = atomicfile.Replace(tmp.Name(), path)
	return err
}

// .
// .
// .
// .
// .
// .
func (a *App) verifyLiveLedger(cfg Config) (int, error) {
	beside, err := a.besideFor(cfg, filepath.Dir(cfg.Identity.LedgerPath))
	if err != nil {
		return 0, err
	}
	n, _, err := genesis.VerifyHeld(cfg.Identity.LedgerPath, beside, nil)
	if err != nil {
		return n, fmt.Errorf("%w; witness: %s", err, beside.Summary())
	}
	return n, nil
}

// .
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	if err := out.Sync(); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// .
// .
const snapshotSums = escrow.SumsName

// .
// .
func writeSums(dir string, names []string) (map[string]string, error) {
	var b strings.Builder
	sums := make(map[string]string, len(names)+1)
	for _, name := range names {
		f, err := os.Open(filepath.Join(dir, filepath.FromSlash(name)))
		if err != nil {
			return nil, err
		}
		h := sha256.New()
		_, err = io.Copy(h, f)
		f.Close()
		if err != nil {
			return nil, err
		}
		sums[name] = hex.EncodeToString(h.Sum(nil))
		b.WriteString(sums[name] + "  " + name + "\n")
	}
	tmp := filepath.Join(dir, "."+snapshotSums+".tmp")
	if err := writeFileDurably(tmp, []byte(b.String())); err != nil {
		return nil, err
	}
	if err := os.Rename(tmp, filepath.Join(dir, snapshotSums)); err != nil {
		os.Remove(tmp)
		return nil, err
	}
	return sums, nil
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
var backupSeqRe = regexp.MustCompile(`^ledger-([0-9]{8}T[0-9]{6}Z)-seq([0-9]+)(` + onDemandMark + `)?(\.tar\.age)?$`)

// .
// .
const onDemandMark = "-ondemand"

// .
type keptSnapshot struct {
	Name      string
	Stamp     string
	Record    uint64
	OnDemand  bool
	Encrypted bool
}

func (k keptSnapshot) at() time.Time {
	t, _ := time.Parse("20060102T150405Z", k.Stamp)
	return t
}

// .
// .
// .
func keptSnapshots(dir string) (kept []keptSnapshot, debris []string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, nil
	}
	for _, e := range entries {
		m := backupSeqRe.FindStringSubmatch(e.Name())
		if m == nil {
			continue
		}
		if !snapshotComplete(dir, e.Name()) {
			debris = append(debris, e.Name())
			continue
		}
		n, err := strconv.ParseUint(m[2], 10, 64)
		if err != nil {
			continue
		}
		kept = append(kept, keptSnapshot{Name: e.Name(), Stamp: m[1], Record: n, OnDemand: m[3] != "", Encrypted: m[4] != ""})
	}
	sort.Slice(kept, func(i, j int) bool {
		if kept[i].Stamp != kept[j].Stamp {
			return kept[i].Stamp < kept[j].Stamp
		}
		if kept[i].Record != kept[j].Record {
			return kept[i].Record < kept[j].Record
		}
		return kept[i].Name < kept[j].Name
	})
	return kept, debris
}

// .
// .
// .
// .
var backupEvidenceRe = regexp.MustCompile(`^ledger-[0-9TZ]+-seq[0-9]+`)

func snapshotComplete(dir, name string) bool {
	info, err := os.Stat(filepath.Join(dir, name))
	if err != nil {
		return false
	}
	if strings.HasSuffix(name, escrow.SnapshotSuffix) {
		return info.Mode().IsRegular()
	}
	if !info.IsDir() {
		return false
	}
	_, err = os.Stat(filepath.Join(dir, name, snapshotSums))
	return err == nil
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
func pruneBackups(dir string, keep int) int {
	kept, debris := keptSnapshots(dir)
	for _, name := range debris {
		os.RemoveAll(filepath.Join(dir, name))
	}
	// .
	// .
	// .
	// .
	// .
	// .
	var daily, asked []keptSnapshot
	for _, k := range kept {
		if k.OnDemand {
			asked = append(asked, k)
		} else {
			daily = append(daily, k)
		}
	}
	removed := 0
	drop := func(set []keptSnapshot, keep int) {
		for i := 0; i < len(set)-keep; i++ {
			if os.RemoveAll(filepath.Join(dir, set[i].Name)) == nil {
				removed++
			}
		}
	}
	drop(daily, keep)
	drop(asked, onDemandKeep)
	return removed
}

// .
// .
// .
const onDemandKeep = 1

// .
// .
// .
const snapshotWorkPrefix = ".snapshot-"

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
func (a *App) sweepSnapshotDebrisAtBoot(cfg Config) {
	if _, inSafe := a.SafeMode(); inSafe {
		return
	}
	dir := a.backupsDir(cfg)
	n, err := sweepSnapshotDebris(dir)
	if err != nil {
		logsink.Error("maintenance.error", "a snapshot pass that never returned left its plaintext working set in %s and it could not all be removed: %v", dir, err)
	}
	if n > 0 {
		logsink.Warn("maintenance.end", "removed %d working set(s) a snapshot pass that never returned left in %s — the identity in the clear, in the directory that leaves the host", n, dir)
	}
}

func sweepSnapshotDebris(dir string) (removed int, err error) {
	entries, rerr := os.ReadDir(dir)
	if rerr != nil {
		if os.IsNotExist(rerr) {
			return 0, nil
		}
		return 0, rerr
	}
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), snapshotWorkPrefix) {
			continue
		}
		if rmErr := os.RemoveAll(filepath.Join(dir, e.Name())); rmErr != nil {
			err = errors.Join(err, rmErr)
			continue
		}
		removed++
	}
	return removed, err
}

// .
// .
// .
func (a *App) maintenanceAlert(kind, detail string) {
	logsink.Error("maintenance.error", "ALERT (%s): %s", kind, detail)
	if a.store == nil {
		return
	}
	id := "maint_" + kind + "_" + time.Now().UTC().Format("20060102")
	if _, err := a.store.AddOutboxMessageOnce(id, "operator", "",
		"[maintenance] "+kind+": "+detail, nil); err != nil {
		logsink.Error("maintenance.error", "could not reach the outbox (%v) — the log line above is the only record", err)
	}
}
