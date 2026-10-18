package actr

import (
	"testing"
	"time"
)

func TestActivationRewardsRecencyAndRepetition(t *testing.T) {
	now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	dayOld := Strength(now.Add(-24*time.Hour), nil, now)
	if dayOld < 0.49 || dayOld > 0.51 {
		t.Fatalf("a day-old memory never recalled = %v, want 0.5", dayOld)
	}
	var today []time.Time
	for i := 0; i < 5; i++ {
		today = append(today, now.Add(-time.Duration(i)*time.Minute))
	}
	if s := Strength(now.Add(-24*time.Hour), today, now); s < 0.95 {
		t.Fatalf("five recalls today = %v, want near 1", s)
	}
	if s := Strength(now.Add(-100*24*time.Hour), nil, now); s > 0.1 {
		t.Fatalf("a hundred days unrecalled = %v, want under 0.1", s)
	}
	old := Strength(now.Add(-100*24*time.Hour), nil, now)
	revived := Strength(now.Add(-100*24*time.Hour), []time.Time{now.Add(-2 * time.Hour)}, now)
	if revived <= old {
		t.Fatalf("one recent recall must revive: %v vs %v", revived, old)
	}
	spaced := Strength(now.Add(-30*24*time.Hour), []time.Time{now.Add(-20 * 24 * time.Hour), now.Add(-10 * 24 * time.Hour), now.Add(-24 * time.Hour)}, now)
	massed := Strength(now.Add(-30*24*time.Hour), []time.Time{now.Add(-30 * 24 * time.Hour), now.Add(-30*24*time.Hour + time.Minute), now.Add(-30*24*time.Hour + 2*time.Minute)}, now)
	if spaced <= massed {
		t.Fatalf("spaced recalls outlast massed ones: %v vs %v", spaced, massed)
	}
	if s := Strength(time.Time{}, nil, now); s != 0.05 {
		t.Fatalf("no record at all is the floor: %v", s)
	}
}
