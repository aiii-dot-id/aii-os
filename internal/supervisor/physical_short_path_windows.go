package supervisor

import (
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/windows"
)

func physicalShortLaunchPath(path string) (string, error) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return "", err
	}
	b := make([]uint16, 32768)
	n, err := windows.GetShortPathName(p, &b[0], uint32(len(b)))
	if err != nil || n == 0 || n >= uint32(len(b)) {
		return path, nil
	}
	short := windows.UTF16ToString(b[:n])
	if len(short) >= len(path) {
		return path, nil
	}
	for _, pair := range [][2]string{{path, short}, {filepath.Dir(path), filepath.Dir(short)}} {
		a, e := os.Stat(pair[0])
		if e != nil {
			return "", e
		}
		z, e := os.Stat(pair[1])
		if e != nil {
			return "", e
		}
		if !os.SameFile(a, z) {
			return "", fmt.Errorf("short native launch path changed physical identity")
		}
	}
	return short, nil
}
