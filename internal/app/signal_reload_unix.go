//go:build !windows

package app

import (
	"log"
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
				log.Printf("SIGHUP: re-reading config")
				a.reloadConfig()
			}
		}
	})
}
