package updates

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/aiii-dot-id/aii-os/internal/logsink"
	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
)

type PackageUpdate struct {
	Version string `json:"version,omitempty"`
	Path    string `json:"path,omitempty"`
	Refusal string `json:"refusal,omitempty"`
}

const dpkgFileList = "/var/lib/dpkg/info/aii-os.list"

func (c *Checker) packagesDir() string { return filepath.Join(c.dataDir, "updates") }

func packageOwns(listPath, exe string) bool {
	f, err := os.Open(listPath)
	if err != nil {
		return false
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if strings.TrimSpace(sc.Text()) == exe {
			return true
		}
	}
	return false
}

func (c *Checker) preparePackage(ctx context.Context, version string) {
	exe, err := os.Executable()
	if err == nil {
		if resolved, rerr := filepath.EvalSymlinks(exe); rerr == nil {
			exe = resolved
		}
	}
	listPath := c.pkgListPath
	if listPath == "" {
		listPath = dpkgFileList
	}
	if c.pkgExe != "" {
		exe = c.pkgExe
	}
	if err != nil || !packageOwns(listPath, exe) {
		c.state.setPackage(PackageUpdate{})
		return
	}
	refuse := func(format string, args ...any) {
		why := fmt.Sprintf(format, args...)
		logsink.Warn("updates.refusal", "package for %s is not offered: %s", version, why)
		c.state.setPackage(PackageUpdate{Version: version, Refusal: why})
	}
	_, arch := hostTarget()
	name := PackageAssetName(version, arch)
	path := filepath.Join(c.packagesDir(), name)
	root := c.platformRoot()

	verify := func(pkg, sig []byte) error {
		signed, err := verifyReleaseSig(sig, root, sha256hex(pkg), version, "linux", arch)
		if err != nil {
			return fmt.Errorf("its signature did not verify: %w", err)
		}
		revRoots := packagefmt.ReleaseTrustRoots(filepath.Join(c.dataDir, "trust"), root)
		if err := packagefmt.CheckReleaseRevocation(revRoots, artifactKindReleaseSig, signed); err != nil {
			return fmt.Errorf("its signature is refused: %w", err)
		}
		return nil
	}

	if pkg, perr := os.ReadFile(path); perr == nil {
		if sig, serr := os.ReadFile(SigAssetName(path)); serr == nil && verify(pkg, sig) == nil {
			c.state.setPackage(PackageUpdate{Version: version, Path: path})
			return
		}
	}

	c.mu.Lock()
	release := c.cachedRelease
	c.mu.Unlock()
	if release == nil {
		refuse("the release that named it is no longer held; the next check asks again")
		return
	}
	pkgURL, sigURL, err := findAssets(release, name, SigAssetName(name))
	if err != nil {
		refuse("the release carries no signed package for this machine (%v)", err)
		return
	}
	pkg, err := c.download(ctx, pkgURL)
	if err != nil {
		refuse("the package could not be fetched: %v", err)
		return
	}
	sig, err := c.download(ctx, sigURL)
	if err != nil {
		refuse("the package's signature could not be fetched: %v", err)
		return
	}
	if err := verify(pkg, sig); err != nil {
		refuse("%v", err)
		return
	}
	if err := os.MkdirAll(c.packagesDir(), 0o700); err != nil {
		refuse("the verified package could not be kept: %v", err)
		return
	}

	if old, _ := filepath.Glob(filepath.Join(c.packagesDir(), "aii-os_*.deb*")); len(old) > 0 {
		for _, f := range old {
			if f != path && f != SigAssetName(path) {
				_ = os.Remove(f)
			}
		}
	}

	if err := writeFileAtomic(SigAssetName(path), sig, 0o644); err != nil {
		refuse("the verified package could not be kept: %v", err)
		return
	}
	if err := writeFileAtomic(path, pkg, 0o644); err != nil {
		refuse("the verified package could not be kept: %v", err)
		return
	}
	logsink.Info("updates.decision", "package %s verified against the release's signature and kept at %s", name, path)
	c.state.setPackage(PackageUpdate{Version: version, Path: path})
}

func (c *Checker) forgetPackages() {
	c.state.setPackage(PackageUpdate{})
	kept, _ := filepath.Glob(filepath.Join(c.packagesDir(), "aii-os_*.deb*"))
	for _, f := range kept {
		_ = os.Remove(f)
	}
}
