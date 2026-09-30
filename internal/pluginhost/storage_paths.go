package pluginhost

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/aiii-dot-id/aii-os/internal/atomicfile"
	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
)

func pluginStorageDir(base, id string) string {
	if base == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(id))
	return filepath.Join(base, ".by-id", hex.EncodeToString(sum[:]))
}

func pluginLegacyReadPath(base, id, selected string) string {
	if base == "" || selected == "" {
		return ""
	}
	root := pluginStorageDir(base, id)
	rel, err := filepath.Rel(root, selected)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return ""
	}

	realBase, err := filepath.EvalSymlinks(base)
	if err != nil {
		return ""
	}
	legacy := filepath.Join(realBase, sanitizeToken(id))
	current, err := os.Stat(root)
	if err != nil {
		return ""
	}
	old, err := os.Stat(legacy)
	if err != nil || !os.SameFile(current, old) {
		return ""
	}
	return filepath.Join(legacy, rel)
}

type StorageUpgradeError struct {
	PluginID, Legacy, Destination, Reason string
	Cause                                 error
}

func (e *StorageUpgradeError) Error() string {
	return fmt.Sprintf("plugin %s: storage upgrade %s -> %s: %s: %v", e.PluginID, e.Legacy, e.Destination, e.Reason, e.Cause)
}
func (e *StorageUpgradeError) Unwrap() error { return e.Cause }

func (o *Options) upgradeStorageDir(ctx context.Context, base, id string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if base != "" && o.StorageUpgradeReady != nil {
		if _, err := os.Lstat(pluginStorageDir(base, id)); errors.Is(err, fs.ErrNotExist) {
			if err := o.StorageUpgradeReady(); err != nil {
				return "", &StorageUpgradeError{PluginID: id, Legacy: filepath.Join(base, sanitizeToken(id)), Destination: pluginStorageDir(base, id), Reason: "wait for host recovery to settle", Cause: err}
			}
		}
	}
	return upgradePluginDir(ctx, base, id, o.LegacyStorageOwners)
}

func upgradePluginDir(ctx context.Context, base, id string, owners func(context.Context) ([]string, error)) (string, error) {
	return upgradePluginDirWith(ctx, base, id, owners, atomicfile.Rename, atomicfile.SyncDir)
}

func upgradePluginDirWith(ctx context.Context, base, id string, owners func(context.Context) ([]string, error),
	rename func(string, string) error, syncDir func(string) error) (string, error) {
	if base == "" {
		return "", nil
	}
	if !packagefmt.ValidManifestID(id) {
		return "", &StorageUpgradeError{PluginID: id, Reason: "invalid manifest ID", Cause: fs.ErrInvalid}
	}
	legacy := filepath.Join(base, sanitizeToken(id))
	dest := pluginStorageDir(base, id)
	refuse := func(why string, err error) (string, error) {
		return "", &StorageUpgradeError{PluginID: id, Legacy: legacy, Destination: dest, Reason: why, Cause: err}
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}

	var installed []string
	if _, oldErr := os.Lstat(legacy); oldErr == nil && owners != nil {
		if _, newErr := os.Lstat(dest); errors.Is(newErr, fs.ErrNotExist) {
			var err error
			installed, err = owners(ctx)
			if err != nil {
				return refuse("verify installed ownership", err)
			}
		}
	}

	release, err := lockRoot(ctx, legacy)
	if err != nil {
		return "", err
	}
	defer release()
	_, oldErr := os.Lstat(legacy)
	_, newErr := os.Lstat(dest)
	if oldErr != nil && !errors.Is(oldErr, fs.ErrNotExist) {
		return refuse("inspect legacy directory", oldErr)
	}
	if newErr != nil && !errors.Is(newErr, fs.ErrNotExist) {
		return refuse("inspect destination", newErr)
	}

	foreignAlias := false
	ownAlias := false
	if oldErr == nil {
		if target, err := os.Readlink(legacy); err == nil {
			if !filepath.IsAbs(target) {
				target = filepath.Join(base, target)
			}
			namespace, nsErr := os.Stat(filepath.Join(base, ".by-id"))
			parent, parentErr := os.Stat(filepath.Dir(target))
			if nsErr == nil && parentErr == nil && os.SameFile(namespace, parent) && reHex64.MatchString(strings.ToLower(filepath.Base(target))) {
				if _, err := os.Stat(target); err != nil {
					return refuse("compatibility link points to unavailable storage", err)
				}
				foreignAlias = !strings.EqualFold(filepath.Base(target), filepath.Base(dest))
				ownAlias = !foreignAlias
				if foreignAlias {
					oldErr = fs.ErrNotExist
				}
			}
		}
	}
	finish := func() (string, error) {
		if !foreignAlias {

			if target, err := os.Readlink(legacy); err == nil && !ownAlias {
				if !filepath.IsAbs(target) {
					target = filepath.Join(base, target)
				}
				if filepath.Clean(target) != filepath.Clean(dest) {
					if err := os.Remove(legacy); err != nil {
						return refuse("replace legacy directory link", err)
					}
				}
			}
			if _, err := os.Lstat(legacy); errors.Is(err, fs.ErrNotExist) {
				if err := atomicfile.LinkDir(dest, legacy); err != nil {
					return refuse("restore the legacy reader's directory link", err)
				}
			} else if err != nil {
				return refuse("inspect compatibility link", err)
			}
		}
		if err := syncDir(filepath.Dir(dest)); err != nil {
			return refuse("sync destination parent", err)
		}
		if err := syncDir(base); err != nil {
			return refuse("sync legacy parent", err)
		}
		return dest, nil
	}
	if newErr == nil {
		newInfo, err := os.Stat(dest)
		if err != nil {
			return refuse("resolve destination directory", err)
		}
		if !newInfo.IsDir() {
			return refuse("destination is not a directory", fs.ErrInvalid)
		}
		if oldErr == nil {
			oldTarget, err := os.Stat(legacy)
			if err != nil || !os.SameFile(oldTarget, newInfo) {
				return refuse("both locations exist; refusing to merge or overwrite", fs.ErrExist)
			}
		}

		return finish()
	}
	if oldErr == nil {

		oldInfo, err := os.Stat(legacy)
		if err != nil {
			return refuse("resolve legacy directory", err)
		}
		if !oldInfo.IsDir() {
			return refuse("legacy path is not a directory", fs.ErrInvalid)
		}
		if owners == nil {
			return refuse("legacy ownership needs the verified installed-package inventory", fs.ErrPermission)
		}
		found := false
		for _, other := range installed {
			if other == id {
				found = true
			}
			if other != id && sanitizeToken(other) == sanitizeToken(id) {
				return refuse("legacy directory is also claimed by "+other, fs.ErrExist)
			}
		}
		if !found {
			return refuse("plugin is absent from the verified installed-package inventory", fs.ErrPermission)
		}
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o700); err != nil {
		return refuse("create storage namespace", err)
	}
	if oldErr == nil {
		if target, linkErr := os.Readlink(legacy); linkErr == nil {
			if !filepath.IsAbs(target) {
				target = filepath.Join(base, target)
			}
			if err := atomicfile.LinkDir(target, dest); err != nil {
				return refuse("preserve linked legacy directory", err)
			}
		} else {
			if err := rename(legacy, dest); err != nil {
				return refuse("rename legacy directory (no data was reset)", err)
			}
		}
	} else if err := os.Mkdir(dest, 0o700); err != nil {
		return refuse("create private directory", err)
	}
	return finish()
}
