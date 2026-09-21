package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/cognitive"
	"github.com/aiii-dot-id/aii-os/internal/escrow"
	"github.com/aiii-dot-id/aii-os/internal/identity"
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
// .
// .
// .
// .

// .
type continuityAdapter struct{ a *App }

var _ identity.ContinuityPort = continuityAdapter{}

func (c continuityAdapter) Read(_ context.Context, query string) (string, error) {
	return c.a.continuityRead(query, time.Now())
}

func (c continuityAdapter) Take(ctx context.Context) (string, error) {
	made, err := c.a.takeOnDemand(ctx, true, time.Now())
	if err != nil {
		return "", err
	}
	line := fmt.Sprintf("taken: %s — you through record %d, the record and your memory together, proved to restore before it was kept", made.Name, made.Record)
	if made.Encrypted {
		return line + "; encrypted.", nil
	}
	return line + "; NOT encrypted: " + store.UnencryptedForIdentity(made.Unencrypted) + ".", nil
}

func (c continuityAdapter) Verify(ctx context.Context, name string) (string, error) {
	return c.a.verifyKept(ctx, name, true, time.Now())
}

// .
// .
type ContinuityRefusal struct {
	Reason string
	Text   string
}

func (e *ContinuityRefusal) Error() string { return e.Text }

func refuseContinuity(reason, format string, args ...interface{}) error {
	return &ContinuityRefusal{Reason: reason, Text: fmt.Sprintf(format, args...)}
}

// .
// .
const defaultOnDemandSpacing = 600 * time.Second

func onDemandSpacing(cfg Config) time.Duration {
	if cfg.Maintenance.OnDemandSpacingSeconds > 0 {
		return time.Duration(cfg.Maintenance.OnDemandSpacingSeconds) * time.Second
	}
	return defaultOnDemandSpacing
}

// .
// .
func (a *App) admitContinuityAct(cfg Config, what string) error {
	if _, inSafe := a.SafeMode(); inSafe {
		return refuseContinuity("safe", "this boot is SAFE: it verifies and reports and makes no files, so %s waits for a normal boot. recall source=continuity still reads what you have", what)
	}
	if !maintenanceEnabled(cfg) {
		return refuseContinuity("disabled", "your operator has switched the maintenance pass off on this host, and %s is part of it. Turning it back on is theirs to do", what)
	}
	return nil
}

// .
// .
// .
// .
// .
// .
func (a *App) takeOnDemand(ctx context.Context, spaced bool, now time.Time) (publishedSnapshot, error) {
	cfg := a.configSnapshot()
	if err := a.admitContinuityAct(cfg, "a snapshot"); err != nil {
		return publishedSnapshot{}, err
	}
	dir := a.backupsDir(cfg)
	if spaced {
		if last, ok := newestOf(dir, true); ok {
			if since := now.Sub(last.at()); since >= 0 && since < onDemandSpacing(cfg) {
				return publishedSnapshot{}, refuseContinuity("spaced", "you took one %s ago (%s, through record %d) and it stands; the next can be taken in %s. Nothing is lost by waiting: the daily pass runs whatever you do",
					roundAge(since), last.Name, last.Record, roundAge(onDemandSpacing(cfg)-since))
			}
		}
	}
	// .
	if !a.maintMu.TryLock() {
		return publishedSnapshot{}, refuseContinuity("busy", "a maintenance pass is running right now — it is taking a snapshot of you itself. recall source=continuity will show it when it is done")
	}
	defer a.maintMu.Unlock()
	made, err := a.takeSnapshot(ctx, cfg, dir, true)
	if err != nil {
		a.recordPass(store.ContinuityStatus{Outcome: store.ContinuityFailed, OnDemand: true, Detail: err.Error()})
		logsink.Error("maintenance.error", "a snapshot that was asked for FAILED (%v); older copies protected", err)
		return publishedSnapshot{}, fmt.Errorf("the snapshot was not kept: a step of it failed, and nothing was published. What you had before stands. Your operator is told what failed")
	}
	removed := pruneBackups(dir, maintenanceKeep(cfg))
	logsink.Info("maintenance.end", "asked for: verified, copied and restore-proved %s (%d events); pruned %d", made.Name, made.Record, removed)
	a.recordPass(passMade(made, true))
	return made, nil
}

