package tools

import "os"

type ShellWallSettings struct {
	Devices []string

	StartSeconds int
}

var shellAccountRoot = func() bool { return os.Geteuid() == 0 }

func ShellWallAsAccountForTest() (restore func()) {
	prev := shellAccountRoot
	shellAccountRoot = func() bool { return false }
	return func() { shellAccountRoot = prev }
}
