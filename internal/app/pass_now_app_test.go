package app

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/cognitive"
	"github.com/aiii-dot-id/aii-os/internal/dashboard"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

// The press through the real App: the turn gate's own fact refuses it, the
// dream's own stall stamp refuses it, an absent rhythm alarm is "absent",
// and a future recurring one is brought to now with TIME's clock as the
// time asked (F-COG-110).
func TestPassNowThroughTheRealApp(t *testing.T) {
	a := newSteerApp(t)
	st, err := store.NewForTest(filepath.Join(t.TempDir(), "pass.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	a.timeFac = cognitive.NewTIME(st, st)
	a.timeFac.RegisterOwner(rhythmStub{})
	dream := &cognitive.DreamFacility{}
	a.cognition.rhythm.Store(&rhythmSet{dream: dream})
	ctx := context.Background()

	res, err := a.PassNow(ctx)
	if err != nil || res.Moved || res.Reason != cognitive.PressAbsent {
		t.Fatalf("with no rhythm alarm the press must be refused absent: %+v %v", res, err)
	}

	period := int64(3_600_000)
	if err := a.armRhythm(int(period / 1000)); err != nil {
		t.Fatal(err)
	}

	if err := a.acquireTurn(ctx); err != nil {
		t.Fatal(err)
	}
	res, err = a.PassNow(ctx)
	if err != nil || res.Moved || res.Reason != passTurnHolding {
		t.Fatalf("with the turn gate held the press must be refused turn_holding: %+v %v", res, err)
	}
	a.EndTurn()

	dream.SetStalledAtForTest(time.Now(), 10*time.Minute)
	res, err = a.PassNow(ctx)
	if err != nil || res.Moved || res.Reason != passStall {
		t.Fatalf("with DREAM stalled inside its spacing the press must be refused stall: %+v %v", res, err)
	}
	dream.SetStalledAtForTest(time.Now().Add(-time.Hour), 10*time.Minute)

	res, err = a.PassNow(ctx)
	if err != nil || !res.Moved || res.Reason != "" || res.AskedAt == "" {
		t.Fatalf("a clear press must bring the rhythm's deadline to now: %+v %v", res, err)
	}
	row, ok, err := st.AlarmByID("rhythm")
	if err != nil || !ok {
		t.Fatalf("rhythm row after the press: ok=%v err=%v", ok, err)
	}
	asked, err := time.Parse("2006-01-02T15:04:05.000Z07:00", res.AskedAt)
	if err != nil {
		t.Fatalf("asked_at must be RFC 3339 UTC to the millisecond: %q (%v)", res.AskedAt, err)
	}
	if asked.UnixMilli() != row.Deadline {
		t.Fatalf("asked_at must be TIME's own clock at the move, the deadline it wrote: %d != %d", asked.UnixMilli(), row.Deadline)
	}
}

// The dashboard's handler reaches the real door: wireTurnHooks installs
// PassNow beside TurnActive, so a page's press is not "not available".
func TestPassNowIsWiredBesideTurnActive(t *testing.T) {
	a := newSteerApp(t)
	h := &dashboard.WSHandler{}
	a.wireTurnHooks(h)
	if h.TurnActive == nil || h.PassNow == nil {
		t.Fatalf("wireTurnHooks must install TurnActive and PassNow: TurnActive=%v PassNow=%v", h.TurnActive != nil, h.PassNow != nil)
	}
}

// rhythmStub stands where the rhythm owner stands, so TIME will arm the
// "rhythm" alarm; it is never dispatched in this test.
type rhythmStub struct{}

func (rhythmStub) Name() string { return "rhythm" }
func (rhythmStub) OnAlarm(context.Context, string, string, int64, string) cognitive.AlarmResult {
	return cognitive.AlarmResult{}
}
