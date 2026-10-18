package identity

import (
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/store"
)

// The recurring set's second line: how often the
// cadence can wake you and what the wakes recorded so far have used, said as
// the record holds it. Every case runs the real verb over a real store whose
// turn_metrics rows are written by the store's own writer; the whole answer is
// compared, so a word added, dropped or moved fails.

// wakeWhen is a fixed instant, so the promise's own line is the same every
// run; the layout is written out here rather than taken from the verb.
const wakeWhen = "2026-08-18T07:00:00-04:00"

var wakeLocal = time.Date(2026, 8, 18, 7, 0, 0, 0, time.FixedZone("", -4*3600)).Local().Format("15:04 MST Mon Jan 2")

// promise is the first line of a set's answer for the probe timer.
func promise(cadence string) string {
	return `Timer probe set: ` + wakeLocal + ` (` + cadence + `) — "check"`
}

// setProbe arms the probe timer through one of the two doors to the same
// handler — the work facade's alarm.set and the internal timer operation —
// and returns the answer.
func setProbe(t *testing.T, e *Engine, door, every string) string {
	t.Helper()
	out, err := trySetProbe(t, e, door, every)
	if err != nil {
		t.Fatalf("%s set every=%q: %v", door, every, err)
	}
	return out
}

// trySetProbe is setProbe for a set that may be refused.
func trySetProbe(t *testing.T, e *Engine, door, every string) (string, error) {
	t.Helper()
	args := map[string]any{"id": "probe", "when": wakeWhen, "message": "check"}
	if every != "" {
		args["every"] = every
	}
	verb := "work"
	switch door {
	case "work":
		args["action"] = "alarm.set"
	case "timer":
		verb, args["action"] = "timer", "set"
	default:
		t.Fatalf("no door %q", door)
	}
	return e.ExecuteAction(ctxBG(), "verb", verb, args)
}

func bothDoors(t *testing.T, run func(t *testing.T, door string)) {
	t.Helper()
	for _, door := range []string{"work", "timer"} {
		t.Run(door, func(t *testing.T) { run(t, door) })
	}
}

// recordTurn writes one turn_metrics row the way the app does, ago before now.
func recordTurn(t *testing.T, st *store.Store, ago time.Duration, source string, rounds int, u *store.TurnUsage) {
	t.Helper()
	if err := st.InsertTurnMetric(store.TurnMetric{TsMs: time.Now().Add(-ago).UnixMilli(), Rounds: rounds, Source: source, Usage: u}); err != nil {
		t.Fatal(err)
	}
}

func recordWake(t *testing.T, st *store.Store, ago time.Duration, rounds int, u *store.TurnUsage) {
	t.Helper()
	recordTurn(t, st, ago, store.TimerWakeSource, rounds, u)
}

// exact is the usage of a turn every one of whose calls reported.
func exact(total int) *store.TurnUsage {
	return &store.TurnUsage{PromptTokens: total - 10, CompletionTokens: 10, TotalTokens: total}
}

// partial is what a turn reported when one call told nothing, and when an
// earlier attempt's usage is unknown: a floor either way.
func partial(total int) *store.TurnUsage {
	u := exact(total)
	u.Silent = 1
	return u
}

func attemptUnknown(total int) *store.TurnUsage {
	u := exact(total)
	u.UnknownAttempts = 1
	return u
}

// noteOf is the answer's second line, the whole of it.
func noteOf(t *testing.T, answer string) string {
	t.Helper()
	first, note, ok := strings.Cut(answer, "\n")
	if !ok || strings.Contains(note, "\n") || first == "" {
		t.Fatalf("a recurring set answers the promise and one more line, got %q", answer)
	}
	return note
}

func TestRecurringSetSaysNoWakeIsRecorded(t *testing.T) {
	bothDoors(t, func(t *testing.T, door string) {
		e, _ := timerEngine(t)
		want := promise("every 10m0s") + "\n" +
			"That is up to 144 wakes a day. " +
			"No wake turn from your timers has been recorded in the last 7 days, so how many tokens one uses is not measured."
		if got := setProbe(t, e, door, "10m"); got != want {
			t.Fatalf("no wake recorded:\n got %q\nwant %q", got, want)
		}
	})
}

// A wake seen only by its absence is not zero: turns that are not wakes leave
// the answer saying none is recorded.
func TestRecurringSetIsNotMovedByTurnsThatAreNotWakes(t *testing.T) {
	e, _ := timerEngine(t)
	st := engineStore(e)
	for _, source := range []string{"operator", "steer", "arrival", "voice", "harvest", "continuation", store.FacilitySource("dream"), store.FacilitySource("timer"), "timers", "Timer", ""} {
		recordTurn(t, st, time.Hour, source, 3, exact(50000))
	}
	recordTurn(t, st, time.Hour, "", 3, nil)
	want := promise("every 10m0s") + "\n" +
		"That is up to 144 wakes a day. " +
		"No wake turn from your timers has been recorded in the last 7 days, so how many tokens one uses is not measured."
	if got := setProbe(t, e, "work", "10m"); got != want {
		t.Fatalf("a turn that is not a wake was counted:\n got %q\nwant %q", got, want)
	}
}

