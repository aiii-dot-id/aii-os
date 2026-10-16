package continuity

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

	"github.com/aiii-dot-id/aii-os/internal/atomicfile"
	"github.com/aiii-dot-id/aii-os/internal/cognitive"
	"github.com/aiii-dot-id/aii-os/internal/crypto"
	"github.com/aiii-dot-id/aii-os/internal/dashboard"
	"github.com/aiii-dot-id/aii-os/internal/escrow"
	"github.com/aiii-dot-id/aii-os/internal/identity"
	"github.com/aiii-dot-id/aii-os/internal/ledger"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

type Wires struct {
	Settings func() Settings

	Active func(configured Identity) Identity

	Safe func() bool

	Store func() *store.Store

	Capture func(ctx context.Context, tailDst string) (ledger.Capture, *store.Pin, error)

	DoorKey func() *crypto.KeyPair

	Key    func() *crypto.KeyPair
	SetKey func(*crypto.KeyPair)

	LastSeq func() (uint64, bool)

	BootChain    func() string
	BootAttested func() (int64, string)

	DisplayName func() string

	Go           func(func()) bool
	UntilStopped func(context.Context) (context.Context, func())

	BirthProvider func(*dashboard.GenesisRequest) error

	StartLive  func() error
	SwapToLive func()

	DatabasePath func() string
}

type Seams struct {
	SnapshotStep func(step string)

	RestoreStep func(step string)

	AfterKeyPublished func(path string)

	SyncDir func(path string) error

	KeyBackHolds func(secret []byte)
}

type Runtime struct {
	Seams
	w     Wires
	maint maintenanceState
}

func New(w Wires) *Runtime {
	return &Runtime{w: w}
}

func (r *Runtime) snapshotStep(step string) {
	if r.SnapshotStep != nil {
		r.SnapshotStep(step)
	}
}

func (r *Runtime) restoreStep(step string) {
	if r.RestoreStep != nil {
		r.RestoreStep(step)
	}
}

func (r *Runtime) afterKeyPublished(path string) {
	if r.AfterKeyPublished != nil {
		r.AfterKeyPublished(path)
	}
}

func (r *Runtime) syncDir(path string) error {
	if r.SyncDir != nil {
		return r.SyncDir(path)
	}
	return atomicfile.SyncDir(path)
}

func (r *Runtime) Port() identity.ContinuityPort { return identityPort{r} }

type identityPort struct{ r *Runtime }

var _ identity.ContinuityPort = identityPort{}

func (c identityPort) Read(_ context.Context, query string) (string, error) {
	return c.r.continuityRead(query, time.Now())
}

func (c identityPort) Take(ctx context.Context) (string, error) {
	made, err := c.r.takeOnDemand(ctx, true, time.Now())
	if err != nil {
		return "", err
	}
	line := fmt.Sprintf("taken: %s — you through record %d, the record and your memory together, proved to restore before it was kept", made.Name, made.Record)
	if made.Encrypted {
		return line + "; encrypted.", nil
	}
	return line + "; NOT encrypted: " + store.UnencryptedForIdentity(made.Unencrypted) + ".", nil
}

func (c identityPort) Verify(ctx context.Context, name string) (string, error) {
	return c.r.verifyKept(ctx, name, true, time.Now())
}

type Refusal struct {
	Reason string
	Text   string
}

func (e *Refusal) Error() string { return e.Text }

func refuseContinuity(reason, format string, args ...any) error {
	return &Refusal{Reason: reason, Text: fmt.Sprintf(format, args...)}
}

const DefaultOnDemandSpacing = 600 * time.Second

func OnDemandSpacing(m MaintenanceConfig) time.Duration {
	if m.OnDemandSpacingSeconds > 0 {
		return time.Duration(m.OnDemandSpacingSeconds) * time.Second
	}
	return DefaultOnDemandSpacing
}

func (r *Runtime) admitContinuityAct(cfg Settings, what string) error {
	if r.w.Safe() {
		return refuseContinuity("safe", "this boot is SAFE: it verifies and reports and makes no files, so %s waits for a normal boot. recall source=continuity still reads what you have", what)
	}
	if !MaintenanceEnabled(cfg.Maintenance) {
		return refuseContinuity("disabled", "your operator has switched the maintenance pass off on this host, and %s is part of it. Turning it back on is theirs to do", what)
	}
	return nil
}

