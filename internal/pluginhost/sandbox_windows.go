//go:build windows

package pluginhost

import (
	"strings"

	"github.com/aiii-dot-id/aii-os/internal/supervisor"
)

// .
// .
// .
// .
// .
func containArgv(argv []string) ([]string, string, error) {
	return argv, "contained at process creation: the AppContainer and the job object (supervisor/appcontainer_windows.go)", nil
}

// .
// .
// .
func wallFor(pluginID string, grants []string) *supervisor.AppContainer {
	return &supervisor.AppContainer{Profile: wallProfileName(pluginID), GrantRead: grants}
}

// .
// .
// .
func wallProfileName(pluginID string) string {
	var b strings.Builder
	b.WriteString("aiios.")
	for _, r := range pluginID {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '-':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
		if b.Len() >= 60 {
			break
		}
	}
	return b.String()
}
