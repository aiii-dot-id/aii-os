//go:build (linux && !android) || (darwin && !ios)

package machine

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"strconv"
	"syscall"
	"time"
)

var ErrManaged = errors.New("the account's service manager holds this link; start the service through it")

func openLink(path string) (net.Listener, bool, error) {
	if ln, ok, err := managerListener(path); ok || err != nil {
		return ln, ok, err
	}
	if err := prepareLinkDir(path); err != nil {
		return nil, false, err
	}
	if _, err := os.Lstat(path); err == nil {
		if c, derr := net.DialTimeout("unix", path, 200*time.Millisecond); derr == nil {
			c.Close()
			return nil, false, fmt.Errorf("%w (%s)", ErrManaged, path)
		}
		if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return nil, false, fmt.Errorf("remove the previous link %s: %w", path, err)
		}
	}
	ln, err := net.Listen("unix", path)
	if err != nil {
		return nil, false, err
	}
	return ln, false, nil
}

func removeLink(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

func dialLink(ctx context.Context, path string, _ string) (net.Conn, error) {
	d := net.Dialer{Timeout: dialTimeout}
	return d.DialContext(ctx, "unix", path)
}

func systemdListener(path string) (net.Listener, bool, error) {
	pid, _ := strconv.Atoi(os.Getenv("LISTEN_PID"))
	n, _ := strconv.Atoi(os.Getenv("LISTEN_FDS"))
	for _, v := range []string{"LISTEN_PID", "LISTEN_FDS", "LISTEN_FDNAMES"} {
		os.Unsetenv(v)
	}
	if pid != os.Getpid() || n < 1 {
		return nil, false, nil
	}

	for fd := 3; fd < 3+n; fd++ {
		syscall.CloseOnExec(fd)
	}
	f := os.NewFile(3, "link")
	defer f.Close()
	return adoptListener(f, path, true)
}

func adoptListener(f *os.File, path string, atPath bool) (net.Listener, bool, error) {
	ln, err := net.FileListener(f)
	if err != nil {
		return nil, false, fmt.Errorf("the service manager's link is not a listener: %w", err)
	}
	ul, ok := ln.(*net.UnixListener)
	if !ok || (atPath && ul.Addr().String() != path) {
		ln.Close()
		return nil, false, fmt.Errorf("the service manager passed %v, not the link at %s", ln.Addr(), path)
	}
	return ln, true, nil
}
