package app

import (
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/dashboard"
	"github.com/aiii-dot-id/aii-os/internal/identity"
	"github.com/aiii-dot-id/aii-os/internal/interaction"
	"github.com/aiii-dot-id/aii-os/internal/pluginhost"
	"github.com/aiii-dot-id/aii-os/internal/ring"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

// THE OPERATOR'S WORD ON A RESULT.
// A chip writes ONE marked operator turn bound to its session and emits
// work.graded once with identifiers and classes only; the turn is found
// in the conversation and its sequence is what seam 3 cites; an
// acceptance item names its project and item; the page shows the grade
// beside the result afterwards; a grade is refused on anything but a
// delivered result, on a word that is not one of the three, and while
// SAFE holds.
func TestAGradeIsOneMarkedOperatorTurnAndOneEvent(t *testing.T) {
	a := gradingApp(t)
	const plugin = "org.example.journal"
	sink := &eventSink{name: pluginhost.ToolNameFor(plugin, "journal.event")}
	if err := a.toolReg.RegisterHostOp(sink, plugin); err != nil {
		t.Fatal(err)
	}
	ap := &pluginhost.ActivePlugin{ID: plugin, Subscriptions: []pluginhost.SubscriptionDecl{
		{Topic: pluginhost.TopicWorkGraded, Operation: "journal.event"},
	}}
	a.startSubscriber(ap)
	defer a.stopSubscriberOf(ap)
	graded := func() []map[string]any {
		deadline := time.Now().Add(5 * time.Second)
		for sink.count() == 0 && time.Now().Before(deadline) {
			time.Sleep(10 * time.Millisecond)
		}
		out := make([]map[string]any, 0, sink.count())
		for _, g := range sink.got {
			if p, _ := g["payload"].(map[string]any); p != nil {
				out = append(out, p)
			}
		}
		return out
	}
	queueChild(t, a.store, "ws_g1")
	if err := a.store.StartWorkSession(t.Context(), "ws_g1", store.SubagentDescription("book the flights")); err != nil {
		t.Fatal(err)
	}
	// Not delivered yet: there is no result to grade.
	if _, err := a.gradeResult(dashboard.GradeRequest{Session: "ws_g1", Grade: "served"}); err == nil {
		t.Fatal("a grade is the word on a result, not on running work")
	}
	if err := a.store.DeliverWorkSession(t.Context(), "ws_g1", "served: both legs booked", store.EvidenceCompletedLocally, ""); err != nil {
		t.Fatal(err)
	}

	seq, err := a.gradeResult(dashboard.GradeRequest{Session: "ws_g1", Grade: "partial", Comment: "the return leg is the wrong day"})
	if err != nil || seq == 0 {
		t.Fatalf("the grade is recorded and carries its turn: %d %v", seq, err)
	}
	turns, err := a.store.RecentTurns(10)
	if err != nil {
		t.Fatal(err)
	}
	var found string
	for _, tr := range turns {
		if strings.HasPrefix(tr.Content, "[grade ws_g1]") {
			found = tr.Content
			if tr.Role != roleOperator {
				t.Fatalf("a grade is the operator's turn, got role %q", tr.Role)
			}
			if tr.TurnSeq != seq {
				t.Fatalf("the returned sequence names the turn: %d vs %d", seq, tr.TurnSeq)
			}
		}
	}
	if found != "[grade ws_g1] partial — the return leg is the wrong day" {
		t.Fatalf("the turn carries the mark, the word and the line: %q", found)
	}
	events := graded()
	if len(events) != 1 {
		t.Fatalf("one event per grade, got %d (%v)", len(events), sink.topics())
	}
	ev := events[0]
	if ev["session"] != "ws_g1" || ev["grade"] != "partial" || ev["turn"] != seq {
		t.Fatalf("the event carries identifiers and classes: %+v", ev)
	}
	if _, leaked := ev["comment"]; leaked {
		t.Fatalf("the operator's words never ride the event: %+v", ev)
	}

	// The page shows it beside the result, after a reload.
	w, err := a.workQueueState()
	if err != nil {
		t.Fatal(err)
	}
	var shown *dashboard.GradeView
	for _, item := range w.Delivered {
		if item.ID == "ws_g1" {
			shown = item.Grade
		}
	}
	if shown == nil || shown.Grade != "partial" || shown.Comment != "the return leg is the wrong day" || shown.Turn != seq {
		t.Fatalf("the recorded grade is read back for the page: %+v", shown)
	}

	// A later grade on the same result replaces the earlier one.
	seq2, err := a.gradeResult(dashboard.GradeRequest{Session: "ws_g1", Grade: "served"})
	if err != nil || seq2 <= seq {
		t.Fatalf("a second grade is a later turn: %d %v", seq2, err)
	}
	w, _ = a.workQueueState()
	for _, item := range w.Delivered {
		if item.ID == "ws_g1" && (item.Grade == nil || item.Grade.Grade != "served" || item.Grade.Comment != "") {
			t.Fatalf("the operator's last word stands: %+v", item.Grade)
		}
	}

	// An acceptance item names its project and item.
	if err := a.store.StartWorkSession(t.Context(), "ws_g2", "the migration"); err != nil {
		t.Fatal(err)
	}
	_ = a.store.DeliverWorkSession(t.Context(), "ws_g2", "served: done", store.EvidenceCompletedLocally, "")
	if _, err := a.gradeResult(dashboard.GradeRequest{Session: "ws_g2", Grade: "unserved", Item: "acceptance-3"}); err != nil {
		t.Fatal(err)
	}
	turns, _ = a.store.RecentTurns(6)
	item := ""
	for _, tr := range turns {
		if strings.Contains(tr.Content, "acceptance-3") {
			item = tr.Content
		}
	}
	if !strings.HasPrefix(item, "[grade ws_g2 /acceptance-3] unserved") {
		t.Fatalf("an item grade names the project and the item: %q", item)
	}

	// Refusals.
	for _, bad := range []dashboard.GradeRequest{
		{Session: "ws_g1", Grade: "thumbs-up"},
		{Session: "ws_g1", Grade: ""},
		{Session: "", Grade: "served"},
		{Session: "ws_nope", Grade: "served"},
		{Session: "ws_g1", Grade: "partial", Comment: strings.Repeat("x", gradeCommentMax+1)},
	} {
		if _, err := a.gradeResult(bad); err == nil {
			t.Fatalf("refused: %+v", bad)
		}
	}

	// SAFE: a grade nobody can cite is not recorded.
	a.enterSafe("test: the record is frozen")
	if _, err := a.gradeResult(dashboard.GradeRequest{Session: "ws_g1", Grade: "served"}); err == nil || !strings.Contains(err.Error(), "SAFE") {
		t.Fatalf("SAFE holds a grade and says so: %v", err)
	}
}

// gradingApp is liveApp with the identity's engine wired: a grade is
// recorded as a conversation turn, which is the engine's to write.
func gradingApp(t *testing.T) *App {
	t.Helper()
	a := liveApp(t)
	a.engine = identity.NewEngine(a.store, nil, ring.NewManager(), toolDiscovererAdapter{a.toolReg}, identity.Ports{})
	return a
}

func TestTheGradeOnThePageIsReadFromItsRecord(t *testing.T) {
	a := gradingApp(t)
	if _, err := a.store.RecordConversation(t.Context(), roleOperator, "graded: unserved — the return leg is the wrong day", "",
		interaction.Details{Grade: &interaction.Grade{Session: "ws_structured", Grade: gradeUnserved}}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.store.RecordConversation(t.Context(), roleOperator, "[grade ws_older] served — on time", "", interaction.Details{}); err != nil {
		t.Fatal(err)
	}
	got := a.gradesOf([]string{"ws_structured", "ws_older"})
	if g := got["ws_structured"]; g.Grade != gradeUnserved || g.Comment != "the return leg is the wrong day" {
		t.Errorf("the recorded grade was not read from its record: %+v", g)
	}
	if g := got["ws_older"]; g.Grade != "served" || g.Comment != "on time" {
		t.Errorf("a grade recorded in text only was lost: %+v", g)
	}
}
