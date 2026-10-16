//go:build android || ios || (!linux && !windows && !darwin)

package supervisor

func treeHeld(int) (int64, bool) { return 0, false }
