//go:build windows

package pluginhost

import "errors"

func cloneFile(string, string) error { return errors.ErrUnsupported }
