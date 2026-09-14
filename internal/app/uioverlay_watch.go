package app

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/aiii-dot-id/aii-os/internal/fsdir"
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
// .
// .
// .
// .
func (a *App) watchUIOverlay() {
	if a.bgCtx == nil {
		return
	}
	// .
	// .
	// .
	// .
	// .
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
			// .
			// .
			// .
			// .
			// .
			// .
			// .
			// .
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

// .
// .
// .
// .
// .
// .
// .
// .
// .
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
			out = append(out, p)
		}
	}
	for p := range oldLines {
		if _, ok := newLines[p]; !ok {
			out = append(out, p)
		}
	}
	sort.Strings(out)
	return out
}

// .
// .
// .
// .
// .
// .
func (a *App) overlaySnapshot() string {
	dir := a.uiOverlayDir()
	var names []string
	names, err := readServable(dir, "")
	if err != nil {
		return ""
	}
	sort.Strings(names)
	return strings.Join(names, "\n")
}

// .
// .
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
				continue
			}
			sub, err := readServable(filepath.Join(dir, e.Name()), name)
			if err != nil {
				continue
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
