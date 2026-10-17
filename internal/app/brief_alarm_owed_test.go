package app

import (
	"context"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/cognitive"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

// briefModel is a local model that writes a brief when it is asked for one,
// and counts the asking.
func briefModel(t *testing.T) (url string, asked func() int64) {
	t.Helper()
	var briefs atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(string(body), "morning brief for the identity") {
			briefs.Add(1)
			_, _ = io.WriteString(w, textReply("A bridge the model wrote."))
			return
		}
		_, _ = io.WriteString(w, textReply("ok"))
	}))
	t.Cleanup(srv.Close)
	return srv.URL, briefs.Load
}

// bootWithBrief starts an identity whose brief is set to at, on a model
// that answers, over a store seed has prepared.
func bootWithBrief(t *testing.T, at, url string, seed func(*store.Store)) *App {
	t.Helper()
	return bootWithBriefSet(t, at, url, seed, nil)
}

// bootWithBriefSet is bootWithBrief with one more thing set in the
// configuration before the start (set), for a test of a setting the brief
// reads.
func bootWithBriefSet(t *testing.T, at, url string, seed func(*store.Store), set func(*Config)) *App {
	t.Helper()
	dir := t.TempDir()
	key, record, database := birthFixture(t, dir, "Bridged")
	buildPriorProjection(t, record, database)
	if seed != nil {
		s, err := store.NewForTest(database)
		if err != nil {
			t.Fatal(err)
		}
		seed(s)
		if err := s.Close(); err != nil {
			t.Fatal(err)
		}
	}
	cfg := safebootConfig(t, dir, "Bridged", key, record, database)
	cfg.LLM = withTestProvider(t, dir, "test", url, "m", "sk-x")
	cfg.Cognition.MorningBriefAt = at
	if set != nil {
		set(cfg)
	}
	a := New(cfg)
	t.Cleanup(func() { stopApp(t, a) })
	if err := a.startLive(); err != nil {
		t.Fatal(err)
	}
	return a
}

func briefAlarm(t *testing.T, a *App) int64 {
	t.Helper()
	rows, err := a.store.DueAlarms("wall", math.MaxInt64, 1000)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rows {
		if r.AlarmID == "morning_brief" && r.OwnerName == "morning_brief" {
			return r.Deadline
		}
	}
	t.Fatal("the brief has no alarm of its own")
	return 0
}

func TestAStartWithTheBriefOnAgainDeliversOnlyABriefThatWasMissed(t *testing.T) {
	now := time.Now().UTC()
	at := now.Add(2 * time.Hour) // the hour set: two hours from now, today's or, late in the day, tomorrow's
	held := (&heldBrief{}).deadline(t)
	for _, c := range []struct {
		name   string
		row    int64
		writes int64
	}{
		{"the row the brief left while it was off", held, 0},
		{"a row overdue since yesterday: a brief that was missed", now.Add(-20 * time.Hour).UnixMilli(), 1},
	} {
		t.Run(c.name, func(t *testing.T) {
			url, asked := briefModel(t)
			a := bootWithBrief(t, at.Format("15:04"), url, func(s *store.Store) {
				if err := s.SetAlarm("morning_brief", "morning_brief", "wall", c.row, nil, ""); err != nil {
					t.Fatal(err)
				}
			})
			waitUntil(t, func() bool { return asked() >= c.writes }, "the model was asked for the missed brief")
			time.Sleep(time.Second) // one that should not be asked for has had its chance
			want := ""
			if c.writes > 0 {
				want = "A bridge the model wrote."
			}
			if got := a.rings.GetBrief(); asked() != c.writes || got != want {
				t.Fatalf("started with the brief on at %s: the model was asked for %d brief(s) and the prompt carries %q; want %d and %q", at.Format("15:04"), asked(), got, c.writes, want)
			}
			due := time.UnixMilli(cognitive.NextLocalDaily(time.Now().UTC(), at.Hour(), at.Minute())).UTC()
			if next := time.UnixMilli(briefAlarm(t, a)).UTC(); !next.Equal(due) {
				t.Fatalf("the brief's alarm is armed for %s; want the next %s, %s", next.Format("2006-01-02 15:04"), at.Format("15:04"), due.Format("2006-01-02 15:04"))
			}
		})
	}
}

