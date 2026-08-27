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

	"github.com/aiii-dot-id/aii-os/internal/cognitive"
	"github.com/aiii-dot-id/aii-os/internal/genesis"
)

// maintenance.go — the substrate's housekeeping: one daily pass that
// verifies the record and, only when verification holds, copies it.
//
// DESIGNED BY DELETION (Method pass ×2, 2026-08-26; the contract lives
// in docs/MAINTENANCE.md). Three versions died on the way here:
// retention solved a growth problem the numbers do not show; VACUUM
// reclaimed freelist pages that SQLite was already reusing; a "runner"
// with its own scheduling window duplicated the TIME facility beside
// itself. What survived is what the truths demand and nothing else:
// the ledger is the ONLY irreplaceable artifact, corruption is silent
// until something looks, and the system already owns a durable
// scheduler with boot catch-up — so a missed day fires late instead of
// never.
//
// VERIFICATION IS THE COPY'S GATE, NOT A SIBLING DUTY. The worst
// backup failure is not a missing copy — it is corruption copied into
// the backup set, aging the good copies out. So nothing is published
// that did not just verify: the pass copies first, walks the COPY's
// full chain (genesis.VerifySelfContained — the same public
// conformance check `aii verify` runs), and only then renames it into
// the set. A live ledger that fails its walk produces no copy, an
// outbox alert, and — by refusing — actively protects the older good
// copies.
//
// INVARIANTS: the ledger is never written, only read and copied;
// identity.sec is never touched; no database table is ever modified
// (QuickCheck is a read); SAFE verifies and reports but writes no new
// files. Restore is documented, not coded: stop, restore the ledger,
// delete the db, boot replays.

const (
	// maintenanceAlarmID / maintenanceOwnerName: the daily pass rides
	// TIME's durable alarms — restart-surviving, and canon #13's boot
	// catch-up turns "the machine was off at 04:00" into a late firing
	// rather than a silent skip, which no cron-style window can do.
	maintenanceAlarmID   = "maintenance.daily"
	maintenanceOwnerName = "maintenance"

	// backupsDirName lives beside the database, inside the identity —
	// the whole identity remains one rsync-able tree, and offsite
	// transport stays what it is: the operator's.
	backupsDirName = "backups"

	// maintenanceHourLocal is the daily deadline — an absolute local hour
	// (see armMaintenanceAlarm), chosen so the pass runs while the machine
	// is quiet rather than beside whatever a boot is doing.
	maintenanceHourLocal = 4

	// defaultBackupKeep bounds the set. Copies of an append-only file
	// are prefixes of each other, so depth is not redundancy against
	// loss — it is history against a bad append or rewrap that
	// verification might one day wave through.
	defaultBackupKeep = 8
)

// maintenanceOwner is the TIME alarm owner. It names its own next
// deadline (see armMaintenanceAlarm) — MORNING_BRIEF's shape, and the
// only shape that keeps the pass on an absolute hour.
type maintenanceOwner struct{ a *App }

func (o maintenanceOwner) Name() string { return maintenanceOwnerName }

func (o maintenanceOwner) OnAlarm(_ context.Context, _ string, _ string, _ int64, _ string) cognitive.AlarmResult {
	o.a.runMaintenance()
	next := cognitive.NextLocalDaily(time.Now(), maintenanceHourLocal, 0)
	return cognitive.AlarmResult{Accepted: true, NextDeadline: &next}
}

// armMaintenanceAlarm arms the one daily alarm at the next 04:00 local.
//
// THE DEADLINE IS ABSOLUTE BECAUSE ARMING RUNS ON EVERY BOOT. It used to
// be now+1h, and a deadline measured from boot is pushed forward by the
// next boot: a host restarted more often than the delay — the operator's
// own daemon restarts ~18 times a day — never reached it, so the pass
// that verifies and copies the only irreplaceable artifact had never run
// once. An absolute hour makes re-arming idempotent — morning_brief
// (app.go's other re-armed alarm) answers the same problem the same way —
// and it serves what the delay was for by a surer route: the pass runs at
// a quiet hour, not an hour after whenever the machine happened to start.
// A deadline already in the past is not this function's to rescue: TIME's
// boot catch-up (canon #13) runs before arming and fires it late.
//
// AND NO repeat_every, FOR THE SAME REASON ONE STEP ON. TIME settles a
// repeating alarm at the clock read AFTER the owner returned
// (applyTransitions in time.go), so a 24h repeat re-arms at
// finished-at+24h and every pass pushes the next one later by however
// long it took — absolute at boot, drifting from then on. The owner
// names the next absolute hour instead, which is why it must always
// return one: an accepted firing with neither repeat nor next deadline
// is DELETED.
func armMaintenanceAlarm(t *cognitive.TIME, now time.Time) error {
	next := cognitive.NextLocalDaily(now, maintenanceHourLocal, 0)
	return t.SetAlarm(maintenanceAlarmID, maintenanceOwnerName, "wall", next, nil, "")
}

