package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/ledger"
)

// A fixture for the anchor: genesis (the life clock), one belief, two raw
// experiences for run markers to cite. Events are kept for replay.
type anchorFixture struct {
	t      *testing.T
	s      *Store
	events []ledger.Event
}

func newAnchorFixture(t *testing.T) *anchorFixture {
	return anchorFixtureOn(t, testStore(t))
}

// anchorFixtureOn is the anchor fixture on a store the test opened.
func anchorFixtureOn(t *testing.T, s *Store) *anchorFixture {
	f := &anchorFixture{t: t, s: s}
	f.mat(ledger.EventRing0Genesis, 0, map[string]any{"name": "S"})
	f.mat(ledger.EventBeliefUpsert, 3, map[string]any{"id": "b", "statement": "stand", "ring": 3, "confidence": 0.5})
	f.mat(ledger.EventExperienceCreate, 3, map[string]any{"id": "x1", "content": "obs", "category": "observation"})
	f.mat(ledger.EventExperienceCreate, 3, map[string]any{"id": "x2", "content": "obs", "category": "observation"})
	return f
}

func (f *anchorFixture) event(et ledger.EventType, ring int, payload map[string]any) ledger.Event {
	b, _ := json.Marshal(payload)
	return ledger.Event{Seq: uint64(len(f.events) + 1), Type: et, Ring: ring, Timestamp: "2026-09-04T10:00:00Z", Payload: b}
}

func (f *anchorFixture) mat(et ledger.EventType, ring int, payload map[string]any) {
	f.t.Helper()
	evt := f.event(et, ring, payload)
	if err := f.s.Materialize(&evt); err != nil {
		f.t.Fatalf("materialize %s: %v", et, err)
	}
	f.events = append(f.events, evt)
}

func (f *anchorFixture) run(input string, confirmed ...map[string]any) map[string]any {
	return map[string]any{"inputs": []string{input}, "outputs": []uint64{}, "confirmed": confirmed}
}

func (f *anchorFixture) setTicks(n int64) {
	f.t.Helper()
	f.s.mu.Lock()
	_, err := f.s.db.Exec(`UPDATE identity_lifetime SET lifetime_ticks = ? WHERE singleton_id = 'current'`, n)
	f.s.mu.Unlock()
	if err != nil {
		f.t.Fatal(err)
	}
}

func (f *anchorFixture) anchor(id string) int64 {
	f.t.Helper()
	var n int64
	f.s.mu.RLock()
	err := f.s.db.QueryRow(`SELECT confirmed_at_ticks FROM beliefs WHERE id = ?`, id).Scan(&n)
	f.s.mu.RUnlock()
	if err != nil {
		f.t.Fatal(err)
	}
	return n
}

func TestTheRunMarkerStampsTheConfirmedAnchorAndReplayKeepsIt(t *testing.T) {
	f := newAnchorFixture(t)
	f.setTicks(120)
	f.mat(ledger.EventConsolidationRun, 3, f.run("x1", map[string]any{"id": "b", "ticks": 100}))
	if got := f.anchor("b"); got != 100 {
		t.Fatalf("anchor after the marker = %d, want 100", got)
	}
	// A later crossing does not move a set anchor: first stamp wins.
	f.mat(ledger.EventConsolidationRun, 3, f.run("x2", map[string]any{"id": "b", "ticks": 115}))
	if got := f.anchor("b"); got != 100 {
		t.Fatalf("a second crossing moved the anchor to %d", got)
	}
	// The rebuild: derived tables cleared and the record replayed — the
	// anchor comes back from the marker, not from the runtime. On main
	// this read finds zero.
	if err := f.s.ReplayAll(EventSlice(f.events)); err != nil {
		t.Fatalf("replay: %v", err)
	}
	if got := f.anchor("b"); got != 100 {
		t.Fatalf("anchor after replay = %d, want 100", got)
	}
}

// A crossing is a signed claim about a belief: a belief that does not
// exist, or a tick that is not lived time, is refused — the marker fails
// closed like its inputs and outputs do.
func TestACrossingRefusesAGhostBeliefAndAZeroTick(t *testing.T) {
	f := newAnchorFixture(t)
	f.setTicks(120)
	ghost := f.event(ledger.EventConsolidationRun, 3, f.run("x1", map[string]any{"id": "nobody", "ticks": 100}))
	var missing *MissingEventTargetError
	if err := f.s.Materialize(&ghost); !errors.As(err, &missing) || missing.Kind != "belief" || missing.ID != "nobody" {
		t.Fatalf("a ghost belief was not refused: %v", err)
	}
	zero := f.event(ledger.EventConsolidationRun, 3, f.run("x1", map[string]any{"id": "b", "ticks": 0}))
	if err := f.s.Materialize(&zero); err == nil || !strings.Contains(err.Error(), "positive tick") {
		t.Fatalf("a zero tick was not refused: %v", err)
	}
	// Neither refusal consumed the input it cited.
	var raw int
	f.s.mu.RLock()
	err := f.s.db.QueryRow(`SELECT raw FROM experiences WHERE id = 'x1'`).Scan(&raw)
	f.s.mu.RUnlock()
	if err != nil || raw != 1 {
		t.Fatalf("a refused marker consumed its input: raw=%d err=%v", raw, err)
	}
}

