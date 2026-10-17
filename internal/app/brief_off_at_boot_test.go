package app

import (
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/cognitive"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

func TestABriefKeptFromBeforeItWasTurnedOffIsNotRestored(t *testing.T) {
	for _, c := range []struct {
		name, at, want string
		rowFromBefore  bool
	}{
		{"the brief is on", "07:00", "yesterday's bridge", true},
		{"the brief was turned off while the identity was stopped", cognitive.MorningBriefOff, "", true},

		{"the brief is off at a start that finds no alarm of its own", cognitive.MorningBriefOff, "", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			key, record, database := birthFixture(t, dir, "Bridged")
			buildPriorProjection(t, record, database)
			s, err := store.NewForTest(database)
			if err != nil {
				t.Fatal(err)
			}
			if err := s.SaveBrief("yesterday's bridge"); err != nil {
				t.Fatal(err)
			}
			if c.rowFromBefore {
				// A row from before the stop, soon due: what an identity
				// whose brief was on until then holds.
				if err := s.SetAlarm("morning_brief", "morning_brief", "wall", time.Now().Add(time.Hour).UnixMilli(), nil, ""); err != nil {
					t.Fatal(err)
				}
			}
			if err := s.Close(); err != nil {
				t.Fatal(err)
			}
			cfg := safebootConfig(t, dir, "Bridged", key, record, database)
			cfg.Cognition.MorningBriefAt = c.at
			a := New(cfg)
			t.Cleanup(func() { stopApp(t, a) })
			if err := a.startLive(); err != nil {
				t.Fatal(err)
			}
			stored, err := a.store.GetBrief()
			if err != nil {
				t.Fatal(err)
			}
			if got := a.rings.GetBrief(); got != c.want || stored != c.want {
				t.Fatalf("after a boot with cognition.morning_brief_at %q the prompt carries the brief %q and the store holds %q; want %q in both", c.at, got, stored, c.want)
			}
			// What the START did with the brief's alarm, read before anything
			// else arms it. On or off the row is the brief's own, which is
			// what keeps the name "morning_brief" from a timer of the
			// identity's, made by the start where none stood; off, it is held
			// out of the clock's reach, wherever a row from before stood.
			deadline := briefAlarm(t, a)
			if far := time.Until(time.UnixMilli(deadline)) > 50*365*24*time.Hour; far != (c.at == cognitive.MorningBriefOff) {
				t.Fatalf("after a start with cognition.morning_brief_at %q the brief's alarm is due %s", c.at, time.UnixMilli(deadline).UTC().Format("2006-01-02 15:04"))
			}
			if err := a.store.SetAlarm("morning_brief", "timers", "wall", 1, nil, "a timer of that name"); err == nil {
				t.Fatalf("AFTER A START WITH cognition.morning_brief_at %q A TIMER TOOK THE BRIEF'S ALARM NAME", c.at)
			}
			// And armed again, as a change of its own setting arms it.
			if said, err := a.armBrief(); err != nil || (said == "off") != (c.at == cognitive.MorningBriefOff) {
				t.Fatalf("arming again: %q, %v", said, err)
			}
		})
	}
}
