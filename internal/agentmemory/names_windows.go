//go:build windows

package agentmemory

import (
	"os"
	"syscall"
)

const openNonblock = 0

func names(f *os.File) (uint64, error) {
	var d syscall.ByHandleFileInformation
	if err := syscall.GetFileInformationByHandle(syscall.Handle(f.Fd()), &d); err != nil {
		return 0, err
	}
	return uint64(d.NumberOfLinks), nil
}
