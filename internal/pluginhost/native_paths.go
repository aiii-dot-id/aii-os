package pluginhost

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/aiii-dot-id/aii-os/internal/firewall"
	"github.com/aiii-dot-id/aii-os/internal/oswall"
)

type nativeFiles struct {
	places  []*firewall.Rule
	read    []string
	private string

	own []string

	view []string

	shown func(string) bool

	startSeconds int

	inherited int
}

type nativePath struct {
	path, source string
	dir, read    bool
}

const nativePathLimit = "; external hard links and later unmasked paths are NOT protected"

func (f nativeFiles) paths() ([]nativePath, error) {
	if f.private != "" {

		if err := os.MkdirAll(f.private, 0o700); err != nil {
			return nil, err
		}
	}
	var paths []nativePath
	add := func(path string, read bool) error {
		if path == "" {
			return nil
		}
		if !filepath.IsAbs(path) {
			return &os.PathError{Op: "native containment requires an absolute path", Path: path, Err: fs.ErrInvalid}
		}
		real, err := filepath.EvalSymlinks(path)
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		info, err := os.Stat(real)
		if err != nil {
			return err
		}
		if !info.IsDir() && !info.Mode().IsRegular() {
			return &os.PathError{Op: "native containment requires a file or directory", Path: path, Err: fs.ErrInvalid}
		}
		paths = append(paths, nativePath{path: real, source: real, dir: info.IsDir(), read: read})

		alias := firewall.ResolveSlot(path)
		if read && alias != real {

			paths = append(paths, nativePath{path: alias, source: real, dir: info.IsDir(), read: true})
		}
		return nil
	}
	for _, p := range f.places {
		if p == nil || !p.Enforced || p.Kind != firewall.KindSubstrate {
			continue
		}
		if err := add(p.Path, false); err != nil {
			return nil, err
		}
		for _, path := range p.Except {
			if !exceptionInPlace(p.Path, path) {
				continue
			}
			if err := add(path, true); err != nil {
				return nil, err
			}
		}
	}
	for _, path := range append(append([]string(nil), f.read...), f.private) {
		if err := add(path, true); err != nil {
			return nil, err
		}
	}

	masks := slices.Clone(paths)
	paths = slices.DeleteFunc(paths, func(p nativePath) bool {
		if !p.read || p.path == p.source {
			return false
		}
		if f.shown != nil && !f.shown(p.path) {
			return false
		}
		for _, mask := range masks {
			if !mask.read && mask.dir && nativeBelow(mask.path, p.path) {
				return false
			}
		}
		return true
	})

	original := slices.Clone(paths)
	for _, alias := range original {
		if !alias.read || alias.path == alias.source {
			continue
		}
		for _, p := range original {
			if !p.read && (p.path == alias.source || nativeBelow(alias.source, p.path)) {
				rel, _ := filepath.Rel(alias.source, p.path)
				p.path = filepath.Join(alias.path, rel)
				paths = append(paths, p)
			}
		}
	}

	sort.SliceStable(paths, func(i, j int) bool {
		a, b := paths[i], paths[j]
		if da, db := strings.Count(a.path, string(filepath.Separator)), strings.Count(b.path, string(filepath.Separator)); da != db {
			return da < db
		}
		if a.path != b.path {
			return a.path < b.path
		}
		return a.read && !b.read
	})
	return slices.Compact(paths), nil
}

func exceptionInPlace(place, except string) bool { return oswall.ExceptionInPlace(place, except) }

func nativeBelow(parent, child string) bool { return oswall.Below(parent, child) }
