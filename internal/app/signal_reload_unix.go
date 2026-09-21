//go:build !windows

package app

import (
	"github.com/aiii-dot-id/aii-os/internal/logsink"
	"os"
	"os/signal"
	"syscall"
)

// .
// .
// .
// .
// .
func (a *App) installReloadSignal() {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGHUP)
	a.runBackground(func() {
		defer signal.Stop(ch)
		for {
			select {
			case <-a.bgCtx.Done():
				return
			case <-ch:
				logsink.Info("config.start", "re-reading config")
				a.reloadConfig()
			}
		}
	})
}
