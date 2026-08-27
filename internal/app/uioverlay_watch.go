package app

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/aiii-dot-id/aii-os/internal/fsdir"
)

// overlayLast records the last snapshot the watcher broadcast — what
// the screen last saw. Written by watchUIOverlay only; read by the
// watcher test as the loop's observable state (the dashboard twin,
// TestBroadcastOverlayOverWebSocket, owns socket delivery).

// watchUIOverlay hot-reloads the frame overlay directory (W3,
// UI_REFORM gap 3): an edit to <data dir>/ui/ reaches open screens
// without an F5. The watchUILayout pattern exactly — app-owned mtime
// poll, quiesced ticker, dies with the app context, parks while
// backgrounded.
//
// The watcher's only job is to make the SCREEN know when overlay bytes
// moved: it fans BroadcastOverlayChanged, the live invalidation. The
// audit readback (BroadcastOverlay) stays with serving decisions and
// is never an invalidation trigger — one message serving both masters
// was the P1 loop (2026-08-25). Resolution itself re-runs per request
// at serve time; nothing is cached here.
//
// An absent directory is the normal case and stays silent. An empty
// one also stays silent: it overrides nothing, so the resolver cannot
// resolve anything differently, and two snapshots equal must mean
// that. The first SERVABLE FILE landing is the author's "I am
// overriding now".
func (a *App) watchUIOverlay() {
	if a.bgCtx == nil {
		return
	}
	// Publish a pointer to a value that is never written again:
	// storing &last would alias the loop's mutable comparison copy,
	// and every later `last = now` would rewrite published memory
	// under readers (the race gate's catch: the pointer was atomic,
	// the pointee was not immutable).
	init := a.overlaySnapshot()
	last := init
	a.overlayLast.Store(&init)
	w := fsdir.New(a.bgCtx, a.gate, a.uiOverlayDir(), fsdir.Options{Heartbeat: a.watcherInterval()})
	for {
		select {
		case <-a.bgCtx.Done():
			return
		case <-w.C:
			now := a.overlaySnapshot()
			if now == last {
				continue
			}
			// The path diff is what moved: the union of both
			// snapshots, both directions. A path gone from the
			// new snapshot is a deletion — it must reach the
			// client as a path, or the browser cannot know what
			// to fall back from (P2, 2026-08-25: a deleted CSS
			// stayed visually active because the push carried a
			// fresh token with no paths, and a no-path push is
			// a no-op on the client).
			paths := overlayDiff(last, now)
			token := a.overlayToken.Add(1)
			last = now
			a.overlayLast.Store(&now)
			if a.dashboard != nil {
				a.dashboard.BroadcastOverlayChanged(token, paths)
			}
		}
	}
}

// overlayDiff returns the servable paths that moved between two
// snapshots: added, edited, or deleted. The UNION of both snapshots
// is the candidate set — a path present only in the old digest is a
// deletion, and a deletion must reach the client as a path or the
// browser has nothing to fall back from (P2, 2026-08-25: fresh token,
// empty paths, no-op client — a deleted CSS stayed visually active).
// Two forms count as a move: the line changed (added/edited), or the
// line vanished (deleted). The caller's monotonic token orders the
// pushes; the paths name the subjects.
func overlayDiff(oldSnap, newSnap string) []string {
	if oldSnap == "" && newSnap == "" {
		return nil
	}
	lines := func(snap string) map[string]string {
		m := make(map[string]string)
		for _, l := range strings.Split(snap, "\n") {
			if l == "" {
				continue
			}
			f := strings.Fields(l)
			if len(f) > 0 {
				m[f[0]] = l
			}
		}
		return m
	}
	oldLines := lines(oldSnap)
	newLines := lines(newSnap)
	var out []string
	for p, nl := range newLines {
		if oldLines[p] != nl {
			out = append(out, p) // added or edited
		}
	}
	for p := range oldLines {
		if _, ok := newLines[p]; !ok {
			out = append(out, p) // deleted
		}
	}
	sort.Strings(out)
	return out
}

// overlaySnapshot digests the overlay directory into one comparable
// string: path, modtime and size of every servable file, sorted, one
// level of recursion for views/*. Two snapshots equal means nothing
// the resolver could resolve differently. Non-servable extensions are
// ignored — the allowlist is the resolver's, and the watcher must not
// fire on a .png drop that can never serve.
func (a *App) overlaySnapshot() string {
	dir := a.uiOverlayDir()
	var names []string
	names, err := readServable(dir, "")
	if err != nil {
		return "" // absent dir = normal; unreadable = fail-closed
	}
	sort.Strings(names)
	return strings.Join(names, "\n")
}

// readServable lists servable overlay files under root, prefixing sub
// names with their parent ("/views/panel.js"), one level deep.
func readServable(dir, prefix string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		name := prefix + "/" + e.Name()
		if e.IsDir() {
			if prefix != "" {
				continue // one level only: views/x.js, never a/b/c
			}
			sub, err := readServable(filepath.Join(dir, e.Name()), name)
			if err != nil {
				continue // an unreadable subdir is not a fault
			}
			out = append(out, sub...)
			continue
		}
		switch strings.ToLower(filepath.Ext(e.Name())) {
		case ".html", ".js", ".css":
		default:
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		out = append(out, fmt.Sprintf("%s %d %d", name, info.ModTime().UnixNano(), info.Size()))
	}
	return out, nil
}
