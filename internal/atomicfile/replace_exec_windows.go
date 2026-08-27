//go:build windows

package atomicfile

import (
	"fmt"
	"os"
)

// ReplaceExecutable publishes srcPath at dstPath where dstPath may be
// the RUNNING executable image. Windows will not replace or delete a
// running image, but it WILL rename it: move the target aside, move
// the new file in. The displaced image stays beside the binary as
// .old — removable only after its process exits, so boot-time cleanup
// owns it (updates.checkRollbackAt), never this call.
func ReplaceExecutable(srcPath, dstPath string) error {
	oldPath := dstPath + ".old"
	_ = os.Remove(oldPath) // stale .old from an earlier swap; best-effort
	if err := os.Rename(dstPath, oldPath); err != nil {
		return fmt.Errorf("rename running image aside: %w", err)
	}
	if err := os.Rename(srcPath, dstPath); err != nil {
		// Put the original back — a failed publish must not leave
		// nothing at the binary's path.
		if rerr := os.Rename(oldPath, dstPath); rerr != nil {
			return fmt.Errorf("publish failed (%v) AND restore failed (%v) — the binary is at %s", err, rerr, oldPath)
		}
		return fmt.Errorf("publish new image: %w", err)
	}
	return nil
}
