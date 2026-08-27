//go:build !android && !ios

package app

// The desktop half of the host facility assembly (PLATFORM_SEAMS §3.2:
// "the app assembles the host's facility set per platform") and the
// supervised-lane worker resolution. Facility NAMES are C-owned
// (sev_manifest.h; internal/facility pins them); what a desktop build
// honestly provides today:
//
//   - sev_transport.local — framed-BBB stdio and the in-process
//     binding exist by construction wherever the daemon runs;
//   - sev_operator_presence.fresh — the dashboard's session presence
//     (sessionConns + grace window), read through the narrow
//     operatorPresent seam;
//   - NOT sev_foreground.lifecycle — no app lifecycle exists on a
//     desktop daemon to deliver;
//   - NOT sev_audio.raw_pcm, NOT sev_keystore.secret — those
//     facilities have no backends yet, and an advertisement without an
//     implementation would be a lie the variant selector then acts on.
//     The empty answer is the honest one.

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/aiii-dot-id/aii-os/internal/facility"
	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
)

// hostFacilities assembles this build's advertised facility set.
func (a *App) hostFacilities() (*facility.Set, error) {
	platform := packagefmt.HostPlatform()
	return facility.NewSet(
		facility.Facility{
			Name:     facility.TransportLocal,
			Provider: "bbb-stdio/in-process (" + platform + ")",
		},
		facility.Facility{
			Name:     facility.OperatorPresenceFresh,
			Provider: "dashboard-session (" + platform + ")",
			Live:     a.operatorPresent,
		},
	)
}

// workerBinaryName is the executable the daemon looks for beside
// itself when config names none.
const workerBinaryName = "aii-plugin-worker"

// resolveWorkerBinary resolves the supervised lane's worker
// executable. Operator-named paths must exist (a stated intent is
// honored or refused loudly, never silently downgraded); the unnamed
// default probes next to the daemon executable and an absence there
// simply keeps the in-process posture.
func (a *App) resolveWorkerBinary() (string, error) {
	if named := a.configSnapshot().Plugins.WorkerBinary; named != "" {
		if _, err := os.Stat(named); err != nil {
			return "", fmt.Errorf("plugins.worker_binary %q: %w", named, err)
		}
		return named, nil
	}
	exe, err := os.Executable()
	if err != nil {
		return "", nil // no self-path, no default probe — in-process
	}
	candidate := filepath.Join(filepath.Dir(exe), workerBinaryName)
	if _, err := os.Stat(candidate); err != nil {
		return "", nil
	}
	return candidate, nil
}
