//go:build !windows

package install

import (
	"os"
	"path/filepath"
)

// writeRefusing writes a tool that records who reached it and refuses.
func writeRefusing(dir, tool, reached string) error {
	script := "#!/bin/sh\necho \"" + tool + " $*\" >> '" + reached + "'\necho 'refused: a test reached the real " + tool + "' >&2\nexit 99\n"
	return os.WriteFile(filepath.Join(dir, tool), []byte(script), 0o755)
}

func guardRegistry() (func() error, error) { return func() error { return nil }, nil }
