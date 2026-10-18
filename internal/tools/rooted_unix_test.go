//go:build !windows

package tools

import "testing"

// On unix "rooted" and "absolute" are the same question, and this pins
// that they stay the same — the seam exists for Windows, not to change
// unix behaviour.
func TestIsRootedIsAbsolutenessOnUnix(t *testing.T) {
	for _, c := range []struct {
		path string
		want bool
	}{
		{"/etc/passwd", true},
		{"/", true},
		{"relative/path", false},
		{"../escape", false},
		{"", false},
		// A backslash is an ordinary filename character here, NOT a
		// separator: "\x" is a relative path called literally that.
		{`\etc\passwd`, false},
	} {
		if got := isRooted(c.path); got != c.want {
			t.Errorf("isRooted(%q) = %v, want %v", c.path, got, c.want)
		}
	}
}
