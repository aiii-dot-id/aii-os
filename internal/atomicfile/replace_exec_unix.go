//go:build !windows

package atomicfile

import "os"

// ReplaceExecutable publishes srcPath at dstPath where dstPath may be
// the RUNNING executable image. On unix a rename over the running
// image is legal — the inode keeps the old program alive until exit —
// so this is a plain rename. The Windows half needs the rename-away
// dance and lives beside this file. Extracted so core code never
// branches on GOOS for this (five-platform law): the swap carried the
// dance inline and the rollback then missed it entirely, leaving
// boot-health rollback unable to restore on Windows at all
// (review F1/F5, 2026-08-26).
func ReplaceExecutable(srcPath, dstPath string) error {
	return os.Rename(srcPath, dstPath)
}
