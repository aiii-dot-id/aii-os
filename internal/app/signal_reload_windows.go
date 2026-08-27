//go:build windows

package app

// installReloadSignal is a no-op on windows: there is no SIGHUP. The
// file watcher (reload.go) is the live-reload mechanism there — and on
// every other platform too; HUP is just the unix-flavored trigger.
func (a *App) installReloadSignal() {}
