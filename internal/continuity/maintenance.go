package continuity

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
	"sync"
	"syscall"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/atomicfile"
	"github.com/aiii-dot-id/aii-os/internal/cognitive"
	"github.com/aiii-dot-id/aii-os/internal/escrow"
	"github.com/aiii-dot-id/aii-os/internal/genesis"
	"github.com/aiii-dot-id/aii-os/internal/ledger"
	"github.com/aiii-dot-id/aii-os/internal/store"
	"github.com/aiii-dot-id/aii-os/internal/witness"
)

const (
	maintenanceAlarmID   = "maintenance.daily"
	maintenanceOwnerName = "maintenance"

	BackupsDirName = "backups"

	maintenanceHourLocal = 4

	defaultBackupKeep = 8
)

type maintenanceOwner struct{ r *Runtime }

func (o maintenanceOwner) Name() string { return maintenanceOwnerName }

func (r *Runtime) AlarmOwner() cognitive.AlarmOwner { return maintenanceOwner{r} }

func (o maintenanceOwner) OnAlarm(ctx context.Context, _ string, _ string, _ int64, _ string) cognitive.AlarmResult {
	o.r.runMaintenance(ctx)
	next := cognitive.NextLocalDaily(time.Now(), maintenanceHourLocal, 0)
	return cognitive.AlarmResult{Accepted: true, NextDeadline: &next}
}

func ArmMaintenanceAlarm(t *cognitive.TIME, now time.Time) error {
	next := cognitive.NextLocalDaily(now, maintenanceHourLocal, 0)
	return t.SetAlarm(maintenanceAlarmID, maintenanceOwnerName, "wall", next, nil, "")
}

func MaintenanceEnabled(m MaintenanceConfig) bool {
	if m.Enabled == nil {
		return true
	}
	return *m.Enabled
}

func MaintenanceKeep(m MaintenanceConfig) int {
	if m.BackupKeep <= 0 {
		return defaultBackupKeep
	}
	return m.BackupKeep
}

func (r *Runtime) BackupsDir(cfg Settings) string {
	return filepath.Join(filepath.Dir(r.w.Active(cfg.Identity).DBPath), BackupsDirName)
}

type maintenanceState struct {
	mu sync.Mutex

	lastVerify time.Time
}

func (r *Runtime) runMaintenance(ctx context.Context) {

	r.maint.mu.Lock()
	defer r.maint.mu.Unlock()
	cfg := r.w.Settings()
	if !MaintenanceEnabled(cfg.Maintenance) {
		logsink.Info("maintenance.refusal", "disabled by config; nothing verified, nothing copied")
		r.recordPass(store.ContinuityStatus{Outcome: store.ContinuityDisabled})
		return
	}
	start := time.Now()
	inSafe := r.w.Safe()

	logsink.PruneCaptures()

	dbNote := "quick_check ok"
	if r.w.Store() != nil {
		databaseHealthy := true
		if err := r.w.Store().QuickCheck(); err != nil {
			databaseHealthy = false
			dbNote = "quick_check FAILED"
			r.Alert("db-quick-check", fmt.Sprintf("%v — %s", err, restoreAdvice))
		}

		if err := r.w.Store().ForeignKeyCheck(); err != nil {
			databaseHealthy = false
			dbNote += ", foreign keys FAILED"
			r.Alert("db-foreign-keys", fmt.Sprintf("%v — %s", err, restoreAdvice))
		} else {
			dbNote += ", foreign keys ok"
		}

		if inSafe || !databaseHealthy {
			dbNote += ", write maintenance skipped"
		} else if note, err := r.w.Store().HousekeepOptimized(ctx, cfg.Compression); err != nil {
			dbNote += ", housekeeping failed: " + err.Error()
			logsink.Warn("maintenance.error", "housekeeping: %v", err)
		} else {
			dbNote += ", " + note
		}
	}

	dir := r.BackupsDir(cfg)

	if inSafe {

		n, err := r.verifyLiveLedger(cfg)
		if err != nil {
			r.Alert("ledger-chain", fmt.Sprintf("SAFE-mode verification failed: %v", err))
			logsink.Error("maintenance.error", "SAFE; chain FAILED; %s; no copy considered (%.1fs)", dbNote, time.Since(start).Seconds())
			r.recordPass(store.ContinuityStatus{Outcome: store.ContinuityFailed, Detail: "the live chain did not verify: " + err.Error()})
			return
		}
		logsink.Info("maintenance.refusal", "SAFE; chain ok (%d events); %s; no copy in SAFE (%.1fs)", n, dbNote, time.Since(start).Seconds())
		r.recordPass(store.ContinuityStatus{Outcome: store.ContinuitySafe, Record: uint64(n)})
		return
	}

	made, err := r.takeSnapshot(ctx, cfg, dir, false)
	if err != nil {
		r.Alert("backup", err.Error())
		logsink.Error("maintenance.error", "chain/copy FAILED (%v); %s; older copies protected (%.1fs)", err, dbNote, time.Since(start).Seconds())
		r.recordPass(store.ContinuityStatus{Outcome: store.ContinuityFailed, Detail: err.Error()})
		return
	}
	removed := pruneBackups(dir, MaintenanceKeep(cfg.Maintenance))
	logsink.Info("maintenance.end", "verified, copied and restore-proved %s (%d events); pruned %d; %s (%.1fs)",
		made.Name, made.Record, removed, dbNote, time.Since(start).Seconds())
	r.recordPass(passMade(made, false))
}

