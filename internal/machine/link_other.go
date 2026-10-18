//go:build !((linux && !android) || (darwin && !ios) || windows)

package machine

import (
	"context"
	"errors"
	"net"
	"runtime"
)

const Supported = false

var errUnsupported = errors.New("there is no machine service on " + runtime.GOOS)

func LinkPath(string) (string, error) { return "", errUnsupported }

func openLink(string) (net.Listener, bool, error) { return nil, false, errUnsupported }

func removeLink(string) error { return nil }

func dialLink(context.Context, string, string) (net.Conn, error) { return nil, errUnsupported }
