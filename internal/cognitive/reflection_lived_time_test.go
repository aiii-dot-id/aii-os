package cognitive

import (
	"context"
	"testing"
	"time"
)

// .
// .
// .
// .
// .
func TestReflectionRunsOnLivedTimeNotWallTime(t *testing.T) {
	tm, _ := newTIME(t)
	sm := &fakeOwner{name: "self_model"}
	r := NewRhythm(&fakeRaw{}, nil, nil, nil, sm, &fakeOwner{name: "review"})
	tm.RegisterOwner(r)
	every := int64(2)
	if err := tm.SetAlarm(ReflectSelfModelAlarm, r.Name(), "life", tm.LifeClock()+every, &every, ""); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	// .
	if err := tm.EvaluateAll(ctx); err != nil {
		t.Fatal(err)
	}
	time.Sleep(20 * time.Millisecond)
	if sm.runs != 0 {
		t.Fatalf("wall time alone ran the reflection %d times", sm.runs)
	}
	// .
	for i := 0; i < 2; i++ {
		if err := tm.AdvanceLifeClock(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if sm.runs != 1 {
		t.Fatalf("after %d accepted pulses the reflection ran %d times, want 1", every, sm.runs)
	}
	// .
	for i := 0; i < 2; i++ {
		if err := tm.AdvanceLifeClock(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if sm.runs != 2 {
		t.Fatalf("the cadence did not rearm: %d runs after 4 pulses", sm.runs)
	}
}
