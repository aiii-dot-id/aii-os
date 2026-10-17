package version

import (
	"os"
	"regexp"
	"testing"
)

// The repo-root VERSION is the authored file; internal/version/VERSION is
// the copy the compiler can reach. Two files holding one fact is a rot
// risk, so this test is the thing that makes the arrangement safe: bump
// one without the other and the gate fails here, naming both paths.
func TestEmbeddedVersionMatchesAuthoredFile(t *testing.T) {
	root, err := os.ReadFile("../../VERSION")
	if err != nil {
		t.Fatalf("repo-root VERSION unreadable: %v", err)
	}
	want := string(root)
	// Compare the MEANING, not the bytes: a trailing-newline difference
	// between the two files is not a version difference, and failing on
	// it would train people to ignore this test.
	if got, trimmed := Authored(), trimSpace(want); got != trimmed {
		t.Errorf("version drift: internal/version/VERSION says %q, repo-root VERSION says %q — bump both", got, trimmed)
	}
}

// TestAuthoredVersionIsValidSemver keeps a malformed bump from reaching a
// release: Compare() is semver-based, so an unparseable version silently
// orders wrong rather than failing loudly.
func TestAuthoredVersionIsValidSemver(t *testing.T) {
	if v := Authored(); !Valid(v) {
		t.Errorf("VERSION %q is not a valid unprefixed semantic version", v)
	}
}

func trimSpace(s string) string {
	for len(s) > 0 && (s[len(s)-1] == '\n' || s[len(s)-1] == '\r' || s[len(s)-1] == ' ' || s[len(s)-1] == '\t') {
		s = s[:len(s)-1]
	}
	for len(s) > 0 && (s[0] == '\n' || s[0] == '\r' || s[0] == ' ' || s[0] == '\t') {
		s = s[1:]
	}
	return s
}

// THE HOST'S OWN VERSION IS WHAT PLUGIN WINDOWS ARE WRITTEN AGAINST. A
// package's aiios_min_version / aiios_max_exclusive_version follow the
// canonical schema's aiios_semver_core — three numbers, nothing else —
// and the host ranks itself in that grammar (internal/packagefmt). A
// VERSION with a prerelease or build suffix would be valid SemVer and
// still unrankable, and every bounded package would refuse this host.
// Caught here, at the bump, not at the first install.
func TestAuthoredVersionIsAnAIIOSVersion(t *testing.T) {
	if v := Authored(); !regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+$`).MatchString(v) {
		t.Errorf("VERSION %q is not three numbers (major.minor.patch): bounded plugins could not be ranked against it", v)
	}
}