func (r *Runtime) takeOnDemand(ctx context.Context, spaced bool, now time.Time) (publishedSnapshot, error) {
	cfg := r.w.Settings()
	if err := r.admitContinuityAct(cfg, "a snapshot"); err != nil {
		return publishedSnapshot{}, err
	}
	dir := r.BackupsDir(cfg)
	if spaced {
		if last, ok := newestOf(dir, true); ok {
			if since := now.Sub(last.at()); since >= 0 && since < OnDemandSpacing(cfg.Maintenance) {
				return publishedSnapshot{}, refuseContinuity("spaced", "you took one %s ago (%s, through record %d) and it stands; the next can be taken in %s. Nothing is lost by waiting: the daily pass runs whatever you do",
					RoundAge(since), last.Name, last.Record, RoundAge(OnDemandSpacing(cfg.Maintenance)-since))
			}
		}
	}

	if !r.maint.mu.TryLock() {
		return publishedSnapshot{}, refuseContinuity("busy", "a maintenance pass is running right now — it is taking a snapshot of you itself. recall source=continuity will show it when it is done")
	}
	defer r.maint.mu.Unlock()
	made, err := r.takeSnapshot(ctx, cfg, dir, true)
	if err != nil {
		r.recordPass(store.ContinuityStatus{Outcome: store.ContinuityFailed, OnDemand: true, Detail: err.Error()})
		logsink.Error("maintenance.error", "a snapshot that was asked for FAILED (%v); older copies protected", err)
		return publishedSnapshot{}, fmt.Errorf("the snapshot was not kept: a step of it failed, and nothing was published. What you had before stands. Your operator is told what failed")
	}
	removed := pruneBackups(dir, MaintenanceKeep(cfg.Maintenance))
	logsink.Info("maintenance.end", "asked for: verified, copied and restore-proved %s (%d events); pruned %d", made.Name, made.Record, removed)
	r.recordPass(passMade(made, true))
	return made, nil
}

func newestOf(dir string, onDemand bool) (keptSnapshot, bool) {
	kept, _ := keptSnapshots(dir)
	for i := len(kept) - 1; i >= 0; i-- {
		if kept[i].OnDemand == onDemand {
			return kept[i], true
		}
	}
	return keptSnapshot{}, false
}

func (r *Runtime) verifyKept(ctx context.Context, name string, spaced bool, now time.Time) (string, error) {
	cfg := r.w.Settings()
	if err := r.admitContinuityAct(cfg, "proving a snapshot"); err != nil {
		return "", err
	}
	dir := r.BackupsDir(cfg)
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
	if !r.maint.mu.TryLock() {
		return "", refuseContinuity("busy", "a maintenance pass is running right now; prove a snapshot when it is done")
	}
	defer r.maint.mu.Unlock()

	if spaced && !r.maint.lastVerify.IsZero() {
		if since := now.Sub(r.maint.lastVerify); since >= 0 && since < OnDemandSpacing(cfg.Maintenance) {
			return "", refuseContinuity("spaced", "you proved one %s ago; the next proof can run in %s", RoundAge(since), RoundAge(OnDemandSpacing(cfg.Maintenance)-since))
		}
	}
	r.maint.lastVerify = now

	if err := r.proveKept(ctx, cfg, dir, target); err != nil {

		r.Alert("snapshot-rot", fmt.Sprintf("%s no longer proves: %v", target.Name, err))
		return "", fmt.Errorf("%s does NOT prove: it no longer restores as it did when it was kept. Your operator is told what failed. Your other snapshots are untouched — prove another, or take a new one", target.Name)
	}
	return fmt.Sprintf("proved: %s holds you through record %d — every file matches its checksum, the chain verifies, and the record and your memory restored together in scratch, which was then removed", target.Name, target.Record), nil
}

