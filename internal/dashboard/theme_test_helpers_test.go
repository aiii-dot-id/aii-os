//go:build !windows

package dashboard

import "strings"

// jsStringArray renders a Go string slice as a JS array literal for a
// harness page (the strings are token names: no quotes to escape).
func jsStringArray(xs []string) string {
	return "['" + strings.Join(xs, "','") + "']"
}
