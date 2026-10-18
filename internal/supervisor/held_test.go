package supervisor

import (
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/logsink"
)

// HELD IS WHAT THE CHILD AND EVERYTHING BELOW IT HOLD. Admission counts a
// serving engine by what it has not yet taken, and what it has taken is
// this number: the memory of the supervised child and of every process
// below it, since an engine is a carrier and the worker it runs.
//
// The child here writes 96 MiB of its own and starts a child that writes
// 64 MiB. The measure must hold both, so it is at least 160 MiB, which the
// child alone is not; and it is not far above, so it is these processes'
// and nobody else's.
func TestHeldIsWhatTheChildAndEverythingBelowItHold(t *testing.T) {
	if _, ok := treeHeld(os.Getpid()); !ok {
		t.Skip("this platform has no measure of what a process holds")
	}
	const own, below = 96 << 20, 64 << 20
	lines := logsink.CaptureForTest(t)
	spec := childSpec("hold")
	spec.Argv = append(spec.Argv, strconv.Itoa(own), strconv.Itoa(below))
	s := mustStart(t, spec, nil)

	deadline := time.Now().Add(20 * time.Second)
	for !(strings.Contains(lines.String(), "holding bytes="+strconv.Itoa(own)) && strings.Contains(lines.String(), "holding bytes="+strconv.Itoa(below))) {
		if time.Now().After(deadline) {
			t.Fatalf("the two processes never said they hold their bytes: %s", lines.String())
		}
		time.Sleep(20 * time.Millisecond)
	}
	held, ok := s.Held()
	if !ok {
		t.Fatal("a live child's holding could not be measured")
	}
	if held < own+below {
		t.Fatalf("the child and the process below it hold at least %d MiB, and the measure says %d MiB: the process below is not counted", (own+below)>>20, held>>20)
	}
	if held > own+below+(96<<20) {
		t.Fatalf("two processes that wrote %d MiB are measured at %d MiB: something else is counted", (own+below)>>20, held>>20)
	}

	// And with no child there is nothing to measure.
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if held, ok := s.Held(); ok {
		t.Fatalf("a supervisor with no child measured %d bytes", held)
	}
}

// THE TREE IS THE ROOT AND WHAT DESCENDS FROM IT, and nothing beside it:
// not its parent, not a sibling, not a process that is not listed.
func TestDescendantsAreTheRootAndWhatIsBelowIt(t *testing.T) {
	parent := map[int]int{1: 0, 10: 1, 11: 10, 12: 10, 13: 12, 20: 1, 21: 20}
	got := map[int]bool{}
	for _, pid := range descendants(10, parent) {
		got[pid] = true
	}
	for _, want := range []int{10, 11, 12, 13} {
		if !got[want] {
			t.Fatalf("process %d is below 10 and is missing: %v", want, got)
		}
	}
	if len(got) != 4 {
		t.Fatalf("the tree of 10 is four processes, and holds %v", got)
	}
	if tree := descendants(99, parent); tree != nil {
		t.Fatalf("a root that is not listed has no tree: %v", tree)
	}
}