func TestTheSameRecordGivesTheSameAnchorWhateverTheClockReads(t *testing.T) {
	f := newAnchorFixture(t)
	f.setTicks(120)
	f.mat(ledger.EventConsolidationRun, 3, f.run("x1", map[string]any{"id": "b", "ticks": 100}))
	for _, clock := range []int64{0, 30, 5000} {
		f.setTicks(clock)
		if err := f.s.ReplayAll(EventSlice(f.events)); err != nil {
			t.Fatalf("replay under a clock at %d: %v", clock, err)
		}
		if got := f.anchor("b"); got != 100 {
			t.Fatalf("the record says tick 100; replayed under a clock at %d the anchor is %d", clock, got)
		}
	}
	// And at admission: a marker materialized under a clock behind its
	// tick is stamped as written.
	g := newAnchorFixture(t)
	g.setTicks(30)
	g.mat(ledger.EventConsolidationRun, 3, g.run("x1", map[string]any{"id": "b", "ticks": 100}))
	if got := g.anchor("b"); got != 100 {
		t.Fatalf("anchor admitted under a tick-30 clock = %d, want the recorded 100", got)
	}
}

// confirmedFixture is the anchor fixture with belief b confirmed (three
// sources of its own) and its crossing recorded at tick 100 under a clock
// at 120: an ordinary record, written under a clock ahead of its anchor.
func confirmedFixture(t *testing.T) *anchorFixture {
	return confirmedFixtureOn(t, testStore(t))
}

func confirmedFixtureOn(t *testing.T, s *Store) *anchorFixture {
	f := anchorFixtureOn(t, s)
	f.mat(ledger.EventExperienceCreate, 3, map[string]any{"id": "x3", "content": "obs", "category": "observation"})
	for _, x := range []string{"x1", "x2", "x3"} {
		f.mat(ledger.EventEdgeCreate, 3, map[string]any{"id": "e_" + x, "from_id": x, "to_id": "b", "edge_type": "SUPPORTS"})
	}
	f.setTicks(120)
	f.mat(ledger.EventConsolidationRun, 3, f.run("x1", map[string]any{"id": "b", "ticks": 100}))
	return f
}

// standingStep is one look at a belief's standing: the clock set to a
// tick, after a start (a rebuild from the record) or with none.
type standingStep struct {
	clock int64
	start bool
	want  string
}

func (f *anchorFixture) walk(id string, steps []standingStep) {
	f.t.Helper()
	for _, step := range steps {
		f.setTicks(step.clock)
		if step.start {
			if err := f.s.ReplayAll(EventSlice(f.events)); err != nil {
				f.t.Fatalf("a start under a clock at %d: %v", step.clock, err)
			}
		}
		if got := mustStanding(f.t, f.s, id); got != step.want {
			f.t.Fatalf("%s, the clock at %d (start here: %v): standing %q, want %s", id, step.clock, step.start, got, step.want)
		}
	}
}

// AN ANCHOR A START FINDS AHEAD OF THE CLOCK COUNTS FROM THAT START. A
// clock lost with its database restarts at zero, and a record brought
// from another life carries that life's ticks: waiting for the clock to
// pass the recorded anchor would make the identity wait out ticks they
// never lived on it. The materializer used to see to this by stamping
// the lower of the recorded tick and the clock
// (TestTheAnchorNeverLiesInTheClocksFuture held that stamp). The column
// is the record's now, and the READ holds the same thing: trusted fifty
// ticks after the start, no sooner; still trusted when the clock passes
// the recorded anchor; counted again from each start; and the column the
// recorded 100 throughout.
func TestAnAnchorAheadOfTheClockCountsFromTheStart(t *testing.T) {
	f := confirmedFixture(t)
	f.walk("b", []standingStep{
		{30, true, "confirmed"}, // a start under a clock behind the anchor
		{79, false, "confirmed"},
		{80, false, "trusted"},  // fifty ticks after that start
		{100, false, "trusted"}, // the clock reaches the recorded anchor
		{149, false, "trusted"}, // and passes it: nothing is taken back
		{150, false, "trusted"},
		{0, true, "confirmed"}, // a clock that restarted at zero
		{49, false, "confirmed"},
		{50, false, "trusted"},
		{60, true, "confirmed"}, // each start counts its own fifty
		{110, false, "trusted"},
	})
	if got := f.anchor("b"); got != 100 {
		t.Fatalf("the column moved with the clock: %d, want the recorded 100", got)
	}
}