func TestAStartLeavesABriefThatIsStillOwedItsAlarm(t *testing.T) {
	for _, c := range []struct {
		name    string
		hour    time.Duration // how far off the brief's hour is set
		spacing time.Duration // the spacing the operator set; 0 for none, which is thirty minutes
		row     time.Duration // how far off the row stands
		left    bool
	}{
		{"an asking-again ten minutes off", 12 * time.Hour, 0, 10 * time.Minute, true},
		{"a row two hours off, further than one spacing", 12 * time.Hour, 0, 2 * time.Hour, false},
		// The spacing is the one in force, not the default: an asking-again
		// an hour off under a spacing of two hours is still owed.
		{"a row an hour off under a spacing of two hours", 12 * time.Hour, 2 * time.Hour, time.Hour, true},
		// No later than the hour's next coming: a row inside one spacing
		// that the hour itself comes before is not what the brief is owed
		// by, and the hour is armed.
		{"a row twenty minutes off when the hour comes in ten", 10 * time.Minute, 0, 20 * time.Minute, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			logs := logsink.CaptureForTest(t)
			now := time.Now().UTC()
			at, row := now.Add(c.hour), now.Add(c.row).UnixMilli()
			url, asked := briefModel(t)
			a := bootWithBriefSet(t, at.Format("15:04"), url, func(s *store.Store) {
				if err := s.SetAlarm("morning_brief", "morning_brief", "wall", row, nil, ""); err != nil {
					t.Fatal(err)
				}
			}, func(cfg *Config) {
				if c.spacing > 0 {
					cfg.Cognition.ConsolidateSpacingSeconds = int(c.spacing / time.Second)
				}
			})
			want := row
			if !c.left {
				want = cognitive.NextLocalDaily(time.Now().UTC(), at.Hour(), at.Minute())
			}
			if got := briefAlarm(t, a); got != want {
				t.Fatalf("AFTER A START THE BRIEF'S ALARM, WHICH STOOD AT %s, IS ARMED FOR %s; want %s",
					time.UnixMilli(row).UTC().Format("15:04:05"), time.UnixMilli(got).UTC().Format("2006-01-02 15:04:05"), time.UnixMilli(want).UTC().Format("2006-01-02 15:04:05"))
			}
			if asked() != 0 {
				t.Fatalf("a brief not yet due was asked for at the start: %d", asked())
			}
			// The start's log line says which it did, to the operator.
			said := "morning_brief(" + time.UnixMilli(row).UTC().Format("15:04 UTC") + ", left as it stood: a brief still owed)"
			if c.left != logs.Contains(said) || !c.left && logs.Contains("left as it stood") {
				t.Fatalf("THE START'S LOG LINE DOES NOT SAY WHAT WAS DONE WITH THE BRIEF'S ALARM (left %v); looked for %q in\n%s", c.left, said, logs.String())
			}
		})
	}
}

// OFF, THE BRIEF'S ALARM IS NEVER LEFT AS IT STANDS (row 9.28). A row ten
// minutes off is what a brief still owed looks like. With the brief turned
// off while the identity was stopped nothing is owed: left due, the row
// would fire at a brief that is off, and would no longer be held out of
// the clock's reach. Off, the start arms it afresh, held, as before.
func TestAStartWithTheBriefOffDoesNotLeaveItsAlarmDue(t *testing.T) {
	url, asked := briefModel(t)
	row := time.Now().Add(10 * time.Minute).UnixMilli()
	a := bootWithBrief(t, cognitive.MorningBriefOff, url, func(s *store.Store) {
		if err := s.SetAlarm("morning_brief", "morning_brief", "wall", row, nil, ""); err != nil {
			t.Fatal(err)
		}
	})
	if got := briefAlarm(t, a); time.Until(time.UnixMilli(got)) < 50*365*24*time.Hour {
		t.Fatalf("WITH THE BRIEF OFF A START LEFT ITS ALARM DUE AT %s (it stood at %s); want it held out of the clock's reach",
			time.UnixMilli(got).UTC().Format("2006-01-02 15:04:05"), time.UnixMilli(row).UTC().Format("15:04:05"))
	}
	if asked() != 0 {
		t.Fatalf("a brief that is off was asked for at the start: %d", asked())
	}
}

