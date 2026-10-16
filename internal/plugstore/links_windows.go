//go:build windows

package plugstore

import (
	"os"

	"golang.org/x/sys/windows"
)

func Links(path string) (uint64, error) {
	f, err := openForCollect(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(windows.Handle(f.Fd()), &info); err != nil {
		return 0, &os.PathError{Op: "links", Path: path, Err: err}
	}
	return uint64(info.NumberOfLinks), nil
}
