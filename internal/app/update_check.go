package app

import (
	"errors"
	"sync"

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
