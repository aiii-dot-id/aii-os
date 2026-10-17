package cognitive

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/ring"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

// duringSimple is a model during whose call something else happens: the
// settings door or a reload, which wait for no pass.
type duringSimple struct {
	*mockLLM
	during func()
}

func (m *duringSimple) ChatSimple(ctx context.Context, system, user string) (string, string, error) {
	text, id, err := m.mockLLM.ChatSimple(ctx, system, user)
	if m.during != nil {
		m.during()
	}
	return text, id, err
}

// duringFit is a model whose client is asked whether a request fits, and
// during whose first answer something else happens: between the fit of a
// request and its sending.
type duringFit struct {
	*mockLLM
	admitted string
	during   func()
}

func (m *duringFit) CheckSimple(_ context.Context, _, user string) error {
	m.admitted = user
	if m.during != nil {
		m.during()
		m.during = nil
	}
	return nil
}

func TestASettingChangedWhileADreamIsWithTheModelActsAtTheNextPass(t *testing.T) {
	dream := func(t *testing.T, before, after DreamConfig, note string) (*mockStore, *mockLedger, *duringSimple, *mockRingWriter, []Condition) {
		t.Helper()
		st := &mockStore{unprocessedCnt: 1, experiences: []store.Experience{{ID: "e1", Content: "the operator shipped at midnight", Raw: 1}}}
		lg := &mockLedger{st: st}
		model := &duringSimple{mockLLM: &mockLLM{override: note}}
		rw := &mockRingWriter{}
		d := NewDream(st, model, lg, rw, before)
		model.during = func() { d.Reconfigure(after) }
		var said []Condition
		d.SetTeller(&Teller{Report: func(c Condition) { said = append(said, c) }, Door: rememberingLedger{lg}, Exists: st.EntityExists})
		if err := d.Execute(context.Background()); err != nil {
			t.Fatal(err)
		}
		if got := d.cfg().MaxChars; got != after.MaxChars {
			t.Fatalf("rig: the change is in force once the pass is over: bound %d, want %d", got, after.MaxChars)
		}
		return st, lg, model, rw, said
	}
	note := strings.Repeat("n", 300)

	t.Run("the note's bound lowered over a pass", func(t *testing.T) {
		st, _, model, rw, said := dream(t, DreamConfig{Threshold: 1, MaxChars: 2000}, DreamConfig{Threshold: 1, MaxChars: 100}, note)
		if !strings.Contains(model.lastSystem, "2000") {
			t.Fatalf("rig: the prompt states the bound the pass began under:\n%s", model.lastSystem)
		}
		if st.unprocessedCnt != 0 || rw.section(ring.Ring3, "surfacing") != note {
			t.Fatalf("A NOTE INSIDE THE BOUND ITS PROMPT STATED WAS REFUSED BY A BOUND THAT CAME LATER: %d still raw, surfacing holds %d characters, the card was told %+v",
				st.unprocessedCnt, len(rw.section(ring.Ring3, "surfacing")), said)
		}
	})

	t.Run("the note's bound raised over a pass", func(t *testing.T) {
		st, lg, model, rw, said := dream(t, DreamConfig{Threshold: 1, MaxChars: 100}, DreamConfig{Threshold: 1, MaxChars: 2000}, note)
		if !strings.Contains(model.lastSystem, "100") {
			t.Fatalf("rig: the prompt states the bound the pass began under:\n%s", model.lastSystem)
		}
		want := "the last dream note was 300 characters, over the 100-character bound (prompt.surfacing_max_chars)"
		if st.unprocessedCnt != 1 || len(lg.appended) != 0 || rw.section(ring.Ring3, "surfacing") != "" || len(said) == 0 || !strings.HasPrefix(said[len(said)-1].Said, want) {
			t.Fatalf("A NOTE ITS PROMPT SAID WOULD BE REFUSED WAS KEPT: %d still raw, the door admitted %v, surfacing holds %d characters, the card was told %+v",
				st.unprocessedCnt, lg.appended, len(rw.section(ring.Ring3, "surfacing")), said)
		}
	})

	// The contradiction view's bound, changed between the fit and the send:
	// the request the client admitted is the request that goes.
	t.Run("the contradiction view's bound lowered between the fit and the send", func(t *testing.T) {
		st := &mockStore{unprocessedCnt: 1, experiences: []store.Experience{{ID: "e1", Content: "the operator shipped at midnight", Raw: 1}}}
		for i := 0; i < 8; i++ {
			st.beliefs = append(st.beliefs, store.Belief{ID: fmt.Sprintf("b%d", i), Ring: 3, Statement: fmt.Sprintf("Belief %d: ships go at midnight, or they do not, number %d", i, i)})
		}
		for i := 0; i < 8; i += 2 {
			st.tensions = append(st.tensions, store.TensionPair{LeftID: fmt.Sprintf("b%d", i), RightID: fmt.Sprintf("b%d", i+1), EdgeID: fmt.Sprintf("edge%d", i)})
		}
		model := &duringFit{mockLLM: &mockLLM{override: "You notice a pattern in what happened."}}
		d := NewDream(st, model, &mockLedger{st: st}, &mockRingWriter{}, DreamConfig{Threshold: 1, TensionsMaxChars: 4000})
		d.SetTensions(st)
		model.during = func() { d.Reconfigure(DreamConfig{Threshold: 1, TensionsMaxChars: 150}) }
		if err := d.Execute(context.Background()); err != nil {
			t.Fatal(err)
		}
		if pairs := strings.Count(model.admitted, "stands against"); pairs < 2 {
			t.Fatalf("rig: the request the client admitted shows the standing contradictions under the bound of 4000: %d pair line(s)\n%s", pairs, model.admitted)
		}
		if model.calls != 1 || model.lastUser != model.admitted {
			t.Fatalf("THE REQUEST SENT IS NOT THE REQUEST THE CLIENT ADMITTED: %d call(s); admitted %d characters with %d pair line(s), sent %d with %d",
				model.calls, len(model.admitted), strings.Count(model.admitted, "stands against"), len(model.lastUser), strings.Count(model.lastUser, "stands against"))
		}
	})
}

