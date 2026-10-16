//go:build linux && !android

package machine

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/aiii-dot-id/aii-os/internal/install"
)

const Supported = true

var ErrNoRuntimeDir = errors.New("XDG_RUNTIME_DIR is not set to an absolute path, so the machine service's link has nowhere to lie")

func LinkPath(dir string) (string, error) {
	rt := os.Getenv("XDG_RUNTIME_DIR")
	if !filepath.IsAbs(rt) {
		return "", ErrNoRuntimeDir
	}
	name := "aii-machine"
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", fmt.Errorf("resolve %s: %w", dir, err)
	}
	if def, err := install.MachineDir(); err != nil || filepath.Clean(def) != filepath.Clean(abs) {
		sum := sha256.Sum256([]byte(filepath.Clean(abs)))
		name += "-" + hex.EncodeToString(sum[:6])
	}
	return filepath.Join(rt, name, linkFileName), nil
}

func prepareLinkDir(path string) error {
	return ensureOwnerOnlyDir(filepath.Dir(path))
}
