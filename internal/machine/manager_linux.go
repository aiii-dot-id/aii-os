//go:build linux && !android

package machine

import "net"

func managerListener(path string) (net.Listener, bool, error) { return systemdListener(path) }
