//go:build linux

package pluginhost

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// A CONTAINED NATIVE CHILD HOLDS NO CAPABILITY. Under a host run as root,
// bubblewrap left the child every capability inside its namespaces, and
// what kept it from unmounting a mask was a profile the distribution
// ships, not anything the wall asked for. Run by an ordinary account the
// child holds none either way, so this bites where the suite runs as
// root, which is where the fault was.
func TestAContainedNativeChildHoldsNoCapability(t *testing.T) {
	if _, err := exec.LookPath("bwrap"); err != nil {
		t.Skip("bubblewrap is absent: containment refuses instead, which TestContainmentRefusesRatherThanRunningBare covers")
	}
	skipWhereTheSandboxCannotBeEstablished(t)

	probe := []string{"/bin/sh", "-c", "cat /proc/self/status && echo checked"}
	argv, wall, err := containArgv(context.Background(), probe, nil, nativeFiles{})
	if err != nil {
		t.Fatalf("containment refused on a host that can build it: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	out, err := walled(t, exec.CommandContext(ctx, argv[0], argv[1:]...), wall).CombinedOutput()
	if err != nil || !strings.Contains(string(out), "checked") {
		t.Fatalf("the probe did not run: %v\n%s", err, out)
	}
	seen := 0
	for _, line := range strings.Split(string(out), "\n") {
		name, value, ok := strings.Cut(line, ":")
		if !ok || (name != "CapPrm" && name != "CapEff" && name != "CapBnd") {
			continue
		}
		seen++
		if strings.Trim(strings.TrimSpace(value), "0") != "" {
			t.Errorf("a contained native child holds capabilities: %s is %s", name, strings.TrimSpace(value))
		}
	}
	if seen != 3 {
		t.Fatalf("the probe did not show the child's three capability sets:\n%s", out)
	}
}
