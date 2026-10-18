package pluginfacility

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// busyRuntime is the shared runtime whose activation is busy with a call:
// its stop answers that the retirement waits, for as many asks as it is
// told, and is the shared runtime's own after that.
type busyRuntime struct {
	*sharedRuntime
	busy atomic.Int64 // asks still to be answered "busy"; negative is for ever
	// pendingOnly answers those asks as pending for another reason than a
	// call in flight.
	pendingOnly bool
	asked       atomic.Int64
}

func (b *busyRuntime) Stop(ctx context.Context, r Running) (Retirement, error) {
	b.asked.Add(1)
	if n := b.busy.Load(); n != 0 {
		if n > 0 {
			b.busy.Add(-1)
		}
		return Retirement{Established: false, Residue: []string{"a call is in flight"}, Busy: !b.pendingOnly}, nil
	}
	return b.sharedRuntime.Stop(ctx, r)
}

// serve brings one package to serving on a facility of the runtime.
func serveOne(t *testing.T, rt Runtime, retire time.Duration) *Facility {
	t.Helper()
	f := New(Config{Runtime: rt, Capacity: ampleCapacity{}, RetireTimeout: retire})
	ctx, cancel := context.WithCancel(context.Background())
	f.Attach(ctx)
	t.Cleanup(func() { cancel(); f.Close() })
	f.Observe(obs("one.aiiospkg"), Policy{Revision: 1})
	auditWaitFacility(t, "the package to serve", func() bool { return viewOf(f, idOf("one.aiiospkg")).State == StateActive })
	return f
}

// A PLUGIN BUSY WITH A CALL WHEN THE HOST LEAVES IS STILL STOPPED. The
// leaving made one try at the retirement. A plugin with a call in flight
// answers that its retirement waits for the call, and at shutdown no
// later pass comes: the child was never stopped, its binding and files
// were left, and the identity exited as if its teardown had been clean.
//
// The plugin here is busy for the first three asks and then stops. Close
// asks until it does: the runtime's own stop ran, and nothing is named as
// left behind.
func TestAPluginBusyWithACallWhenTheHostLeavesIsStillStopped(t *testing.T) {
	rt := &busyRuntime{sharedRuntime: newSharedRuntime()}
	f := serveOne(t, rt, 5*time.Second)
	rt.busy.Store(3)
	f.Close()
	if left := f.Unretired(); len(left) != 0 {
		t.Fatalf("the plugin stopped at the fourth ask, and Close names it as left behind: %v", left)
	}
	if got := rt.asked.Load(); got != 4 {
		t.Fatalf("the stop was asked %d times; three busy answers want a fourth ask", got)
	}
	stops := 0
	rt.mu.Lock()
	for _, c := range rt.calls {
		if strings.HasPrefix(c, "stop:") {
			stops++
		}
	}
	rt.mu.Unlock()
	if stops != 1 {
		t.Fatalf("the runtime's own stop ran %d times, want once: %v", stops, rt.calls)
	}
}

// AND ONE THAT NEVER STOPS IS NAMED, WITHIN THE BOUND. A plugin whose call
// never returns holds its retirement for as long as it is asked. The
// leaving does not wait for ever, and does not pretend: Close returns
// within the retirement's bound and a little, and names the plugin with
// what it still held.
func TestAPluginThatNeverStopsIsNamedWhenTheHostLeaves(t *testing.T) {
	rt := &busyRuntime{sharedRuntime: newSharedRuntime()}
	f := serveOne(t, rt, 300*time.Millisecond)
	rt.busy.Store(-1)
	began := time.Now()
	f.Close()
	if took := time.Since(began); took > 5*time.Second {
		t.Fatalf("Close behind a plugin that never stops took %s; its bound is 300ms", took)
	}
	left := f.Unretired()
	if len(left) != 1 || !strings.Contains(left[0], idOf("one.aiiospkg")) || !strings.Contains(left[0], "a call is in flight") {
		t.Fatalf("Close must name the plugin it could not stop and what it held: %v", left)
	}
	if rt.asked.Load() < 2 {
		t.Fatalf("the stop was asked %d times; the leaving must ask again within its bound", rt.asked.Load())
	}
}

// A CLEAN CLOSE NAMES NOTHING.
func TestACleanCloseNamesNothingLeftBehind(t *testing.T) {
	rt := &busyRuntime{sharedRuntime: newSharedRuntime()}
	f := serveOne(t, rt, 5*time.Second)
	f.Close()
	if left := f.Unretired(); len(left) != 0 || rt.asked.Load() != 1 {
		t.Fatalf("a plugin that stops at once: asked %d times, left behind %v", rt.asked.Load(), left)
	}
}

// A RETIREMENT PENDING FOR ANOTHER REASON IS ASKED ONCE, AS BEFORE. A
// session still open, or a child that will not reap, is not changed by
// asking again, and the application's own teardown is what ends it. The
// leaving asks once, does not wait out the bound, and names nothing: a
// stop with a voice session open is not a failed stop.
func TestARetirementPendingForAnotherReasonIsAskedOnce(t *testing.T) {
	rt := &busyRuntime{sharedRuntime: newSharedRuntime(), pendingOnly: true}
	f := serveOne(t, rt, 5*time.Second)
	rt.busy.Store(-1)
	began := time.Now()
	f.Close()
	if took := time.Since(began); took > 2*time.Second {
		t.Fatalf("Close waited %s on a retirement that asking again does not change", took)
	}
	if left := f.Unretired(); len(left) != 0 || rt.asked.Load() != 1 {
		t.Fatalf("asked %d times, left behind %v; want one ask and nothing named", rt.asked.Load(), left)
	}
}