// AN ANCHOR BEHIND THE CLOCK IS READ AS IT ALWAYS WAS: fifty ticks past
// the recorded anchor, whether or not a start comes between. This is every
// identity whose clock was never lost.
func TestAnAnchorBehindTheClockIsReadAsItAlwaysWas(t *testing.T) {
	f := confirmedFixture(t)
	f.walk("b", []standingStep{
		{120, false, "confirmed"},
		{149, false, "confirmed"},
		{150, false, "trusted"},
		{149, true, "confirmed"}, // a start changes nothing: 49 past the anchor
		{150, false, "trusted"},
		{100, true, "confirmed"}, // a start at the anchor itself is not ahead of it
		{149, false, "confirmed"},
		{150, false, "trusted"},
		{5000, true, "trusted"},
	})
}

// A CONFIRMATION MADE WHILE THE PROCESS RUNS WAITS ITS OWN FIFTY TICKS,
// in a process whose start found other anchors ahead of the clock. Its
// anchor is the clock's own tick, above the clock the start found, and a
// rule that took the lower of the two for every belief would call it
// trusted the moment it was confirmed.
func TestAFreshConfirmationWaitsItsFiftyTicksWhereTheStartFoundTheClockBehind(t *testing.T) {
	f := confirmedFixture(t)
	f.mat(ledger.EventBeliefUpsert, 3, map[string]any{"id": "fresh", "statement": "new here", "ring": 3, "confidence": 0.5})
	for _, x := range []string{"x1", "x2", "x3"} {
		f.mat(ledger.EventEdgeCreate, 3, map[string]any{"id": "f_" + x, "from_id": x, "to_id": "fresh", "edge_type": "SUPPORTS"})
	}
	f.walk("b", []standingStep{{30, true, "confirmed"}, {80, false, "trusted"}})
	f.setTicks(90)
	f.mat(ledger.EventConsolidationRun, 3, f.run("x2", map[string]any{"id": "fresh", "ticks": 90}))
	f.walk("fresh", []standingStep{
		{90, false, "confirmed"}, // confirmed this tick: not trusted for having outrun the start's clock
		{139, false, "confirmed"},
		{140, false, "trusted"},
	})
}

// A START THAT OPENS THE DATABASE TAKES THE CLOCK AS IT FINDS IT, before
// any standing is read. A belief whose recorded anchor (100) lies ahead of
// the clock the opening found (30) is trusted fifty ticks after that
// opening, at 80, however late its first standing read comes. Taken at
// the first read instead, the count would start from whatever the clock
// had reached by then.
func TestAnOpeningTakesTheClockItFindsBeforeAnyStandingIsRead(t *testing.T) {
	path := filepath.Join(t.TempDir(), "anchors.db")
	s, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	f := confirmedFixtureOn(t, s)
	f.setTicks(30) // the clock a lost database restarts behind its record
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	f.s = reopened
	f.walk("b", []standingStep{
		{79, false, "confirmed"}, // the first standing read since the opening, at 79
		{80, false, "trusted"},   // fifty ticks after the clock the opening found
	})
	if got := f.anchor("b"); got != 100 {
		t.Fatalf("the column is %d; the record says 100", got)
	}
}

