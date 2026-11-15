package updates

import (
	"encoding/json"
	"errors"
	"fmt"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/atomicfile"
	"github.com/aiii-dot-id/aii-os/internal/filelock"
)

type updatePending struct {
	Attempts int `json:"attempts"`

	BackupSHA256 string `json:"backup_sha256"`

	NewSHA256 string `json:"new_sha256"`

	Version string `json:"version,omitempty"`

	Owner string `json:"owner,omitempty"`

	Beside []besideSwap `json:"beside,omitempty"`
}

type besideSwap struct {
	Name string `json:"name"`

	BackupSHA256 string `json:"backup_sha256,omitempty"`

	NewSHA256 string `json:"new_sha256"`
}

type standing struct {
	there bool
	data  []byte
}

func besideBackup(tgt target, name string) string {
	return filepath.Join(filepath.Dir(tgt.path), "."+name+".previous")
}

func besideBackups(tgt target, pend updatePending) []string {
	if tgt.path == "" || tgt.bundle {
		return nil
	}
	platform, _ := hostTarget()
	names := installedBeside(platform)
	for _, b := range pend.Beside {
		if !slices.Contains(names, b.Name) && b.Name == filepath.Base(b.Name) && b.Name != "" {
			names = append(names, b.Name)
		}
	}
	paths := make([]string, len(names))
	for i, name := range names {
		paths[i] = besideBackup(tgt, name)
	}
	return paths
}

func withdrawSwap(tgt target, pend updatePending) error {
	var errs []error
	for _, path := range besideBackups(tgt, pend) {
		errs = append(errs, withdraw(path))
	}
	errs = append(errs, withdrawBackup(tgt))
	return errors.Join(append(errs, withdraw(tgt.record))...)
}

func withdrawBackup(tgt target) error {
	if !tgt.bundle {
		return withdraw(tgt.backup)
	}
	if err := os.RemoveAll(tgt.backup); err != nil {
		return fmt.Errorf("could not withdraw %s: %w", tgt.backup, err)
	}
	return nil
}

type target struct {
	path   string
	exe    string
	bundle bool
	lock   string
	record string
	backup string
	failed string

	highest string

	incomplete string
}

func targetOf(exePath string) (target, error) {
	if exePath == "" {

		return target{}, errors.New("the running program's path is unknown")
	}
	exe, err := resolveLinks(exePath)
	if err != nil {
		return target{}, fmt.Errorf("resolve the running program %s: %w", exePath, err)
	}
	return targetAt(exe), nil
}

func Program() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("locate the running program: %w", err)
	}
	tgt, err := targetOf(exe)
	if err != nil {
		return "", err
	}
	return tgt.path, nil
}

func Leftovers(exe string) []string {
	t := targetAt(exe)
	names := []string{t.lock, t.record, t.backup, t.failed, t.highest}
	if t.incomplete != "" {
		names = append(names, t.incomplete)
	}
	return append(names, besideBackups(t, updatePending{})...)
}

func stageStem(target string) string { return "." + filepath.Base(target) + ".stage-" }

func Beside() (files, stems []string, err error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, nil, fmt.Errorf("locate the running program: %w", err)
	}
	tgt, err := targetOf(exe)
	if err != nil {
		return nil, nil, err
	}
	files = Leftovers(tgt.exe)
	dir := filepath.Dir(tgt.path)
	if tgt.bundle {
		return files, []string{filepath.Join(dir, bundleStagePrefix)}, nil
	}
	stems = []string{filepath.Join(dir, stageStem(tgt.path))}
	platform, _ := hostTarget()
	for _, name := range installedBeside(platform) {
		files = append(files, filepath.Join(dir, name))
		stems = append(stems, filepath.Join(dir, stageStem(name)))
	}
	return files, stems, nil
}

