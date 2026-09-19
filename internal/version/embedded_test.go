package version

import (
	"os"
	"regexp"
	"testing"
)

// .
// .
// .
// .
func TestEmbeddedVersionMatchesAuthoredFile(t *testing.T) {
	root, err := os.ReadFile("../../VERSION")
	if err != nil {
		t.Fatalf("repo-root VERSION unreadable: %v", err)
	}
	want := string(root)
	// .
	// .
	// .
	if got, trimmed := Authored(), trimSpace(want); got != trimmed {
		t.Errorf("version drift: internal/version/VERSION says %q, repo-root VERSION says %q — bump both", got, trimmed)
	}
}

// .
// .
// .
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

// .
// .
// .
// .
// .
// .
// .
func TestAuthoredVersionIsAnAIIOSVersion(t *testing.T) {
	if v := Authored(); !regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+$`).MatchString(v) {
		t.Errorf("VERSION %q is not three numbers (major.minor.patch): bounded plugins could not be ranked against it", v)
	}
}