func TestAReplayUnderAClockNeverLostGivesTheBeliefsItGaveBefore(t *testing.T) {
	f := newAnchorFixture(t)
	f.mat(ledger.EventBeliefUpsert, 3, map[string]any{"id": "c", "statement": "second", "ring": 3, "confidence": 0.5})
	f.mat(ledger.EventBeliefUpsert, 3, map[string]any{"id": "d", "statement": "third", "ring": 3, "confidence": 0.5})
	for _, x := range []string{"x3", "y1", "y2", "y3"} {
		f.mat(ledger.EventExperienceCreate, 3, map[string]any{"id": x, "content": "obs", "category": "observation"})
	}
	for _, b := range []string{"b", "c", "d"} {
		for _, x := range []string{"x1", "x2", "x3"} {
			f.mat(ledger.EventEdgeCreate, 3, map[string]any{"id": b + "_" + x, "from_id": x, "to_id": b, "edge_type": "SUPPORTS"})
		}
	}
	// Each marker records the clock's tick as it was written (the
	// producer reads it); the clock runs on between them.
	first := map[string]int64{}
	for _, m := range []struct {
		clock, tick int64
		input       string
		ids         []string
	}{
		{100, 100, "y1", []string{"b"}},
		{180, 180, "y2", []string{"c", "b"}}, // b crosses again: its first stamp stands
		{260, 260, "y3", []string{"d"}},
	} {
		f.setTicks(m.clock)
		var crossings []map[string]any
		for _, id := range m.ids {
			crossings = append(crossings, map[string]any{"id": id, "ticks": m.tick})
			if _, seen := first[id]; !seen {
				first[id] = m.tick
			}
		}
		f.mat(ledger.EventConsolidationRun, 3, f.run(m.input, crossings...))
	}
	const beliefs = `SELECT * FROM beliefs ORDER BY id`
	standings := func() string {
		var out []string
		for _, id := range []string{"b", "c", "d"} {
			out = append(out, id+"="+mustStanding(t, f.s, id))
		}
		return strings.Join(out, " ")
	}
	for _, clock := range []int64{260, 5000} {
		f.setTicks(clock)
		written := fmt.Sprint(startupQueryRows(t, f.s, beliefs))
		standing := standings()
		if err := f.s.ReplayAll(EventSlice(f.events)); err != nil {
			t.Fatalf("a start under a clock at %d: %v", clock, err)
		}
		if got := fmt.Sprint(startupQueryRows(t, f.s, beliefs)); got != written {
			t.Fatalf("a start under a clock at %d changed the beliefs:\n before %s\n after  %s", clock, written, got)
		}
		if got := standings(); got != standing {
			t.Fatalf("a start under a clock at %d changed the standings: %s, before %s", clock, got, standing)
		}
		for id, tick := range first {
			if got, before := f.anchor(id), min(tick, clock); got != before {
				t.Fatalf("under a clock at %d %s is anchored at %d; the stamp before 2026-10-13, the lower of the recorded %d and the clock, was %d", clock, id, got, tick, before)
			}
		}
	}
	if got := standings(); got != "b=trusted c=trusted d=trusted" {
		t.Fatalf("under a clock at 5000 the standings are %s", got)
	}
}

// A LIVE CROSSING IS NEVER AHEAD OF THE CLOCK. The one producer reads the
// clock when it observes a crossing, so a crossing admitted at a tick
// ahead of it did not come from that producer; it would stand as an
// anchor until the clock passed it, counted from each start meanwhile.
// Admission refuses it (the 0.2.0 plan's Q-1 under row 3.10); a crossing
// at the clock's tick is admitted; and replay stays as written: the same
// marker in the record still stamps its own tick under any clock.
func TestALiveCrossingAheadOfTheClockIsRefusedAtAdmissionAndReplayIsUnchanged(t *testing.T) {
	f := newAnchorFixture(t)
	f.setTicks(30)
	ahead, err := json.Marshal(f.run("x1", map[string]any{"id": "b", "ticks": 100}))
	if err != nil {
		t.Fatal(err)
	}
	err = f.s.ValidateEvent(ledger.EventConsolidationRun, 3, ahead)
	if err == nil || !strings.Contains(err.Error(), "ahead of the lived clock (30)") {
		t.Fatalf("a crossing at tick 100 under a clock at 30 was not refused at admission: %v", err)
	}
	at, err := json.Marshal(f.run("x1", map[string]any{"id": "b", "ticks": 30}))
	if err != nil {
		t.Fatal(err)
	}
	if err := f.s.ValidateEvent(ledger.EventConsolidationRun, 3, at); err != nil {
		t.Fatalf("a crossing at the clock's own tick was refused: %v", err)
	}
	// A malformed crossing is still the materializer's to name.
	ghost, _ := json.Marshal(f.run("x1", map[string]any{"id": "nobody", "ticks": 100}))
	var missing *MissingEventTargetError
	if err := f.s.ValidateEvent(ledger.EventConsolidationRun, 3, ghost); !errors.As(err, &missing) || missing.ID != "nobody" {
		t.Fatalf("a ghost belief ahead of the clock: want the materializer's refusal, got %v", err)
	}
	// Replay reads no clock and refuses nothing of the record.
	f.mat(ledger.EventConsolidationRun, 3, f.run("x1", map[string]any{"id": "b", "ticks": 100}))
	if err := f.s.ReplayAll(EventSlice(f.events)); err != nil {
		t.Fatalf("replay of a record whose crossing is ahead of the clock: %v", err)
	}
	if got := f.anchor("b"); got != 100 {
		t.Fatalf("replayed, the anchor is %d; the record says 100", got)
	}
}
