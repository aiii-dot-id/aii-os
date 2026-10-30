//go:build android || ios

package tools

import "path/filepath"

const platformNoWrite = true

func isRooted(p string) bool { return filepath.IsAbs(p) }

func spelledRoot(string) bool { return false }
