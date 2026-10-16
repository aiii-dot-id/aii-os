//go:build linux || android || darwin || ios

package pluginhost

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/aiii-dot-id/aii-os/internal/firewall"
)

func (f nativeFiles) ownPaths() ([]nativePath, error) {
	var paths []nativePath
	for _, path := range f.own {
		if path == "" {
			continue
		}
		if !filepath.IsAbs(path) {
			return nil, &os.PathError{Op: "native containment requires an absolute path", Path: path, Err: fs.ErrInvalid}
		}
		real, err := filepath.EvalSymlinks(path)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		info, err := os.Stat(real)
		if err != nil {
			return nil, err
		}
		paths = append(paths, nativePath{path: real, source: real, dir: info.IsDir(), read: true})
		if alias := firewall.ResolveSlot(path); alias != real {
			paths = append(paths, nativePath{path: alias, source: real, dir: info.IsDir(), read: true})
		}
	}
	return paths, nil
}
