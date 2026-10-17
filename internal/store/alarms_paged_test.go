package store

import (
	"fmt"
	"path/filepath"
	"testing"
)

// DueAlarmsAfter pages the due set by key in the scheduler's own order:
// each call resumes strictly after the last key of the previous one, so
// a walk reads every due row exactly once however many there are — and
// rows that never transition (SAFE) never pin a fixed LIMIT to the same
// first page.
func TestDueAlarmsAfterPagesTheDueSetByKey(t *testing.T) {
	st, err := New(filepath.Join(t.TempDir(), "a.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	// 7 rows: three share deadline 100 (order by id inside a deadline),
	// two at 200, one at 300, and one future row at 900.
	rows := []struct {
		id string
		at int64
	}{
		{"c", 100}, {"a", 100}, {"b", 100}, {"e", 200}, {"d", 200}, {"f", 300}, {"z", 900},
	}
	for _, r := range rows {
		if err := st.SetAlarm(r.id, "timers", "wall", r.at, nil, ""); err != nil {
			t.Fatal(err)
		}
	}
	var walked []string
	afterDeadline, afterID := int64(-1)<<62, ""
	for {
		page, err := st.DueAlarmsAfter("wall", 300, afterDeadline, afterID, 2)
		if err != nil {
			t.Fatal(err)
		}
		for _, a := range page {
			walked = append(walked, fmt.Sprintf("%s@%d", a.AlarmID, a.Deadline))
		}
		if len(page) < 2 {
			break
		}
		afterDeadline, afterID = page[len(page)-1].Deadline, page[len(page)-1].AlarmID
	}
	want := []string{"a@100", "b@100", "c@100", "d@200", "e@200", "f@300"}
	if fmt.Sprint(walked) != fmt.Sprint(want) {
		t.Fatalf("walk = %v, want %v", walked, want)
	}
	// The continuation after the last due key is the first row not yet due.
	next, err := st.DueAlarmsAfter("wall", int64(1)<<62, 300, "f", 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(next) != 1 || next[0].AlarmID != "z" {
		t.Fatalf("after the last due key comes the first future row, got %+v", next)
	}
	// A read: it answers under the freeze.
	st.SetFrozen(true, "test")
	if page, err := st.DueAlarmsAfter("wall", 300, int64(-1)<<62, "", 10); err != nil || len(page) != 6 {
		t.Fatalf("frozen read = %d rows, %v", len(page), err)
	}
}
