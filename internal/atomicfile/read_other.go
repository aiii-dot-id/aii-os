//go:build !windows

package atomicfile

import "os"

func Open(path string) (*os.File, error) { return os.Open(path) }

func ReadFile(path string) ([]byte, error) { return os.ReadFile(path) }
