//go:build windows

package machine

import (
	"context"
	"sync"
	"time"

	"golang.org/x/sys/windows"

	"github.com/aiii-dot-id/aii-os/internal/install"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
)

const RelaunchesService = true

func watchStop(ctx context.Context, dir string, wg *sync.WaitGroup) <-chan struct{} {
	var h windows.Handle
	for _, name := range install.StopChannelNames(dir) {
		n, err := windows.UTF16PtrFromString(name)
		if err != nil {
			continue
		}
		hh, err := windows.CreateEvent(nil, 1, 0, n)
		if err != nil && err != windows.ERROR_ALREADY_EXISTS {
			continue
		}
		h = hh
		break
	}
	if h == 0 {
		logsink.Warn("machine.error", "no stop channel could be opened for %s: unregistering will not reach this service", dir)
		return nil
	}
	ch := make(chan struct{})
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer windows.CloseHandle(h)
		for ctx.Err() == nil {
			ev, err := windows.WaitForSingleObject(h, uint32((250 * time.Millisecond).Milliseconds()))
			if err != nil {
				return
			}
			if ev == windows.WAIT_OBJECT_0 {
				close(ch)

				<-ctx.Done()
				return
			}
		}
	}()
	return ch
}

func AccountRelaunch() func() error { return install.StartRegisteredMachine }

func AccountSuccessor(string) func() error { return install.StartRegisteredMachine }