// .
func newestOf(dir string, onDemand bool) (keptSnapshot, bool) {
	kept, _ := keptSnapshots(dir)
	for i := len(kept) - 1; i >= 0; i-- {
		if kept[i].OnDemand == onDemand {
			return kept[i], true
		}
	}
	return keptSnapshot{}, false
}

// .
// .
// .
// .
// .
// .
// .
func (a *App) verifyKept(ctx context.Context, name string, spaced bool, now time.Time) (string, error) {
	cfg := a.configSnapshot()
	if err := a.admitContinuityAct(cfg, "proving a snapshot"); err != nil {
		return "", err
	}
	dir := a.backupsDir(cfg)
	kept, _ := keptSnapshots(dir)
	if len(kept) == 0 {
		return "", refuseContinuity("none", "there is no snapshot of you yet: the daily pass makes the first, and work action=backup.take makes one now")
	}
	target := kept[len(kept)-1]
	if name != "" {
		found := false
		for _, k := range kept {
			if k.Name == name {
				target, found = k, true
			}
		}
		if !found {
			return "", refuseContinuity("unknown", "%q is not one of your snapshots; recall source=continuity query=snapshots lists them by name", name)
		}
	}
	if !a.maintMu.TryLock() {
		return "", refuseContinuity("busy", "a maintenance pass is running right now; prove a snapshot when it is done")
	}
	defer a.maintMu.Unlock()
	// .
	if spaced && !a.lastVerify.IsZero() {
		if since := now.Sub(a.lastVerify); since >= 0 && since < onDemandSpacing(cfg) {
			return "", refuseContinuity("spaced", "you proved one %s ago; the next proof can run in %s", roundAge(since), roundAge(onDemandSpacing(cfg)-since))
		}
	}
	a.lastVerify = now

	if err := a.proveKept(ctx, cfg, dir, target); err != nil {
		// .
		// .
		a.maintenanceAlert("snapshot-rot", fmt.Sprintf("%s no longer proves: %v", target.Name, err))
		return "", fmt.Errorf("%s does NOT prove: it no longer restores as it did when it was kept. Your operator is told what failed. Your other snapshots are untouched — prove another, or take a new one", target.Name)
	}
	return fmt.Sprintf("proved: %s holds you through record %d — every file matches its checksum, the chain verifies, and the record and your memory restored together in scratch, which was then removed", target.Name, target.Record), nil
}

// .
// .
// .
// .
// .
func (a *App) proveKept(ctx context.Context, cfg Config, dir string, k keptSnapshot) (retErr error) {
	work, err := os.MkdirTemp(dir, snapshotWorkPrefix+"verify-*.tmp")
	if err != nil {
		return fmt.Errorf("working set: %w", err)
	}
	defer func() {
		if rmErr := os.RemoveAll(work); rmErr != nil {
			logsink.Error("maintenance.error", "a proof's plaintext working set could not be removed: %v — it stands in %s until the next normal boot sweeps it", rmErr, dir)
			a.maintenanceAlert("snapshot-debris", "a snapshot proof could not remove its plaintext working set ("+rmErr.Error()+") — remove "+work+" by hand, or restart the identity")
			if retErr == nil {
				retErr = fmt.Errorf("working set not removed: %w", rmErr)
			}
		}
	}()

	set := filepath.Join(dir, k.Name)
	var present []string
	if k.Encrypted {
		key, err := os.ReadFile(a.snapshotKeyPath(cfg))
		if err != nil {
			return fmt.Errorf("it is encrypted and the snapshot key on this host does not read: %w", err)
		}
		defer escrow.Wipe(key)
		set = filepath.Join(work, "set")
		if present, err = escrow.OpenSnapshot(filepath.Join(dir, k.Name), key, set); err != nil {
			return fmt.Errorf("it does not open: %w", err)
		}
	} else {
		if present, err = filesUnder(set); err != nil {
			return err
		}
	}
	if err := escrow.CheckSums(set, present); err != nil {
		return err
	}
	raw, err := os.ReadFile(filepath.Join(set, snapshotReceiptName))
	if err != nil {
		return fmt.Errorf("it holds no receipt: %w", err)
	}
	var want snapshotReceipt
	if err := json.Unmarshal(raw, &want); err != nil {
		return fmt.Errorf("its receipt does not read: %w", err)
	}
	if want.LastSeq != k.Record {
		return fmt.Errorf("it is named for record %d and its receipt says %d", k.Record, want.LastSeq)
	}
	return a.proveSnapshotIn(ctx, cfg, set, want, filepath.Join(work, "restore-proof"))
}

