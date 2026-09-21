package app

import (
	"errors"

	"github.com/aiii-dot-id/aii-os/internal/dashboard"
)

// .
// .
// .
// .
// .
// .
// .
func (a *App) checkForUpdateNow() (*dashboard.UpdateState, error) {
	if a.updateChecker == nil {
		return nil, errors.New("updates are not available in this process")
	}
	if a.bgCtx == nil {
		return nil, errors.New("application lifecycle is unavailable")
	}
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	err := a.updateChecker.CheckNow(a.bgCtx, func() { a.broadcastWhenOwned() })
	return a.updateStateView(), err
}

// .
// .
// .
func (a *App) broadcastWhenOwned() bool {
	return a.runBackground(func() {
		if a.dashboard != nil {
			a.dashboard.BroadcastStatus()
		}
	})
}
