// ui-layout.json — the slot→section layout with form-factor profiles
// (R66 UP2, UI_FRAME.md §5). The file lives in the DATA DIR ROOT
// (beside the ledger), deliberately: it must be BOTH-HANDS editable —
// inside the identity's world, where their own file tools reach (the
// data dir sits under the Ring 5 sandbox in every stock layout), and
// OUTSIDE config.json's substrate protection (SetProtectedPaths pins
// ledger/key/db/config; the layout is not among them) — because a
// layout grants no authority: the server gates every command a
// section can send, so rearranging the home is the intended
// co-modification loop, not a privilege. Absent file = no sections —
// frame-only, today's dashboard, the safe default.
//
// Watching reuses the repo's live-reload core (reload.go): an mtime
// poll, because mtime is observable identically on all five platforms
// — one mechanism, zero platform code.

package app

import (
	_ "embed"
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/fsdir"
)

// overlayREADME is the capability matrix for identities WITHOUT the Go
// source: what writing a file in ui/ does, verified from the wire, not
// remembered from the source. Seeded once at boot into the overlay dir
// itself — the point of use — never clobbering an identity's own edits:
// the seed is content-addressed (seed only when absent or byte-equal).
var (
	//go:embed overlay_README.md
	overlayREADME []byte
)

// maxUILayoutBytes bounds the file read — a layout is a screenful of
// JSON; anything bigger is a mistake refused loudly.
const maxUILayoutBytes = 64 * 1024

// uiLayoutFile is the file name in the data dir root.
const uiLayoutFile = "ui-layout.json"

// uiOverlayDirName is the operator's frame overlay directory, in the
// data dir root beside ui-layout.json and theme.json — the same
// both-hands surface, because it is the same kind of input. Absent
// directory is the normal case and means the compiled frame.
const uiOverlayDirName = "ui"

// uiOverlayDir returns the T1 overlay directory. It derives from the
// same write-once snapshot as the layout path, so it cannot disagree
// with the layout and theme about where the data dir root is — one
// derivation, not three copies of a filepath.Join.
func (a *App) uiOverlayDir() string {
	return filepath.Join(filepath.Dir(a.uiLayoutPath()), uiOverlayDirName)
}

// overlayShippedSeeds is every version of the overlay README this
// platform has ever shipped (docSeedKey over the raw bytes — no stamp
// here), oldest first, CURRENT LAST. Same contract and same gate test
// as skillsShippedSeeds: edit the template, append the key, never
// remove one.
var overlayShippedSeeds = []string{
	"e4556fa91989d2e2b218bf50f306c897c62b85cf9a77d1958bc53d4e7d011a0b", // 2026-08-26: the first shipped matrix (72f82d2)
}

// seedOverlayREADME drops the capability matrix into the overlay dir
// at boot: absent → seed; any version WE shipped (overlayShippedSeeds)
// → re-seed; anything else is the identity's rewrite and wins forever.
// An earlier version compared against the CURRENT embed only, under a
// comment promising that updates reach identities that never touched
// the doc — they did not: an untouched doc from an older build differs
// from the new embed exactly the way an edit does, and was classified
// as one. See seeddoc.go.
func (a *App) seedOverlayREADME() {
	dir := a.uiOverlayDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		log.Printf("[ui-overlay] seed: mkdir %s: %v", dir, err)
		return
	}
	seedDoc(filepath.Join(dir, "README.md"), overlayREADME, nil, overlayShippedSeeds, "[ui-overlay] seed")
}

// uiLayoutPath returns the write-once snapshot taken in startLive
// BEFORE any watcher goroutine exists. Deriving it live from a.cfg
// raced applyConfigChange's whole-struct rollback write (caught by
// -race at UP2 landing verification — the composition-seams lesson:
// a new goroutine composed with an old mutation path). The ledger
// path is fixed at boot, so a snapshot is the honest shape, not a
// lock.
func (a *App) uiLayoutPath() string {
	a.snapshotUILayoutPath(a.configSnapshot().Identity.LedgerPath)
	return a.uiLayoutFilePath
}

// snapshotUILayoutPath takes the write-once boot-path snapshot. The
// sync.Once keeps directly-constructed test Apps working and makes a
// second call a no-op, never a re-derivation.
func (a *App) snapshotUILayoutPath(ledgerPath string) {
	a.uiLayoutPathOnce.Do(func() {
		a.uiLayoutFilePath = filepath.Join(filepath.Dir(ledgerPath), uiLayoutFile)
	})
}

