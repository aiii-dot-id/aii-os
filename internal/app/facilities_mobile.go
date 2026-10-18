//go:build android || ios

package app

import "github.com/aiii-dot-id/aii-os/internal/pluginhost"

func hostFacilities() []string {
	return []string{pluginhost.FacilityTransportLocal, pluginhost.FacilityOperatorPresenceFresh, pluginhost.FacilityForegroundLifecycle}
}

func (a *App) resolveWorkerBinary() (string, []string, error) { return "", nil, nil }
