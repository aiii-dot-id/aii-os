package app

import (
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/conversation"
	"github.com/aiii-dot-id/aii-os/internal/dashboard"
	"github.com/aiii-dot-id/aii-os/internal/interaction"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

// A TURN THAT FAILED WITHOUT A REPLY IS RECORDED WITH ITS CAUSE. A failed
// turn with words has its marker; a silent one left nothing, so a reload
// drew a question with no answer and no reason, and the identity's next
// turn read the same. The row is the loop's own kind of line, beside the
// words the turn last took in; its cause is FailureCause's words. Words
// approved by the operator, 2026-11-05.
func TestAFailedTurnWithoutAReplyIsRecordedWithItsCause(t *testing.T) {
	app, _ := promptCacheApp(t)
	operatorTurn(t, app, "first question")
	ids, _ := wordsRows(t, app, string(interaction.Resident))
	if len(ids) == 0 {
		t.Fatal("the first turn recorded no reply")
	}
	failed := interaction.WithTurn(t.Context(), "turn-silent")
	app.recordInterruptedTurnContext(failed, conversation.Result{}, "the model provider failed: HTTP 503 Service Unavailable")
	rows, err := app.store.DB().Query(`SELECT content, COALESCE(related_id,'') FROM conversations
		WHERE role='system' AND json_extract(details,'$.reason_code')=? ORDER BY turn_seq`, store.ReasonLoopNote)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var notes []string
	related := ""
	for rows.Next() {
		var content, rel string
		if err := rows.Scan(&content, &rel); err != nil {
			t.Fatal(err)
		}
		notes = append(notes, content)
		related = rel
	}
	want := "[This turn ended without a reply: the model provider failed: HTTP 503 Service Unavailable.]"
	if len(notes) != 1 || notes[0] != want {
		t.Fatalf("the silent failure is not recorded as %q: %q", want, notes)
	}
	if related != ids[len(ids)-1] {
		t.Fatalf("the row stands beside %q, not the conversation's newest words %s", related, ids[len(ids)-1])
	}
	// The next turn reads it, once, after the words it stands beside.
	next := interaction.WithTurn(t.Context(), "turn-next")
	if _, err := app.recordConversationRef(next, string(interaction.Operator), "second question"); err != nil {
		t.Fatal(err)
	}
	conv, _, err := app.buildHistory(next)
	if err != nil {
		t.Fatal(err)
	}
	carried := 0
	for _, m := range conv {
		if strings.Contains(m.Content, want) {
			carried++
		}
	}
	if carried != 1 {
		t.Fatalf("the next turn carries the cause %d times, want once", carried)
	}
}

// THE FRAME SAYS WHAT FOLLOWS A TURN THAT ENDS BY ITSELF: THE WORK
// CONTINUES. Each turn a work session is active with
// no condition of the identity's set, its card says a fresh turn follows
// and how to stop instead; with no session there is nothing to say it of.
func TestTheFrameSaysTheWorkContinues(t *testing.T) {
	app, _ := promptCacheApp(t)
	before, err := app.buildWorkState(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(before, workContinuesLine) {
		t.Fatalf("said of no session: %q", before)
	}
	if err := app.store.StartWorkSession(t.Context(), "ws_open", "a long task"); err != nil {
		t.Fatal(err)
	}
	after, err := app.buildWorkState(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(after, workContinuesLine) != 1 {
		t.Fatalf("the active session's card does not say it once: %q", after)
	}
	if strings.Index(after, "### Current work session: ws_open") > strings.Index(after, workContinuesLine) {
		t.Fatalf("the line is not under the session it is said of: %q", after)
	}
}

// THE PAGE IS TOLD NOTHING IS SCHEDULED ONLY WHEN NOTHING IS. The host's one
// decision: a work session is active, and no fresh turn, alarm, sub-agent,
// waiting delivery or open question will carry it on. With no session there
// is no work to say it of.
func TestThePageIsToldNothingIsScheduledOnlyWhenNothingIs(t *testing.T) {
	app, _ := promptCacheApp(t)
	read := func() bool {
		t.Helper()
		w, err := app.workQueueState()
		if err != nil {
			t.Fatal(err)
		}
		return w.NothingScheduled
	}
	if read() {
		t.Fatal("said with no work session active")
	}
	if err := app.store.StartWorkSession(t.Context(), "ws_idle", "a long task"); err != nil {
		t.Fatal(err)
	}
	if !read() {
		t.Fatal("an active session with nothing to continue it is not said")
	}
	app.addQueuedLegs(1)
	if read() {
		t.Fatal("said while a fresh turn is queued")
	}
	// A second leg, scheduled by a timeout, and the first then starting:
	// one leg still waits, so something is still scheduled.
	app.addQueuedLegs(1)
	app.addQueuedLegs(-1)
	if read() {
		t.Fatal("one leg starting cleared another that still waits")
	}
	app.addQueuedLegs(-1)
	if !read() {
		t.Fatal("a fresh turn that began or stood down still counts as queued")
	}
	if app.nothingScheduled(&dashboard.WorkState{Alarms: []dashboard.AlarmView{{ID: "a"}}}) {
		t.Fatal("said with an alarm set")
	}
	if app.nothingScheduled(&dashboard.WorkState{Live: []dashboard.WorkSessionItem{{ID: "child"}}}) {
		t.Fatal("said with a sub-agent running")
	}
	if app.nothingScheduled(&dashboard.WorkState{Queued: 1}) {
		t.Fatal("said with a sub-agent queued")
	}
	if err := app.proposeAsk("ws_idle", "Which one?", nil, ""); err != nil {
		t.Fatal(err)
	}
	if read() {
		t.Fatal("said with a question open to the operator")
	}
}

// A FAILED TURN WHOSE ONLY RECORD IS REFUSED IS NOT RECORDED (a review 1844
// item 4): a turn with no words is recorded by its notes alone, so when the
// record refuses the note nothing is there for a fresh turn to read, and a
// timeout's continuation must not take the turn for recorded.
func TestAFailedTurnWhoseOnlyNoteIsRefusedIsNotRecorded(t *testing.T) {
	app, _ := promptCacheApp(t)
	operatorTurn(t, app, "first question")
	if _, err := app.store.DB().Exec(`CREATE TRIGGER refuse_notes BEFORE INSERT ON conversations WHEN NEW.role = 'system' BEGIN SELECT RAISE(ABORT, 'the record refused the note'); END`); err != nil {
		t.Fatal(err)
	}
	const cause = "the model provider failed: HTTP 503 Service Unavailable"
	if app.recordInterruptedTurnContext(interaction.WithTurn(t.Context(), "turn-refused"), conversation.Result{}, cause) {
		t.Fatal("a failed turn whose only note the record refused was taken for recorded")
	}
	if _, err := app.store.DB().Exec(`DROP TRIGGER refuse_notes`); err != nil {
		t.Fatal(err)
	}
	if !app.recordInterruptedTurnContext(interaction.WithTurn(t.Context(), "turn-kept"), conversation.Result{}, cause) {
		t.Fatal("a failed turn whose note was recorded was not taken for recorded")
	}
}
