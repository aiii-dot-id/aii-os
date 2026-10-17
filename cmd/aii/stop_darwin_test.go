//go:build darwin && !ios

package main

import (
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/install"
)

func TestStopOnAnAgentWithNoProcessSucceeds(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("SUDO_USER", "")
	// Stop acts on the account's own launchd labels: the home is taken
	// for the account's (install.OwnAccount).
	t.Cleanup(install.AccountHomeForTest(home))
	if err := os.MkdirAll(filepath.Join(home, install.Dir, "identity-90"), 0o700); err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	fake := "#!/bin/sh\necho 'No process to signal.'\nexit 3\n"
	if err := os.WriteFile(filepath.Join(bin, "launchctl"), []byte(fake), 0o755); err != nil {
		t.Fatal(err)
	}
	// PATH is only the fake: the real launchctl is in /bin, and a fake that
	// went missing must not let this signal the operator's own agents.
	t.Setenv("PATH", bin)
	if code := runStop([]string{"identity-90"}, io.Discard, io.Discard); code != 0 {
		t.Fatalf("stopping a stopped identity exits %d, want 0", code)
	}
}
