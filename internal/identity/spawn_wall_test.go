package identity

import (
	"os"
	"strings"
	"testing"
)

// A spawn's wall is its route's: the app's port answers it for the role
// (Ports.RouteLimits, which gives a local model the local wall); with no
// port, or a port that names no wall, the wall SetAgencyLimits gave.
func TestASpawnOnALocalRouteGetsTheLocalWall(t *testing.T) {
	e := &Engine{}
	e.SetAgencyLimits(3, 3, 20, 600)
	if got := e.spawnLimits("anything").WallSeconds; got != 600 {
		t.Fatalf("no port installed: want the default 600, got %d", got)
	}
	e.routeLimits = func(role string) SpawnLimits {
		if role == "local-flash" {
			return SpawnLimits{WallSeconds: 1800}
		}
		return SpawnLimits{WallSeconds: 600}
	}
	if got := e.spawnLimits("local-flash").WallSeconds; got != 1800 {
		t.Fatalf("a local route got %d, want the local wall 1800", got)
	}
	if got := e.spawnLimits("claude").WallSeconds; got != 600 {
		t.Fatalf("a remote route got %d, want the default 600", got)
	}
	e.routeLimits = func(string) SpawnLimits { return SpawnLimits{} }
	if got := e.spawnLimits("local-flash").WallSeconds; got != 600 {
		t.Fatalf("a port that names no wall must fall back to the default, got %d", got)
	}
}

// The lease is derived from the wall the request carries. The enqueue
// must take the wall from the route's limits and derive the lease from
// THAT — a lease derived from a different wall expires under a live run.
func TestTheEnqueueDerivesItsLeaseFromTheSpawnWall(t *testing.T) {
	b, err := os.ReadFile("work.go")
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	i := strings.Index(s, "wallSeconds := spawn.WallSeconds")
	j := strings.Index(s, "leaseMs := int64(wallSeconds+60)")
	if i < 0 || j < 0 || i > j || !strings.Contains(s, "spawn := e.spawnLimits(role)") {
		t.Fatalf("the enqueue no longer derives the lease from the route's wall (wall at %d, lease at %d)", i, j)
	}
	if strings.Contains(s, "_, wallSeconds := e.agencyLimits()") {
		t.Fatal("the enqueue reads the default wall from agencyLimits again — the route is ignored")
	}
}
