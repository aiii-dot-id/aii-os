package test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The maintainer scripts' behaviour — prerm stops identities on remove
// only, postinst reloads then restarts them, postrm touches no identity —
// is run in deb_maintainer_scripts_test.go. What stays here is what only
// the package build decides.

// The helper both scripts source has to ship, or they fail at their
// first line on a real machine — a failure no build-time check sees.
func TestTheUnitHelperIsInstalledByThePackage(t *testing.T) {
	root := repoRoot(t)
	build := readScript(t, filepath.Join(root, "packaging", "deb", "build-deb.sh"))
	if !strings.Contains(build, "usr/share/aii-os/aii-units.sh") {
		t.Fatal("prerm and postinst source /usr/share/aii-os/aii-units.sh, and the package does not install it — both scripts would fail on the first line")
	}
	// And the dependency that provides --user support must be declared.
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

// stripComments drops shell comments so a script's PROSE cannot satisfy
// or violate a check about its CODE.
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
