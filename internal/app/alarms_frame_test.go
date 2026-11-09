package app

import (
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/store"
)

// The work frame carries the identity's own timers and nothing else: each
// with its tag and message as the identity set them, when it is due, how
// often it repeats, and when it last fired; the rhythm's deadline rides
// beside them as next_pass_at, never among them (F-DASH-039; spec 1a and
// step 5).
func TestTheWorkFrameCarriesTheIdentitysOwnTimers(t *testing.T) {
	a := New(&Config{SourcePath: filepath.Join(t.TempDir(), "config.json")})
	st, err := store.NewForTest(filepath.Join(t.TempDir(), "alarms.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	a.store = st

	now := time.Now().UnixMilli()
	every := int64(24 * 60 * 60 * 1000)
	if err := st.SetAlarm("t_later", "timers", "wall", now+7_200_000, nil, `{"tag":"ops","message":"check the build"}`); err != nil {
		t.Fatal(err)
	}
	if err := st.SetAlarm("t_daily", "timers", "wall", now+3_600_000, &every, `{"tag":"","message":"morning review"}`); err != nil {
		t.Fatal(err)
	}
	if err := st.SetAlarm("rhythm", "rhythm", "wall", now+1_800_000, &every, ""); err != nil {
		t.Fatal(err)
	}
	if err := st.SetAlarm("reflect:self", "reflect", "life", 99, nil, ""); err != nil {
		t.Fatal(err)
	}
	if err := st.AddOutboxMessage("timer_t_daily_"+wallStamp(now-86_400_000), "operator", "", "[timer t_daily fired] morning review", nil); err != nil {
		t.Fatal(err)
	}

	w, err := a.workQueueState()
	if err != nil {
		t.Fatal(err)
	}
	if len(w.Alarms) != 2 {
		t.Fatalf("the frame must carry the two timers and no other owner's alarm: %+v", w.Alarms)
	}
	if w.Alarms[0].ID != "t_daily" || w.Alarms[1].ID != "t_later" {
		t.Fatalf("timers must come in deadline order: %+v", w.Alarms)
	}
	daily, later := w.Alarms[0], w.Alarms[1]
	if daily.Message != "morning review" || daily.Every == nil || *daily.Every != every || daily.DueAt != now+3_600_000 {
		t.Fatalf("the repeating timer must carry its message, its period and its deadline: %+v", daily)
	}
	if daily.FiredAt == nil {
		t.Fatalf("the timer that fired must carry when it last fired: %+v", daily)
	}
	if later.Tag != "ops" || later.Message != "check the build" || later.Every != nil || later.FiredAt != nil {
		t.Fatalf("the one-shot timer must carry its tag and message, no period and no firing: %+v", later)
	}
	if w.NextPassAt == nil || *w.NextPassAt != now+1_800_000 {
		t.Fatalf("next_pass_at must be the rhythm alarm's deadline, not %v", w.NextPassAt)
	}

	raw, err := json.Marshal(w)
	if err != nil {
		t.Fatal(err)
	}
	var frame map[string]json.RawMessage
	if err := json.Unmarshal(raw, &frame); err != nil {
		t.Fatal(err)
	}
	if _, ok := frame["alarms"]; !ok {
		t.Fatalf("the frame must carry alarms by that name: %s", raw)
	}
	if _, ok := frame["next_pass_at"]; !ok {
		t.Fatalf("the frame must carry next_pass_at by that name: %s", raw)
	}
}

// With no timers and no rhythm alarm the frame says so: an empty list, no
// next_pass_at — never a missing member the page could read as unread.
func TestTheWorkFrameSaysNoTimersAsAnEmptyList(t *testing.T) {
	a := New(&Config{SourcePath: filepath.Join(t.TempDir(), "config.json")})
	st, err := store.NewForTest(filepath.Join(t.TempDir(), "alarms.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	a.store = st
	w, err := a.workQueueState()
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(w)
	var frame map[string]json.RawMessage
	_ = json.Unmarshal(raw, &frame)
	if string(frame["alarms"]) != "[]" {
		t.Fatalf("no timers must be an empty list, not %s", frame["alarms"])
	}
	if _, has := frame["next_pass_at"]; has {
		t.Fatalf("with no rhythm alarm next_pass_at must be absent: %s", raw)
	}
}

func wallStamp(n int64) string {
	return time.UnixMilli(0).Add(time.Duration(n) * time.Millisecond).Format("20060102150405")
}
