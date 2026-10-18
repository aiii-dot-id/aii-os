//go:build (darwin && !ios) || (linux && !android) || windows

package main

import (
	"fmt"
	"net"
	"time"
)

func serving(port int) bool {
	c, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 300*time.Millisecond)
	if err != nil {
		return false
	}
	c.Close()
	return true
}
