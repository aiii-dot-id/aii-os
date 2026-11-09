package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/dashboard"
	"github.com/aiii-dot-id/aii-os/internal/fsdir"
)

const maxUIThemeBytes = 64 * 1024

const maxUIThemeTokens = 500

const maxUIThemeValueLen = 200

const uiThemeFile = "theme.json"

func (a *App) uiThemePath() string {
	a.snapshotUILayoutPath(a.configSnapshot().Identity.LedgerPath)
	return filepath.Join(filepath.Dir(a.dash.uiLayoutFilePath), uiThemeFile)
}

type uiThemeShape struct {
	V      int               `json:"v"`
	Tokens map[string]string `json:"tokens"`
}

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

func validateUITheme(raw []byte) (uiThemeShape, error) {
	var shape uiThemeShape
	if len(raw) > maxUIThemeBytes {
		return shape, fmt.Errorf("%d bytes exceeds the %d ceiling (a theme is a screenful of JSON)", len(raw), maxUIThemeBytes)
	}
	if err := json.Unmarshal(raw, &shape); err != nil {
		return shape, fmt.Errorf("invalid JSON (mid-edit saves must not repaint the screen): %v", err)
	}
	if shape.V != 1 {
		return shape, fmt.Errorf("v must be 1, got %d", shape.V)
	}
	if len(shape.Tokens) > maxUIThemeTokens {
		return shape, fmt.Errorf("%d tokens exceeds the %d ceiling", len(shape.Tokens), maxUIThemeTokens)
	}
	for name, val := range shape.Tokens {
		if !validThemeName(name) {
			return shape, fmt.Errorf("%q is not a CSS custom property (--name)", name)
		}
		if !validThemeValue(val) {
			return shape, fmt.Errorf("value for %s is refused (allowlist: colours, lengths, font names; no url(), no comments, no selectors)", name)
		}
	}
	return shape, nil
}

func (a *App) loadUITheme(quiet bool) bool {
	path := a.uiThemePath()
	raw, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			logsink.Warn("theme.error", "unreadable, keeping current: %v", err)
			return false
		}
		raw = nil
	}
	var clean []byte
	if raw != nil {
		shape, err := validateUITheme(raw)
		if err != nil {
			logsink.Warn("theme.refusal", "%v — keeping current", err)
			return false
		}

		if !quiet {
			for _, name := range inertThemeTokens(shape.Tokens) {
				logsink.Info("theme.decision", "token %s is kept but INERT in the frame — theme.css declares no such property, so it restyles nothing in the frame (a section may still consume it; otherwise check the spelling against theme.css)", name)
			}
		}

		clean, err = json.Marshal(uiThemeShape{V: 1, Tokens: shape.Tokens})
		if err != nil {
			logsink.Warn("theme.error", "cannot re-encode validated tokens, keeping current: %v", err)
			return false
		}
	}
	a.dash.mu.Lock()
	changed := string(a.dash.uiThemeRaw) != string(clean)
	a.dash.uiThemeRaw = clean
	a.dash.mu.Unlock()
	if changed && !quiet {
		if clean == nil {
			logsink.Info("theme.start", "file absent — compiled defaults")
		} else {
			logsink.Info("theme.start", "loaded %s (%d bytes validated)", path, len(clean))
		}
	}
	return changed
}

func (a *App) currentUITheme() []byte {
	a.dash.mu.Lock()
	defer a.dash.mu.Unlock()
	return a.dash.uiThemeRaw
}

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

var (
	errThemeFile  = errors.New("theme.json is not a valid theme as it stands, so it was left as it is")
	errThemeToken = errors.New("the page sent a token theme.json does not take")
)

func (a *App) setUIThemeTokens(set map[string]string) error {
	a.dash.themeWrite.Lock()
	defer a.dash.themeWrite.Unlock()
	declared := dashboard.DeclaredThemeTokens()
	if len(declared) == 0 {
		return fmt.Errorf("%w: the frame's token list could not be read", errThemeToken)
	}
	for name, val := range set {
		if !validThemeName(name) || !declared[name] || (val != "" && !validThemeValue(val)) {
			return fmt.Errorf("%w: %s", errThemeToken, name)
		}
	}
	path := a.uiThemePath()
	doc := map[string]json.RawMessage{}
	shape := uiThemeShape{V: 1, Tokens: map[string]string{}}
	raw, err := os.ReadFile(path)
	switch {
	case err == nil:
		if shape, err = validateUITheme(raw); err != nil {
			return fmt.Errorf("%w: %v", errThemeFile, err)
		}
		if err := json.Unmarshal(raw, &doc); err != nil {
			return fmt.Errorf("%w: %v", errThemeFile, err)
		}
		if shape.Tokens == nil {
			shape.Tokens = map[string]string{}
		}
	case !os.IsNotExist(err):
		return fmt.Errorf("%w: unreadable: %v", errThemeFile, err)
	}
	for name, val := range set {
		if val == "" {
			delete(shape.Tokens, name)
		} else {
			shape.Tokens[name] = val
		}
	}
	if len(shape.Tokens) > maxUIThemeTokens {
		return fmt.Errorf("%w: %d tokens exceeds the %d ceiling", errThemeToken, len(shape.Tokens), maxUIThemeTokens)
	}
	tokens, err := json.Marshal(shape.Tokens)
	if err != nil {
		return err
	}
	doc["v"], doc["tokens"] = json.RawMessage("1"), tokens
	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}

	out = append(out, '\n')
	if _, err := validateUITheme(out); err != nil {
		return fmt.Errorf("%w: the file as it would be written is refused: %v", errThemeToken, err)
	}
	if _, err := writeFileAtomic(path, out); err != nil {
		return err
	}
	a.loadUITheme(false)
	return nil
}