func TestABriefTurnedOffWhileItIsWithTheModelIsNotWritten(t *testing.T) {
	run := func(turnOff bool) (*mockBriefWriter, *MorningBriefFacility, *duringSimple, []Condition) {
		bw := &mockBriefWriter{brief: "yesterday's bridge"}
		model := &duringSimple{mockLLM: &mockLLM{override: "A short bridge, written this morning."}}
		mb := NewMorningBrief(selfModelStore(), model, bw, MorningBriefConfig{LocalTime: "07:00"})
		var said []Condition
		mb.SetTeller(&Teller{Report: func(c Condition) { said = append(said, c) }})
		if turnOff {
			// What the app does when the operator turns the brief off.
			model.during = func() { mb.Reconfigure(MorningBriefConfig{LocalTime: MorningBriefOff}) }
		}
		if res := mb.OnAlarm(context.Background(), "morning_brief", "wall", 0, ""); !res.Accepted || model.calls != 1 {
			t.Fatalf("rig: the alarm ran one brief: accepted %v, %d call(s)", res.Accepted, model.calls)
		}
		return bw, mb, model, said
	}
	if bw, mb, _, _ := run(false); mb.Off() || bw.brief != "A short bridge, written this morning." {
		t.Fatalf("rig: a brief nobody turned off is written: off %v, the brief held is %q", mb.Off(), bw.brief)
	}
	bw, mb, model, said := run(true)
	if !mb.Off() || bw.brief != "" {
		t.Fatalf("THE BRIEF IS OFF, THE APP TOOK IT AWAY, AND A BRIEF STANDS: off %v, the brief held is %q", mb.Off(), bw.brief)
	}
	for _, c := range said {
		if c.Facility == "brief" {
			t.Fatalf("the card was told of a brief that was not kept: %+v", c)
		}
	}
	// Turned on again, the next brief is written: the clearing held back
	// the one pass it met, and no other.
	model.during = nil
	mb.Reconfigure(MorningBriefConfig{LocalTime: "07:00"})
	if res := mb.OnAlarm(context.Background(), "morning_brief", "wall", 0, ""); !res.Accepted || bw.brief != "A short bridge, written this morning." {
		t.Fatalf("a brief turned on again is written at its next alarm: accepted %v, the brief held is %q", res.Accepted, bw.brief)
	}
}

