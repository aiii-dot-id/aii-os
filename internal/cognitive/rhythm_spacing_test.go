package cognitive

import (
	"context"
	"testing"
)

// .
// .
// .
// .
// .
// .
type failingOwner struct {
	name string
	runs int
}

func (f *failingOwner) Name() string { return f.name }
func (f *failingOwner) OnAlarm(ctx context.Context, alarmID, clock string, deadline int64, payload string) AlarmResult {
	f.runs++
	return AlarmResult{Accepted: false}
}

func TestAFailedReflectivePassSpendsItsCadenceAnyway(t *testing.T) {
	sm := &failingOwner{name: "self_model"}
	r := NewRhythm(&fakeRaw{}, nil, nil, nil, sm, &fakeOwner{name: "review"})
	res := r.OnAlarm(context.Background(), ReflectSelfModelAlarm, "life", 50, "")
	if sm.runs != 1 {
		t.Fatalf("the facility ran once, got %d", sm.runs)
	}
	if !res.Accepted || res.NextDeadline != nil {
		t.Fatalf("a run that happened is accepted so the alarm rearms by its cadence, got %+v", res)
	}
}

// .
// .
// .
type busyGate struct{}

func (busyGate) TryBeginTurn() bool { return false }
func (busyGate) EndTurn()           {}

func TestAReflectiveAlarmInATurnIsOwedOnTheNextPulse(t *testing.T) {
	sm := &fakeOwner{name: "self_model"}
	r := NewRhythm(&fakeRaw{}, busyGate{}, nil, nil, sm, &fakeOwner{name: "review"})
	res := r.OnAlarm(context.Background(), ReflectSelfModelAlarm, "life", 50, "")
	if sm.runs != 0 {
		t.Fatal("the facility must not run while the identity is in a turn")
	}
	if res.Accepted || res.NextDeadline == nil || *res.NextDeadline != 51 {
		t.Fatalf("deferred to the next pulse (51), got %+v", res)
	}
}
