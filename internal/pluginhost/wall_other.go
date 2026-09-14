//go:build !windows

package pluginhost

import "github.com/aiii-dot-id/aii-os/internal/supervisor"

// .
// .
// .
func wallFor(string, []string) *supervisor.AppContainer { return nil }
