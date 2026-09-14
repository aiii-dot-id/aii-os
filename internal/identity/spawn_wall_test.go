package identity

import (
	"os"
	"strings"
	"testing"
)

// .
// .
// .
func TestASpawnOnALocalRouteGetsTheLocalWall(t *testing.T) {
	e := &Engine{}
	e.SetAgencyLimits(3, 3, 20, 600)
	e.SetLocalSpawnWall(1800)
	if got := e.spawnWall("anything"); got != 600 {
		t.Fatalf("no oracle installed: want the default 600, got %d", got)
	}
	e.SetRouteIsLocal(func(role string) bool { return role == "local-flash" })
	if got := e.spawnWall("local-flash"); got != 1800 {
		t.Fatalf("a local route got %d, want the local wall 1800", got)
	}
	if got := e.spawnWall("claude"); got != 600 {
		t.Fatalf("a remote route got %d, want the default 600", got)
	}
	e.SetLocalSpawnWall(0)
	if got := e.spawnWall("local-flash"); got != 600 {
		t.Fatalf("a local wall never configured must fall back to the default, got %d", got)
	}
}

// .
// .
// .
func TestTheEnqueueDerivesItsLeaseFromTheSpawnWall(t *testing.T) {
	b, err := os.ReadFile("work.go")
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	i := strings.Index(s, "wallSeconds := e.spawnWall(role)")
	j := strings.Index(s, "leaseMs := int64(wallSeconds+60)")
	if i < 0 || j < 0 || i > j {
		t.Fatalf("the enqueue no longer derives the lease from spawnWall's answer (spawnWall at %d, lease at %d)", i, j)
	}
	if strings.Contains(s, "_, wallSeconds := e.agencyLimits()") {
		t.Fatal("the enqueue reads the default wall from agencyLimits again — the route is ignored")
	}
}
