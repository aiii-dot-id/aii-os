package app

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/aiii-dot-id/aii-os/internal/dashboard"
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
// .
// .
func (a *App) watchProjects() {
	if a.bgCtx == nil {
		return
	}
	root := a.projectsRoot()
	if root == "" {
		return
	}
	last := a.projectsSnapshotMap()
	marker := "ready"
	a.projectsLast.Store(&marker)
	w := fsdir.New(a.bgCtx, a.gate, root, fsdir.Options{Heartbeat: a.watcherInterval(), Depth: 1})
	for {
		select {
		case <-a.bgCtx.Done():
			return
		case <-w.C:
			now := a.projectsSnapshotMap()
			moved, listChanged := projectsDiffMaps(last, now)
			if len(moved) == 0 && !listChanged {
				continue
			}
			last = now
			a.projectsWatcherDispatch(moved, listChanged)
		}
	}
}

// .
// .
// .
// .
func (a *App) projectsWatcherDispatch(moved []string, listChanged bool) {
	if a.projectsPush != nil {
		for _, id := range moved {
			a.projectsPush(id, a.getProjectWorkspaceForPush(id))
		}
	}
	if listChanged && a.projectsListPush != nil {
		a.projectsListPush()
	}
}

const manifestFile = "project.json"

// .
// .
// .
// .
// .
// .
// .
// .
type slugSnap struct {
	manifest string
	digest   string
}

// .
// .
// .
func projectsDiffMaps(oldM, newM map[string]slugSnap) (moved []string, listChanged bool) {
	for slug, o := range oldM {
		n, ok := newM[slug]
		if !ok {
			moved = append(moved, slug)
			listChanged = true
			continue
		}
		if o.digest != n.digest {
			moved = append(moved, slug)
		}
		if o.manifest != n.manifest {
			listChanged = true
		}
	}
	for slug := range newM {
		if _, ok := oldM[slug]; !ok {
			moved = append(moved, slug)
			listChanged = true
		}
	}
	sort.Strings(moved)
	return moved, listChanged
}

// .
func (a *App) projectsRoot() string {
	if a.projects == nil {
		return ""
	}
	return a.projects.Root()
}

// .
// .
// .
// .
func (a *App) projectsSnapshotMap() map[string]slugSnap {
	root := a.projectsRoot()
	if root == "" {
		return nil
	}
	out := map[string]slugSnap{}
	slugDirs, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	for _, sd := range slugDirs {
		if !sd.IsDir() {
			continue
		}
		var files []string
		manifest := ""
		entries, err := os.ReadDir(filepath.Join(root, sd.Name()))
		// .
		// .
		// .
		if err == nil {
			for _, f := range entries {
				if f.IsDir() {
					continue
				}
				info, ierr := f.Info()
				if ierr != nil {
					continue
				}
				line := fmt.Sprintf("%s %d %d", f.Name(), info.ModTime().UnixNano(), info.Size())
				files = append(files, line)
				if f.Name() == manifestFile {
					manifest = line
				}
			}
		}
		sort.Strings(files)
		out[sd.Name()] = slugSnap{manifest: manifest, digest: strings.Join(files, "\n")}
	}
	return out
}

// .
// .
// .
func (a *App) getProjectWorkspaceForPush(id string) *dashboard.WorkspaceState {
	ws, err := a.getProjectWorkspace(id)
	if err != nil {
		return nil
	}
	return ws
}
