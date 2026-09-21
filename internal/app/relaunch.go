package app

import (
	"github.com/aiii-dot-id/aii-os/internal/logsink"
	"os"

	"github.com/aiii-dot-id/aii-os/internal/hostcap"
)

// .
// .
// .
// .
const exitRestart = 3

// .
// .
// .
// .
// .
// .
func relaunch() {
	if sr := hostcap.Can(hostcap.SelfReplace); !sr.Available {
		logsink.Warn("boot.refusal", "this host cannot replace its own process (%s); leaving with exit %d for its shell to start the identity again", sr.Reason, exitRestart)
		os.Exit(exitRestart)
	}
	logsink.Info("boot.end", "handing over to the binary at this executable's path")
	if err := reexecSelf(); err != nil {
		logsink.Error("boot.error", "hand-over failed (%v); leaving with exit %d for the service manager", err, exitRestart)
		os.Exit(exitRestart)
	}
}
