//go:build android || ios

package app

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

import "github.com/aiii-dot-id/aii-os/internal/facility"

// .
func (a *App) hostFacilities() (*facility.Set, error) {
	return facility.NewSet(
		facility.Facility{
			Name:     facility.TransportLocal,
			Provider: "bbb-in-process (mobile shell)",
		},
		facility.Facility{
			Name:     facility.OperatorPresenceFresh,
			Provider: "shell-foreground+dashboard-session (mobile shell)",
			Live:     a.operatorPresent,
		},
		facility.Facility{
			Name:     facility.ForegroundLifecycle,
			Provider: "mobile-binding SetForeground",
		},
	)
}

// .
// .
func (a *App) resolveWorkerBinary() (string, []string, error) { return "", nil, nil }
