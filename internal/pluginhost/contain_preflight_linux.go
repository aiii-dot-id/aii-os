//go:build linux

package pluginhost

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync/atomic"
	"time"
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
type ContainmentUnavailableError struct {
	// .
	Mechanism string
	// .
	// .
	Observed string
	// .
	// .
	// .
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

// .
// .
// .
const apparmorUsernsSwitch = "/proc/sys/kernel/apparmor_restrict_unprivileged_userns"

// .
// .
// .
const probeTimeout = 10 * time.Second

// .
// .
// .
// .
// .
// .
// .
// .
// .
const bwrapProfileRemedy = `This host confines unprivileged user namespaces (AppArmor), which bubblewrap
needs in order to build the sandbox. Running as root would hide the problem
rather than fix it, and the plugin must not be run without the sandbox.

To allow bubblewrap — and only bubblewrap — to create the namespace, an
administrator can install the profile Ubuntu ships for this purpose:

    sudo tee /etc/apparmor.d/bwrap >/dev/null <<'PROFILE'
    abi <abi/4.0>,
    include <tunables/global>

    profile bwrap /usr/bin/bwrap flags=(unconfined) {
      userns,
      include if exists <local/bwrap>
    }
    PROFILE
    sudo apparmor_parser -r /etc/apparmor.d/bwrap

This leaves bubblewrap unconfined and grants it the namespace permission;
what runs INSIDE the sandbox stays contained by it. It is a change to this
machine's security policy: make it deliberately, or not at all.`

// .
// .
// .
var namespaceProven atomic.Bool

// .
// .
func checkNamespace(ctx context.Context, bwrap string) error {
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
	probe, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	out, err := exec.CommandContext(probe, bwrap, "--unshare-all", "--ro-bind", "/", "/", truth).CombinedOutput()
	if err == nil {
		namespaceProven.Store(true)
		return nil
	}
	// .
	// .
	// .
	// .
	// .
	if cerr := ctx.Err(); cerr != nil {
		return fmt.Errorf("pluginhost: containment preflight interrupted: %w", cerr)
	}
	if probe.Err() != nil {
		return &ContainmentUnavailableError{Mechanism: "bubblewrap",
			Observed: fmt.Sprintf("the probe did not finish within %s, so whether a sandbox can be built here could not be decided", probeTimeout)}
	}
	return classifyContainmentFailure(strings.TrimSpace(string(out)), usernsRestricted())
}

// .
func usernsRestricted() bool {
	b, err := os.ReadFile(apparmorUsernsSwitch)
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(b)) == "1"
}

// .
// .
// .
// .
// .
func classifyContainmentFailure(observed string, restricted bool) *ContainmentUnavailableError {
	out := &ContainmentUnavailableError{Mechanism: "bubblewrap", Observed: observed}
	if observed == "" {
		out.Observed = "the probe failed without saying why"
	}
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	if restricted && usernsDenial(observed) {
		out.Remedy = bwrapProfileRemedy
	}
	return out
}

// .
// .
// .
func usernsDenial(observed string) bool {
	low := strings.ToLower(observed)
	if !strings.Contains(low, "operation not permitted") && !strings.Contains(low, "permission denied") {
		return false
	}
	for _, about := range []string{"loopback", "rtm_newaddr", "uid map", "uid_map", "gid map", "gid_map", "setgroups", "namespace", "unshare", "clone"} {
		if strings.Contains(low, about) {
			return true
		}
	}
	return false
}
