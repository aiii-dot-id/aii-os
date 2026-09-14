package app

import (
	"runtime/debug"

	"github.com/aiii-dot-id/aii-os/internal/version"
)

// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
var Version = ""

// .
// .
// .
// .
// .
// .
func BuildIdentity() string {
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return "unknown"
	}
	var commit, modified string
	for _, kv := range bi.Settings {
		switch kv.Key {
		case "vcs.revision":
			// .
			// .
			if len(kv.Value) >= 12 {
				commit = kv.Value[:12]
			} else {
				commit = kv.Value
			}
		case "vcs.modified":
			if kv.Value == "true" {
				modified = " (dirty)"
			}
		}
	}
	if commit == "" {
		return "unknown"
	}
	return commit + modified
}

// .
// .
func VersionString() string {
	return Current()
}

// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
func Current() string {
	if Version != "" {
		return Version
	}
	return version.Authored()
}