func TestRecurringSetStatesExactWakesAsARange(t *testing.T) {
	bothDoors(t, func(t *testing.T, door string) {
		e, _ := timerEngine(t)
		st := engineStore(e)
		for i, total := range []int{9860, 7522, 8200, 9000, 7600, 8100} {
			recordWake(t, st, time.Duration(i+1)*time.Hour, 2, exact(total))
		}
		// sorted 7522 7600 8100 8200 9000 9860: the lower middle of an even
		// count is a recorded total (8100), not the upper one or a mean (8150).
		want := promise("every 10m0s") + "\n" +
			"That is up to 144 wakes a day. " +
			"Wake turns from your timers recorded in the last 7 days: 6 with an exact total, 7522 to 9860 tokens each (median 8100)."
		if got := setProbe(t, e, door, "10m"); got != want {
			t.Fatalf("exact wakes:\n got %q\nwant %q", got, want)
		}
	})
}

// A floor is a lower bound and is said as one; it never enters the range, and
// a wake whose row says nothing is counted, not guessed at. The NULL-usage
// wake has rounds, so a reader that read NULL as zero would take it for an
// exact wake of no tokens and pull the range's low end to 0.
func TestRecurringSetKeepsFloorsAndUnknownOutOfTheRange(t *testing.T) {
	bothDoors(t, func(t *testing.T, door string) {
		e, _ := timerEngine(t)
		st := engineStore(e)
		for i, total := range []int{7522, 9860, 8100} {
			recordWake(t, st, time.Duration(i+1)*time.Hour, 2, exact(total))
		}
		recordWake(t, st, 4*time.Hour, 2, attemptUnknown(12000))
		recordWake(t, st, 5*time.Hour, 3, partial(61000)) // the largest floor is neither the newest nor the oldest
		recordWake(t, st, 6*time.Hour, 2, partial(40000))
		recordWake(t, st, 7*time.Hour, 2, nil)                              // usage never kept
		recordWake(t, st, 8*time.Hour, 2, &store.TurnUsage{Silent: 2})      // no call reported
		recordWake(t, st, 9*time.Hour, 0, &store.TurnUsage{TotalTokens: 5}) // total but no round: not exact
		want := promise("every 10m0s") + "\n" +
			"That is up to 144 wakes a day. " +
			"Wake turns from your timers recorded in the last 7 days: " +
			"3 with an exact total, 7522 to 9860 tokens each (median 8100); " +
			"4 with a partial total, the largest at least 61000 tokens; " +
			"2 with no total recorded."
		if got := setProbe(t, e, door, "10m"); got != want {
			t.Fatalf("mixed wakes:\n got %q\nwant %q", got, want)
		}
	})
}

// Every recorded turn that is not a timer's is present and none is counted:
// the answer is the one the wakes alone give.
func TestRecurringSetCountsOnlyTimerWakes(t *testing.T) {
	e, _ := timerEngine(t)
	st := engineStore(e)
	for i, total := range []int{7522, 9860, 8100} {
		recordWake(t, st, time.Duration(i+1)*time.Hour, 2, exact(total))
	}
	recordWake(t, st, 4*time.Hour, 3, partial(61000))
	want := promise("every 10m0s") + "\n" +
		"That is up to 144 wakes a day. " +
		"Wake turns from your timers recorded in the last 7 days: " +
		"3 with an exact total, 7522 to 9860 tokens each (median 8100); " +
		"1 with a partial total, the largest at least 61000 tokens."
	if got := setProbe(t, e, "work", "10m"); got != want {
		t.Fatalf("before the other turns:\n got %q\nwant %q", got, want)
	}
	// A facility's call and a continuation carry the largest totals here, and
	// a facility named timer is still a facility.
	for _, source := range []string{store.FacilitySource("dream"), store.FacilitySource("timer"), "continuation", "operator", "arrival", "harvest"} {
		recordTurn(t, st, 2*time.Hour, source, 5, exact(900000))
		recordTurn(t, st, 2*time.Hour, source, 5, partial(950000))
		recordTurn(t, st, 2*time.Hour, source, 5, nil)
	}
	if got := setProbe(t, e, "work", "10m"); got != want {
		t.Fatalf("the other turns moved the answer:\n got %q\nwant %q", got, want)
	}
}

