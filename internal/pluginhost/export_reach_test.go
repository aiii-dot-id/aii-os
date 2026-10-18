//go:build linux || darwin

package pluginhost

import (
	"context"

	"github.com/aiii-dot-id/aii-os/internal/firewall"
	"github.com/aiii-dot-id/aii-os/internal/supervisor"
)

// What the external tests (package pluginhost_test) reach of the wall
// tests' own: an external test may import a package that imports this
// one, as the machine service does.
var (
	RequireWallForTest  = requireWall
	RunWallProbeForTest = runWallProbe
	RunContainedForTest = runContained
	ShortDirForTest     = shortDir
)

// FakechildForTest is the probe binary TestMain built.
func FakechildForTest() string { return fakechildBin }

// ContainForTest walls argv as a native child is walled, with these
// places masked.
func ContainForTest(ctx context.Context, argv []string, places []*firewall.Rule) ([]string, supervisor.Containment, error) {
	return containArgv(ctx, argv, nil, nativeFiles{places: places})
}
