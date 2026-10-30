package app

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/aiii-dot-id/aii-os/internal/atomicfile"
	"github.com/aiii-dot-id/aii-os/internal/dashboard"
	"github.com/aiii-dot-id/aii-os/internal/hold"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
	"github.com/aiii-dot-id/aii-os/internal/pluginfacility"
)

const heldFile = "held"

const nativeHoldWords = "Runs as a native program; signed for %s. Not started yet: press Start when you have read this."

const (
	startNeedsLevel    = "autoload"
	startNeedsOperator = "operator_start"
)

type pluginStartRefusal struct {
	ID          string
	Requirement string
	Reason      string
}

func (r *pluginStartRefusal) Error() string { return r.Reason }

func heldMark(slot string) bool {
	_, err := os.Lstat(filepath.Join(slot, heldFile))
	return !errors.Is(err, fs.ErrNotExist)
}

func writeHeldMark(slot string) (wrote bool, err error) {
	f, err := os.OpenFile(filepath.Join(slot, heldFile), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o640)
	if errors.Is(err, fs.ErrExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("the mark that holds a native plugin until Start could not be written: %w", err)
	}
	err = f.Close()
	if err == nil {
		err = atomicfile.SyncDir(slot)
	}
	if err != nil {
		return true, fmt.Errorf("the mark that holds a native plugin until Start could not be made durable: %w", err)
	}
	return true, nil
}

func (a *App) alreadyServes(ev pluginfacility.Evidence) bool {
	a.pluginMu.Lock()
	defer a.pluginMu.Unlock()
	meta, ok := a.activeMeta[ev.ID]
	if !ok || meta.owner == nil || meta.owner.Plugin == nil {
		return false
	}
	if packagefmt.NativeRuntime(meta.owner.Plugin.Runtime) {
		return true
	}
	return ev.PackageHash != "" && meta.owner.Hash == ev.PackageHash && meta.owner.ManifestHash == ev.ManifestHash
}

func (a *App) nativeHold(ev pluginfacility.Evidence) *pluginStartRefusal {
	if !ev.Native || ev.Package == "" || !heldMark(filepath.Dir(ev.Package)) || a.alreadyServes(ev) {
		return nil
	}
	signed := "nothing"
	if len(ev.Capabilities) > 0 {
		signed = strings.Join(ev.Capabilities, ", ")
	}
	return &pluginStartRefusal{ID: ev.ID, Requirement: startNeedsOperator, Reason: fmt.Sprintf(nativeHoldWords, signed)}
}

func (a *App) startRows() []dashboard.PluginPendingView {
	var kept []pluginfacility.Skip
	for _, sk := range a.pluginFacility().Skips() {
		if sk.Kind == pluginfacility.SkipPolicy {
			kept = append(kept, sk)
		}
	}
	if len(kept) == 0 {
		return nil
	}
	autoload := ""
	a.cfgMu.RLock()
	if a.cfg != nil {
		autoload = a.cfg.Plugins.Autoload
	}
	a.cfgMu.RUnlock()
	minTier, _, ok := autoloadTier(autoload)
	if !ok {
		minTier = packagefmt.TierT1
	}
	var out []dashboard.PluginPendingView
	for _, sk := range kept {
		r := a.startRefusal(autoload, minTier, pluginfacility.Evidence{ID: sk.ID, Version: sk.Version, Package: sk.Package,
			Tier: sk.Tier, Native: sk.Native, Capabilities: sk.Capabilities})
		switch {
		case r == nil:
			out = append(out, dashboard.PluginPendingView{ID: sk.ID, Version: sk.Version, Phase: lifeStarting,
				Summary: lifecycleText(pluginLifecycle{phase: lifeStarting})})
		case r.Requirement == startNeedsOperator:
			out = append(out, dashboard.PluginPendingView{ID: sk.ID, Version: sk.Version, Phase: lifeUnstarted, Summary: r.Reason})
		}
	}
	return out
}

func (a *App) safeBootHeld() map[string]pendingMark {
	reason, safe := a.SafeMode()
	if !safe {
		return nil
	}
	marks, _ := filepath.Glob(filepath.Join("plugins", "*", heldFile))
	out := make(map[string]pendingMark, len(marks))
	for _, m := range marks {
		out[filepath.Base(filepath.Dir(m))] = pendingMark{phase: lifeHeld, text: safeHoldSentence(reason)}
	}
	return out
}

func (a *App) StartPlugin(id string) error {
	if !plainName(id) {
		return fmt.Errorf("plugins: refusing to start %q — not a plain plugin id", id)
	}
	if h, held := a.Hold(); held {
		why := safeHoldSentence(h.Reason)
		if h.Waiting {
			why = "nothing new is activated " + hold.While + " (" + h.Reason + ")"
		}
		return fmt.Errorf("plugins: %s is not started: %s", id, why)
	}
	slot := filepath.Join("plugins", id)
	if _, err := os.Stat(slot); err != nil {
		return fmt.Errorf("plugins: %s is not installed", id)
	}
	switch err := os.Remove(filepath.Join(slot, heldFile)); {
	case err == nil:

		if serr := atomicfile.SyncDir(slot); serr != nil {
			logsink.Warn("plugins.error", "plugin %s: its Start is not yet durable (%v); after a power loss it would wait for Start again", id, serr)
		}

		a.pluginFacility().Retry(id)
		logsink.Info("plugins.decision", "starting %s at the operator's word", id)
	case !errors.Is(err, fs.ErrNotExist):
		return fmt.Errorf("plugins: %s is not started: the mark that holds it could not be removed: %w", id, err)
	}
	a.pokePluginSweep()
	return nil
}
