package tools

import (
	"path/filepath"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/firewall"
)

// A DOOR THAT CHANGES NOTHING IS GIVEN A STEADY LOOK; A DOOR THAT WRITES IS
// NOT. A steady look asks each kept place's path once for the call
// (firewall.Policy.SteadyLook), which a walk needs (a two-level listing of
// a large drive took nineteen minutes asking every place again for every
// entry: an identity's log, 2026-11-08); a call that writes may make a
// directory on a place's path between two questions, so its look asks
// again. Read by what each look asks: a second question of a steady look
// asks no place's path again, and one of a writing door's look does.
func TestADoorThatChangesNothingIsGivenASteadyLook(t *testing.T) {
	g := newOffRig(t)
	again := func(look *firewall.Policy) int64 {
		look.Check("ls", filepath.Join(g.work, "proj", "main.go"))
		before := firewall.PlaceStatsForTest()
		look.Check("ls", filepath.Join(g.work, "proj", "other.go"))
		return firewall.PlaceStatsForTest() - before
	}
	for _, c := range []struct {
		door   Tool
		steady bool
	}{
		{&LsTool{}, true}, {&GrepTool{}, true}, {&ReadTool{}, true},
		{&WriteTool{}, false}, {&EditTool{}, false},
	} {
		n := again(g.r.lookFor(c.door))
		if c.steady && n != 0 {
			t.Errorf("%T: its look asked the places %d times again at a second question; a door that changes nothing asks them once a call", c.door, n)
		}
		if !c.steady && n == 0 {
			t.Errorf("%T: its look asked no place again at a second question; a door that writes must ask them at each", c.door)
		}
	}
}
