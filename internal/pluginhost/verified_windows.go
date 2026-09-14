//go:build windows

package pluginhost

import (
	"fmt"
	"os"
	"syscall"
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
type lockedImage struct {
	f    *os.File
	path string
}

// .
// .
// .
// .
// .
// .
// .
// .
func bindImage(path string) (VerifiedImage, error) {
	name, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return nil, fmt.Errorf("artifact path: %w", err)
	}
	h, err := syscall.CreateFile(name,
		syscall.GENERIC_READ,
		syscall.FILE_SHARE_READ,
		nil, syscall.OPEN_EXISTING, syscall.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		return nil, fmt.Errorf("open artifact without write/delete sharing: %w", err)
	}
	return &lockedImage{f: os.NewFile(uintptr(h), path), path: path}, nil
}

func (i *lockedImage) handle() *os.File { return i.f }

func (i *lockedImage) Argv() []string         { return []string{i.path} }
func (i *lockedImage) ExtraFiles() []*os.File { return nil }
func (i *lockedImage) Binding() string {
	return "share-locked (no write, delete or rename by anyone while this child runs)"
}
func (i *lockedImage) Close() error { return i.f.Close() }
