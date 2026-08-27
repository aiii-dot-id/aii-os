//go:build !windows

package app

import (
	"log"
	"os"
	"os/signal"
	"syscall"
)

// installReloadSignal wires SIGHUP -> config reload, the unix
// convention. The portable mechanism is the file watcher (reload.go);
// this is the same reload on a familiar trigger. No-op on windows
// (signal_reload_windows.go); harmless on mobile (app sandboxes never
// deliver HUP).
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