// uiLayoutShape is the v1 grammar: {"v":1,"profiles":{"desktop":
// {"panel":["<section-id>"],...},"mobile":{"dock":[...]}}}. The frame
// selects the profile by viewport class and tolerates unknown slots
// (schema-tolerant, §5); validation here only refuses what cannot be
// a layout at all — the raw bytes are what travels, the operator's
// file is the truth.
type uiLayoutShape struct {
	V        int                            `json:"v"`
	Profiles map[string]map[string][]string `json:"profiles"`
}

// selectableUIProfiles mirrors profileName() in static/sections.js,
// which resolves a 767px matchMedia to exactly these two names. The
// duplication is deliberate and load-bearing: the set of profiles the
// frame can ASK for is what makes an inert profile detectable at all.
// If sections.js grows a name, add it here — the drift shows up as a
// spurious "INERT" line, which is the loud failure, not a silent one.
var selectableUIProfiles = map[string]bool{"desktop": true, "mobile": true}

func selectableUIProfileNames() []string {
	names := make([]string, 0, len(selectableUIProfiles))
	for name := range selectableUIProfiles {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// loadUILayout reads and validates the file into a.uiLayoutRaw.
// Returns true when the stored bytes changed. Absent file = nil (no
// sections); an invalid file KEEPS the previous state and logs — a
// mid-edit save must not blank the operator's screen.
func (a *App) loadUILayout(quiet bool) bool {
	path := a.uiLayoutPath()
	raw, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			log.Printf("ui-layout: unreadable, keeping current: %v", err)
			return false
		}
		raw = nil // absent = frame-only
	}
	if len(raw) > maxUILayoutBytes {
		log.Printf("ui-layout: %d bytes exceeds the %d ceiling — keeping current (a layout is a screenful of JSON)", len(raw), maxUILayoutBytes)
		return false
	}
	if raw != nil {
		var shape uiLayoutShape
		if err := json.Unmarshal(raw, &shape); err != nil {
			log.Printf("ui-layout: invalid JSON, keeping current (mid-edit saves must not blank the screen): %v", err)
			return false
		}
		if shape.V != 1 {
			log.Printf("ui-layout: v must be 1, got %d — keeping current", shape.V)
			return false
		}
		// Tolerate AND log. An unknown profile name is kept on purpose:
		// a layout written for a newer frame must survive a downgrade,
		// so refusing here would be wrong. But the frame selects by
		// viewport and can only ever ask for the names below, so any
		// other profile is INERT — parsed, stored, transmitted, and
		// never rendered. Tolerance without a word is indistinguishable
		// from a typo being swallowed; that silence is the defect, not
		// the tolerance.
		if !quiet {
			var inert []string
			for name := range shape.Profiles {
				if !selectableUIProfiles[name] {
					inert = append(inert, name)
				}
			}
			sort.Strings(inert)
			for _, name := range inert {
				log.Printf("ui-layout: profile %q is kept but INERT — the frame selects only %s, so nothing in it will ever render", name, strings.Join(selectableUIProfileNames(), " or "))
			}
		}
	}
	a.uiLayoutMu.Lock()
	changed := string(a.uiLayoutRaw) != string(raw)
	a.uiLayoutRaw = raw
	a.uiLayoutMu.Unlock()
	if changed && !quiet {
		if raw == nil {
			log.Printf("ui-layout: file absent — frame-only (no sections laid out)")
		} else {
			log.Printf("ui-layout: loaded %s (%d bytes)", path, len(raw))
		}
	}
	return changed
}

// currentUILayout is the dashboard's layout source (SetLayoutSource).
func (a *App) currentUILayout() []byte {
	a.uiLayoutMu.Lock()
	defer a.uiLayoutMu.Unlock()
	return a.uiLayoutRaw
}

// watchUILayout hot-reloads the layout on mtime change — the
// watchConfig pattern, including file appearance and deletion (an
// operator deleting the file is a statement: back to frame-only).
// Dies with the app context. GOVERNED (quiesce, 2026-08-19): same 2s
// mtime poll, same ~43k wakeups/day while backgrounded — parked with
// its sibling; the foreground catch-up tick re-stats.
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
			var mt time.Time
			if fi, err := os.Stat(path); err == nil {
				mt = fi.ModTime()
			}
			if mt.Equal(last) {
				continue
			}
			last = mt
			if a.loadUILayout(false) && a.dashboard != nil {
				// The push half of "hot-reloaded" (§5): every connected
				// frame remounts without a refresh.
				a.dashboard.BroadcastLayout()
			}
		}
	}
}
