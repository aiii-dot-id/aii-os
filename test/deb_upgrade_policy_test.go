package test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
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
func TestTheDebianUpgradeDoesNotStopIdentities(t *testing.T) {
	root := repoRoot(t)
	prerm := readScript(t, filepath.Join(root, "packaging", "deb", "prerm"))
	postinst := readScript(t, filepath.Join(root, "packaging", "deb", "postinst"))

	// .
	// .
	code := stripComments(prerm)

	if !regexp.MustCompile(`\$1"?\s*=\s*remove`).MatchString(code) {
		t.Fatalf("prerm must stop on remove:\n%s", code)
	}
	for _, forbidden := range []string{"upgrade", "deconfigure"} {
		if strings.Contains(code, forbidden) {
			t.Errorf("prerm acts on %q — an upgrade must NOT stop identities; the process keeps its old inode across the unpack and postinst's restart is what moves it to the new binary:\n%s", forbidden, code)
		}
	}
	if !strings.Contains(code, "deb-systemd-invoke") {
		t.Error("prerm must stop through deb-systemd-invoke: it consults policy-rc.d, enumerates the user instances, and skips units that are disabled and not running")
	}

	pcode := stripComments(postinst)
	if !strings.Contains(pcode, "deb-systemd-invoke --user restart") {
		t.Errorf("postinst must RESTART identities on configure, or an upgrade leaves them on the old binary:\n%s", pcode)
	}
	if !strings.Contains(pcode, "deb-systemd-invoke --user daemon-reload") {
		t.Error("postinst must reload the user managers before restarting, or the restart uses the old unit definition")
	}
	if !regexp.MustCompile(`configure`).MatchString(pcode) {
		t.Error("postinst must act on configure")
	}
}

// .
// .
func TestTheUnitHelperIsInstalledByThePackage(t *testing.T) {
	root := repoRoot(t)
	build := readScript(t, filepath.Join(root, "packaging", "deb", "build-deb.sh"))
	if !strings.Contains(build, "usr/share/aii-os/aii-units.sh") {
		t.Fatal("prerm and postinst source /usr/share/aii-os/aii-units.sh, and the package does not install it — both scripts would fail on the first line")
	}
	for _, s := range []string{"prerm", "postinst"} {
		body := readScript(t, filepath.Join(root, "packaging", "deb", s))
		if !strings.Contains(body, "/usr/share/aii-os/aii-units.sh") {
			t.Errorf("%s does not source the shared unit list — the two scripts could disagree about which units exist", s)
		}
	}
	// .
	if !strings.Contains(build, "init-system-helpers") {
		t.Error("deb-systemd-invoke --user needs init-system-helpers >= 1.66~; the package must depend on it rather than hope it is present")
	}
}

func readScript(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}

// .
// .
func stripComments(s string) string {
	var out []string
	for _, line := range strings.Split(s, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}
