//go:build darwin && !ios

package pluginhost

import "golang.org/x/sys/unix"

func cloneFile(src, dst string) error {
	return unix.Clonefile(src, dst, unix.CLONE_NOFOLLOW|unix.CLONE_NOOWNERCOPY)
}
