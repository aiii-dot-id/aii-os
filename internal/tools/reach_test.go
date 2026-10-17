package tools

import (
	"context"
	"os/exec"
	"strings"
	"testing"
)

func TestShellReachesInstalledTooling(t *testing.T) {
	// Probe with something the host ACTUALLY has, and skip rather than
	// assert a machine we did not provision.
	var probe string
	for _, bin := range []string{"git", "node", "python3", "python", "go"} {
		if _, err := exec.LookPath(bin); err == nil {
			probe = bin
			break
		}
	}
	if probe == "" {
		t.Skip("no probe tool installed on this host")
	}

	r := NewRegistry(t.TempDir(), nil, Timeouts{})
	res, err := r.Execute(context.Background(), "shell",
		map[string]any{"command": probe + " --version"})
	if err != nil {
		t.Fatalf("shell dispatch: %v", err)
	}
	if res.Error != "" {
		t.Fatalf("%s is installed on this host, but the identity's shell cannot reach it: %s", probe, res.Error)
	}
	out := strings.TrimSpace(res.Output)
	if out == "" {
		t.Fatalf("%s ran through the shell but produced nothing", probe)
	}
	// A command the shell could not find EXITS NON-ZERO, and this tool
	// reports that in the OUTPUT — result.Error stays empty for anything
	// that actually ran. Checking Error alone made this test pass
	// against a shell that could not find the tool at all, which is the
	// precise regression it exists to catch.
	if strings.Contains(out, "exit status") {
		t.Fatalf("%s is installed on this host, but the identity's shell could not run it:\n%s\n"+
			"the shell's PATH must carry the machine's tools, or this platform is a lesser seat than the others",
			probe, out)
	}
}
