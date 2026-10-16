//go:build windows

package app

import (
	"fmt"

	"golang.org/x/sys/windows"
)

func readSystemVersion() string {
	v := windows.RtlGetVersion()
	return fmt.Sprintf("%d.%d.%d", v.MajorVersion, v.MinorVersion, v.BuildNumber)
}