// A BRIEF MISSED WHILE THE IDENTITY WAS STOPPED, WHOSE CALL THEN FAILS, IS
// ASKED FOR AGAIN THAT DAY (row 9.28). The start fires the overdue alarm
// and then armed it afresh for the next day's hour; the firing's answer,
// "ask again one spacing on", was compared against a row that had been
// replaced under it and moved nothing. The row the firing was made for is
// left to it.
func TestABriefMissedAndThenFailedAtAStartIsAskedForAgainThatDay(t *testing.T) {
	var asked atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if strings.Contains(string(body), "morning brief for the identity") {
			asked.Add(1)
			http.Error(w, "fixture provider failure", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, textReply("ok"))
	}))
	t.Cleanup(srv.Close)
	now := time.Now().UTC()
	at := now.Add(12 * time.Hour)
	a := bootWithBrief(t, at.Format("15:04"), srv.URL, func(s *store.Store) {
		if err := s.SetAlarm("morning_brief", "morning_brief", "wall", now.Add(-20*time.Hour).UnixMilli(), nil, ""); err != nil {
			t.Fatal(err)
		}
	})
	// The spacing where none is set is thirty minutes: the asking-again is
	// armed inside it, and nowhere near the hour twelve hours off.
	again := func() bool {
		due := time.Until(time.UnixMilli(briefAlarm(t, a)))
		return asked.Load() > 0 && due > 0 && due <= 31*time.Minute
	}
	deadline := time.Now().Add(60 * time.Second)
	for !again() {
		if time.Now().After(deadline) {
			t.Fatalf("THE MISSED BRIEF'S CALL FAILED (%d ask(s)) AND ITS ALARM IS ARMED FOR %s: it is not asked for again today",
				asked.Load(), time.UnixMilli(briefAlarm(t, a)).UTC().Format("2006-01-02 15:04:05"))
		}
		time.Sleep(100 * time.Millisecond)
	}
	if got := a.rings.GetBrief(); got != "" {
		t.Fatalf("a brief was written from a call that failed: %q", got)
	}
}

// heldBrief answers where a brief that is off holds its alarm, from the
// facility itself.
type heldBrief struct{}

func (heldBrief) deadline(t *testing.T) int64 {
	t.Helper()
	mb := cognitive.NewMorningBrief(nil, nil, nil, cognitive.MorningBriefConfig{LocalTime: cognitive.MorningBriefOff})
	d := mb.NextDeadline()
	if time.Until(time.UnixMilli(d)) < 50*365*24*time.Hour {
		t.Fatalf("rig: off, the brief's alarm is held far ahead: %s", time.UnixMilli(d).UTC())
	}
	return d
}

func TestABriefThatIsOwedIsNotPutOffByAnotherSetting(t *testing.T) {
	url, asked := briefModel(t)
	at := time.Now().UTC().Add(-2 * time.Minute).Format("15:04")
	a := bootWithBrief(t, at, url, nil)
	if !a.TryBeginTurn() {
		t.Fatal("rig: the turn was not free")
	}
	released := false
	t.Cleanup(func() {
		if !released {
			a.releaseTurn()
		}
	})
	// Its hour arrives while the identity is in a turn: the row as TIME
	// holds it at that moment, and the firing it defers.
	if err := a.store.SetAlarm("morning_brief", "morning_brief", "wall", time.Now().UnixMilli()-1000, nil, ""); err != nil {
		t.Fatal(err)
	}
	if err := a.timeFac.EvaluateAll(context.Background()); err != nil {
		t.Fatal(err)
	}
	soon := func() bool { return time.Until(time.UnixMilli(briefAlarm(t, a))) < time.Hour }
	if !soon() || asked() != 0 {
		t.Fatalf("rig: a brief due in a turn is deferred, not written and not dropped: next alarm %s, %d asked", time.UnixMilli(briefAlarm(t, a)).UTC().Format("2006-01-02 15:04:05"), asked())
	}
	if _, err := a.applyConfigChange(map[string]any{"cognition.consolidate_spacing_seconds": float64(2400)}); err != nil {
		t.Fatal(err)
	}
	if !soon() {
		t.Fatalf("ANOTHER SETTING OF THE RHYTHM WAS SAVED AND TODAY'S BRIEF, OWED, IS NOW ARMED FOR %s", time.UnixMilli(briefAlarm(t, a)).UTC().Format("2006-01-02 15:04:05"))
	}
	// Its own hour changed is the operator saying when: that arms it.
	later := time.Now().UTC().Add(3 * time.Hour)
	if _, err := a.applyConfigChange(map[string]any{"cognition.morning_brief_at": later.Format("15:04")}); err != nil {
		t.Fatal(err)
	}
	if next := time.UnixMilli(briefAlarm(t, a)).UTC(); next.Hour() != later.Hour() || next.Minute() != later.Minute() {
		t.Fatalf("the brief's own hour was set to %s and its alarm is armed for %s", later.Format("15:04"), next.Format("2006-01-02 15:04"))
	}
}

