//go:build (darwin && !ios) || (linux && !android) || windows

package main

import (
	"net/url"
	"os/exec"
	"strings"

	"github.com/aiii-dot-id/aii-os/internal/consolewin"
)

func signInURL(aii, dir, plain string) (string, bool) {
	u, err := url.Parse(plain)
	if err != nil || u.Port() == "" {
		return plain, false
	}
	cmd := exec.Command(aii, "dashboard-link", "-dir", dir, "-port", u.Port())
	consolewin.Hide(cmd)
	out, err := cmd.Output()
	got := strings.TrimSpace(string(out))
	if err != nil || (got != plain && !strings.HasPrefix(got, plain+"/auth/link#")) {
		return plain, false
	}
	return got, true
}

func signedInOpener(aii, dir string, open func(string) error, linked *bool) func(string) error {
	return func(plain string) error {
		target, ok := signInURL(aii, dir, plain)
		*linked = ok
		return open(target)
	}
}

func tokenCommand(aii, dir string) string {
	line, _ := launcherShell.Command(aii, "dashboard-token", "-dir", dir)
	return line
}

func firstStartTokenNote(aii, dir string) string {
	where := terminalName
	if launcherShell.Name() != terminalName {
		where += " (" + launcherShell.Name() + ")"
	}
	return "The dashboard asks for its access token, once in each browser; that browser then stays signed in.\n\n" +
		"To read the token, run this in " + where + ":\n\n" +
		tokenCommand(aii, dir) + "\n\n" +
		"and paste what it prints into the page."
}