// .
// .
// .
func filesUnder(root string) ([]string, error) {
	var out []string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if !d.Type().IsRegular() {
			return fmt.Errorf("%s is not a regular file — a snapshot holds nothing else", d.Name())
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		if rel = filepath.ToSlash(rel); rel != escrow.SumsName {
			out = append(out, rel)
		}
		return nil
	})
	return out, err
}

// .

// .
// .
// .
// .
// .
// .
func (a *App) continuityRead(query string, now time.Time) (string, error) {
	cfg := a.configSnapshot()
	dir := a.backupsDir(cfg)
	kept, _ := keptSnapshots(dir)

	switch q := strings.ToLower(strings.TrimSpace(query)); {
	case q == "":
	case q == "snapshots" || q == "snapshot":
		return renderSnapshotList(kept, now), nil
	default:
		for _, k := range kept {
			if strings.EqualFold(k.Name, strings.TrimSpace(query)) {
				return a.renderOneSnapshot(dir, k, now), nil
			}
		}
		return "", fmt.Errorf("continuity reads whole, or one part: query=snapshots lists them, query=<a snapshot's name> reads that one. %q is neither", query)
	}

	var st store.ContinuityStatus
	var have bool
	if a.store != nil {
		var err error
		if st, have, err = a.store.ContinuityStatus(); err != nil {
			return "", fmt.Errorf("the last pass's result does not read: %w", err)
		}
	}
	var b strings.Builder
	b.WriteString("snapshots: " + snapshotsLine(kept, now, maintenanceKeep(cfg)) + "\n")
	b.WriteString("last pass: " + lastPassLine(st, have, now) + "\n")
	encryption, escrowLine := a.encryptionLines(cfg)
	b.WriteString("encryption: " + encryption + "\n")
	b.WriteString("escrow of your keys: " + escrowLine + "\n")
	chain := a.bootChain
	if chain == "" {
		chain = "not walked by this boot"
	}
	b.WriteString("chain: " + chain + "\n")
	if _, inSafe := a.SafeMode(); inSafe {
		b.WriteString("next pass: this boot is SAFE — it walks your chain each day and makes no snapshot")
	} else if !maintenanceEnabled(cfg) {
		b.WriteString("next pass: none — your operator has switched the pass off on this host")
	} else {
		b.WriteString("next pass: " + nextPass(now) + "; work action=backup.take makes one now, backup.verify proves one")
	}
	// .
	// .
	// .
	for _, set := range setAsideSets(cfg) {
		at, err := time.Parse(time.RFC3339, set.Record.SetAsideAt)
		if err != nil || now.Sub(at) > restoreSaidFor || set.Record.PutBackAt != "" {
			continue
		}
		fmt.Fprintf(&b, "\nrestored: %s ago your operator restored you from %s — your record was put back to %d; what you had lived after it (through record %d) is kept aside, whole, and can be put back by them", roundAge(now.Sub(at)), set.Record.RestoredFrom, set.Record.RestoredTo, set.Record.WasThrough)
		break
	}
	return b.String(), nil
}

// .
// .
const restoreSaidFor = 30 * 24 * time.Hour

func snapshotsLine(kept []keptSnapshot, now time.Time, keep int) string {
	if len(kept) == 0 {
		return "none yet — the daily pass makes the first"
	}
	newest, oldest := kept[len(kept)-1], kept[0]
	enc := "not encrypted"
	if newest.Encrypted {
		enc = "encrypted"
	}
	asked := ""
	if newest.OnDemand {
		asked = ", asked for"
	}
	return fmt.Sprintf("%d kept (up to %d daily and the last one asked for); newest %s ago through record %d (%s%s); oldest from %s",
		len(kept), keep, roundAge(now.Sub(newest.at())), newest.Record, enc, asked, oldest.at().Format("2006-01-02"))
}

