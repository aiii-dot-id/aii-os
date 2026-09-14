package app

import (
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

// .
// .
// .
// .
// .
// .
// .
// .
const (
	uiNameFileName    = "name"
	uiNameMaxFileByte = 256
	uiNameMaxRunes    = 64
)

// .
// .
func (a *App) resolveDisplayName() string {
	if name, ok := a.uiNameFromFile(); ok {
		return name
	}
	if a.store != nil {
		if n := a.store.IdentityName(); n != "" {
			return n
		}
	}
	return ""
}

// .
// .
// .
func (a *App) uiNameFromFile() (string, bool) {
	b, err := os.ReadFile(filepath.Join(a.uiOverlayDir(), uiNameFileName))
	if err != nil || len(b) > uiNameMaxFileByte {
		return "", false
	}
	line, _, _ := strings.Cut(string(b), "\n")
	line = strings.TrimSpace(line)
	if line == "" || utf8.RuneCountInString(line) > uiNameMaxRunes {
		return "", false
	}
	for _, r := range line {
		if r < 0x20 || r == 0x7f {
			return "", false
		}
		if (r >= 0x202a && r <= 0x202e) || (r >= 0x2066 && r <= 0x2069) {
			return "", false
		}
	}
	return line, true
}
