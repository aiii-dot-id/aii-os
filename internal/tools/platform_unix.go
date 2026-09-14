//go:build !android && !ios && !windows

// .
// .
// .
package tools

import "path/filepath"

// .
// .
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
func isRooted(p string) bool { return filepath.IsAbs(p) }
