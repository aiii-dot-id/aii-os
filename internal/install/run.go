//go:build (darwin && !ios) || (linux && !android)

package install

import (
	"fmt"
	"os/exec"
	"strings"

	"github.com/aiii-dot-id/aii-os/internal/hostcap"
)

func run(name string, args ...string) (string, error) {
	if cap := hostcap.Can(hostcap.Subprocess); !cap.Available {
		return "", fmt.Errorf("subprocesses unavailable: %s", cap.Reason)
	}
	cmd := exec.Command(name, args...)
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}
