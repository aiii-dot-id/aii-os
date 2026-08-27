//go:build windows

// The Windows provider: a standalone daemon that owns its process —
// subprocesses, contained native children (job objects), self-replace
// (the rename dance), and the shell all work. The shell is Windows
// PowerShell (R79, 2026-08-27): the organ was honestly absent from
// 2026-08-18 until a real need picked the Windows shape — Beta 1
// journey 5 and Aster's signed gap report were that need. The tool
// declares the dialect; shell_windows.go owns the host facts.

package hostcap

func can(c Capability) Status {
	switch c {
	case Subprocess, NativeChild, SelfReplace, Shell:
		return Status{Available: true}
	}
	return Status{Reason: "unknown capability"}
}
