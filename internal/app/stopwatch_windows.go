//go:build windows

package app

import (
	"log"
	"os"

	"golang.org/x/sys/windows"

	"github.com/aiii-dot-id/aii-os/internal/install"
)

// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
func watchStopRequest() <-chan struct{} {
	dir, err := os.Getwd()
	if err != nil {
		log.Printf("stop channel unavailable (cannot resolve working directory): %v", err)
		return nil
	}
	var h windows.Handle
	for _, name := range install.StopChannelNames(dir) {
		n, err := windows.UTF16PtrFromString(name)
		if err != nil {
			continue
		}
		// .
		// .
		// .
		hh, err := windows.CreateEvent(nil, 1, 0, n)
		if err != nil && err != windows.ERROR_ALREADY_EXISTS {
			continue
		}
		h = hh
		break
	}
	if h == 0 {
		// .
		// .
		log.Printf("WARNING: no stop channel could be opened — `aii stop` will not reach this identity")
		return nil
	}
	ch := make(chan struct{})
	go func() {
		windows.WaitForSingleObject(h, windows.INFINITE)
		close(ch)
	}()
	return ch
}
