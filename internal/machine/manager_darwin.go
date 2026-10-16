//go:build darwin && !ios

package machine

import (
	"errors"
	"net"
	"os"
	"syscall"
)

const launchdSocketName = "Link"

func managerListener(path string) (net.Listener, bool, error) {
	fds, err := launchActivateSocket(launchdSocketName)
	if errors.Is(err, syscall.ESRCH) || errors.Is(err, syscall.ENOENT) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if len(fds) == 0 {
		return nil, false, nil
	}
	for _, fd := range fds[1:] {
		syscall.Close(fd)
	}
	syscall.CloseOnExec(fds[0])
	f := os.NewFile(uintptr(fds[0]), "link")
	defer f.Close()
	return adoptListener(f, path, false)
}
