//go:build (darwin && !ios) || (linux && !android)

package main

import (
	"errors"
	"fmt"
	"net"

	"github.com/aiii-dot-id/aii-os/internal/install"
)

type asker func(question, yes, no string) (answer, asked bool)

var errPortKept = errors.New("the dashboard port was kept")

func movePort(root, slotDir string, port int, ask asker, write func(port int) error, free func(port int) bool) (int, error) {
	const held = "is already in use by another program, and this identity is not running"
	next, err := install.NextFreePort(root, port, free)
	if err != nil {
		return 0, fmt.Errorf("port %d %s; %v. Close the program using port %d and open AII OS again.", port, held, err, port)
	}
	url := fmt.Sprintf("http://127.0.0.1:%d", next)
	question := fmt.Sprintf("Port %d %s.\n\nUse port %d instead? The dashboard's address becomes %s — bookmarks of the old one will not reach it — and a browser keeps its dashboard sign-in per address, so if the dashboard asks for its access token, you sign in once more there.\n\nNot now changes nothing.",
		port, held, next, url)
	yes, asked := ask(question, fmt.Sprintf("Use port %d", next), "Not now")
	switch {
	case !asked:
		return 0, fmt.Errorf("port %d %s, and this desktop has no way to ask whether to move it. Port %d is free: to move the dashboard there, run\n\n  aii dashboard-port -dir %q %d\n\nand open AII OS again. Its address becomes %s, and a browser signs in again there if the dashboard asks for its access token.",
			port, held, next, slotDir, next, url)
	case !yes:
		return 0, fmt.Errorf("%w: port %d %s; nothing was changed", errPortKept, port, held)
	}
	if err := write(next); err != nil {
		return 0, fmt.Errorf("port %d %s, and moving the dashboard to port %d failed, so nothing was started: %w", port, held, next, err)
	}
	return next, nil
}

func portFree(port int) bool {
	if serving(port) {
		return false
	}
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return false
	}
	ln.Close()
	return true
}