func TestRecurringSetReadsOnlyTheWindow(t *testing.T) {
	e, _ := timerEngine(t)
	st := engineStore(e)
	recordWake(t, st, 6*24*time.Hour+23*time.Hour, 2, exact(1000)) // inside, by an hour
	recordWake(t, st, 8*24*time.Hour, 2, exact(700000))
	recordWake(t, st, 7*24*time.Hour+time.Hour, 2, partial(800000)) // outside, by an hour
	recordWake(t, st, 40*24*time.Hour, 2, nil)
	want := promise("every 10m0s") + "\n" +
		"That is up to 144 wakes a day. " +
		"Wake turns from your timers recorded in the last 7 days: 1 with an exact total, 1000 tokens each."
	if got := setProbe(t, e, "work", "10m"); got != want {
		t.Fatalf("the window:\n got %q\nwant %q", got, want)
	}
}

// One-shot answers are what they were, byte for byte, with the meter full of
// wakes that a wrongly added sentence would show.
func TestOneShotSetAnswerIsUnchanged(t *testing.T) {
	e, _ := timerEngine(t)
	st := engineStore(e)
	for i, total := range []int{7522, 9860, 8100} {
		recordWake(t, st, time.Duration(i+1)*time.Hour, 2, exact(total))
	}
	set := func(door string, args map[string]any) string {
		t.Helper()
		args["when"] = wakeWhen
		verb := "work"
		if door == "timer" {
			verb, args["action"] = "timer", "set"
		} else {
			args["action"] = "alarm.set"
		}
		out, err := e.ExecuteAction(ctxBG(), "verb", verb, args)
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	for _, door := range []string{"work", "timer"} {
		for name, c := range map[string]struct {
			args map[string]any
			want string
		}{
			"message":                  {map[string]any{"id": "probe", "message": "check"}, `Timer probe set: ` + wakeLocal + ` (once) — "check"`},
			"tag and message":          {map[string]any{"id": "probe", "tag": "ops", "message": "check"}, `Timer probe [#ops] set: ` + wakeLocal + ` (once) — "check"`},
			"neither":                  {map[string]any{"id": "probe"}, `Timer probe set: ` + wakeLocal + ` (once)`},
			"an empty every":           {map[string]any{"id": "probe", "every": "", "message": "check"}, `Timer probe set: ` + wakeLocal + ` (once) — "check"`},
			"a message with a newline": {map[string]any{"id": "probe", "message": "a\nb"}, `Timer probe set: ` + wakeLocal + ` (once) — "a\nb"`},
		} {
			if got := set(door, c.args); got != c.want {
				t.Errorf("%s / %s:\n got %q\nwant %q", door, name, got, c.want)
			}
		}
	}
}

// The line states what is recorded and never a price: no figure in money, no
// word that asks the reader to think in it, whatever the meter holds.
func TestRecurringSetNamesNoPriceOrMoney(t *testing.T) {
	money := regexp.MustCompile(`(?i)[$€£¥]|\b(usd|eur|gbp|dollars?|cents?|prices?|pric(ed|ing)|bill(ed|ing)?|cost(s|ly)?|charged?|spend|spent|cheap|expensive)\b`)
	e, _ := timerEngine(t)
	st := engineStore(e)
	fill := map[string]func(){
		"nothing recorded": func() {},
		"exact": func() {
			for i, total := range []int{7522, 9860, 8100} {
				recordWake(t, st, time.Duration(i+1)*time.Hour, 2, exact(total))
			}
		},
		"mixed": func() {
			recordWake(t, st, 4*time.Hour, 3, partial(61000))
			recordWake(t, st, 5*time.Hour, 2, nil)
		},
	}
	for _, name := range []string{"nothing recorded", "exact", "mixed"} {
		fill[name]()
		for _, every := range []string{"1s", "10m", "24h"} {
			if got := setProbe(t, e, "work", every); money.MatchString(got) {
				t.Errorf("%s, every=%s: a money word or figure in %q", name, every, got)
			}
		}
	}
	if got := setProbe(t, e, "work", "10m"); !strings.Contains(got, "tokens") {
		t.Fatalf("the fixture's answer names no unit: %q", got)
	}
}

// The timer is already set when the meter is read. A meter that cannot be
// read is said, and the promise stands: no refusal, no error, no silence.
func TestRecurringSetStandsWhenTheMeterCannotBeRead(t *testing.T) {
	bothDoors(t, func(t *testing.T, door string) {
		e, _ := timerEngine(t)
		if _, err := engineStore(e).DB().Exec(`DROP TABLE turn_metrics`); err != nil {
			t.Fatal(err)
		}
		want := promise("every 10m0s") + "\n" +
			"That is up to 144 wakes a day. " +
			"Wake turns from your timers recorded in the last 7 days could not be read this time."
		if got := setProbe(t, e, door, "10m"); got != want {
			t.Fatalf("an unreadable meter:\n got %q\nwant %q", got, want)
		}
		timers, err := e.timers.ListTimers()
		if err != nil || len(timers) != 1 || timers[0].ID != "probe" {
			t.Fatalf("the timer was not left armed: %v %+v", err, timers)
		}
	})
}

// How often the cadence can wake you is arithmetic on the period, said when a
// day holds more than one firing: firings are a period or more apart, so a
// day holds at most ceil(24h / period) of them.
func TestRecurringSetStatesHowOftenItCanWake(t *testing.T) {
	none := "No wake turn from your timers has been recorded in the last 7 days, so how many tokens one uses is not measured."
	e, _ := timerEngine(t)
	for every, want := range map[string]string{
		"1s":   "That is up to 86400 wakes a day. " + none,
		"1m":   "That is up to 1440 wakes a day. " + none,
		"90m":  "That is up to 16 wakes a day. " + none,
		"7h":   "That is up to 4 wakes a day. " + none, // 24/7 is 3.4: a day can hold four
		"12h":  "That is up to 2 wakes a day. " + none,
		"23h":  "That is up to 2 wakes a day. " + none,
		"24h":  none,
		"36h":  none,
		"7d":   none,
		"2w":   none,
		"1d":   none,
		"0.5d": "That is up to 2 wakes a day. " + none,
	} {
		if got := noteOf(t, setProbe(t, e, "work", every)); got != want {
			t.Errorf("every %s:\n got %q\nwant %q", every, got, want)
		}
	}
}

// wakeUsageSentence's grammar where a count of one, or of two, reads
// differently, and the classes a verb-level fixture does not isolate.
func TestWakeUsageSentence(t *testing.T) {
	for name, c := range map[string]struct {
		u    store.WakeUsage
		want string
	}{
		"none": {store.WakeUsage{}, "No wake turn from your timers has been recorded in the last 7 days, so how many tokens one uses is not measured."},
		"one exact wake": {store.WakeUsage{Complete: 1, Min: 7522, Median: 7522, Max: 7522},
			"Wake turns from your timers recorded in the last 7 days: 1 with an exact total, 7522 tokens each."},
		"two exact wakes have no median": {store.WakeUsage{Complete: 2, Min: 7522, Median: 7522, Max: 9860},
			"Wake turns from your timers recorded in the last 7 days: 2 with an exact total, 7522 to 9860 tokens each."},
		"three exact wakes have one": {store.WakeUsage{Complete: 3, Min: 7522, Median: 8100, Max: 9860},
			"Wake turns from your timers recorded in the last 7 days: 3 with an exact total, 7522 to 9860 tokens each (median 8100)."},
		"equal wakes are one figure, with no median": {store.WakeUsage{Complete: 4, Min: 8100, Median: 8100, Max: 8100},
			"Wake turns from your timers recorded in the last 7 days: 4 with an exact total, 8100 tokens each."},
		"floors alone": {store.WakeUsage{Floors: 2, LargestFloor: 61000},
			"Wake turns from your timers recorded in the last 7 days: 2 with a partial total, the largest at least 61000 tokens."},
		"unknown alone": {store.WakeUsage{Unknown: 2},
			"Wake turns from your timers recorded in the last 7 days: 2 with no total recorded."},
		"floors and unknown": {store.WakeUsage{Floors: 1, LargestFloor: 300, Unknown: 1},
			"Wake turns from your timers recorded in the last 7 days: 1 with a partial total, the largest at least 300 tokens; 1 with no total recorded."},
	} {
		if got := wakeUsageSentence(c.u); got != c.want {
			t.Errorf("%s:\n got %q\nwant %q", name, got, c.want)
		}
	}
}

// The row keeps a period in whole milliseconds and refuses zero, so a period
// finer than that is refused in words the resident can act on — not by the
// store's constraint text. S-12's "no floor" rules how often a timer may wake;
// it does not say the row can hold a period finer than it counts.
func TestARecurringSetFinerThanTheRowKeepsIsRefusedInWords(t *testing.T) {
	bothDoors(t, func(t *testing.T, door string) {
		e, _ := timerEngine(t)
		const want = "timer every must be at least 1ms, the finest period a timer keeps"
		for _, every := range []string{"500us", "999µs", "1ns"} {
			if _, err := trySetProbe(t, e, door, every); err == nil || err.Error() != want {
				t.Fatalf("every=%q: want the refusal %q, got %v", every, want, err)
			}
		}
		list, _ := e.ExecuteAction(ctxBG(), "verb", "timer", map[string]any{"action": "list"})
		if strings.Contains(list, "probe") {
			t.Fatalf("a refused set armed a timer: %q", list)
		}
		if _, err := trySetProbe(t, e, door, "1ms"); err != nil {
			t.Fatalf("the finest period the row keeps was refused: %v", err)
		}
	})
}
