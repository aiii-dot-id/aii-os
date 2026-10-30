//go:build linux || android

package pluginhost

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync/atomic"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/oswall"
	"github.com/aiii-dot-id/aii-os/internal/supervisor"
)

type ContainmentUnavailableError struct {
	Mechanism string

	Observed string

	Remedy string
}

func (e *ContainmentUnavailableError) Error() string {
	msg := "pluginhost: " + e.Mechanism + " is installed but cannot build a sandbox on this host, so native T3 plugins are not run (they are never run uncontained)"
	if e.Observed != "" {
		msg += ": " + e.Observed
	}
	if e.Remedy != "" {
		msg += "\n" + e.Remedy
	}
	return msg
}

var bwrapProfileRemedy = oswall.ProfileRemedy("the plugin")

var namespaceProven atomic.Bool

func checkNamespace(ctx context.Context, bwrap string, wait time.Duration) error {
	if namespaceProven.Load() {
		return nil
	}
	truth := "/bin/true"
	if _, err := os.Stat(truth); err != nil {
		truth = "/usr/bin/true"
		if _, err := os.Stat(truth); err != nil {
			return nil
		}
	}
	probe, cancel := context.WithTimeout(ctx, wait)
	defer cancel()

	var out []byte
	var err error
	for attempt := 0; attempt < supervisor.ContainmentBuildAttempts && probe.Err() == nil; attempt++ {
		out, err = exec.CommandContext(probe, bwrap, "--unshare-all", "--ro-bind", "/", "/", "--proc", "/proc", truth).CombinedOutput()
		if err == nil {
			namespaceProven.Store(true)
			return nil
		}
	}

	if cerr := ctx.Err(); cerr != nil {
		return fmt.Errorf("pluginhost: containment preflight interrupted: %w", cerr)
	}
	if probe.Err() != nil {
		return &ContainmentUnavailableError{Mechanism: "bubblewrap",
			Observed: fmt.Sprintf("the probe did not finish within %s, so whether a sandbox can be built here could not be decided", wait)}
	}
	return classifyContainmentFailure(strings.TrimSpace(string(out)), oswall.UsernsRestricted())
}

func classifyContainmentFailure(observed string, restricted bool) *ContainmentUnavailableError {
	out := &ContainmentUnavailableError{Mechanism: "bubblewrap", Observed: observed}
	if observed == "" {
		out.Observed = "the probe failed without saying why"
	}

	if restricted && oswall.UsernsDenial(observed) {
		out.Remedy = bwrapProfileRemedy
	}
	return out
}
