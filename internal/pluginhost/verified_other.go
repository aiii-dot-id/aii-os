//go:build !linux && !darwin && !windows

package pluginhost

import (
	"fmt"
	"os"
)

// .
// .
// .
// .
// .
type unboundImage struct {
	f    *os.File
	path string
}

func bindImage(path string) (VerifiedImage, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open artifact: %w", err)
	}
	return &unboundImage{f: f, path: path}, nil
}

func (i *unboundImage) handle() *os.File { return i.f }

func (i *unboundImage) Argv() []string         { return []string{i.path} }
func (i *unboundImage) ExtraFiles() []*os.File { return nil }
func (i *unboundImage) Binding() string {
	return "path exec — NOT bound: this platform has no native lane"
}
func (i *unboundImage) Close() error { return i.f.Close() }