// gateHook is a turn gate at whose taking something else happens: between
// an alarm's dispatch and the pass it runs.
type gateHook struct{ taking func() }

func (g *gateHook) TryBeginTurn() bool {
	if g.taking != nil {
		g.taking()
	}
	return true
}
func (g *gateHook) EndTurn() {}

// lockedBrief is a brief writer two goroutines may reach.
type lockedBrief struct {
	mu    sync.Mutex
	brief string
}

func (b *lockedBrief) SetBrief(content string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.brief = content
}

func (b *lockedBrief) held() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.brief
}

func TestNoBriefStandsWhileTheBriefIsOffWheneverItWasTurnedOff(t *testing.T) {
	off := func(mb *MorningBriefFacility) func() {
		return func() { mb.Reconfigure(MorningBriefConfig{LocalTime: MorningBriefOff}) } // all the app does
	}
	t.Run("between the alarm and its pass", func(t *testing.T) {
		bw := &mockBriefWriter{brief: "yesterday's bridge"}
		model := &mockLLM{override: "A short bridge, written this morning."}
		mb := NewMorningBrief(selfModelStore(), model, bw, MorningBriefConfig{LocalTime: "07:00"})
		mb.SetTurnGate(&gateHook{taking: off(mb)})
		res := mb.OnAlarm(context.Background(), "morning_brief", "wall", 0, "")
		if !res.Accepted || res.NextDeadline == nil || model.calls != 0 || bw.brief != "" {
			t.Fatalf("A BRIEF TURNED OFF BEFORE ITS PASS BEGAN WAS ASKED FOR OR WRITTEN: accepted %v, armed again %v, %d call(s), the brief held is %q", res.Accepted, res.NextDeadline != nil, model.calls, bw.brief)
		}
	})
	t.Run("during the first of two asks", func(t *testing.T) {
		bw := &mockBriefWriter{brief: "yesterday's bridge"}
		model := &duringSimple{mockLLM: &mockLLM{override: strings.Repeat("a long bridge. ", 20)}}
		mb := NewMorningBrief(selfModelStore(), model, bw, MorningBriefConfig{LocalTime: "07:00", MaxChars: 100})
		model.during = off(mb)
		if res := mb.OnAlarm(context.Background(), "morning_brief", "wall", 0, ""); !res.Accepted || model.calls != 1 || bw.brief != "" {
			t.Fatalf("A BRIEF TURNED OFF DURING ITS FIRST ASK WAS ASKED FOR AGAIN OR WRITTEN: accepted %v, %d call(s), the brief held is %q", res.Accepted, model.calls, bw.brief)
		}
	})
	// The door and a pass at once, as they meet in a running identity:
	// however they interleave, the brief is off afterwards and none stands.
	// Under the race detector this is also what holds the two writers of
	// the brief to their lock.
	t.Run("at the same moment, many times", func(t *testing.T) {
		for i := 0; i < 300; i++ {
			bw := &lockedBrief{brief: "yesterday's bridge"}
			mb := NewMorningBrief(selfModelStore(), &mockLLM{override: "A short bridge, written this morning."}, bw, MorningBriefConfig{LocalTime: "07:00"})
			var both sync.WaitGroup
			both.Add(2)
			go func() { defer both.Done(); mb.OnAlarm(context.Background(), "morning_brief", "wall", 0, "") }()
			go func() { defer both.Done(); off(mb)() }()
			both.Wait()
			if got := bw.held(); !mb.Off() || got != "" {
				t.Fatalf("run %d: THE BRIEF IS OFF AND A BRIEF STANDS: off %v, the brief held is %q", i, mb.Off(), got)
			}
		}
	})
}

