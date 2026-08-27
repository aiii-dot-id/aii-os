package app

import "runtime/debug"

// Version is the platform version string, injected at build time via
// ldflags from the VERSION file at the repo root:
//
//	go build -ldflags "-X github.com/aiii-dot-id/aii-os/internal/app.Version=0.1.0"
//
// Empty = a dev build with no ldflags injection. The VERSION file is
// the act of authorship; git tags trigger the release workflow but
// never determine the version alone (versioning ruling 2026-08-21).
// ReadBuildInfo() VCS data (commit hash, dirty flag) is available as
// a traceability supplement via runtime/debug — automatic, Go 1.18+,
// never load-bearing for version comparison.
var Version = ""

// BuildIdentity returns the traceability supplement the version comment
// always promised: the VCS commit and dirty state of the running binary,
// via ReadBuildInfo. This is the "which binary booted" line that the
// 2026-08-21 emergency-swap forensics lacked — with it, every boot log
// answers binary identity forever. Honest when absent: a build without
// VCS data (go run, some CI) reports "unknown", never a fabricated hash.
func BuildIdentity() string {
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return "unknown"
	}
	var commit, modified string
	for _, kv := range bi.Settings {
		switch kv.Key {
		case "vcs.revision":
			// Full hash truncated to 12 — enough to identify a commit,
			// short enough to keep the log line readable.
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

// VersionString returns the version for operator-facing display. A dev
// build (empty Version) reports "dev" — honest, not fabricated.
func VersionString() string {
	if Version == "" {
		return "dev"
	}
	return Version
}
