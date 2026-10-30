package app

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/dashboard"
	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
	"github.com/aiii-dot-id/aii-os/internal/plugincatalog"
	"github.com/aiii-dot-id/aii-os/internal/updates"
	"github.com/aiii-dot-id/aii-os/internal/version"
)

type stageProbe struct {
	once sync.Once
	why  string
}

func (p *stageProbe) refusal() string {
	p.once.Do(func() { p.why = updates.StageRefusal() })
	return p.why
}

var highestVerified = updates.HighestVerified

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

		CheckIntervalMinutes: updateIntervalSetting.n(a.configSnapshot()),
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
	view.Lists, view.ListsNote = snap.Lists, snap.ListsNote
	view.Stranded, view.StrandedNote = strandedView(snap)
	view.RunningWithdrawn, view.RunningUnknown = snap.RunningWithdrawn, snap.RunningUnknown

	view.PreviousKept = updates.PreviousKept()

	if seen, ok := highestVerified(); ok {
		view.HighestVersion = seen.Version
		if at, err := time.Parse(time.RFC3339, seen.PublishedAt); err == nil {
			view.HighestPublished = at.UTC().Format("2006-01-02")
		}
		if !seen.VerifiedAt.IsZero() {
			view.HighestVerified = seen.VerifiedAt.UTC().Format("2006-01-02")
		}
	}

	if cat := a.catalog.Current(); cat != nil && cat.Announced.Release != "" {
		view.AnnouncedVersion = cat.Announced.Release
		if at, err := time.Parse(time.RFC3339, cat.Announced.At); err == nil {
			view.AnnouncedOn = at.UTC().Format("2006-01-02")
		}
		if u := a.effectiveCatalogURL(); u != plugincatalog.DefaultCatalogURL {
			view.AnnouncedFrom = u
		}

		view.AnnouncedNotOffered, view.AnnouncedBehind = announcedAgainst(cat.Announced.Release, snap.CurrentVersion,
			snap.AvailableVersion, !snap.LastCheck.IsZero() && snap.LastError == "")
	}
	return view
}

func strandedView(snap updates.StateSnapshot) ([]dashboard.UpdateStranded, string) {
	if snap.AvailableVersion == "" {
		return nil, ""
	}
	var rows []dashboard.UpdateStranded
	for _, s := range snap.Stranded {
		rows = append(rows, dashboard.UpdateStranded{ID: s.ID, Version: s.Version, Reason: s.Reason})
	}
	return rows, snap.StrandedNote
}

const (
	strandedUnpublished = "The AII OS %s catalog is not published yet: updating to %s would leave every installed plugin (%d) without a version listed for it."
	strandedUnknown     = "Whether updating to %s leaves installed plugins without a version for it could not be told: %v."
)

func (a *App) strandedBy(ctx context.Context, offered string) ([]updates.StrandedPlugin, string) {
	line := plugincatalog.HostLine(offered)
	if line == "" || line == a.catalog.Line() {
		return nil, ""
	}
	a.pluginMu.Lock()
	installed := make(map[string]string, len(a.plugins))
	for _, p := range a.plugins {
		installed[p.ID] = p.Version
	}
	a.pluginMu.Unlock()
	if len(installed) == 0 {
		return nil, ""
	}
	cat, err := a.catalog.FetchLine(ctx, line)
	var unpublished *plugincatalog.NotPublishedError
	switch {
	case errors.As(err, &unpublished):
		return nil, fmt.Sprintf(strandedUnpublished, line, offered, len(installed))
	case err != nil:
		return nil, fmt.Sprintf(strandedUnknown, offered, err)
	}
	var out []updates.StrandedPlugin
	for _, s := range cat.StrandedBy(installed, offered, packagefmt.HostPlatform(), packagefmt.HostArch()) {
		out = append(out, updates.StrandedPlugin{ID: s.ID, Version: s.Version, Reason: s.Reason})
	}
	return out, ""
}

func packageView(snap updates.StateSnapshot) *dashboard.UpdatePackage {
	p := snap.Package
	if p.Version == "" || p.Version != snap.AvailableVersion {
		return nil
	}
	return &dashboard.UpdatePackage{Version: p.Version, Path: p.Path, Refusal: p.Refusal}
}

func announcedAgainst(announced, running, available string, checked bool) (notOffered, behind bool) {
	if !version.Valid(announced) || !version.Valid(running) {
		return false, false
	}
	offered := running
	if version.Valid(available) {
		offered = available
	}
	return checked && version.Compare(announced, offered) > 0, version.Compare(announced, running) < 0
}
