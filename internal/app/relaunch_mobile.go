//go:build android || ios

package app

import (
	"os"

	"github.com/aiii-dot-id/aii-os/internal/logsink"
)

func relaunch() {
	logsink.Warn("boot.refusal", "the mobile app host cannot replace its own process; leaving for its shell to start the identity again")
	os.Exit(3)
}