// maintenanceEnabled: an absent block means ON. A *bool because the Go
// zero value would silently read an omitted config as OFF — and a
// protection that defaults to off protects the people who least know
// to ask for it.
func maintenanceEnabled(cfg Config) bool {
	if cfg.Maintenance.Enabled == nil {
		return true
	}
	return *cfg.Maintenance.Enabled
}

func maintenanceKeep(cfg Config) int {
	if cfg.Maintenance.BackupKeep <= 0 {
		return defaultBackupKeep // turning maintenance off is enabled:false, not keep:0
	}
	return cfg.Maintenance.BackupKeep
}

func (a *App) backupsDir(cfg Config) string {
	return filepath.Join(filepath.Dir(cfg.Identity.DBPath), backupsDirName)
}

// runMaintenance is the whole pass. Every step fails soft and is said
// out loud; the one receipt line at the end is the honest record of
// what happened, including nothing.
func (a *App) runMaintenance() {
	cfg := a.configSnapshot()
	if !maintenanceEnabled(cfg) {
		log.Printf("MAINTENANCE: disabled by config; nothing verified, nothing copied")
		return
	}
	start := time.Now()

	// Database canary — cheap, read-only, independent of the ledger
	// walk. The db is f(ledger) and fully rebuildable, so a failure
	// here is an alert and a replay recommendation, never a repair
	// attempt.
	dbNote := "quick_check ok"
	if a.store != nil {
		if err := a.store.QuickCheck(); err != nil {
			dbNote = "quick_check FAILED"
			a.maintenanceAlert("db-quick-check",
				fmt.Sprintf("%v — the database is a projection; stop, delete it, and boot to replay from the ledger", err))
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
		// SAFE: integrity evidence is exactly what is wanted; new
		// files are not. Verify the live file, report, stop.
		n, _, err := genesis.VerifySelfContained(cfg.Identity.LedgerPath)
		if err != nil {
			a.maintenanceAlert("ledger-chain", fmt.Sprintf("SAFE-mode verification failed: %v", err))
			log.Printf("MAINTENANCE: SAFE; chain FAILED; %s; no copy considered (%.1fs)", dbNote, time.Since(start).Seconds())
			return
		}
		log.Printf("MAINTENANCE: SAFE; chain ok (%d events); %s; no copy in SAFE (%.1fs)", n, dbNote, time.Since(start).Seconds())

	case cur > newest:
		// The ledger grew: today's verification IS the copy's gate.
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
		// Nothing new to copy — the day's duty is verification alone.
		n, _, err := genesis.VerifySelfContained(cfg.Identity.LedgerPath)
		if err != nil {
			a.maintenanceAlert("ledger-chain", fmt.Sprintf("verification failed: %v", err))
			log.Printf("MAINTENANCE: chain FAILED; %s; existing copies untouched (%.1fs)", dbNote, time.Since(start).Seconds())
			return
		}
		log.Printf("MAINTENANCE: chain ok (%d events); nothing new since seq %d; %s (%.1fs)", n, newest, dbNote, time.Since(start).Seconds())
	}
}

// maintenanceBackup copies the ledger to the backups directory,
// verifies THE COPY end-to-end, and publishes it with a sidecar.
//
// The copy stops at the last complete line, so an append racing the
// copy costs one event's timing, never a torn tail. The sidecar is
// written LAST and in `sha256sum -c` format: a copy without its
// sidecar is by definition incomplete and is pruned; a copy with one
// is verifiable by the operator with stock tools, no aii binary
// needed.
func (a *App) maintenanceBackup(cfg Config, dir string) (string, uint64, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", 0, fmt.Errorf("backups dir: %w", err)
	}
	// ONE TEMP PER PASS, never a shared name. The path was fixed, so two
	// passes running at once wrote the same file: the copy spliced, the
	// gate below refused it, and the operator got a corruption-shaped
	// alert and no backup that day — the alarm was innocent. Same
	// directory, so publishing stays an atomic same-filesystem rename.
	tf, err := os.CreateTemp(dir, ".ledger.copy.*.tmp")
	if err != nil {
		return "", 0, fmt.Errorf("copy: %w", err)
	}
	tmp := tf.Name()
	tf.Close()
	// Every path that does not publish leaves debris; after the rename the
	// name is already gone and this is a no-op.
	defer os.Remove(tmp)
	if err := copyToLastNewline(cfg.Identity.LedgerPath, tmp); err != nil {
		return "", 0, fmt.Errorf("copy: %w", err)
	}
	// THE GATE. The artifact that will be kept is the artifact that
	// was verified — no gap for an append or a fault to slip through.
	n, fp, err := genesis.VerifySelfContained(tmp)
	if err != nil {
		return "", 0, fmt.Errorf("copy does not verify — not published: %w", err)
	}
	name := fmt.Sprintf("ledger-%s-seq%d.jsonl", time.Now().UTC().Format("20060102T150405Z"), n)
	dst := filepath.Join(dir, name)
	if err := os.Rename(tmp, dst); err != nil {
		return "", 0, fmt.Errorf("publish: %w", err)
	}
	if err := writeSidecar(dst, name); err != nil {
		return "", 0, fmt.Errorf("sidecar for %s: %w", name, err)
	}
	// The witness tail travels with the newest copy; tiny, refreshed
	// each pass, absent is normal (no witness configured).
	copySmall(filepath.Join(filepath.Dir(cfg.Identity.DBPath), "witness-tail.json"),
		filepath.Join(dir, "witness-tail.json"))
	log.Printf("MAINTENANCE: identity %s", fp)
	return name, uint64(n), nil
}

