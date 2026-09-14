//go:build darwin

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
type pathImage struct {
	f    *os.File
	path string
}

func bindImage(path string) (VerifiedImage, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open artifact: %w", err)
	}
	return &pathImage{f: f, path: path}, nil
}

// .
// .
// .
func (i *pathImage) handle() *os.File { return i.f }

func (i *pathImage) Argv() []string         { return []string{i.path} }
func (i *pathImage) ExtraFiles() []*os.File { return nil }
func (i *pathImage) Binding() string {
	return "path exec — NOT bound: darwin has no fexecve, so same-user replacement between verify and exec is not prevented"
}
func (i *pathImage) Close() error { return i.f.Close() }