func targetAt(exe string) target {
	if app, ok := bundleRoot(exe); ok {
		dir := filepath.Dir(app)
		return target{path: app, exe: exe, bundle: true,
			lock:    filepath.Join(dir, ".aii-os-update.lock"),
			record:  filepath.Join(dir, ".aii-os-update.json"),
			backup:  filepath.Join(dir, previousBundleName),
			failed:  filepath.Join(dir, ".aii-os-update.failed.json"),
			highest: filepath.Join(dir, ".aii-os-update.highest.json")}
	}
	dir, base := filepath.Split(exe)
	return target{path: exe, exe: exe,
		lock:       filepath.Join(dir, "."+base+".update.lock"),
		record:     filepath.Join(dir, "."+base+".update.json"),
		backup:     filepath.Join(dir, "."+base+".previous"),
		failed:     filepath.Join(dir, "."+base+".update.failed.json"),
		highest:    filepath.Join(dir, "."+base+".update.highest.json"),
		incomplete: filepath.Join(dir, "."+base+".update.incomplete.json")}
}

type failedUpdate struct {
	Version   string    `json:"version"`
	NewSHA256 string    `json:"new_sha256"`
	At        time.Time `json:"at"`
}

type FailedHereError struct {
	Version, NewSHA256 string
	At                 time.Time
}

func (e *FailedHereError) Error() string {
	return fmt.Sprintf("%s did not finish a boot on this machine on %s and was rolled back; it is not installed again here. A later release is installed as usual",
		e.Version, e.At.UTC().Format("2006-01-02"))
}

func (t target) refusedHere(available string) error {
	if t.failed == "" {
		return nil
	}
	raw, err := atomicfile.ReadFile(t.failed)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	var f failedUpdate
	if err == nil {
		err = json.Unmarshal(raw, &f)
	}
	switch {
	case err != nil:
		logsink.Warn("updates.refusal", "the record of a release rolled back here (%s) does not read: %v — it is removed, and updates go on as if none had failed", t.failed, err)
	case f.Version != "" && f.Version == available:
		return &FailedHereError{Version: f.Version, NewSHA256: f.NewSHA256, At: f.At}
	}
	os.Remove(t.failed)
	return nil
}

func ownerOf(dataDir string) (string, error) {
	abs, err := filepath.Abs(dataDir)
	if err != nil {
		return "", err
	}
	return resolveLinks(abs)
}

func lockTarget(tgt target) (release func(), err error) {
	f, err := os.OpenFile(tgt.lock, os.O_CREATE|os.O_RDONLY, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open the update lock: %w", err)
	}
	if err := filelock.Lock(f); err != nil {
		f.Close()
		if errors.Is(err, filelock.ErrHeld) {
			return nil, fmt.Errorf("%w — another process is replacing %s (%s): %w", ErrAlreadyApplying, tgt.path, tgt.lock, err)
		}
		return nil, fmt.Errorf("lock %s: %w", tgt.lock, err)
	}
	return func() { f.Close() }, nil
}

func (tgt target) replace(newHash string, swap func(oldHash string) error) (already bool, err error) {
	release, err := lockTarget(tgt)
	if err != nil {
		return false, err
	}
	defer release()
	replaceStep("locked")
	have, err := imageHash(tgt.path)
	if err != nil {
		return false, fmt.Errorf("read the installed image to compare: %w", err)
	}
	if have == newHash {
		return true, nil
	}
	if err := tgt.unclaimed(); err != nil {
		return false, err
	}
	return false, swap(have)
}

func (tgt target) unclaimed() error {
	p, present, err := readClaim(tgt.record)
	switch {
	case !present:
		return nil
	case err != nil:
		return fmt.Errorf("%w: the update record %s cannot be read (%v) — it is kept, since no identity can be shown to own it; once no update awaits a restart, remove it by hand", ErrUpdatePending, tgt.record, err)
	default:
		return fmt.Errorf("%w: it was installed by the identity whose data is at %s, and that identity's next healthy boot retires it", ErrUpdatePending, p.Owner)
	}
}

func (tgt target) claimedBy(owner string) bool {
	p, present, err := readClaim(tgt.record)
	return present && err == nil && p.Owner == owner
}
