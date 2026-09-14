//go:build !linux && !darwin && !windows

package pluginhost

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
// .
// .
// .
// .
// .
// .
func containArgv(argv []string) ([]string, string, error) {
	return argv, "no argv-level containment on this platform (see sandbox_other.go)", nil
}
