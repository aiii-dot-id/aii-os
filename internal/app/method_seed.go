package app

import (
	_ "embed"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
	"os"
	"path/filepath"
)

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
// .
// .
// .
var (
	//go:embed METHOD.md
	methodMD []byte
)

const methodFileName = "METHOD.md"

// .
// .
// .
// .
var methodShippedSeeds = []string{
	"de11e86ed497bdca808df97631744cefb401bb55fffd44ca53d721ce977210d6",
	"0039ba4aceda742d04f309e5ec7aa9fbcb801d8cdf66585098ba16ff692a0b42",
}

// .
// .
// .
func (a *App) seedMethodDoc() {
	dir := filepath.Dir(a.cfg.SourcePath)
	if abs, err := filepath.Abs(dir); err == nil {
		dir = abs
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		logsink.Warn("seed.error", "seed: mkdir %s: %v", dir, err)
		return
	}
	seedDoc(filepath.Join(dir, methodFileName), methodMD, nil, methodShippedSeeds, "[method] seed")
}
