package pluginfacility

import (
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// .
// .
// .
// .
// .
// .

func TestNoObservationClearsAHoldTheHostDeclared(t *testing.T) {
	rt := newSharedRuntime()
	f := auditFacility(t, rt)
	id := idOf("late.aiiospkg")
	f.Hold("the identity is in SAFE")
	// .
	f.Observe(obs("late.aiiospkg"), Policy{Revision: 1})
	auditWaitPass(t, f)
	time.Sleep(100 * time.Millisecond)
	if strings.Contains(rt.seen(), "start:late.aiiospkg") {
		t.Fatal("an observation made before the hold started a plugin after it")
	}
	if v := viewOf(f, id); v.Held == "" || v.State == StateActive {
		t.Errorf("state=%s held=%q", v.State, v.Held)
	}
	// .
	// .
	f.Observe(obs("late.aiiospkg"), Policy{Revision: 2})
	f.Observe(obs("late.aiiospkg"), Policy{Revision: 3})
	auditWaitPass(t, f)
	time.Sleep(100 * time.Millisecond)
	if strings.Contains(rt.seen(), "start:late.aiiospkg") {
		t.Fatal("a later observation cleared the facility's hold")
	}

	// .
	// .
	var stillSafe atomic.Bool
	stillSafe.Store(true)
	if f.Unhold(stillSafe.Load) {
		t.Fatal("the hold was released while the host still holds")
	}
	stillSafe.Store(false)
	if !f.Unhold(stillSafe.Load) {
		t.Fatal("the hold was not released when the host no longer holds")
	}
	auditWaitFacility(t, "what the hold kept waiting to start", func() bool { return viewOf(f, id).State == StateActive })
	if f.Unhold(nil) {
		t.Error("a hold that is not in force was released again")
	}
}

func TestAnObservationOlderThanThePolicyInForceIsNotApplied(t *testing.T) {
	rt := newSharedRuntime()
	f := auditFacility(t, rt)
	forbids := func(Evidence) (bool, string) { return false, "the level was raised" }
	// .
	f.Observe(nil, Policy{Revision: 5, TrustGen: 2, Allows: forbids})
	// .
	if f.observe(obs("one.aiiospkg"), Policy{Revision: 4, TrustGen: 2}, nil) {
		t.Fatal("an observation made under an older policy revision was applied")
	}
	// .
	if f.observe(obs("one.aiiospkg"), Policy{Revision: 5, TrustGen: 1}, nil) {
		t.Fatal("an observation made under an older trust generation was applied")
	}
	auditWaitPass(t, f)
	time.Sleep(80 * time.Millisecond)
	if strings.Contains(rt.seen(), "start:") || len(f.Snapshot().Instances) != 0 {
		t.Fatalf("the stale observation acted: %s %+v", rt.seen(), f.Snapshot().Instances)
	}
	// .
	stale := []Skip{{Kind: SkipPolicy, ID: "stale"}}
	f.observe(nil, Policy{Revision: 4, TrustGen: 2}, &stale)
	if len(f.Skips()) != 0 {
		t.Error("a stale scan's skips replaced the current ones")
	}
	// .
	fresh := []Skip{{Kind: SkipPolicy, ID: "fresh"}}
	if !f.observe(nil, Policy{Revision: 5, TrustGen: 2, Allows: forbids}, &fresh) || len(f.Skips()) != 1 {
		t.Error("an observation under the policy in force was not applied")
	}
	if !f.observe(obs("one.aiiospkg"), Policy{Revision: 6, TrustGen: 2}, nil) {
		t.Error("a newer observation was not applied")
	}
	auditWaitFacility(t, "the newer observation to act", func() bool { return viewOf(f, idOf("one.aiiospkg")).State == StateActive })
}
