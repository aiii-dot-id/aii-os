//go:build darwin && !ios

package machine

import "path/filepath"

const Supported = true

func LinkPath(dir string) (string, error) {
	return filepath.Join(dir, linkFileName), nil
}

func prepareLinkDir(string) error { return nil }
