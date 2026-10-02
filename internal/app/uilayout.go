package app

import (
	_ "embed"
	"encoding/json"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/fsdir"
)

var (
	//go:embed overlay_README.md
	overlayREADME []byte
)

const maxUILayoutBytes = 64 * 1024

const uiLayoutFile = "ui-layout.json"

const uiOverlayDirName = "ui"

func (a *App) uiOverlayDir() string {
	return filepath.Join(filepath.Dir(a.uiLayoutPath()), uiOverlayDirName)
}

var overlayShippedSeeds = []string{
	"e4556fa91989d2e2b218bf50f306c897c62b85cf9a77d1958bc53d4e7d011a0b",
	"2f070e93ad6f5502e0d06b5433d860dfbe234985c03f33100aedf864a0647375",
	"074dfa9f1ac869859b0ae944a5f1d6186c617f006ed8d8bf1bb4c1c3592ec7cc",
	"fe9b2de4ffc019aebb5614d74dabdd55957667e953fcdc55cd6713507c3295a9",
	"39120234b76fb07aa2f9b42f62d9105c841f8ade45fd81f1d4e6dfd61fedbc51",
	"dcaa806c251ad13d0b385ee4f1723d2d304c6110ea1e74665f389c0d4b4b3311",
}

func (a *App) seedOverlayREADME() {
	dir := a.uiOverlayDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		logsink.Warn("layout.error", "seed: mkdir %s: %v", dir, err)
		return
	}
	seedDoc(filepath.Join(dir, "README.md"), overlayREADME, nil, overlayShippedSeeds, "[ui-overlay] seed")
}

type dashState struct {
	mu sync.Mutex

	dashboardAccessToken string

	uiLayoutRaw []byte

	uiThemeRaw []byte

	uiLayoutFilePath string
	uiLayoutPathOnce sync.Once

	overlayLast atomic.Pointer[string]

	overlayToken atomic.Uint64
}

func (a *App) uiLayoutPath() string {
	a.snapshotUILayoutPath(a.configSnapshot().Identity.LedgerPath)
	return a.dash.uiLayoutFilePath
}

func (a *App) snapshotUILayoutPath(ledgerPath string) {
	a.dash.uiLayoutPathOnce.Do(func() {
		a.dash.uiLayoutFilePath = filepath.Join(filepath.Dir(ledgerPath), uiLayoutFile)
	})
}

type uiLayoutShape struct {
	V        int                            `json:"v"`
	Profiles map[string]map[string][]string `json:"profiles"`
}

var selectableUIProfiles = map[string]bool{"desktop": true, "mobile": true}

func selectableUIProfileNames() []string {
	names := make([]string, 0, len(selectableUIProfiles))
	for name := range selectableUIProfiles {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func (a *App) loadUILayout(quiet bool) bool {
	path := a.uiLayoutPath()
	raw, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			logsink.Warn("layout.error", "unreadable, keeping current: %v", err)
			return false
		}
		raw = nil
	}
	if len(raw) > maxUILayoutBytes {
		logsink.Warn("layout.refusal", "%d bytes exceeds the %d ceiling — keeping current (a layout is a screenful of JSON)", len(raw), maxUILayoutBytes)
		return false
	}
	if raw != nil {
		var shape uiLayoutShape
		if err := json.Unmarshal(raw, &shape); err != nil {
			logsink.Warn("layout.refusal", "invalid JSON, keeping current (mid-edit saves must not blank the screen): %v", err)
			return false
		}
		if shape.V != 1 {
			logsink.Warn("layout.refusal", "v must be 1, got %d — keeping current", shape.V)
			return false
		}

		if !quiet {
			var inert []string
			for name := range shape.Profiles {
				if !selectableUIProfiles[name] {
					inert = append(inert, name)
				}
			}
			sort.Strings(inert)
			for _, name := range inert {
				logsink.Info("layout.decision", "profile %q is kept but INERT — the frame selects only %s, so nothing in it will ever render", name, strings.Join(selectableUIProfileNames(), " or "))
			}
		}
	}
	a.dash.mu.Lock()
	changed := string(a.dash.uiLayoutRaw) != string(raw)
	a.dash.uiLayoutRaw = raw
	a.dash.mu.Unlock()
	if changed && !quiet {
		if raw == nil {
			logsink.Info("layout.start", "file absent — frame-only (no sections laid out)")
		} else {
			logsink.Info("layout.start", "loaded %s (%d bytes)", path, len(raw))
		}
	}
	return changed
}

func (a *App) currentUILayout() []byte {
	a.dash.mu.Lock()
	defer a.dash.mu.Unlock()
	return a.dash.uiLayoutRaw
}

func (a *App) watchUILayout() {
	if a.bgCtx == nil {
		return
	}
	path := a.uiLayoutPath()
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

			if a.gate.Paused() {
				continue
			}
			var mt time.Time
			if fi, err := os.Stat(path); err == nil {
				mt = fi.ModTime()
			}
			if mt.Equal(last) {
				continue
			}
			last = mt
			if a.loadUILayout(false) && a.dashboard != nil {

				a.dashboard.BroadcastLayout()
			}
		}
	}
}