func passMade(made publishedSnapshot, onDemand bool) store.ContinuityStatus {
	return store.ContinuityStatus{Outcome: store.ContinuityOK, Snapshot: made.Name, Record: made.Record,
		OnDemand: onDemand, Encrypted: made.Encrypted, Unencrypted: made.Unencrypted}
}

func (r *Runtime) recordPass(st store.ContinuityStatus) {
	if r.w.Store() == nil {
		return
	}
	if err := r.w.Store().SetContinuityStatus(st); err != nil {
		logsink.Warn("maintenance.error", "the pass's result could not be recorded (%v) — the log line is its only record today", err)
	}
}

const restoreAdvice = "stop the identity, move the database aside — never delete it, it holds every conversation and the lived clock — and restore aii.db with its ledger from the newest snapshot under data/backups (the Restore procedure in the maintenance contract)"

type publishedSnapshot struct {
	Name        string
	Record      uint64
	Encrypted   bool
	Unencrypted string
}

func (r *Runtime) takeSnapshot(ctx context.Context, cfg Settings, dir string, onDemand bool) (publishedSnapshot, error) {
	none := func(err error) (publishedSnapshot, error) { return publishedSnapshot{}, err }
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return none(fmt.Errorf("backups dir: %w", err))
	}

	tmp, err := os.MkdirTemp(dir, SnapshotWorkPrefix+"*.tmp")
	if err != nil {
		return none(fmt.Errorf("copy: %w", err))
	}

	defer func() {
		if rmErr := os.RemoveAll(tmp); rmErr != nil {
			logsink.Error("maintenance.error", "the pass's plaintext working set could not be removed: %v — it stands in %s until the next normal boot sweeps it", rmErr, dir)
			r.Alert("snapshot-debris", "a snapshot pass could not remove its plaintext working set ("+rmErr.Error()+") — remove "+tmp+" by hand, or restart the identity")
		}
	}()

	srcDir := filepath.Dir(r.w.Active(cfg.Identity).LedgerPath)
	tailRaw, tailErr := atomicfile.ReadFile(witness.TailPath(srcDir))
	if tailErr != nil && !errors.Is(tailErr, os.ErrNotExist) {
		return none(fmt.Errorf("copy the witness tail: %w", tailErr))
	}

	at, pin, err := r.w.Capture(ctx, filepath.Join(tmp, "ledger.jsonl"))
	if err != nil {
		return none(fmt.Errorf("capture: %w", err))
	}

	defer pin.Release()
	stamp := time.Now().UTC()
	r.snapshotStep("captured")
	names := []string{"ledger.jsonl"}

	dbCopy := filepath.Join(tmp, SnapshotDB)
	if err := pin.BackupTo(ctx, dbCopy); err != nil {
		return none(fmt.Errorf("copy the database: %w", err))
	}
	facts, err := pin.Facts(ctx)
	pin.Release()
	if err != nil {
		return none(fmt.Errorf("capture: %w", err))
	}
	names = append(names, SnapshotDB)

	for _, seg := range at.Segments {
		n := filepath.Base(seg)
		if err := copyFile(seg, filepath.Join(tmp, n)); err != nil {
			return none(fmt.Errorf("copy %s: %w", n, err))
		}
		names = append(names, n)
	}

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

	if tailErr == nil {
		if err := writeFileDurably(filepath.Join(tmp, witness.TailFileName), tailRaw); err != nil {
			return none(fmt.Errorf("copy the witness tail: %w", err))
		}
		names = append(names, witness.TailFileName)
	}

	receipt := snapshotReceipt{
		Identity:   r.w.DoorKey().Fingerprint(),
		CapturedAt: stamp.Format(time.RFC3339),
		LastSeq:    at.LastSeq,
		LastHash:   at.LastHash,
		SealedSeq:  at.SealedSeq,
		Runtime:    facts,
	}
	if err := r.proveSnapshot(ctx, cfg, tmp, receipt); err != nil {
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

		for _, taken := range []string{name, name + escrow.SnapshotSuffix} {
			if _, err := os.Lstat(filepath.Join(dir, taken)); err == nil {
				aside, err := os.MkdirTemp(dir, SnapshotWorkPrefix+"replaced-*.tmp")
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

	key, recipient, reason, whyNot := r.snapshotEncryption(cfg)
	if whyNot != "" {

		if reason != store.UnencryptedNoEscrow {
			r.Alert("snapshot-unencrypted", "today's snapshot is NOT encrypted: "+whyNot)
		} else {
			r.sayNoLongerEncrypted(dir, name)
		}

		if err := r.syncTree(tmp); err != nil {
			return none(fmt.Errorf("publish: %w", err))
		}
		if err := os.Rename(tmp, filepath.Join(dir, name)); err != nil {
			return none(fmt.Errorf("publish: %w", err))
		}
	} else {
		defer escrow.Wipe(key)
		members := append(append([]string{}, names...), SnapshotSums)
		sealed, err := os.CreateTemp(dir, SnapshotWorkPrefix+"*"+escrow.SnapshotSuffix+".tmp")
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
		r.snapshotStep("encrypted:" + sealedPath)
		sumsRaw, err := os.ReadFile(filepath.Join(tmp, SnapshotSums))
		if err != nil {
			return none(fmt.Errorf("encrypt — not published: %w", err))
		}
		sumsHash := sha256.Sum256(sumsRaw)
		sums[SnapshotSums] = hex.EncodeToString(sumsHash[:])
		if err := escrow.VerifySnapshot(sealedPath, key, members, sums); err != nil {
			return none(fmt.Errorf("the encrypted snapshot does not verify — not published: %w", err))
		}
		name += escrow.SnapshotSuffix
		if err := os.Rename(sealedPath, filepath.Join(dir, name)); err != nil {
			return none(fmt.Errorf("publish: %w", err))
		}
	}
	if err := r.syncDir(dir); err != nil {
		return none(fmt.Errorf("publish: %w", err))
	}
	logsink.Info("maintenance.end", "identity %s; captured at record %d with %d lived ticks, last turn %d",
		receipt.Identity, at.LastSeq, facts.LifetimeTicks, facts.LastTurnSeq)
	return publishedSnapshot{Name: name, Record: at.LastSeq, Encrypted: reason == "", Unencrypted: reason}, nil
}

func (r *Runtime) proveSnapshot(ctx context.Context, cfg Settings, dir string, want snapshotReceipt) error {
	return r.proveSnapshotIn(ctx, cfg, dir, want, filepath.Join(dir, ".restore-proof"))
}

func (r *Runtime) proveSnapshotIn(ctx context.Context, cfg Settings, dir string, want snapshotReceipt, scratch string) error {

	beside, err := besideFor(cfg, dir)
	if err != nil {
		return fmt.Errorf("copy does not verify — not published: %w", err)
	}
	ledgerCopy, dbCopy := filepath.Join(dir, "ledger.jsonl"), filepath.Join(dir, SnapshotDB)
	var verified store.MirrorHead
	n, fp, err := genesis.VerifyHeld(ledgerCopy, beside, func(string) func(*ledger.Event) error {
		return func(evt *ledger.Event) error {
			verified = store.MirrorHead{Seq: evt.Seq, Hash: evt.EntryHash()}
			return nil
		}
	})
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
	head, restored, err := store.ProveRestore(ctx, dbCopy, ledgerCopy, scratch, verified)
	if err != nil {
		return fmt.Errorf("copy does not restore — not published: %w", err)
	}
	if head.Hash != want.LastHash {
		return fmt.Errorf("copy does not restore — not published: replayed over the database copy, the record copy ends at record %d (%.12s), captured at %d (%.12s)",
			head.Seq, head.Hash, want.LastSeq, want.LastHash)
	}
	if d := want.Runtime.DiffersAfterRestore(restored); d != "" {
		return fmt.Errorf("copy does not restore — not published: %s", d)
	}
	return nil
}

const SnapshotDB = "aii.db"

const snapshotReceiptName = "RECEIPT.json"

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

func (r *Runtime) syncTree(root string) error {
	return filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return r.syncDir(path)
		}
		return nil
	})
}

