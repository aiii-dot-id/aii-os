//go:build !windows

package test

import (
	"os/exec"
	"regexp"
	"strings"
	"testing"
)

// A PRE-RELEASE PACKAGE SORTS BELOW ITS RELEASE FOR DPKG TOO. The program
// compares semver (internal/version): 0.2.1 < 0.2.2-m.1 < 0.2.2. dpkg reads a
// '-' as the upstream/revision separator and ranks 0.2.2-m.1 ABOVE 0.2.2, so a
// milestone's package written with VERSION verbatim would make the final
// release install as a downgrade. build-deb.sh writes the control file's and
// the changelog's version in Debian's spelling, the first '-' as '~', and keeps
// VERSION in the package's file name, which the updater asks for by that name
// (internal/updates.PackageAssetName). A release with no '-' is unchanged.
func TestTheDebianVersionOfAPreReleaseSortsBelowItsRelease(t *testing.T) {
	script := readPackaging(t, "deb", "build-deb.sh")

	// The mapping, run as the script runs it: its one assignment line, under sh.
	line := regexp.MustCompile(`(?m)^DEB_VERSION=.*$`).FindString(script)
	if line == "" {
		t.Fatal("build-deb.sh sets no DEB_VERSION: a pre-release's package would carry dpkg's wrong order")
	}
	deb := func(version string) string {
		t.Helper()
		out, err := exec.Command("sh", "-c", `VERSION="$1"; `+line+`; printf '%s' "$DEB_VERSION"`, "sh", version).Output()
		if err != nil {
			t.Fatalf("the mapping line fails for %q: %v", version, err)
		}
		return string(out)
	}
	for in, want := range map[string]string{
		"0.2.1": "0.2.1", "0.2.2": "0.2.2", "10.20.30": "10.20.30", // a release is unchanged
		"0.2.2-m.1": "0.2.2~m.1", "0.2.2-m.10": "0.2.2~m.10", "0.3.0-rc.2": "0.3.0~rc.2",
	} {
		if got := deb(in); got != want {
			t.Errorf("Debian version of %q = %q, want %q", in, got, want)
		}
	}

	// Where each spelling goes: Debian's in the control file and the changelog's
	// header, VERSION in the file name and nowhere a package manager compares.
	for _, want := range []string{"Version: $DEB_VERSION\n", `printf 'aii-os (%s) unstable; urgency=medium\n\n' "$DEB_VERSION"`,
		`deb="$OUT/aii-os_${VERSION}_${ARCH}.deb"`} {
		if !strings.Contains(script, want) {
			t.Errorf("build-deb.sh does not hold %q", want)
		}
	}
	if strings.Contains(script, "Version: $VERSION\n") {
		t.Error("the control file still takes VERSION verbatim")
	}

	// The order dpkg itself gives, where dpkg is present (it is on the Linux build
	// host and the gate's runners; a host without it skips only this half).
	dpkg, err := exec.LookPath("dpkg")
	if err != nil {
		t.Skip("dpkg is not installed here: the mapping is checked, dpkg's own order is not")
	}
	ascending := []string{deb("0.2.1"), deb("0.2.2-m.1"), deb("0.2.2-m.9"), deb("0.2.2-m.10"), deb("0.2.2"), deb("0.2.3-m.1"), deb("0.2.3")}
	for i := 1; i < len(ascending); i++ {
		if err := exec.Command(dpkg, "--compare-versions", ascending[i-1], "lt", ascending[i]).Run(); err != nil {
			t.Errorf("dpkg does not order %s below %s", ascending[i-1], ascending[i])
		}
	}
}
