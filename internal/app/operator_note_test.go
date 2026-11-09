package app

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/cognitive"
	"github.com/aiii-dot-id/aii-os/internal/interaction"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

// THE NOTE NEVER DROPS A FAILURE (spec 1b): what did not succeed in the
// note's window is read from the record on every read of the note, over
// the window kept with it, however long it stands; every record failed,
// unknown, cancelled or refused is listed, bounded and counted past the
// bound; a read that fails is said as unread, never as none; and the note
// is never ring content.
func TestNoteNeverDropsAFailure(t *testing.T) {
	st, err := store.NewMemory()
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	a := &App{store: st}
	// The producer's own path: a call started, then done with its outcome.
	tool := func(turn string, ordinal int, name string, outcome interaction.Outcome) {
		t.Helper()
		if err := st.RecordToolStart(turn, ordinal, "main", "m", "", name, "{}"); err != nil {
			t.Fatal(err)
		}
		if err := st.RecordToolDoneMeasured(turn, ordinal, name, "{}", "the result of "+name, outcome, false, store.ToolMeasurement{}); err != nil {
			t.Fatal(err)
		}
	}
	tool("turn_before", 1, "before_the_window", interaction.Failed)
	from := time.Now()
	tool("turn_a", 1, "send_email", interaction.Failed)
	tool("turn_a", 2, "read_file", interaction.Succeeded)
	tool("turn_a", 3, "web_fetch", interaction.Refused)
	tool("turn_a", 4, "shell", interaction.Cancelled)
	// A call the runtime ended before it finished: unknown, as the restore
	// closes it.
	if err := st.RecordToolStart("turn_a", 5, "main", "m", "", "deploy", "{}"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AbandonUnfinishedToolCalls(); err != nil {
		t.Fatal(err)
	}
	if _, err := st.RecordInteraction(t.Context(), store.InteractionInput{ID: "msg_1", Kind: interaction.Message, Role: interaction.Operator, Content: "please send it"}); err != nil {
		t.Fatal(err)
	}
	until := time.Now()
	tool("turn_after", 1, "after_the_window", interaction.Failed)

	const big = uint64(1)<<53 + 1
	keep := func(k cognitive.KeptNote) {
		t.Helper()
		content, err := json.Marshal(k)
		if err != nil {
			t.Fatal(err)
		}
		if err := st.SaveOperatorNote(string(content)); err != nil {
			t.Fatal(err)
		}
	}
	keep(cognitive.KeptNote{Note: "I sent the email. The deploy is out.", Claims: []cognitive.NoteClaim{{Sentence: "I sent the email.", Entries: []uint64{big, 7}, Outcomes: []string{}}}, Unchecked: 1, WindowFrom: from, WindowUntil: until})

	read := func(ctx context.Context) (names []string, view noteViewOf) {
		t.Helper()
		v, err := a.operatorNoteView(ctx)
		if err != nil || v == nil {
			t.Fatalf("the kept note was not read: %v", err)
		}
		for _, o := range v.NotSucceeded {
			name, _, _ := strings.Cut(o.Summary, ":")
			names = append(names, name+" "+o.Outcome)
		}
		sort.Strings(names)
		return names, noteViewOf{v.Text, v.Unchecked, v.NotSucceededUnread, v.NotSucceededMore, len(v.Claims)}
	}
	want := []string{"deploy unknown", "send_email failed", "shell cancelled", "web_fetch refused"}
	names, view := read(t.Context())
	if fmt.Sprint(names) != fmt.Sprint(want) {
		t.Fatalf("WHAT DID NOT SUCCEED IN THE NOTE'S WINDOW IS NOT ALL LISTED, OR MORE IS:\n got %v\nwant %v", names, want)
	}
	if view.unread != "" || view.more != 0 || view.text != "I sent the email. The deploy is out." || view.unchecked != 1 || view.claims != 1 {
		t.Fatalf("the note read is not the note kept: %+v", view)
	}
	// Read again later, with more failing since: the window does not slide.
	tool("turn_later", 1, "later_still", interaction.Failed)
	if again, _ := read(t.Context()); fmt.Sprint(again) != fmt.Sprint(want) {
		t.Fatalf("the window slid with the clock: %v", again)
	}

	// On the wire a record entry is a string, so one past 2^53 reaches the
	// page as it was kept.
	v, _ := a.operatorNoteView(t.Context())
	wire, _ := json.Marshal(v)
	if !strings.Contains(string(wire), `"entries":["9007199254740993","7"]`) {
		t.Fatalf("the claim's entries are not carried exactly as strings: %s", wire)
	}

	// A read that fails is said as unread, never as a list of none.
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if names, view := read(ctx); len(names) != 0 || view.unread == "" {
		t.Fatalf("a failed read of what did not succeed was drawn as %v, unread %q; want it said as unread", names, view.unread)
	}
	// A note kept without its window cannot say what failed in it.
	keep(cognitive.KeptNote{Note: "an old note"})
	if names, view := read(t.Context()); len(names) != 0 || view.unread == "" {
		t.Fatalf("a note without its window was read as %v, unread %q", names, view.unread)
	}

	t.Run("bounded, and the rest counted", func(t *testing.T) {
		st, err := store.NewMemory()
		if err != nil {
			t.Fatal(err)
		}
		defer st.Close()
		a := &App{store: st}
		// A second before: a record kept in the same millisecond as a
		// nanosecond from fell outside the window on the Windows guest
		// (the native run of 2026-11-07), and the view read one short.
		from := time.Now().Add(-time.Second)
		for i := 1; i <= noteOutcomesShown+2; i++ {
			if err := st.RecordToolStart("turn_many", i, "main", "m", "", "shell", "{}"); err != nil {
				t.Fatal(err)
			}
			if err := st.RecordToolDoneMeasured("turn_many", i, "shell", "{}", "no", interaction.Failed, false, store.ToolMeasurement{}); err != nil {
				t.Fatal(err)
			}
		}
		content, _ := json.Marshal(cognitive.KeptNote{Note: "A busy day.", WindowFrom: from, WindowUntil: time.Now()})
		if err := st.SaveOperatorNote(string(content)); err != nil {
			t.Fatal(err)
		}
		v, err := a.operatorNoteView(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if len(v.NotSucceeded) != noteOutcomesShown || v.NotSucceededMore != 2 || v.NotSucceededUnread != "" {
			t.Fatalf("%d listed, %d more, unread %q; want %d listed and 2 more", len(v.NotSucceeded), v.NotSucceededMore, v.NotSucceededUnread, noteOutcomesShown)
		}
	})

	// NEVER RING CONTENT: the note is kept beside the brief and is neither a
	// ring section a boot restores nor the brief.
	if err := st.SaveBrief("this morning's brief"); err != nil {
		t.Fatal(err)
	}
	snaps, err := st.RingSnapshots()
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range snaps {
		if strings.Contains(s.Section, "note") || strings.Contains(s.Content, "an old note") {
			t.Fatalf("the note to the operator is ring content: section %q", s.Section)
		}
	}
	if brief, err := st.GetBrief(); err != nil || brief != "this morning's brief" {
		t.Fatalf("the brief is %q (%v) beside a kept note", brief, err)
	}
}

type noteViewOf struct {
	text      string
	unchecked int
	unread    string
	more      int
	claims    int
}
