package tools

import (
	"context"
	"os/exec"
	"strings"
	"testing"
)

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
func TestShellReachesInstalledTooling(t *testing.T) {
	// .
	// .
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
		map[string]interface{}{"command": probe + " --version"})
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
	// .
	// .
	// .
	// .
	// .
	if strings.Contains(out, "exit status") {
		t.Fatalf("%s is installed on this host, but the identity's shell could not run it:\n%s\n"+
			"the shell's PATH must carry the machine's tools, or this platform is a lesser seat than the others",
			probe, out)
	}
}
