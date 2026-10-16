//go:build !windows

package app

import (
	"os"
	"syscall"
	"time"
)

func processEnded(pid int, bound time.Duration) (ended, later bool) {
	deadline := time.Now().Add(bound)
	for {
		p, err := os.FindProcess(pid)
		if err != nil || p.Signal(syscall.Signal(0)) != nil {
			return true, false
		}
		if time.Now().After(deadline) {
			return false, false
		}
		time.Sleep(handoverLook)
	}
}
