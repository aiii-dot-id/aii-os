//go:build (linux && !android) || (darwin && !ios)

// The desktop unix provider: a standalone daemon that owns its
// process. Everything is available here — this is the topology the
// codebase was born in, and the tag above is the discipline the
// review demanded: `linux` alone matches android and `darwin` alone
// matches ios (GOOS implication), which is exactly how the re-exec
// path captured two platforms that cannot run it.

package hostcap

func can(c Capability) Status {
	switch c {
	case Subprocess, Shell, NativeChild, SelfReplace:
		return Status{Available: true}
	}
	return Status{Reason: "unknown capability"}
}
