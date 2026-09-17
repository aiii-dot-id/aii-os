//go:build !windows

package supervisor

// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
func containProcess(pid int, rlimitASBytes uint64) (func() error, string, error) {
	return nil, "", nil
}
