package app

import (
	"reflect"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/dashboard"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
	"github.com/aiii-dot-id/aii-os/internal/pluginhost"
)

func (a *App) reconcileLateModels() {
	late := a.lateModels
	if late == nil {
		return
	}
	if _, safe := a.SafeMode(); safe {

		late.Keep(map[string]bool{})
		return
	}
	cfg := a.configSnapshot()
	a.pluginMu.Lock()
	active := append([]*pluginhost.ActivePlugin(nil), a.plugins...)
	a.pluginMu.Unlock()
	running := map[string]bool{}
	for _, ap := range active {
		if len(ap.ModelSettings()) == 0 || ap.ModelsDir == "" {
			continue
		}
		running[ap.ID] = true
		want, err := ap.ModelsWanted(cfg.Plugins.Settings[ap.ID])
		if err != nil {
			logsink.Warn("plugins.refusal", "plugin %s: the models its settings need cannot be worked out (%v) — nothing is fetched for it", ap.ID, err)
			late.Forget(ap.ID)
			continue
		}
		absent := ap.ModelsAbsent(want)
		if len(absent) == 0 {
			late.Forget(ap.ID)
			continue
		}

		if acq := a.acquirer(); acq != nil {
			if st, busy := acq.Status(ap.ID); busy && st.Phase != pluginhost.PhaseReady {
				continue
			}
		}
		late.Want(ap.LateMaterial(absent))
	}

	late.Keep(running)
}

func (a *App) lateModelsReady(id string) {
	if late := a.lateModels; late != nil {
		late.Forget(id)
	}
	logsink.Info("plugins.decision", "plugin %s: the models its settings came to need are present and verified — no restart was needed", id)
	a.pluginsChanged()
}

func pluginSettingsMoved(before, after *Config) bool {
	return !reflect.DeepEqual(before.Plugins.Settings, after.Plugins.Settings)
}

func (a *App) settingModelsView(c *Config, ap *pluginhost.ActivePlugin, key string) *dashboard.SettingModelsView {
	if !ap.ModelSettings()[key] || ap.ModelsDir == "" {
		return nil
	}
	want, err := ap.ModelsWanted(c.Plugins.Settings[ap.ID])
	if err != nil {
		return &dashboard.SettingModelsView{State: dashboard.SettingModelsAbsent, LastError: err.Error()}
	}

	var mine []pluginhost.ModelDecl
	for _, d := range want {
		if d.When != nil && d.When.Setting == key {
			mine = append(mine, d)
		}
	}
	view := &dashboard.SettingModelsView{State: dashboard.SettingModelsReady}
	for _, st := range ap.ModelStatuses(mine) {
		view.FilesTotal++
		view.BytesTotal += st.Size
		if st.Present {
			view.FilesPresent++
			view.BytesPresent += st.Size
		} else {
			view.BytesPresent += st.Partial
		}
	}
	if view.FilesPresent == view.FilesTotal {
		return view
	}
	view.State = dashboard.SettingModelsAbsent
	if late := a.lateModels; late != nil {
		if st, ok := late.Status(ap.ID); ok {
			switch st.Phase {
			case pluginhost.PhaseFetching:
				view.State = dashboard.SettingModelsFetching
			case pluginhost.PhaseWaiting:
				view.State, view.LastError = dashboard.SettingModelsWaiting, st.LastError
				if !st.RetryAt.IsZero() {
					view.RetryAt = st.RetryAt.UTC().Format(time.RFC3339)
				}

				if st.Remedy != "" {
					view.State, view.Remedy = dashboard.SettingModelsStopped, st.Remedy
				}
			}
		}
	}
	return view
}