func writeFileDurably(path string, data []byte) error {

	published, err := atomicfile.WriteReplace(path, data, 0o600)
	if err != nil && published {
		return fmt.Errorf("%s published but not durable: %w", filepath.Base(path), err)
	}
	return err
}

func (r *Runtime) verifyLiveLedger(cfg Settings) (int, error) {
	ledgerPath := r.w.Active(cfg.Identity).LedgerPath
	beside, err := besideFor(cfg, filepath.Dir(ledgerPath))
	if err != nil {
		return 0, err
	}
	n, _, err := genesis.VerifyHeld(ledgerPath, beside, nil)
	if err != nil {
		return n, fmt.Errorf("%w; witness: %s", err, beside.Summary())
	}
	return n, nil
}

func besideFor(cfg Settings, ledgerDir string) (*witness.Beside, error) {
	platform, err := witness.LoadPlatformEnvelope(cfg.WitnessPlatform)
	if err != nil {
		return nil, err
	}
	return witness.LoadBeside(ledgerDir, platform)
}

func copyFile(src, dst string) error {
	in, err := atomicfile.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	if st, err := in.Stat(); err != nil {
		return err
	} else if st.IsDir() {
		return &os.PathError{Op: "read", Path: src, Err: syscall.EISDIR}
	}
	return copyInto(in, dst)
}

