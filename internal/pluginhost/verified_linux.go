//go:build linux

package pluginhost

import (
	"fmt"
	"os"
)

// .
// .
// .
// .
type fdImage struct {
	f *os.File
}

// .
// .
// .
func bindImage(path string) (VerifiedImage, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open artifact: %w", err)
	}
	return &fdImage{f: f}, nil
}

func (i *fdImage) handle() *os.File { return i.f }

func (i *fdImage) Argv() []string         { return []string{"/proc/self/fd/3"} }
func (i *fdImage) ExtraFiles() []*os.File { return []*os.File{i.f} }
func (i *fdImage) Binding() string        { return "descriptor-bound (the verified inode, not the path)" }
func (i *fdImage) Close() error           { return i.f.Close() }
