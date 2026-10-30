package updates

import (
	"fmt"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/aiii-dot-id/aii-os/internal/atomicfile"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
)

const bundleSuffix = ".app"

const previousBundleName = ".aii-os-previous.app"

const bundleStagePrefix = ".aii-update-"

func bundleRoot(exePath string) (string, bool) {
	macOSDir := filepath.Dir(exePath)
	contentsDir := filepath.Dir(macOSDir)
	appDir := filepath.Dir(contentsDir)
	if filepath.Base(macOSDir) != "MacOS" || filepath.Base(contentsDir) != "Contents" {
		return "", false
	}
	if !strings.HasSuffix(appDir, bundleSuffix) {
		return "", false
	}
	return appDir, true
}

func bundleAssetName(version string) string {
	platform, arch := hostTarget()
	return BundleAssetName(version, platform, arch)
}

func errBareBinaryIntoBundle(bundlePath string) error {
	return fmt.Errorf(
		"refusing to replace the executable inside %s: on %s the signed unit is the whole .app, "+
			"and writing a new binary into Contents/MacOS invalidates the bundle's code signature "+
			"(codesign reports \"nested code is modified or invalid\"). The release must carry %s",
		bundlePath, runtime.GOOS, bundleAssetName("<version>"))
}

var exchangeBundles = swapBundles

func (r recovery) exchangeBack(pend updatePending) string {
	live, err := imageHash(r.tgt.path)
	if err != nil {
		logsink.Error("updates.error", "ROLLBACK FAILED — cannot read the installed bundle %s: %v; the previous bundle and the record are kept, and the next boot tries again", r.tgt.path, err)
		return ""
	}
	if live != pend.NewSHA256 {
		logsink.Info("updates.decision", "rollback skipped — %s is not the bundle the update installed (the previous one is back already, or was never exchanged out, or the bundle was replaced by hand); retiring update state", r.tgt.path)
		r.retire(pend)
		return ""
	}
	kept, err := imageHash(r.backup)
	if err == nil && kept != pend.BackupSHA256 {
		err = fmt.Errorf("it hashes to %.12s, the install recorded %.12s", kept, pend.BackupSHA256)
	}
	if err != nil {
		logsink.Error("updates.refusal", "ROLLBACK REFUSED — the bundle kept at %s is not the one the update displaced (%v); the installed bundle stays", r.backup, err)
		return ""
	}
	noted := r.noteFailed(pend)
	if err := exchangeBundles(r.tgt.path, r.backup); err != nil {
		if noted {
			_ = withdraw(r.tgt.failed)
		}
		logsink.Error("updates.error", "ROLLBACK FAILED — %v; the installed bundle is unchanged, and the next boot tries again", err)
		return ""
	}
	if err := atomicfile.SyncDir(filepath.Dir(r.tgt.path)); err != nil {

		logsink.Error("updates.error", "ROLLBACK exchanged the previous bundle back but could not make the exchange durable — keeping both bundles and the record: %v", err)
		return r.tgt.path
	}
	r.retire(pend)
	logsink.Warn("updates.end", "ROLLBACK — exchanged the previous bundle back into %s (update failed to boot)", r.tgt.path)
	return r.tgt.path
}
