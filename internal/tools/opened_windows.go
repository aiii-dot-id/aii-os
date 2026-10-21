//go:build windows

package tools

import (
	"os"
	"strings"

	"golang.org/x/sys/windows"
)

func openedAt(by *os.File) (string, error) {
	h := windows.Handle(by.Fd())
	buf := make([]uint16, windows.MAX_PATH)
	for {
		n, err := windows.GetFinalPathNameByHandle(h, &buf[0], uint32(len(buf)), 0)
		if err != nil {
			return "", err
		}
		if int(n) < len(buf) {
			final := windows.UTF16ToString(buf[:n])
			switch {
			case strings.HasPrefix(final, `\\?\UNC\`):
				return `\\` + final[len(`\\?\UNC\`):], nil
			case strings.HasPrefix(final, `\\?\`):
				return final[len(`\\?\`):], nil
			}
			return final, nil
		}
		buf = make([]uint16, n+1)
	}
}
