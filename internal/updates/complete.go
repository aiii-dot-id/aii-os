package updates

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/aiii-dot-id/aii-os/internal/atomicfile"
	"os"
	"path/filepath"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/logsink"
)

type incompleteInstall struct {
	NewSHA256 string    `json:"new_sha256"`
	At        time.Time `json:"at"`
}

type IncompleteInstallError struct {
	Version string
	Cause   error
}

func (e *IncompleteInstallError) Error() string {
	return fmt.Sprintf("this install of %s is incomplete: the update that brought it installed aii alone, and the files the release ships beside it are not yet in place (%v). It is tried again at the next check; running the %s installer over this install completes it at once",
		e.Version, e.Cause, e.Version)
}

func (e *IncompleteInstallError) Unwrap() error { return e.Cause }

func noteIncomplete(tgt target, pend updatePending) {
	if tgt.incomplete == "" || pend.NewSHA256 == "" || len(pend.Beside) > 0 {
		return
	}
	if platform, _ := hostTarget(); len(installedBeside(platform)) == 0 {
		return
	}
	raw, err := json.Marshal(incompleteInstall{NewSHA256: pend.NewSHA256, At: time.Now().UTC()})
	if err == nil {
		err = writeFileAtomic(tgt.incomplete, raw, 0o644)
	}
	if err != nil {
		logsink.Warn("updates.error", "this boot could not note that the update installed aii alone (%v) — the files beside it stay the earlier release's until the next update", err)
		return
	}
	logsink.Info("updates.decision", "the update that brought this program installed aii alone — what its release ships beside it is completed at the next check")
}

func (c *Checker) completeIfIncomplete(ctx context.Context) {
	if c.isMobile != nil && c.isMobile() {
		return
	}
	exe, err := os.Executable()
	if err != nil {
		return
	}
	var incomplete *IncompleteInstallError
	if err := c.completeInstall(ctx, exe); errors.As(err, &incomplete) {
		c.state.setLastError(err.Error())
		logsink.Warn("updates.refusal", "%v", err)
	}
}

func (c *Checker) completeInstall(ctx context.Context, exePath string) error {
	tgt, err := targetOf(exePath)
	if err != nil || tgt.incomplete == "" {
		return nil
	}
	raw, err := atomicfile.ReadFile(tgt.incomplete)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	current := c.currentVer()
	drop := func(format string, args ...any) error {
		logsink.Info("updates.decision", "completing this install is ended: "+format, args...)
		os.Remove(tgt.incomplete)
		return nil
	}
	var fact incompleteInstall
	if err == nil {
		err = json.Unmarshal(raw, &fact)
	}
	if err != nil {
		return drop("its note does not read (%v)", err)
	}

	running, err := imageHash(tgt.exe)
	if err != nil {
		return &IncompleteInstallError{Version: current, Cause: fmt.Errorf("the running program could not be read: %w", err)}
	}
	if running != fact.NewSHA256 {
		return drop("the program is no longer the image its note names")
	}
	if ok, why := canStageBesideAt(tgt.exe); !ok {
		return drop("this install is its package manager's to complete (%s)", why)
	}
	fail := func(format string, args ...any) error {
		return &IncompleteInstallError{Version: current, Cause: fmt.Errorf(format, args...)}
	}
	root := c.platformRoot()
	if root == nil {
		return fail("no platform_release root is pinned, so nothing can be verified")
	}
	own := c.ownRelease
	if own == nil {
		own = func(ctx context.Context) (*githubRelease, error) { return c.fetchRelease(ctx, "tags/v"+current) }
	}
	release, err := own(ctx)
	if err != nil {
		return fail("the release %s could not be read: %w", current, err)
	}
	name := assetName(current)
	archiveURL, sigURL, err := findAssets(release, name, SigAssetName(name))
	if err != nil {
		return fail("the release %s does not carry this platform's archive: %w", current, err)
	}
	archive, err := c.verifiedArchive(ctx, archiveURL, sigURL, name, current, root)
	if err != nil {
		return fail("%w", err)
	}
	binary, beside, err := extractBinary(archive)
	if err != nil {
		return fail("the verified archive does not open: %w", err)
	}

	if sha256hex(binary) != running {
		return drop("the release %s is verified and its aii is not the program running here (%.12s, not %.12s): this program is not that release's build, and its files are not put beside it", current, sha256hex(binary), running)
	}
	release2, err := lockTarget(tgt)
	if err != nil {
		return fail("%w", err)
	}
	defer release2()

	if now, err := imageHash(tgt.exe); err != nil {
		return fail("the running program could not be read again: %w", err)
	} else if now != running {
		return drop("the program was replaced while its release was being fetched")
	}
	if again, err := atomicfile.ReadFile(tgt.incomplete); err != nil || !bytes.Equal(again, raw) {
		logsink.Info("updates.decision", "completing this install is ended: its note was settled while its release was being fetched")
		return nil
	}
	dir := filepath.Dir(tgt.exe)
	type stood struct {
		there bool
		data  []byte
	}
	before := make([]stood, len(beside))
	for i, f := range beside {
		old, err := os.ReadFile(filepath.Join(dir, f.name))
		switch {
		case err == nil:
			before[i] = stood{there: true, data: old}
		case !errors.Is(err, os.ErrNotExist):
			return fail("read %s as it stands: %w", f.name, err)
		}
	}
	for i, f := range beside {
		if before[i].there && sha256hex(before[i].data) == sha256hex(f.data) {
			continue
		}
		if err := installBeside(filepath.Join(dir, f.name), f.data); err != nil {

			errs := []error{fmt.Errorf("install %s: %w", f.name, err)}
			for j := undoThrough(i, err) - 1; j >= 0; j-- {
				path := filepath.Join(dir, beside[j].name)
				if before[j].there {
					errs = append(errs, installBeside(path, before[j].data))
				} else {
					errs = append(errs, withdraw(path))
				}
			}
			return fail("%w", errors.Join(errs...))
		}
		replaceStep("completed " + f.name)
	}
	os.Remove(tgt.incomplete)
	logsink.Info("updates.end", "this install of %s is complete: %d file(s) of the release are in place beside aii", current, len(beside))
	return nil
}
