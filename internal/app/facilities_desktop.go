//go:build !android && !ios

package app

import (
	"fmt"
	"os"

	"github.com/aiii-dot-id/aii-os/internal/pluginhost"
)

func hostFacilities() []string {
	return []string{pluginhost.FacilityTransportLocal, pluginhost.FacilityOperatorPresenceFresh}
}

func (a *App) resolveWorkerBinary() (string, []string, error) {
	if named := a.configSnapshot().Plugins.WorkerBinary; named != "" {
		if _, err := os.Stat(named); err != nil {
			return "", nil, fmt.Errorf("plugins.worker_binary %q: %w", named, err)
		}
		return named, nil, nil
	}
	if harnessLane != nil {
		return harnessLane()
	}
	return defaultWorkerLane()
}

func defaultWorkerLane() (string, []string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", nil, fmt.Errorf("supervised lane: the daemon cannot resolve its own executable: %w", err)
	}
	return exe, []string{WorkerSubcommand}, nil
}
