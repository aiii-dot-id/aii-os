//go:build android || ios

package app

// The mobile half of the host facility assembly (PLATFORM_SEAMS §3.2)
// and the supervised-lane refusal. The shells (Kotlin/Swift) host a
// mobile_app_host topology:
//
//   - sev_transport.local — the in-process binding exists by
//     construction;
//   - sev_operator_presence.fresh — the shell's SetForeground truth
//     OR'd with dashboard sessions, via the same operatorPresent seam;
//   - sev_foreground.lifecycle — the mobile binding's SetForeground
//     delivers real foreground/background transitions; this is the
//     one facility mobile has that desktop does not;
//   - audio/keystore stay unadvertised until their backends exist.
//
// The supervised exec lane does NOT exist here: iOS forbids exec
// outright, and Android's plugin story is bundled in-process T3
// (PLUGIN_FRAMEWORK §13 — the OS app sandbox is the wall). The worker
// resolution therefore answers empty unconditionally; a config naming
// one is a desktop setting that carries no meaning in a shell build.

import "github.com/aiii-dot-id/aii-os/internal/facility"

// hostFacilities assembles this build's advertised facility set.
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

// resolveWorkerBinary: no exec lane on mobile — in-process only.
func (a *App) resolveWorkerBinary() (string, error) { return "", nil }
