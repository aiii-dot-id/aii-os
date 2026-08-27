//go:build windows

package supervisor

import "fmt"

// The native-T3 address-space envelope on Windows rides the CONTAINMENT
// job object (contain_windows.go), not a second job: Windows refuses to
// assign a process to a second job when the first carries UI
// restrictions — the documented nested-job constraint this file's first
// version tripped over (D19, Sev 2026-08-26; the refused assignment
// grounded every enveloped plugin). One mechanism, one owner: the
// containment call receives the spec's ceiling and folds it into the
// single job's extended limits before this runs.
func applyAddressSpaceLimit(pid int, bytes uint64) (string, error) {
	return fmt.Sprintf("memory envelope %d bytes rides the containment job object (one job — nested UI+memory jobs are refused by Windows)", bytes), nil
}
