package app

import (
	"fmt"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/interaction"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

// A RECORD AT THE WINDOW'S OWN INSTANT IS IN THE WINDOW (the Windows
// guest, 2026-11-07: TestNoteNeverDropsAFailure read one failure short
// because the last record and the note's keep carried the same stamp, and
// the store's "recorded_at < until" put the record outside). The bounds
// are spelled as the store's stamps are, and the end is the instant the
// note was kept, inclusive.
func TestAFailureAtTheWindowsOwnInstantIsInTheWindow(t *testing.T) {
	st, err := store.NewMemory()
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	a := &App{store: st}
	if err := st.RecordToolStart("turn_edge", 1, "main", "m", "", "shell", "{}"); err != nil {
		t.Fatal(err)
	}
	if err := st.RecordToolDoneMeasured("turn_edge", 1, "shell", "{}", "no", interaction.Failed, false, store.ToolMeasurement{}); err != nil {
		t.Fatal(err)
	}
	// The record's own stamp, as the store holds it.
	p, err := a.QueryInteractions(t.Context(), interaction.Query{Version: 1, Source: "recorded", Filter: interaction.Filter{Kinds: []interaction.Kind{interaction.ToolResult}}, Limit: 5})
	if err != nil || len(p.Rows) != 1 {
		t.Fatalf("the record was not read back: %v %d", err, len(p.Rows))
	}
	at, err := time.Parse(time.RFC3339Nano, p.Rows[0].RecordedAt)
	if err != nil {
		t.Fatal(err)
	}
	// From and until both at the record's instant: the window holds it.
	out, _, err := a.outcomesBetween(t.Context(), at, at)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 1 {
		t.Fatalf("a failure at the window's own instant (from = until = %s) was read as %d outcomes; want 1", at.Format(time.RFC3339Nano), len(out))
	}
	// And one nanosecond later it is not: the window's end is the keep.
	out, _, err = a.outcomesBetween(t.Context(), at.Add(time.Nanosecond), at.Add(time.Nanosecond))
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 0 {
		t.Fatalf("a failure before the window's start was read: %d", len(out))
	}
}

// A BUSY WINDOW STILL HAS A NOTE (an identity's log, 2026-11-05 to 11-08:
// "the note to the operator was not written: what did not succeed could not
// be read (the window holds more than 2000 records)", every morning, since
// the next window starts where the last kept note ended). The bound is on
// the records that did not succeed: more than 2,000 that did, and one that
// failed, read as that one.
func TestABusyWindowReadsOnlyWhatDidNotSucceed(t *testing.T) {
	st, err := store.NewMemory()
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	a := &App{store: st}
	since := time.Now().UTC().Add(-time.Second)
	ok := noteOutcomesPages*50 + 1
	for i := 1; i <= ok+1; i++ {
		outcome := interaction.Succeeded
		if i == ok+1 {
			outcome = interaction.Failed
		}
		if _, err := st.RecordInteraction(t.Context(), store.InteractionInput{ID: fmt.Sprintf("notice_busy_%d", i), Kind: interaction.Notice, Role: interaction.System, SessionID: "main", Content: "done", Outcome: outcome}); err != nil {
			t.Fatal(err)
		}
	}
	out, beyond, err := a.outcomesBetween(t.Context(), since, time.Time{})
	if err != nil {
		t.Fatalf("a window of %d records that succeeded and one that failed was refused: %v", ok, err)
	}
	if len(out) != 1 || out[0].Outcome != string(interaction.Failed) || beyond {
		t.Fatalf("the window read as %+v (beyond %v); want its one failure", out, beyond)
	}
}

// A WINDOW WITH MORE FAILURES THAN THE READ TAKES IS READ TO THE BOUND AND
// SAID AS HOLDING MORE, never refused: a refusal kept the next window where
// this one began, and it only grew.
func TestAWindowFullerThanTheReadIsReadToItsBoundAndSaysSo(t *testing.T) {
	st, err := store.NewMemory()
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	a := &App{store: st}
	since := time.Now().UTC().Add(-time.Second)
	bound := noteOutcomesPages * 50
	for i := 1; i <= bound+1; i++ {
		if _, err := st.RecordInteraction(t.Context(), store.InteractionInput{ID: fmt.Sprintf("notice_failed_%d", i), Kind: interaction.Notice, Role: interaction.System, SessionID: "main", Content: "did not go", Outcome: interaction.Failed}); err != nil {
			t.Fatal(err)
		}
	}
	out, beyond, err := a.outcomesBetween(t.Context(), since, time.Time{})
	if err != nil {
		t.Fatalf("a window of %d failures was refused: %v", bound+1, err)
	}
	if !beyond || len(out) != bound {
		t.Fatalf("the window read as %d outcomes (beyond %v); want %d read and beyond", len(out), beyond, bound)
	}
	// The read takes the newest: the last failure recorded is among them,
	// and the first is not.
	read := map[string]bool{}
	for _, o := range out {
		read[o.ID] = true
	}
	if !read[fmt.Sprintf("notice_failed_%d", bound+1)] || read["notice_failed_1"] {
		t.Fatalf("the read past its bound did not take the newest failures (newest read %v, oldest read %v)", read[fmt.Sprintf("notice_failed_%d", bound+1)], read["notice_failed_1"])
	}
	for _, o := range out {
		if o.Outcome != string(interaction.Failed) {
			t.Fatalf("the read took a record that did not fail: %+v", o)
		}
	}
}
