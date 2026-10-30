package oswall

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/aiii-dot-id/aii-os/internal/firewall"
)

const (
	DefaultStartSeconds = 10
	MaxStartSeconds     = 120
)

func Below(parent, child string) bool {
	rel, err := filepath.Rel(parent, child)
	return err == nil && rel != "." && rel != ".." && !filepath.IsAbs(rel) && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func ExceptionInPlace(place, except string) bool {

	if fi, err := os.Lstat(except); err == nil && fi.Mode()&(os.ModeSymlink|os.ModeIrregular) != 0 {
		return false
	} else if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return false
	}
	p, err := filepath.EvalSymlinks(place)
	if err != nil {
		return false
	}
	e, err := filepath.EvalSymlinks(except)
	if err != nil {
		return errors.Is(err, fs.ErrNotExist)
	}
	return e == firewall.ResolveSlot(except) && Below(p, e)
}
