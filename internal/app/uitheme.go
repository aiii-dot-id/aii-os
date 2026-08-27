// theme.json — operator/identity-authored CSS custom properties, the
// T0 tier of the extensible UI (UI_FRAME.md §5, blank-slate design).
// Lives in the DATA DIR ROOT beside ui-layout.json and for the same
// reason: BOTH-HANDS editable, inside the identity's world, outside
// config.json's substrate protection. Absent file = the compiled
// theme.css defaults — frame as shipped, the safe default.
//
// Watching reuses the mtime-poll core (reload.go / uilayout.go): one
// mechanism, zero platform code.
//
// WHY THIS TIER IS TOKENS AND NOT CSS. Anything injected into the
// frame runs same-origin with the dashboard's full authority. The
// frame now carries a Content-Security-Policy of its own
// (uiCSP, internal/dashboard/server.go) as well as the strict
// section policy, but that is DEFENCE IN DEPTH and not the guard
// being relied on here: this file is where the operator's bytes
// enter, so this file is the boundary. A stylesheet is
// not inert: `url(https://attacker/x)` in a single property value is
// a beacon that fires on render, and it needs no script to do it. So
// this tier accepts NAME/VALUE PAIRS under a character allowlist,
// never a stylesheet, never a selector, never a script. The values
// are re-checked here on the server rather than trusted to the
// browser, because the file is the input and this is the boundary.

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

// maxUIThemeBytes bounds the file read — a token set is a screenful
// of JSON; anything bigger is a mistake refused loudly.
const maxUIThemeBytes = 64 * 1024

// maxUIThemeTokens bounds the token COUNT independently of the byte
// ceiling: 64 KiB of two-character names would otherwise be tens of
// thousands of properties to apply on every render.
const maxUIThemeTokens = 500

// maxUIThemeValueLen bounds a single value. Real token values are
// short (`#0b0f14`, `13px`, `rgba(255,255,255,.06)`); length beyond
// this is a smell, not a theme.
const maxUIThemeValueLen = 200

// uiThemeFile is the file name in the data dir root.
const uiThemeFile = "theme.json"

// uiThemePath returns the write-once boot snapshot, for the same
// reason uiLayoutPath does: deriving it live from a.cfg races
// applyConfigChange's whole-struct rollback write.
func (a *App) uiThemePath() string {
	a.snapshotUILayoutPath(a.configSnapshot().Identity.LedgerPath)
	return filepath.Join(filepath.Dir(a.uiLayoutFilePath), uiThemeFile)
}

// uiThemeShape is the v1 grammar:
// {"v":1,"tokens":{"--accent":"#7cc4ff","--bg":"#0b0f14"}}
type uiThemeShape struct {
	V      int               `json:"v"`
	Tokens map[string]string `json:"tokens"`
}

// validThemeName: a CSS custom property — two dashes then [A-Za-z0-9-_].
// Anything else cannot name a token, so it cannot be a typo worth
// tolerating.
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

// validThemeValue is a CHARACTER ALLOWLIST, not a denylist: only what
// a colour/length/font-stack needs. Excluded on purpose —
//
//	/ and *  → no comments (/* */), no font shorthand
//	; { } :  → cannot close a declaration or open a rule
//	@        → no @import
//	\ < >    → no escapes, no markup breakout
//	"        → no string breakout ( ' is allowed for font names )
//
// and `url(` is refused by substring on top of the allowlist, because
// url is the one construct that turns a stylesheet into a network
// client. Belt and braces: the allowlist already forbids nothing in
// `url(`, so this check is what actually stops it.
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

// inertThemeTokens returns the validated token names that the compiled
// frame stylesheet does not declare, sorted for a stable log.
//
// The vocabulary is READ FROM theme.css rather than mirrored into a Go
// list here — internal/dashboard owns that file and derives the set
// from the shipped bytes, so adding a token to the stylesheet cannot
// leave this check stale. An empty vocabulary means the stylesheet
// could not be read, and yields NO names on purpose: an unknown
// vocabulary must not manufacture a warning about every token the
// operator wrote.
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

// loadUITheme reads, validates and stores the token set. Returns true
// when the stored bytes changed. Absent file = nil (compiled
// defaults). An invalid file KEEPS the previous state and logs — a
// mid-edit save must not repaint the operator's screen in garbage.
//
// Refusal is WHOLE-FILE, matching ui-layout.json: a half-applied
// theme is a worse artifact than the previous one, and partial
// application would hide the typo it came from.
func (a *App) loadUITheme(quiet bool) bool {
	path := a.uiThemePath()
	raw, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			log.Printf("theme: unreadable, keeping current: %v", err)
			return false
		}
		raw = nil // absent = compiled theme.css defaults
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
		// Tolerate AND log, exactly as ui-layout does for a profile the
		// frame can never select. A token theme.css does not declare
		// is NOT refused: sections ship their own stylesheets and are
		// handed the same tokens over the bridge, so an unknown name
		// can be deliberate. But `--acent` for `--acc` is well-formed
		// too, and would otherwise be validated, stored, transmitted
		// and applied to documentElement while changing nothing that
		// anyone can see. That is the silent-inert class; the silence
		// is the defect, not the tolerance.
		if !quiet {
			for _, name := range inertThemeTokens(shape.Tokens) {
				log.Printf("theme: token %s is kept but INERT in the frame — theme.css declares no such property, so it restyles nothing in the frame (a section may still consume it; otherwise check the spelling against theme.css)", name)
			}
		}
		// Re-marshal the VALIDATED map: what travels to the browser is
		// what passed this gate, never the operator's raw bytes. The
		// layout can ship raw because it names sections; a theme is
		// injected into a document with no CSP, so the server emits it.
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

// currentUITheme is the dashboard's theme source (SetThemeSource).
func (a *App) currentUITheme() []byte {
	a.uiThemeMu.Lock()
	defer a.uiThemeMu.Unlock()
	return a.uiThemeRaw
}

// watchUITheme hot-reloads the theme on mtime change — the
// watchUILayout pattern exactly, including appearance and deletion
// (deleting the file is a statement: back to compiled defaults).
// Dies with the app context; parked while backgrounded.
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
