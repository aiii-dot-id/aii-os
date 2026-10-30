//go:build !windows && !(linux && !android)

package app

import (
	"fmt"
	"testing"
)

// leftJobGroup is the process group of the job a command left: the shell's
// own pid, the first line the command printed, since the shell leads the
// group it was started in and numbers processes as this host does.
func leftJobGroup(t *testing.T, output, _ string) int {
	t.Helper()
	var pgid int
	if _, err := fmt.Sscanf(output, "%d\n", &pgid); err != nil {
		t.Fatalf("the shell's own pid is the first line of %q: %v", output, err)
	}
	return pgid
}
