//go:build windows

package tools

import (
	"os"
	"path/filepath"
	"strings"
)

const platformNoWrite = false

func isRooted(p string) bool {
	if p == "" {
		return false
	}

	if p[0] == '/' || p[0] == '\\' {
		return true
	}

	if filepath.VolumeName(p) != "" {
		return true
	}
	return filepath.IsAbs(p)
}

func spelledRoot(path string) bool {
	if !isRooted(path) {
		return false
	}
	clean := filepath.Clean(path)
	vol := filepath.VolumeName(clean)
	switch {
	case len(vol) == 2 && vol[1] == ':':
		return len(clean) == 2 || !os.IsPathSeparator(clean[2])
	case len(vol) > 2 && os.IsPathSeparator(vol[0]) && os.IsPathSeparator(vol[1]):

		return !strings.ContainsAny(vol[2:], `\/`)
	}
	return true
}
