//go:build windows

// .
// .
// .
// .
// .
// .
// .
// .
package tools

import "path/filepath"

const platformNoWrite = false

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
func isRooted(p string) bool {
	if p == "" {
		return false
	}
	// .
	// .
	if p[0] == '/' || p[0] == '\\' {
		return true
	}
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	if filepath.VolumeName(p) != "" {
		return true
	}
	return filepath.IsAbs(p)
}
