//go:build !linux && !darwin && !windows

package pluginhost

import "context"

import "github.com/aiii-dot-id/aii-os/internal/supervisor"

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
func containArgv(_ context.Context, argv []string, _ *AcceleratorProfile) ([]string, supervisor.Containment, error) {
	return argv, supervisor.Containment{Description: "no argv-level containment on this platform (see sandbox_other.go)"}, nil
}
