package app

import (
	_ "embed"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
	"os"
	"path/filepath"
)

var (
	//go:embed METHOD.md
	methodMD []byte
)

const methodFileName = "METHOD.md"

var methodShippedSeeds = []string{
	"de11e86ed497bdca808df97631744cefb401bb55fffd44ca53d721ce977210d6",
	"b6e07e905b7aedccb013b0f8f485e1f157ec049d0cbe045818851658eef33cd0",
	"0039ba4aceda742d04f309e5ec7aa9fbcb801d8cdf66585098ba16ff692a0b42",
	"b6e07e905b7aedccb013b0f8f485e1f157ec049d0cbe045818851658eef33cd0",
}

func (a *App) seedMethodDoc() {
	path := a.seededDocPath(methodFileName)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		logsink.Warn("seed.error", "seed: mkdir %s: %v", filepath.Dir(path), err)
		return
	}
	a.bringSeededDocHome(path, nil, methodShippedSeeds, "[method] seed")
	seedDoc(path, methodMD, nil, methodShippedSeeds, a.seedRecordPath(methodFileName), "[method] seed")
}
