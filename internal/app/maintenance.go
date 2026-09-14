package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/atomicfile"
	"github.com/aiii-dot-id/aii-os/internal/cognitive"
	"github.com/aiii-dot-id/aii-os/internal/genesis"
	"github.com/aiii-dot-id/aii-os/internal/ledger"
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

func (o maintenanceOwner) OnAlarm(_ context.Context, _ string, _ string, _ int64, _ string) cognitive.AlarmResult {
	o.a.runMaintenance()
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
func (a *App) runMaintenance() {
	cfg := a.configSnapshot()
	if !maintenanceEnabled(cfg) {
		log.Printf("MAINTENANCE: disabled by config; nothing verified, nothing copied")
		return
	}
	start := time.Now()

	// .
	// .
	// .
	// .
	dbNote := "quick_check ok"
	if a.store != nil {
		if err := a.store.QuickCheck(); err != nil {
			dbNote = "quick_check FAILED"
			a.maintenanceAlert("db-quick-check",
				fmt.Sprintf("%v — the database is a projection; stop, delete it, and boot to replay from the ledger", err))
		}
		// .
		// .
		// .
		// .
		if err := a.store.ForeignKeyCheck(); err != nil {
			dbNote += ", foreign keys FAILED"
			a.maintenanceAlert("db-foreign-keys",
				fmt.Sprintf("%v — the database is a projection; stop, delete it, and boot to replay from the ledger", err))
		} else {
			dbNote += ", foreign keys ok"
		}
		// .
		// .
		// .
		if note, err := a.store.Housekeep(); err != nil {
			dbNote += ", housekeeping failed: " + err.Error()
			log.Printf("MAINTENANCE: housekeeping: %v", err)
		} else {
			dbNote += ", " + note
		}
	}

	_, inSafe := a.SafeMode()
	dir := a.backupsDir(cfg)
	newest := newestBackupSeq(dir)
	var cur uint64
	if a.ledger != nil {
		cur = a.ledger.LastSeq()
	}

	switch {
	case inSafe:
		// .
		// .
		n, err := a.verifyLiveLedger(cfg)
		if err != nil {
			a.maintenanceAlert("ledger-chain", fmt.Sprintf("SAFE-mode verification failed: %v", err))
			log.Printf("MAINTENANCE: SAFE; chain FAILED; %s; no copy considered (%.1fs)", dbNote, time.Since(start).Seconds())
			return
		}
		log.Printf("MAINTENANCE: SAFE; chain ok (%d events); %s; no copy in SAFE (%.1fs)", n, dbNote, time.Since(start).Seconds())

	case cur > newest:
		// .
		name, n, err := a.maintenanceBackup(cfg, dir)
		if err != nil {
			a.maintenanceAlert("backup", err.Error())
			log.Printf("MAINTENANCE: chain/copy FAILED (%v); %s; older copies protected (%.1fs)", err, dbNote, time.Since(start).Seconds())
			return
		}
		removed := pruneBackups(dir, maintenanceKeep(cfg))
		log.Printf("MAINTENANCE: verified and copied %s (%d events); pruned %d; %s (%.1fs)",
			name, n, removed, dbNote, time.Since(start).Seconds())

	default:
		// .
		n, err := a.verifyLiveLedger(cfg)
		if err != nil {
			a.maintenanceAlert("ledger-chain", fmt.Sprintf("verification failed: %v", err))
			log.Printf("MAINTENANCE: chain FAILED; %s; existing copies untouched (%.1fs)", dbNote, time.Since(start).Seconds())
			return
		}
		log.Printf("MAINTENANCE: chain ok (%d events); nothing new since seq %d; %s (%.1fs)", n, newest, dbNote, time.Since(start).Seconds())
	}
}

// .
// .
// .
// .
// .
// .
// .
// .
func (a *App) maintenanceBackup(cfg Config, dir string) (string, uint64, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", 0, fmt.Errorf("backups dir: %w", err)
	}
	// .
	// .
	// .
	// .
	// .
	tmp, err := os.MkdirTemp(dir, ".snapshot-*.tmp")
	if err != nil {
		return "", 0, fmt.Errorf("copy: %w", err)
	}
	// .
	// .
	defer os.RemoveAll(tmp)
	srcDir := filepath.Dir(cfg.Identity.LedgerPath)
	var names []string
	// .
	entries, err := os.ReadDir(srcDir)
	if err != nil {
		return "", 0, fmt.Errorf("ledger directory: %w", err)
	}
	for _, e := range entries {
		if e.IsDir() || !ledger.IsSegmentName(e.Name()) {
			continue
		}
		if err := copyFile(filepath.Join(srcDir, e.Name()), filepath.Join(tmp, e.Name())); err != nil {
			return "", 0, fmt.Errorf("copy %s: %w", e.Name(), err)
		}
		names = append(names, e.Name())
	}
	// .
	// .
	if keys, err := os.ReadDir(witness.WitnessKeysDir(srcDir)); err == nil {
		sub := filepath.Join(tmp, witness.WitnessKeysDirName)
		if err := os.MkdirAll(sub, 0o700); err != nil {
			return "", 0, fmt.Errorf("copy witness keys: %w", err)
		}
		for _, e := range keys {
			n := e.Name()
			if e.IsDir() || strings.HasPrefix(n, ".") || !strings.HasSuffix(n, ".json") {
				continue
			}
			if err := copyFile(filepath.Join(srcDir, witness.WitnessKeysDirName, n), filepath.Join(sub, n)); err != nil {
				return "", 0, fmt.Errorf("copy witness key %s: %w", n, err)
			}
			names = append(names, witness.WitnessKeysDirName+"/"+n)
		}
	}
	// .
	// .
	if err := copyToLastNewline(cfg.Identity.LedgerPath, filepath.Join(tmp, "ledger.jsonl")); err != nil {
		return "", 0, fmt.Errorf("copy: %w", err)
	}
	names = append(names, "ledger.jsonl")
	// .
	if copySmall(filepath.Join(filepath.Dir(cfg.Identity.DBPath), "witness-tail.json"), filepath.Join(tmp, "witness-tail.json")) {
		names = append(names, "witness-tail.json")
	}
	// .
	// .
	// .
	heads, err := a.headVerifierFor(cfg, tmp)
	if err != nil {
		return "", 0, fmt.Errorf("copy does not verify — not published: witness keys: %w", err)
	}
	n, fp, err := genesis.VerifySelfContainedWith(filepath.Join(tmp, "ledger.jsonl"), heads)
	if err != nil {
		return "", 0, fmt.Errorf("copy does not verify — not published: %w", err)
	}
	if err := writeSums(tmp, names); err != nil {
		return "", 0, fmt.Errorf("checksum list: %w", err)
	}
	name := fmt.Sprintf("ledger-%s-seq%d", time.Now().UTC().Format("20060102T150405Z"), n)
	if err := os.Rename(tmp, filepath.Join(dir, name)); err != nil {
		return "", 0, fmt.Errorf("publish: %w", err)
	}
	if err := atomicfile.SyncDir(dir); err != nil {
		return "", 0, fmt.Errorf("publish: %w", err)
	}
	log.Printf("MAINTENANCE: identity %s", fp)
	return name, uint64(n), nil
}

