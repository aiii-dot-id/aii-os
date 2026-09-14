package install

import (
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"
	"strings"
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
// .
// .
// .
// .
func StopChannelNames(dir string) []string {
	abs, err := filepath.Abs(dir)
	if err != nil {
		abs = dir
	}
	// .
	// .
	sum := sha256.Sum256([]byte(strings.ToLower(filepath.Clean(abs))))
	id := hex.EncodeToString(sum[:8])
	return []string{`Global\aii-os-stop-` + id, `aii-os-stop-` + id}
}
