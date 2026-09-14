package updates

import (
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
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
const bundleSuffix = ".app"

// .
// .
// .
// .
// .
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

// .
// .
// .
// .
func bundleAssetName(version string) string {
	platform, arch := hostTarget()
	return BundleAssetName(version, platform, arch)
}

// .
// .
// .
// .
func errBareBinaryIntoBundle(bundlePath string) error {
	return fmt.Errorf(
		"refusing to replace the executable inside %s: on %s the signed unit is the whole .app, "+
			"and writing a new binary into Contents/MacOS invalidates the bundle's code signature "+
			"(codesign reports \"nested code is modified or invalid\"). The release must carry %s",
		bundlePath, runtime.GOOS, bundleAssetName("<version>"))
}