// .
// .
func (a *App) verifyLiveLedger(cfg Config) (int, error) {
	heads, err := a.headVerifierFor(cfg, filepath.Dir(cfg.Identity.LedgerPath))
	if err != nil {
		return 0, fmt.Errorf("witness keys: %w", err)
	}
	n, _, err := genesis.VerifySelfContainedWith(cfg.Identity.LedgerPath, heads)
	return n, err
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
// .
func copyToLastNewline(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	var written, lastNL int64
	buf := make([]byte, 1<<20)
	for {
		n, rerr := in.Read(buf)
		if n > 0 {
			if _, werr := out.Write(buf[:n]); werr != nil {
				out.Close()
				return werr
			}
			for i := n - 1; i >= 0; i-- {
				if buf[i] == '\n' {
					lastNL = written + int64(i) + 1
					break
				}
			}
			written += int64(n)
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			out.Close()
			return rerr
		}
	}
	// .
	// .
	// .
	if err := out.Truncate(lastNL); err != nil {
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
const snapshotSums = "SHA256SUMS"

// .
// .
func writeSums(dir string, names []string) error {
	var b strings.Builder
	for _, name := range names {
		f, err := os.Open(filepath.Join(dir, filepath.FromSlash(name)))
		if err != nil {
			return err
		}
		h := sha256.New()
		_, err = io.Copy(h, f)
		f.Close()
		if err != nil {
			return err
		}
		b.WriteString(hex.EncodeToString(h.Sum(nil)) + "  " + name + "\n")
	}
	tmp := filepath.Join(dir, "."+snapshotSums+".tmp")
	if err := os.WriteFile(tmp, []byte(b.String()), 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, filepath.Join(dir, snapshotSums)); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

// .
// .
func copySmall(src, dst string) bool {
	raw, err := os.ReadFile(src)
	if err != nil {
		return false
	}
	tmp := dst + ".tmp"
	if os.WriteFile(tmp, raw, 0o600) != nil {
		return false
	}
	if os.Rename(tmp, dst) != nil {
		os.Remove(tmp)
		return false
	}
	return true
}

// .
// .
var backupSeqRe = regexp.MustCompile(`^ledger-[0-9TZ]+-seq([0-9]+)$`)

// .
// .
// .
// .
var backupEvidenceRe = regexp.MustCompile(`^ledger-[0-9TZ]+-seq[0-9]+`)

func snapshotComplete(dir, name string) bool {
	_, err := os.Stat(filepath.Join(dir, name, snapshotSums))
	return err == nil
}

func newestBackupSeq(dir string) uint64 {
	var newest uint64
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	for _, e := range entries {
		if !e.IsDir() || !snapshotComplete(dir, e.Name()) {
			continue
		}
		if m := backupSeqRe.FindStringSubmatch(e.Name()); m != nil {
			if n, err := strconv.ParseUint(m[1], 10, 64); err == nil && n > newest {
				newest = n
			}
		}
	}
	return newest
}

// .
// .
// .
// .
func pruneBackups(dir string, keep int) int {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	type bk struct {
		seq  uint64
		name string
	}
	var set []bk
	for _, e := range entries {
		m := backupSeqRe.FindStringSubmatch(e.Name())
		if m == nil {
			continue
		}
		if !e.IsDir() || !snapshotComplete(dir, e.Name()) {
			os.RemoveAll(filepath.Join(dir, e.Name()))
			continue
		}
		if n, err := strconv.ParseUint(m[1], 10, 64); err == nil {
			set = append(set, bk{seq: n, name: e.Name()})
		}
	}
	sort.Slice(set, func(i, j int) bool { return set[i].seq < set[j].seq })
	removed := 0
	for i := 0; i < len(set)-keep; i++ {
		if os.RemoveAll(filepath.Join(dir, set[i].name)) == nil {
			removed++
		}
	}
	return removed
}

// .
// .
// .
func (a *App) maintenanceAlert(kind, detail string) {
	log.Printf("MAINTENANCE ALERT (%s): %s", kind, detail)
	if a.store == nil {
		return
	}
	id := "maint_" + kind + "_" + time.Now().UTC().Format("20060102")
	if _, err := a.store.AddOutboxMessageOnce(id, "operator", "",
		"[maintenance] "+kind+": "+detail, nil); err != nil {
		log.Printf("MAINTENANCE: could not reach the outbox (%v) — the log line above is the only record", err)
	}
}
