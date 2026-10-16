package app

import (
	"errors"
	"sync"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/dashboard"
	"github.com/aiii-dot-id/aii-os/internal/updates"
)

type stageProbe struct {
	once sync.Once
	why  string
}

func (p *stageProbe) refusal() string {
	p.once.Do(func() { p.why = updates.StageRefusal() })
	return p.why
}

var errUpdatesUnavailable = errors.New("updates are not available in this process")

func (a *App) checkForUpdateNow() (*dashboard.UpdateState, error) {
	if a.updateChecker == nil {
		return nil, errUpdatesUnavailable
	}
	if a.bgCtx == nil {
		return nil, errors.New("application lifecycle is unavailable")
	}

	err := a.updateChecker.CheckNow(a.bgCtx, func() { a.broadcastWhenOwned() })
	return a.updateStateView(), err
}

func (a *App) broadcastWhenOwned() bool {
	return a.runBackground(func() {
		if a.dashboard != nil {
			a.dashboard.BroadcastStatus()
		}
	})
}

func (a *App) updateStateView() *dashboard.UpdateState {
	if a.updateChecker == nil {
		return nil
	}
	snap := a.updateChecker.State().Snapshot(Current())
	view := &dashboard.UpdateState{
		CurrentVersion: snap.CurrentVersion,
		Checking:       snap.Checking,
		Enabled:        a.updateChecker.Armed(),
		Automatic:      a.configSnapshot().Updates.Automatic,
	}
	view.StageRefusal = a.stage.refusal()

	if v := snap.AvailableVersion; v != "" || snap.InstalledVersion != "" {
		if v == "" {
			v = snap.InstalledVersion
		}
		repo := a.configSnapshot().Updates.Repo
		if repo == "" {
			repo = updates.DefaultRepo
		}
		view.ReleaseURL = "https://github.com/" + repo + "/releases/tag/v" + v
	}
	if snap.AvailableVersion != "" {
		view.AvailableVersion = snap.AvailableVersion
		view.Package = packageView(snap)
	}
	if snap.InstalledVersion != "" {
		view.InstalledVersion = snap.InstalledVersion
		view.NeedsRestart = snap.NeedsRestart
	}
	if snap.LastError != "" {
		view.Error = snap.LastError
	}
	if !snap.LastCheck.IsZero() {
		view.CheckedAt = snap.LastCheck.Format(time.RFC3339)
	}
	return view
}

func packageView(snap updates.StateSnapshot) *dashboard.UpdatePackage {
	p := snap.Package
	if p.Version == "" || p.Version != snap.AvailableVersion {
		return nil
	}
	return &dashboard.UpdatePackage{Version: p.Version, Path: p.Path, Refusal: p.Refusal}
}
