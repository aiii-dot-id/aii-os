//go:build (linux && !android) || (darwin && !ios)

// .
// .
// .
// .
// .
// .

package hostcap

func can(c Capability) Status {
	switch c {
	case Subprocess, Shell, NativeChild, SelfReplace:
		return Status{Available: true}
	}
	return Status{Reason: "unknown capability"}
}
