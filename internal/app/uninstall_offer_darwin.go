//go:build darwin && !ios

package app

import (
	"fmt"
	"os/exec"

	"github.com/aiii-dot-id/aii-os/internal/hostcap"
)

const uninstallFromApp = true

var openUninstall = func(bundle string) error {
	if cap := hostcap.Can(hostcap.Subprocess); !cap.Available {
		return fmt.Errorf("cannot open the app here: %s", cap.Reason)
	}
	if out, err := exec.Command("/usr/bin/open", "-n", "-a", bundle, "--args", "--uninstall").CombinedOutput(); err != nil {
		return fmt.Errorf("open %s to uninstall: %v: %s", bundle, err, out)
	}
	return nil
}
