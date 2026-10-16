//go:build darwin || ios

package firewall

import (
	"path/filepath"
	"strings"
)

const volfs = "/.vol"

func byIdentity(path string) bool {
	if !filepath.IsAbs(path) {
		return false
	}
	p := canonical(filepath.Clean(path))
	return p == volfs || strings.HasPrefix(p, volfs+"/")
}
