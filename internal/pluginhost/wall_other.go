//go:build !windows

package pluginhost

import (
	"github.com/aiii-dot-id/aii-os/internal/plugstore"
	"github.com/aiii-dot-id/aii-os/internal/supervisor"
)

func wallFor(string, []string, []plugstore.Entry) *supervisor.AppContainer { return nil }
