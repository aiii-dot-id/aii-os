//go:build windows

// .
// .
// .
// .
// .
// .
// .

package hostcap

func can(c Capability) Status {
	switch c {
	case Subprocess, NativeChild, SelfReplace, Shell:
		return Status{Available: true}
	}
	return Status{Reason: "unknown capability"}
}