func copyInto(r io.Reader, dst string) error {
	tmp, err := os.CreateTemp(filepath.Dir(dst), "."+filepath.Base(dst)+".copy-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := io.Copy(tmp, r); err != nil {
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
	published, err := atomicfile.PublishNew(tmp.Name(), dst)
	if err != nil && published {

		return fmt.Errorf("the copy is in place, whole, and its directory could not be made durable: %w", err)
	}
	return err
}

const SnapshotSums = escrow.SumsName

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
	tmp := filepath.Join(dir, "."+SnapshotSums+".tmp")
	if err := writeFileDurably(tmp, []byte(b.String())); err != nil {
		return nil, err
	}
	if err := os.Rename(tmp, filepath.Join(dir, SnapshotSums)); err != nil {
		os.Remove(tmp)
		return nil, err
	}
	return sums, nil
}

var backupSeqRe = regexp.MustCompile(`^ledger-([0-9]{8}T[0-9]{6}Z)-seq([0-9]+)(` + onDemandMark + `)?(\.tar\.age)?$`)

const onDemandMark = "-ondemand"

func (r *Runtime) sayNoLongerEncrypted(dir, today string) {
	kept, _ := keptSnapshots(dir)
	var last keptSnapshot
	for _, k := range kept {
		if k.Encrypted {
			last = k
		}
	}
	if last.Name == "" {
		return
	}
	detail := "this identity's snapshots were encrypted and are not any more: " + today + " is being written in the clear, because no checked recovery copy of the snapshot key is recorded on this machine. The last encrypted one is " + last.Name + ". Anything that copies the backups folder off this machine now copies the whole record unencrypted. Check your escrow file in Settings → Backups & Keys to encrypt again"
	logsink.Error("maintenance.error", "ALERT (snapshots-no-longer-encrypted): %s", detail)
	if r.w.Store() == nil {
		return
	}
	said, err := r.w.Store().OutboxIDsUnder(noLongerEncryptedNotice)
	if err != nil {
		logsink.Error("maintenance.error", "could not read the outbox (%v) — the log line above is the only record", err)
		return
	}
	for _, id := range said {
		if m := backupSeqRe.FindStringSubmatch(strings.TrimPrefix(id, noLongerEncryptedNotice)); m != nil && m[1] >= last.Stamp {
			return
		}
	}
	if _, err := r.w.Store().AddOutboxNoticeOnce(noLongerEncryptedNotice+last.Name, "operator", "",
		"[maintenance] snapshots-no-longer-encrypted: "+detail, nil); err != nil {
		logsink.Error("maintenance.error", "could not reach the outbox (%v) — the log line above is the only record", err)
	}
}

const noLongerEncryptedNotice = "maint_snapshots-no-longer-encrypted_"

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

var backupEvidenceRe = regexp.MustCompile(`^ledger-[0-9TZ]+-seq[0-9]+`)

func IsBackupEvidence(name string) bool {
	return backupEvidenceRe.MatchString(name) || strings.HasPrefix(name, SnapshotWorkPrefix)
}

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
	_, err = os.Stat(filepath.Join(dir, name, SnapshotSums))
	return err == nil
}

func pruneBackups(dir string, keep int) int {
	kept, debris := keptSnapshots(dir)
	for _, name := range debris {
		os.RemoveAll(filepath.Join(dir, name))
	}

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

const onDemandKeep = 1

const SnapshotWorkPrefix = ".snapshot-"

func (r *Runtime) SweepSnapshotDebrisAtBoot(cfg Settings) {
	if r.w.Safe() {
		return
	}
	dir := r.BackupsDir(cfg)
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
		if !strings.HasPrefix(e.Name(), SnapshotWorkPrefix) {
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

func (r *Runtime) Alert(kind, detail string) {
	logsink.Error("maintenance.error", "ALERT (%s): %s", kind, detail)
	if r.w.Store() == nil {
		return
	}
	id := "maint_" + kind + "_" + time.Now().UTC().Format("20060102")
	if _, err := r.w.Store().AddOutboxNoticeOnce(id, "operator", "",
		"[maintenance] "+kind+": "+detail, nil); err != nil {
		logsink.Error("maintenance.error", "could not reach the outbox (%v) — the log line above is the only record", err)
	}
}
