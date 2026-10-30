package pluginhost

import (
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/supervisor"
)

// THE BOUNDS' DEFAULTS ARE THE NUMBERS THE CODE CARRIED: nothing changes
// until the operator sets one.
func TestTheChildBoundsDefaultsAreTheNumbersTheCodeCarried(t *testing.T) {
	var none ChildBounds
	if none.StopBudget() != 5*time.Second || none.HandOverBudget() != 30*time.Second || none.SessionControlBound() != 5*time.Second {
		t.Fatalf("unset bounds: stop %s, hand-over %s, control %s; want 5s, 30s, 5s", none.StopBudget(), none.HandOverBudget(), none.SessionControlBound())
	}
	if (*Options)(nil).bounds() != none {
		t.Fatal("no options are not the defaults")
	}
}

// A RETIREMENT ALLOWS THE OPERATOR'S STOP. The facility asks its runtime
// what a retirement must allow the child it stops (RetireBounder): the stop
// in force now and the kill's reap after it.
func TestARetirementIsToldTheOperatorsStop(t *testing.T) {
	opts := &Options{Bounds: ChildBounds{Stop: 45 * time.Second}}
	if got, want := NewRuntime(RuntimeWires{Options: func() *Options { return opts }}).RetireBound(), 45*time.Second+supervisor.KillReapBound(); got != want {
		t.Fatalf("a stop of 45s: the runtime tells a retirement %s, want %s", got, want)
	}
	if got, want := NewRuntime(RuntimeWires{}).RetireBound(), DefaultStopBudget+supervisor.KillReapBound(); got != want {
		t.Fatalf("no options: the runtime tells a retirement %s, want the default stop and the reap, %s", got, want)
	}
}