func TestABriefsBoundChangedWhileItIsWithTheModelActsAtTheNextBrief(t *testing.T) {
	brief := strings.Repeat("b", 300)
	run := func(before, after int) (*mockBriefWriter, *duringSimple) {
		bw := &mockBriefWriter{}
		model := &duringSimple{mockLLM: &mockLLM{override: brief}}
		mb := NewMorningBrief(selfModelStore(), model, bw, MorningBriefConfig{LocalTime: "07:00", MaxChars: before})
		model.during = func() { mb.Reconfigure(MorningBriefConfig{LocalTime: "07:00", MaxChars: after}) }
		if err := mb.Execute(context.Background()); err != nil {
			t.Fatal(err)
		}
		return bw, model
	}
	if bw, model := run(1200, 100); bw.brief != brief || model.calls != 1 {
		t.Fatalf("A BRIEF INSIDE THE BOUND ITS PROMPT STATED WAS REFUSED BY A BOUND THAT CAME LATER: %d call(s), the brief held is %d characters", model.calls, len(bw.brief))
	}
	if bw, model := run(100, 2000); bw.brief == brief || model.calls != 2 {
		t.Fatalf("A BRIEF OVER THE BOUND ITS PROMPT STATED WAS KEPT UNDER A BOUND THAT CAME LATER: %d call(s), the brief held is %d characters", model.calls, len(bw.brief))
	}
}

func TestTheBriefsAlarmIsHeldWhileOffAndArmedAgainOnlyByItsOwnSetting(t *testing.T) {
	mb := NewMorningBrief(selfModelStore(), &mockLLM{}, &mockBriefWriter{brief: "yesterday's bridge"}, MorningBriefConfig{LocalTime: "07:00"})
	day := 24 * time.Hour
	if next := time.Until(time.UnixMilli(mb.NextDeadline())); next <= 0 || next > day {
		t.Fatalf("rig: on at 07:00 the next alarm is within a day: %s", next)
	}
	for _, c := range []struct {
		what string
		cfg  MorningBriefConfig
		want bool
	}{
		{"the same hour again", MorningBriefConfig{LocalTime: "07:00"}, false},
		{"the bound alone", MorningBriefConfig{LocalTime: "07:00", MaxChars: 900}, false},
		{"another hour", MorningBriefConfig{LocalTime: "08:30", MaxChars: 900}, true},
		{"another zone", MorningBriefConfig{LocalTime: "08:30", Timezone: "America/New_York"}, true},
		{"off", MorningBriefConfig{LocalTime: MorningBriefOff, Timezone: "America/New_York"}, true},
		{"off again", MorningBriefConfig{LocalTime: MorningBriefOff, Timezone: "America/New_York"}, false},
	} {
		if got := mb.Reconfigure(c.cfg); got != c.want {
			t.Errorf("%s: Reconfigure says what arms the alarm changed = %v, want %v", c.what, got, c.want)
		}
	}
	if next := time.Until(time.UnixMilli(mb.NextDeadline())); next < 50*365*day {
		t.Fatalf("OFF, THE BRIEF'S ALARM IS WITHIN THE CLOCK'S REACH: due in %s", next)
	}
	if !mb.Reconfigure(MorningBriefConfig{LocalTime: "07:00"}) {
		t.Fatal("on again: what arms the alarm changed")
	}
	if next := time.Until(time.UnixMilli(mb.NextDeadline())); next <= 0 || next > day {
		t.Fatalf("on again at 07:00 the next alarm is within a day: %s", next)
	}
}

// offAsker is a brief writer that asks, when the brief is taken away,
// whether "off" is in force yet.
type offAsker struct {
	mb       *MorningBriefFacility
	brief    string
	tooEarly int
}

func (w *offAsker) SetBrief(content string) {
	if content == "" && w.mb != nil && !w.mb.Off() {
		w.tooEarly++
	}
	w.brief = content
}

func TestOffIsInForceWhenTheBriefIsTakenAway(t *testing.T) {
	bw := &offAsker{brief: "yesterday's bridge"}
	mb := NewMorningBrief(selfModelStore(), &mockLLM{}, bw, MorningBriefConfig{LocalTime: "07:00"})
	bw.mb = mb
	mb.Reconfigure(MorningBriefConfig{LocalTime: MorningBriefOff})
	if bw.brief != "" || bw.tooEarly != 0 || !mb.Off() {
		t.Fatalf("THE BRIEF WAS TAKEN AWAY WITH \"off\" NOT YET IN FORCE (%d time(s)); off now %v, the brief held is %q", bw.tooEarly, mb.Off(), bw.brief)
	}
}