func TestAZoneSetThroughTheDoorReachesTheBriefAtOnce(t *testing.T) {
	url, asked := briefModel(t)
	a := bootWithBrief(t, "08:30", url, nil)
	if at := time.UnixMilli(briefAlarm(t, a)).UTC(); at.Hour() != 8 || at.Minute() != 30 {
		t.Fatalf("rig: with no zone the brief's 08:30 is UTC's: %s", at.Format("15:04"))
	}
	zone, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}

	for _, c := range []struct {
		set string
		in  *time.Location
	}{{"America/New_York", zone}, {"", time.UTC}, {"America/New_York", zone}} {
		st, err := a.applyConfigChange(map[string]any{"timezone": c.set})
		if err != nil {
			t.Fatal(err)
		}
		if at := time.UnixMilli(briefAlarm(t, a)).In(c.in); at.Hour() != 8 || at.Minute() != 30 {
			t.Fatalf("THE ZONE WAS SET TO %q THROUGH THE DOOR AND THE BRIEF'S ALARM IS STILL ANOTHER ZONE'S 08:30: %s there", c.set, at.Format("15:04"))
		}
		if len(st.RestartRequired) != 0 {
			t.Fatalf("the zone %q is in force at once, and the door answered that it needs a restart: %v", c.set, st.RestartRequired)
		}
	}

	tokyo, err := time.LoadLocation("Asia/Tokyo")
	if err != nil {
		t.Fatal(err)
	}
	edited := a.configSnapshot()
	edited.Timezone = "Asia/Tokyo"
	if _, err := saveConfig(&edited); err != nil {
		t.Fatal(err)
	}
	a.reloadConfig()
	if at := time.UnixMilli(briefAlarm(t, a)).In(tokyo); at.Hour() != 8 || at.Minute() != 30 {
		t.Fatalf("THE FILE'S ZONE WAS CHANGED TO Asia/Tokyo AND RELOADED, AND THE BRIEF'S ALARM IS STILL ANOTHER ZONE'S 08:30: %s there", at.Format("15:04"))
	}
	// And so a brief owed after that is not moved by another setting.
	if !a.TryBeginTurn() {
		t.Fatal("rig: the turn was not free")
	}
	defer a.releaseTurn()
	if err := a.store.SetAlarm("morning_brief", "morning_brief", "wall", time.Now().UnixMilli()-1000, nil, ""); err != nil {
		t.Fatal(err)
	}
	if err := a.timeFac.EvaluateAll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := a.applyConfigChange(map[string]any{"cognition.consolidate_spacing_seconds": float64(2400)}); err != nil {
		t.Fatal(err)
	}
	if owed := time.Until(time.UnixMilli(briefAlarm(t, a))); owed > time.Hour || asked() != 0 {
		t.Fatalf("after a zone set through the door, another setting's saving put an owed brief off: due in %s, %d asked", owed.Round(time.Second), asked())
	}
}

func TestARhythmSettingSavedBeforeTheIdentityWasLiveIsInForceOnceItIs(t *testing.T) {
	url, _ := briefModel(t)
	a := bootWithBrief(t, "08:30", url, nil)
	zone, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	// The moment before live, as a start passes through it.
	started := a.configSnapshot()
	a.live.Store(false)
	st, err := a.applyConfigChange(map[string]any{"timezone": "America/New_York", "agency.rhythm_seconds": float64(started.Agency.RhythmSeconds + 60)})
	if err != nil {
		t.Fatal(err)
	}
	metabolism := func() int64 {
		t.Helper()
		rows, err := a.store.DueAlarms("wall", math.MaxInt64, 1000)
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range rows {
			if r.AlarmID == "rhythm" && r.RepeatEvery != nil {
				return *r.RepeatEvery / 1000
			}
		}
		t.Fatal("the metabolism has no alarm")
		return 0
	}
	if got := metabolism(); got != int64(started.Agency.RhythmSeconds) {
		t.Fatalf("rig: before live the metabolism's cadence is the start's, %d seconds; it is %d", started.Agency.RhythmSeconds, got)
	}
	if at := time.UnixMilli(briefAlarm(t, a)).UTC(); len(st.RestartRequired) != 0 || at.Hour() != 8 || at.Minute() != 30 {
		t.Fatalf("rig: before live the door saves the zone, asks no restart and applies nothing: restart %v, alarm %s UTC", st.RestartRequired, at.Format("15:04"))
	}
	a.goLive(started)
	if at := time.UnixMilli(briefAlarm(t, a)).In(zone); !a.live.Load() || at.Hour() != 8 || at.Minute() != 30 {
		t.Fatalf("THE IDENTITY WENT LIVE AND THE ZONE SAVED JUST BEFORE IS STILL IN FORCE NOWHERE: the brief's alarm is %s in New York", at.Format("15:04"))
	}
	if got := metabolism(); got != int64(started.Agency.RhythmSeconds+60) {
		t.Fatalf("THE IDENTITY WENT LIVE AND THE METABOLISM'S CADENCE SAVED JUST BEFORE IS IN FORCE NOWHERE: every %d seconds, saved %d", got, started.Agency.RhythmSeconds+60)
	}
}
