package version

import "runtime/debug"

func Commit() string {
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return "unknown"
	}
	return CommitOf(bi)
}

func CommitOf(bi *debug.BuildInfo) string {
	if bi == nil {
		return "unknown"
	}
	var commit, modified string
	for _, kv := range bi.Settings {
		switch kv.Key {
		case "vcs.revision":

			commit = kv.Value
			if len(commit) > 12 {
				commit = commit[:12]
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
