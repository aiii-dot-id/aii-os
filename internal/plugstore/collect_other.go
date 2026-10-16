//go:build (linux && !android) || (darwin && !ios)

package plugstore

import "os"

func openForCollect(path string) (*os.File, error) { return os.Open(path) }

func Busy(err error) error { return err }
