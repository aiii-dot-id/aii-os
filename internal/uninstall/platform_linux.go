//go:build linux && !android

package uninstall

import "github.com/aiii-dot-id/aii-os/internal/install"

func processes() ([]Process, error) { return nil, nil }

func sweep() ([]string, error) { return nil, nil }

func platformStays() []Stay {
	return []Stay{
		{What: StayLinger, Why: "registering a slot enabled lingering for this account, so its identity ran while you were logged out; other services may rely on it. Turn it off yourself if nothing else does: loginctl disable-linger"},
		{What: StayJournal, Why: "what the identities and the machine service wrote to the journal stays until the journal rotates it away"},
	}
}

func programPath(exe string) string { return exe }

func removeInstruction(string) string {
	return "remove the package: sudo apt remove aii-os (apt purge also removes every account's plugin stores and machine service directories, and never an identity)"
}

func shortcuts([]install.InstalledProgram) ([]string, []Stay) { return nil, nil }
