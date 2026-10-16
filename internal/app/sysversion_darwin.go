//go:build darwin && !ios

package app

import "golang.org/x/sys/unix"

func readSystemVersion() string {
	v, err := unix.Sysctl("kern.osproductversion")
	if err != nil {
		return ""
	}
	return v
}