// copyToLastNewline streams src to dst, then truncates dst to the last
// byte of the last complete line. A ledger being appended mid-copy
// yields yesterday's-plus events, never half an event.
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
	if lastNL == 0 {
		out.Close()
		return fmt.Errorf("no complete events in %s", src)
	}
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

// writeSidecar hashes the published file and writes <file>.sha256 in
// coreutils format, atomically, AFTER the file it describes exists
// under its real name.
func writeSidecar(path, name string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	h := sha256.New()
	_, err = io.Copy(h, f)
	f.Close()
	if err != nil {
		return err
	}
	line := hex.EncodeToString(h.Sum(nil)) + "  " + name + "\n"
	tmp := path + ".sha256.tmp"
	if err := os.WriteFile(tmp, []byte(line), 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, path+".sha256"); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

func copySmall(src, dst string) {
	raw, err := os.ReadFile(src)
	if err != nil {
		return // absent is the normal case
	}
	tmp := dst + ".tmp"
	if os.WriteFile(tmp, raw, 0o600) == nil {
		if os.Rename(tmp, dst) != nil {
			os.Remove(tmp)
		}
	}
}

// backupSeqRe pins the published name shape; anything else in the
// directory is not ours to count or to prune.
var backupSeqRe = regexp.MustCompile(`^ledger-[0-9TZ]+-seq([0-9]+)\.jsonl$`)

func newestBackupSeq(dir string) uint64 {
	var newest uint64
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	for _, e := range entries {
		if m := backupSeqRe.FindStringSubmatch(e.Name()); m != nil {
			if n, err := strconv.ParseUint(m[1], 10, 64); err == nil && n > newest {
				newest = n
			}
		}
	}
	return newest
}

// pruneBackups removes the oldest copies beyond keep — file first,
// sidecar second, and an orphaned sidecar (its file half-removed by a
// crash here) counts as debris and goes too. The newest copy is never
// eligible: keep is floored at 1 above.
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
	have := map[string]bool{}
	for _, e := range entries {
		have[e.Name()] = true
		if m := backupSeqRe.FindStringSubmatch(e.Name()); m != nil {
			n, err := strconv.ParseUint(m[1], 10, 64)
			if err == nil {
				set = append(set, bk{seq: n, name: e.Name()})
			}
		}
	}
	sort.Slice(set, func(i, j int) bool { return set[i].seq < set[j].seq })
	removed := 0
	for i := 0; i < len(set)-keep; i++ {
		if os.Remove(filepath.Join(dir, set[i].name)) == nil {
			removed++
		}
		os.Remove(filepath.Join(dir, set[i].name+".sha256"))
	}
	// Orphaned sidecars: a sidecar whose file is gone describes nothing.
	for name := range have {
		base, ok := strings.CutSuffix(name, ".sha256")
		if ok && backupSeqRe.MatchString(base) && !have[base] {
			os.Remove(filepath.Join(dir, name))
		}
	}
	return removed
}

// maintenanceAlert reaches the operator where they look — the outbox —
// idempotently per day per kind, so a broken chain does not become
// thirty copies of the same message. The log line is unconditional.
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
