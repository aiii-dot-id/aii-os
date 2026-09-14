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
// .
// .
// .
// .
// .

package app

import (
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/dashboard"
	"github.com/aiii-dot-id/aii-os/internal/fsdir"
)

// .
// .
const maxUIThemeBytes = 64 * 1024

// .
// .
// .
const maxUIThemeTokens = 500

// .
// .
// .
const maxUIThemeValueLen = 200

// .
const uiThemeFile = "theme.json"

// .
// .
// .
func (a *App) uiThemePath() string {
	a.snapshotUILayoutPath(a.configSnapshot().Identity.LedgerPath)
	return filepath.Join(filepath.Dir(a.uiLayoutFilePath), uiThemeFile)
}

// .
// .
type uiThemeShape struct {
	V      int               `json:"v"`
	Tokens map[string]string `json:"tokens"`
}

// .
// .
// .
func validThemeName(name string) bool {
	if !strings.HasPrefix(name, "--") || len(name) < 3 || len(name) > 64 {
		return false
	}
	for _, r := range name[2:] {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
		default:
			return false
		}
	}
	return true
}

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
func validThemeValue(v string) bool {
	if v == "" || len(v) > maxUIThemeValueLen {
		return false
	}
	if strings.Contains(strings.ToLower(v), "url(") {
		return false
	}
	for _, r := range v {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == ' ', r == '#', r == '%', r == '.', r == ',', r == '(', r == ')',
			r == '\'', r == '-', r == '_', r == '+', r == '=':
		default:
			return false
		}
	}
	return true
}

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
func inertThemeTokens(tokens map[string]string) []string {
	declared := dashboard.DeclaredThemeTokens()
	if len(declared) == 0 {
		return nil
	}
	var inert []string
	for name := range tokens {
		if !declared[name] {
			inert = append(inert, name)
		}
	}
	sort.Strings(inert)
	return inert
}

// .
// .
// .
// .
// .
// .
// .
// .
func (a *App) loadUITheme(quiet bool) bool {
	path := a.uiThemePath()
	raw, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			log.Printf("theme: unreadable, keeping current: %v", err)
			return false
		}
		raw = nil
	}
	if len(raw) > maxUIThemeBytes {
		log.Printf("theme: %d bytes exceeds the %d ceiling — keeping current (a theme is a screenful of JSON)", len(raw), maxUIThemeBytes)
		return false
	}
	var clean []byte
	if raw != nil {
		var shape uiThemeShape
		if err := json.Unmarshal(raw, &shape); err != nil {
			log.Printf("theme: invalid JSON, keeping current (mid-edit saves must not repaint the screen): %v", err)
			return false
		}
		if shape.V != 1 {
			log.Printf("theme: v must be 1, got %d — keeping current", shape.V)
			return false
		}
		if len(shape.Tokens) > maxUIThemeTokens {
			log.Printf("theme: %d tokens exceeds the %d ceiling — keeping current", len(shape.Tokens), maxUIThemeTokens)
			return false
		}
		for name, val := range shape.Tokens {
			if !validThemeName(name) {
				log.Printf("theme: %q is not a CSS custom property (--name) — keeping current", name)
				return false
			}
			if !validThemeValue(val) {
				log.Printf("theme: value for %s is refused (allowlist: colours, lengths, font names; no url(), no comments, no selectors) — keeping current", name)
				return false
			}
		}
		// .
		// .
		// .
		// .
		// .
		// .
		// .
		// .
		// .
		if !quiet {
			for _, name := range inertThemeTokens(shape.Tokens) {
				log.Printf("theme: token %s is kept but INERT in the frame — theme.css declares no such property, so it restyles nothing in the frame (a section may still consume it; otherwise check the spelling against theme.css)", name)
			}
		}
		// .
		// .
		// .
		// .
		clean, err = json.Marshal(uiThemeShape{V: 1, Tokens: shape.Tokens})
		if err != nil {
			log.Printf("theme: cannot re-encode validated tokens, keeping current: %v", err)
			return false
		}
	}
	a.uiThemeMu.Lock()
	changed := string(a.uiThemeRaw) != string(clean)
	a.uiThemeRaw = clean
	a.uiThemeMu.Unlock()
	if changed && !quiet {
		if clean == nil {
			log.Printf("theme: file absent — compiled defaults")
		} else {
			log.Printf("theme: loaded %s (%d bytes validated)", path, len(clean))
		}
	}
	return changed
}

// .
func (a *App) currentUITheme() []byte {
	a.uiThemeMu.Lock()
	defer a.uiThemeMu.Unlock()
	return a.uiThemeRaw
}

// .
// .
// .
// .
func (a *App) watchUITheme() {
	if a.bgCtx == nil {
		return
	}
	path := a.uiThemePath()
	var last time.Time
	if fi, err := os.Stat(path); err == nil {
		last = fi.ModTime()
	}
	w := fsdir.New(a.bgCtx, a.gate, filepath.Dir(path), fsdir.Options{
		Heartbeat: a.watcherInterval(),
		File:      filepath.Base(path),
	})
	for {
		select {
		case <-a.bgCtx.Done():
			return
		case <-w.C:
			var mt time.Time
			if fi, err := os.Stat(path); err == nil {
				mt = fi.ModTime()
			}
			if mt.Equal(last) {
				continue
			}
			last = mt
			if a.loadUITheme(false) && a.dashboard != nil {
				a.dashboard.BroadcastTheme()
			}
		}
	}
}
