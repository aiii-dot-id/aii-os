package pluginfacility

import (
	"context"
	"strings"
	"testing"
)

// gatedRuntime is sharedRuntime whose Start for a named package is held
// until its gate closes, so one attempt can be watched mid-start while
// another finishes. Its map is written before the facility starts.
type gatedRuntime struct {
	*sharedRuntime
	gates map[string]chan struct{}
}

func (r *gatedRuntime) Start(ctx context.Context, p Prepared, lease *Lease) (Running, error) {
	if g := r.gates[p.Evidence.Package]; g != nil {
		select {
		case <-g:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return r.sharedRuntime.Start(ctx, p, lease)
}

// A GRANTED ATTEMPT STOPS SAYING IT WAITS (design §3.2: Admitting →
// Starting when a reservation is granted). An attempt that waited for
// admission at all kept saying so through its whole start — "waiting:
// 1 start(s) in flight" on the card for as long as a native engine took
// to stand up — because nothing said the reservation had been granted:
// only serving or refusal cleared the wait.
func TestAGrantedAttemptStopsSayingItWaits(t *testing.T) {
	rt := &gatedRuntime{sharedRuntime: newSharedRuntime(), gates: map[string]chan struct{}{
		"one.aiiospkg": make(chan struct{}), "two.aiiospkg": make(chan struct{})}}
	f := facilityWith(t, rt, ampleCapacity{})
	f.Observe(obs("one.aiiospkg", "two.aiiospkg"), Policy{Revision: 1, Admission: AdmissionPolicy{MaxConcurrentStarts: 1}})
	var first, second string
	until(t, "one starting and the other waiting on the operator's cap", func() bool {
		a, b := viewOf(f, idOf("one.aiiospkg")), viewOf(f, idOf("two.aiiospkg"))
		switch {
		case a.State == StateStarting && b.State == StateAdmitting && strings.Contains(b.Admission, "the operator allows 1 at once"):
			first, second = "one.aiiospkg", "two.aiiospkg"
		case b.State == StateStarting && a.State == StateAdmitting && strings.Contains(a.Admission, "the operator allows 1 at once"):
			first, second = "two.aiiospkg", "one.aiiospkg"
		default:
			return false
		}
		return true
	})
	close(rt.gates[first])
	until(t, "the first to serve", func() bool { return viewOf(f, idOf(first)).State == StateActive })
	// The second is granted the moment the first stops being a start in
	// flight; its own Start is held, so it is STARTING — and says so.
	until(t, "the second to be starting rather than waiting", func() bool {
		v := viewOf(f, idOf(second))
		return v.State == StateStarting && strings.HasPrefix(v.Admission, "admitted")
	})
	close(rt.gates[second])
	until(t, "the second to serve", func() bool { return viewOf(f, idOf(second)).State == StateActive })
}
