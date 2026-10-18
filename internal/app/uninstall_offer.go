package app

import (
	"errors"
	"strings"

	"github.com/aiii-dot-id/aii-os/internal/logsink"
	"github.com/aiii-dot-id/aii-os/internal/updates"
)

var uninstallProgram = updates.Program

var errUninstallNotOffered = errors.New("uninstall is offered from the dashboard only on macOS, for an identity that runs from the app (AII OS.app); run aii uninstall instead")

func uninstallBundle() string {
	if !uninstallFromApp {
		return ""
	}
	p, err := uninstallProgram()
	if err != nil || !strings.HasSuffix(p, ".app") {
		return ""
	}
	return p
}

func (a *App) openUninstallApp() error {
	bundle := uninstallBundle()
	if bundle == "" {
		return errUninstallNotOffered
	}
	logsink.Info("dashboard.decision", "the operator asked to uninstall AII OS; %s opens on this Mac to ask there", bundle)
	return openUninstall(bundle)
}
