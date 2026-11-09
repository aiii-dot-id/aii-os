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
	"6c5c998f4c7d582d74d85926774ce315e8124221087ce40b0f913e4c74c7d180",
	"fe9b2de4ffc019aebb5614d74dabdd55957667e953fcdc55cd6713507c3295a9",
	"39120234b76fb07aa2f9b42f62d9105c841f8ade45fd81f1d4e6dfd61fedbc51",
	"dcaa806c251ad13d0b385ee4f1723d2d304c6110ea1e74665f389c0d4b4b3311",
	"b6f814ed6f7dc4eb0e709f2864610fe4c177e5f569c509ea08b6ffb8486f3cf3",
	"3fb8d2fc62cc0a4c48d974f2454adfe846122efaf53e0bc02453f43a202f44ec",
	"1c4ed9afcfd5bf233b688ada05828c584cf50a77374d63c49a1659c5ee79df9b",
	"b6665114b1afb827380241ff257c211e8d902a6004b1e6eb483b9b542e92f355",
	"9c720f5a25d657148b3db2b05104b5c4d1cdd1404d56d96511a5d29a88403cc4",
	"ca61864fe72b82719ae7119806ec9643cc5483a38fcf5a5a9b8adb39ce59cdde",
	"0e519bfe61b213122cdfb3bb6c7f0373d5eff28d67b6b6b1ecfa015ccbb04b4c",
	"2356b2c64d6f02e05f7f6d1ea0b685b56219188410ff7acef84fa5caa9ae443f",
	"2485072a331720334ac164e339124b2439b39b74264159453523bde54b5b8f9a",
	"4ae0ba2b44e5cb0b7b4ebb8353a20aca1d931badda88fa3a462d45f4b0f588fd",
	"e3401bf3027753f6a56e2be153583926695846b6df1eff66197f6fe2c17dbfda",
	"cb43a8166ab98a713854c04cb6476ed414c9ba56e6de9df9ff94ac53c2c38905",
	"a08ab630841240328f86d360e7ea3a46d7091845590876b07d3845310670ebe2",
	"26309d184e398cf493df4e5c15f716ed9cae527880543146f7634a465d58f8d2",
	"5d463a2911f6ccae6ea00b1b0bf91366659fe1c3de01f1898a334fe5a284fae0",
	"2dd9a58bb90aa89880eabcbcc2fc011f57db8b79a0f34da9431c9197fd6f34ff",
	"c24fcd7d6801fd686ab392973af9f40d78e805989e2af4f1fbf95d6f73461f79",
	"d6e7044b0faee05305b446b16d0ae30f831bd0187f7e660c43efa76fefa38a37",
	"6ce335e477c4699703fadf1e77e366602ce5b4ae3f9f546e877208d2c8c12216",
	"3b239b04334069dbbe991bd99df4aa49e91d57d15b5f81814133699487b09e23",
	"b4ccdcfcc1d09769025c047bd749934508469a952d9946b39f8f70935ee1b8a4",
}

func (a *App) seedOverlayREADME() {
	dir := a.uiOverlayDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		logsink.Warn("layout.error", "seed: mkdir %s: %v", dir, err)
		return
	}
	seedDoc(filepath.Join(dir, "README.md"), overlayREADME, nil, overlayShippedSeeds, a.seedRecordPath(overlayRecordName), "[ui-overlay] seed")
}

type dashState struct {
	mu sync.Mutex

	dashboardAccessToken string

	servingUnkept string

	uiLayoutRaw []byte

	uiThemeRaw []byte

	themeWrite sync.Mutex

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
