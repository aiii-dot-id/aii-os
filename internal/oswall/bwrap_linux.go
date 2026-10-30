//go:build linux || android

package oswall

import (
	"errors"
	"os"
	"os/exec"
	"strings"
	"time"
)

var ErrNotInstalled = errors.New("bubblewrap is not installed")

func Program() (string, error) {
	path, err := exec.LookPath("bwrap")
	if err != nil {
		return "", ErrNotInstalled
	}
	return path, nil
}

const usernsSwitch = "/proc/sys/kernel/apparmor_restrict_unprivileged_userns"

func UsernsRestricted() bool {
	b, err := os.ReadFile(usernsSwitch)
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(b)) == "1"
}

func UsernsDenial(observed string) bool {
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

func ProfileRemedy(refused string) string {
	return `This host confines unprivileged user namespaces (AppArmor), which bubblewrap
needs in order to build the sandbox. Running as root would hide the problem
rather than fix it, and ` + refused + ` must not be run without the sandbox.

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
}

func StartWait(seconds int) time.Duration {
	switch {
	case seconds <= 0:
		seconds = DefaultStartSeconds
	case seconds > MaxStartSeconds:
		seconds = MaxStartSeconds
	}
	return time.Duration(seconds) * time.Second
}
