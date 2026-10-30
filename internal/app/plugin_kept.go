package app

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/aiii-dot-id/aii-os/internal/atomicfile"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
	"github.com/aiii-dot-id/aii-os/internal/pluginfacility"
)

const (
	keptPrefix = ".kept-"
	keptSep    = "+"
)

func keptPath(id, base string) string {
	return filepath.Join("plugins", keptPrefix+id+keptSep+base)
}

func parseKept(name string) (id, base string, ok bool) {
	rest, found := strings.CutPrefix(name, keptPrefix)
	if !found {
		return "", "", false
	}
	id, base, found = strings.Cut(rest, keptSep)
	if !found || !plainName(id) || !plainName(base) || !strings.HasSuffix(base, ".aiiospkg") {
		return "", "", false
	}
	return id, base, true
}

const (
	keptAtInstallWords = "plugin %s: the release that serves, %s, is kept beside the slots until the update serves"

	keptRemovedWords = "plugin %s: %s serves, so the release it replaced is no longer kept (%s removed)"

	keptBackWords = "plugin %s: nothing serves, so the release that served before the update, %s, is back in its slot"

	keptGoneWords = "plugin %s: it is no longer installed, so the release kept for it, %s, is removed"

	keptFailedWords = "plugin %s: the kept release %s could not be judged: %v"
)

func (a *App) servingPackage(slot string) string {
	for _, v := range a.pluginFacility().Snapshot().Instances {
		if v.Dir != slot {
			continue
		}
		for _, act := range v.Activations {
			if act.Role == pluginfacility.RoleActive {
				return act.Package
			}
		}
	}
	return ""
}

func setAside(id, slot, serving string) (kept string, err error) {
	if filepath.Dir(serving) != slot {
		return "", nil
	}
	if _, err := os.Lstat(serving); err != nil {
		return "", nil
	}
	kept = keptPath(id, filepath.Base(serving))
	if err := atomicfile.Rename(serving, kept); err != nil {
		return "", err
	}
	if err := removeKept(id, kept); err != nil {
		logsink.Warn("plugins.error", keptFailedWords, id, filepath.Base(kept), err)
	}
	for _, d := range []string{slot, "plugins"} {
		if err := atomicfile.SyncDir(d); err != nil {
			logsink.Warn("plugins.error", keptFailedWords, id, filepath.Base(kept), err)
		}
	}
	return kept, nil
}

func removeKept(id, except string) error {
	all, _ := filepath.Glob(keptPath(id, "*"))
	var errs []error
	for _, k := range all {
		if k == except {
			continue
		}
		if err := os.Remove(k); err != nil && !errors.Is(err, fs.ErrNotExist) {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (a *App) judgeKeptReleases() {
	kept, _ := filepath.Glob(filepath.Join("plugins", keptPrefix+"*"))
	if len(kept) == 0 {
		return
	}
	a.installMu.Lock()
	defer a.installMu.Unlock()
	snap := a.pluginFacility().Snapshot()
	for _, k := range kept {
		a.judgeKept(k, snap)
	}
}

func (a *App) judgeKept(k string, snap pluginfacility.Snapshot) {
	id, base, ok := parseKept(filepath.Base(k))
	if !ok {
		return
	}
	if _, err := os.Lstat(k); err != nil {
		return
	}
	slot := filepath.Join("plugins", id)
	st, err := os.Stat(slot)
	if errors.Is(err, fs.ErrNotExist) {
		if err := os.Remove(k); err != nil && !errors.Is(err, fs.ErrNotExist) {
			logsink.Warn("plugins.error", keptFailedWords, id, filepath.Base(k), err)
			return
		}
		syncKept(id, k, "plugins")
		logsink.Info("plugins.decision", keptGoneWords, id, base)
		return
	}
	if err != nil || !st.IsDir() {
		return
	}
	pkgs, _ := filepath.Glob(filepath.Join(slot, "*.aiiospkg"))
	if len(pkgs) > 1 {
		return
	}
	current := ""
	if len(pkgs) == 1 {
		current = pkgs[0]
	}
	serving, admitted, inFlight, refused := false, false, false, false
	for _, v := range snap.Instances {
		if v.Dir != slot {
			continue
		}
		for _, act := range v.Activations {
			switch act.Role {
			case pluginfacility.RoleActive:
				serving = true
				admitted = admitted || (current != "" && act.Package == current)
			case pluginfacility.RoleStarting, pluginfacility.RoleCandidate:
				inFlight = true
			}
		}
		refused = refused || (current != "" && v.Package == current && v.RefusedForGood)
	}
	for _, sk := range snap.Skips {
		refused = refused || (current != "" && sk.Kind == pluginfacility.SkipUnverified && sk.Package == current)
	}
	switch {
	case admitted:
		if err := os.Remove(k); err != nil && !errors.Is(err, fs.ErrNotExist) {
			logsink.Warn("plugins.error", keptFailedWords, id, filepath.Base(k), err)
			return
		}
		syncKept(id, k, "plugins")
		logsink.Info("plugins.decision", keptRemovedWords, id, filepath.Base(current), base)
	case serving || inFlight:

	case current == "" || refused:
		back := filepath.Join(slot, base)

		a.slotsMu.Lock()
		if current != "" {
			if err := os.Remove(current); err != nil && !errors.Is(err, fs.ErrNotExist) {
				a.slotsMu.Unlock()
				logsink.Warn("plugins.error", keptFailedWords, id, filepath.Base(k), err)
				return
			}
		}
		err := atomicfile.Rename(k, back)
		a.slotsMu.Unlock()
		if err != nil {
			logsink.Warn("plugins.error", keptFailedWords, id, filepath.Base(k), err)
			return
		}
		syncKept(id, k, slot, "plugins")
		logsink.Info("plugins.decision", keptBackWords, id, base)
		a.pokePluginSweep()
	}
}

func syncKept(id, k string, dirs ...string) {
	for _, d := range dirs {
		if err := atomicfile.SyncDir(d); err != nil {
			logsink.Warn("plugins.error", keptFailedWords, id, filepath.Base(k), err)
		}
	}
}