func (r *Runtime) proveKept(ctx context.Context, cfg Settings, dir string, k keptSnapshot) (retErr error) {
	work, err := os.MkdirTemp(dir, SnapshotWorkPrefix+"verify-*.tmp")
	if err != nil {
		return fmt.Errorf("working set: %w", err)
	}
	defer func() {
		if rmErr := os.RemoveAll(work); rmErr != nil {
			logsink.Error("maintenance.error", "a proof's plaintext working set could not be removed: %v — it stands in %s until the next normal boot sweeps it", rmErr, dir)
			r.Alert("snapshot-debris", "a snapshot proof could not remove its plaintext working set ("+rmErr.Error()+") — remove "+work+" by hand, or restart the identity")
			if retErr == nil {
				retErr = fmt.Errorf("working set not removed: %w", rmErr)
			}
		}
	}()

	set := filepath.Join(dir, k.Name)
	var present []string
	if k.Encrypted {
		key, err := os.ReadFile(r.snapshotKeyPath(cfg))
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
	return r.proveSnapshotIn(ctx, cfg, set, want, filepath.Join(work, "restore-proof"))
}

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

func (r *Runtime) continuityRead(query string, now time.Time) (string, error) {
	cfg := r.w.Settings()
	dir := r.BackupsDir(cfg)
	kept, _ := keptSnapshots(dir)

	switch q := strings.ToLower(strings.TrimSpace(query)); {
	case q == "":
	case q == "snapshots" || q == "snapshot":
		return renderSnapshotList(kept, now), nil
	default:
		for _, k := range kept {
			if strings.EqualFold(k.Name, strings.TrimSpace(query)) {
				return r.renderOneSnapshot(dir, k, now), nil
			}
		}
		return "", fmt.Errorf("continuity reads whole, or one part: query=snapshots lists them, query=<a snapshot's name> reads that one. %q is neither", query)
	}

	var st store.ContinuityStatus
	var have bool
	if r.w.Store() != nil {
		var err error
		if st, have, err = r.w.Store().ContinuityStatus(); err != nil {
			return "", fmt.Errorf("the last pass's result does not read: %w", err)
		}
	}
	var b strings.Builder
	b.WriteString("snapshots: " + snapshotsLine(kept, now, MaintenanceKeep(cfg.Maintenance)) + "\n")
	b.WriteString("last pass: " + lastPassLine(st, have, now) + "\n")
	encryption, escrowLine := r.encryptionLines(cfg)
	b.WriteString("encryption: " + encryption + "\n")
	b.WriteString("escrow of your keys: " + escrowLine + "\n")
	chain := r.w.BootChain()
	if chain == "" {
		chain = "not walked by this boot"
	}
	b.WriteString("chain: " + chain + "\n")
	if r.w.Safe() {
		b.WriteString("next pass: this boot is SAFE — it walks your chain each day and makes no snapshot")
	} else if !MaintenanceEnabled(cfg.Maintenance) {
		b.WriteString("next pass: none — your operator has switched the pass off on this host")
	} else {
		b.WriteString("next pass: " + nextPass(now) + "; work action=backup.take makes one now, backup.verify proves one")
	}

	for _, set := range setAsideSets(cfg) {
		at, err := time.Parse(time.RFC3339, set.Record.SetAsideAt)
		if err != nil || now.Sub(at) > RestoreSaidFor || set.Record.PutBackAt != "" {
			continue
		}
		fmt.Fprintf(&b, "\nrestored: %s ago your operator restored you from %s — your record was put back to %d; what you had lived after it (through record %d) is kept aside, whole, and can be put back by them", RoundAge(now.Sub(at)), set.Record.RestoredFrom, set.Record.RestoredTo, set.Record.WasThrough)
		break
	}
	return b.String(), nil
}

const RestoreSaidFor = 30 * 24 * time.Hour

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
		len(kept), keep, RoundAge(now.Sub(newest.at())), newest.Record, enc, asked, oldest.at().Format("2006-01-02"))
}

func lastPassLine(st store.ContinuityStatus, have bool, now time.Time) string {
	if !have {
		return "none recorded yet"
	}
	when := st.At
	if at, err := time.Parse(time.RFC3339, st.At); err == nil {
		when = RoundAge(now.Sub(at)) + " ago"
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
			line += "; failing for " + RoundAge(now.Sub(since))
		}
		return line + ". Your operator has the detail"
	}
	return when + " — " + st.Outcome
}

func (r *Runtime) encryptionLines(cfg Settings) (encryption, escrowLine string) {
	key, _, reason, _ := r.snapshotEncryption(cfg)
	escrow.Wipe(key)
	if reason == "" {
		line := "checked — a copy of your keys, sealed under your operator's passphrase, was opened and proved"
		if raw, err := atomicfile.ReadFile(r.escrowReceiptPath(cfg)); err == nil {
			var receipt escrow.Receipt
			if json.Unmarshal(raw, &receipt) == nil && receipt.CheckedAt != "" {
				line += " on " + receipt.CheckedAt
			}
		}
		return "on — each snapshot is encrypted before it can leave this host", line
	}
	return "off — " + store.UnencryptedForIdentity(reason), escrowStateFor(reason)
}

func escrowStateFor(reason string) string {
	switch reason {
	case store.UnencryptedNoEscrow, store.UnencryptedNoKey:
		return "none checked"
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
		fmt.Fprintf(&b, "%s — %s ago, through record %d, %s, %s\n", k.Name, RoundAge(now.Sub(k.at())), k.Record, class, enc)
	}
	return strings.TrimRight(b.String(), "\n")
}

func (r *Runtime) renderOneSnapshot(dir string, k keptSnapshot, now time.Time) string {
	head := fmt.Sprintf("%s — %s ago, through record %d", k.Name, RoundAge(now.Sub(k.at())), k.Record)
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
	var rec snapshotReceipt
	if json.Unmarshal(raw, &rec) != nil {
		return head + "; its receipt does not read"
	}
	return fmt.Sprintf("%s; captured %s with %d lived ticks and %d conversation rows; not encrypted", head, rec.CapturedAt, rec.Runtime.LifetimeTicks, rec.Runtime.Ephemeral["conversations"])
}

func nextPass(now time.Time) string {
	next := time.UnixMilli(cognitive.NextLocalDaily(now, maintenanceHourLocal, 0)).In(now.Location())
	return next.Format("15:04") + " (in " + RoundAge(next.Sub(now)) + ")"
}

func RoundAge(d time.Duration) string {
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
