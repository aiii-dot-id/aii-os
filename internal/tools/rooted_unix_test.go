//go:build !windows

package tools

import "testing"

// .
// .
// .
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
		// .
		// .
		{`\etc\passwd`, false},
	} {
		if got := isRooted(c.path); got != c.want {
			t.Errorf("isRooted(%q) = %v, want %v", c.path, got, c.want)
		}
	}
}