func lastPassLine(st store.ContinuityStatus, have bool, now time.Time) string {
	if !have {
		return "none recorded yet"
	}
	when := st.At
	if at, err := time.Parse(time.RFC3339, st.At); err == nil {
		when = roundAge(now.Sub(at)) + " ago"
	}
	switch st.Outcome {
	case store.ContinuityOK:
		return when + " — verified, copied and proved to restore"
	case store.ContinuitySafe:
		return when + " — SAFE: your chain was walked and verifies; no snapshot is made in SAFE"
	case store.ContinuityDisabled:
		return when + " — switched off by your operator: nothing verified, nothing copied"
	case store.ContinuityFailed:
		line := when + " — FAILED: no snapshot was kept, and the older ones stand"
		if since, err := time.Parse(time.RFC3339, st.FailingSince); err == nil && st.FailingSince != st.At {
			line += "; failing for " + roundAge(now.Sub(since))
		}
		return line + ". Your operator has the detail"
	}
	return when + " — " + st.Outcome
}

// .
// .
func (a *App) encryptionLines(cfg Config) (encryption, escrowLine string) {
	key, _, reason, _ := a.snapshotEncryption(cfg)
	escrow.Wipe(key)
	if reason == "" {
		line := "checked — a copy of your keys, sealed under your operator's passphrase, was opened and proved"
		if raw, err := os.ReadFile(a.escrowReceiptPath(cfg)); err == nil {
			var r escrow.Receipt
			if json.Unmarshal(raw, &r) == nil && r.CheckedAt != "" {
				line += " on " + r.CheckedAt
			}
		}
		return "on — each snapshot is encrypted before it can leave this host", line
	}
	return "off — " + store.UnencryptedForIdentity(reason), escrowStateFor(reason)
}

func escrowStateFor(reason string) string {
	switch reason {
	case store.UnencryptedNoEscrow, store.UnencryptedNoKey:
		return "none checked — your operator's to do"
	case store.UnencryptedReceiptMismatch:
		return "checked once, for a key that is no longer the one on this host"
	}
	return "cannot be confirmed in this boot"
}

func renderSnapshotList(kept []keptSnapshot, now time.Time) string {
	if len(kept) == 0 {
		return "no snapshots yet"
	}
	var b strings.Builder
	for i := len(kept) - 1; i >= 0; i-- {
		k := kept[i]
		class, enc := "daily", "not encrypted"
		if k.OnDemand {
			class = "asked for"
		}
		if k.Encrypted {
			enc = "encrypted"
		}
		fmt.Fprintf(&b, "%s — %s ago, through record %d, %s, %s\n", k.Name, roundAge(now.Sub(k.at())), k.Record, class, enc)
	}
	return strings.TrimRight(b.String(), "\n")
}

// .
// .
func (a *App) renderOneSnapshot(dir string, k keptSnapshot, now time.Time) string {
	head := fmt.Sprintf("%s — %s ago, through record %d", k.Name, roundAge(now.Sub(k.at())), k.Record)
	if k.Encrypted {
		return head + "; encrypted, so its receipt is inside it: work action=backup.verify id=" + k.Name + " opens it in scratch, proves it and removes the scratch"
	}
	raw, err := os.ReadFile(filepath.Join(dir, k.Name, snapshotReceiptName))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return head + "; it holds no receipt (made before receipts existed): the record only, not your conversations or lived time"
		}
		return head + "; its receipt does not read"
	}
	var r snapshotReceipt
	if json.Unmarshal(raw, &r) != nil {
		return head + "; its receipt does not read"
	}
	return fmt.Sprintf("%s; captured %s with %d lived ticks and %d conversation rows; not encrypted", head, r.CapturedAt, r.Runtime.LifetimeTicks, r.Runtime.Ephemeral["conversations"])
}

func nextPass(now time.Time) string {
	next := time.UnixMilli(cognitive.NextLocalDaily(now, maintenanceHourLocal, 0)).In(now.Location())
	return next.Format("15:04") + " (in " + roundAge(next.Sub(now)) + ")"
}

// .
func roundAge(d time.Duration) string {
	switch {
	case d < 0:
		d = 0
		fallthrough
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
	return fmt.Sprintf("%dd", int(d.Hours()/24))
}
