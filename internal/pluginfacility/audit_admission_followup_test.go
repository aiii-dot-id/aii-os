// .
// .
// .
// .

package pluginfacility

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type auditChangingCapacity struct {
	available atomic.Int64
	measured  atomic.Int32
}

func (c *auditChangingCapacity) Measure() Availability {
	c.measured.Add(1)
	return Availability{HostKnown: true, HostTotal: 16 * gib, HostAvailable: c.available.Load()}
}

func TestAuditAdmissionBudgetCannotReplaceMeasuredDeficit(t *testing.T) {
	for _, tc := range []struct {
		name                        string
		capacity                    Capacity
		reserve, budget, held, need int64
		want                        bool
	}{
		{"reserve-exceeds-available", fixedCapacity{16 * gib, gib}, 2 * gib, 8 * gib, 0, gib, false},
		{"prior-reservations-exceed-available", fixedCapacity{16 * gib, gib}, 0, 8 * gib, 2 * gib, gib, false},
		{"known-physical-limit", fixedCapacity{16 * gib, 4 * gib}, gib, 8 * gib, 0, 4 * gib, false},
		{"known-room", fixedCapacity{16 * gib, 8 * gib}, gib, 4 * gib, 0, 3 * gib, true},
		{"unknown-budget", UnknownCapacity{}, 0, 4 * gib, 0, 3 * gib, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := New(Config{Capacity: tc.capacity})
			f.policy.Admission = AdmissionPolicy{ReserveBytes: tc.reserve, BudgetBytes: tc.budget}
			if tc.held > 0 {
				f.reserved[1] = &reservation{gen: 1, id: "held", host: tc.held, serving: true}
			}
			w := &admitWaiter{seq: 1, gen: 2, id: "candidate"}
			f.queue = []*admitWaiter{w}
			f.mu.Lock()
			ok, why, never := f.admissibleLocked(w, Prepared{HostBytes: tc.need})
			f.mu.Unlock()
			if ok != tc.want || never != nil {
				t.Errorf("admitted=%v want=%v reason=%q refusal=%v", ok, tc.want, why, never)
			}
		})
	}
}

func TestAuditAdmissionDeficitCannotStartAChild(t *testing.T) {
	rt := newSharedRuntime()
	rt.hostBytesFor["one.aiiospkg"] = gib
	f := New(Config{Runtime: rt, Capacity: fixedCapacity{16 * gib, gib}})
	auditAttachFacility(t, f)
	f.Observe(obs("one.aiiospkg"), Policy{Revision: 1, Admission: AdmissionPolicy{ReserveBytes: 2 * gib, BudgetBytes: 8 * gib}})
	auditWaitFacility(t, "admission decision", func() bool {
		s := viewOf(f, idOf("one.aiiospkg")).State
		return s == StateAdmitting || s == StateActive
	})
	if strings.Contains(rt.seen(), "start:one.aiiospkg") {
		t.Error("configured budget allowed actual Start despite negative measured headroom")
	}
}

func TestAuditAdmissionRechecksChangedInputs(t *testing.T) {
	for _, change := range []string{"policy", "capacity"} {
		t.Run(change, func(t *testing.T) {
			rt := newSharedRuntime()
			rt.hostBytesFor["one.aiiospkg"] = 3 * gib
			cap := &auditChangingCapacity{}
			cap.available.Store(4 * gib)
			pol := Policy{Revision: 1, Admission: AdmissionPolicy{ReserveBytes: 2 * gib}}
			f := New(Config{Runtime: rt, Capacity: cap})
			auditAttachFacility(t, f)
			f.Observe(obs("one.aiiospkg"), pol)
			auditWaitFacility(t, "parked admission", func() bool {
				return viewOf(f, idOf("one.aiiospkg")).State == StateAdmitting && cap.measured.Load() >= 2
			})
			if change == "policy" {
				pol.Revision = 2
				pol.Admission.ReserveBytes = 0
				f.Observe(obs("one.aiiospkg"), pol)
			} else {
				cap.available.Store(8 * gib)
				f.Poke("capacity changed")
			}
			auditWaitPass(t, f)
			deadline := time.Now().Add(150 * time.Millisecond)
			for !strings.Contains(rt.seen(), "start:one.aiiospkg") && time.Now().Before(deadline) {
				time.Sleep(time.Millisecond)
			}
			started := strings.Contains(rt.seen(), "start:one.aiiospkg")
			// .
			f.mu.Lock()
			f.ringDoorbellsLocked()
			f.mu.Unlock()
			auditWaitFacility(t, "admission after direct doorbell", func() bool { return viewOf(f, idOf("one.aiiospkg")).State == StateActive })
			if !started {
				t.Errorf("changed %s reconciled, but parked admission did not recheck until its doorbell was manually rung", change)
			}
		})
	}
}

func TestAuditAdmissionRejectsCancelledOrClosedLifetime(t *testing.T) {
	for _, mode := range []string{"cancelled", "closed"} {
		t.Run(mode, func(t *testing.T) {
			f := New(Config{Capacity: ampleCapacity{}})
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if mode == "cancelled" {
				cancel()
			} else {
				f.Close()
			}
			release, _, err := f.admit(ctx, 900, Prepared{Evidence: Evidence{ID: "one"}, HostBytes: gib}, nil)
			if release != nil {
				release()
			}
			if mode == "cancelled" && !errors.Is(err, context.Canceled) {
				t.Errorf("cancelled admission returned release=%v err=%v", release != nil, err)
			}
			if mode == "closed" && err == nil {
				t.Error("closed facility granted a new reservation")
			}
		})
	}
}
