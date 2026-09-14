package app

import (
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/dashboard"
	"github.com/aiii-dot-id/aii-os/internal/identity"
	"github.com/aiii-dot-id/aii-os/internal/pluginhost"
	"github.com/aiii-dot-id/aii-os/internal/ring"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

// .
// .
// .
// .
// .
// .
// .
// .
func TestAGradeIsOneMarkedOperatorTurnAndOneEvent(t *testing.T) {
	a := gradingApp(t)
	const plugin = "org.example.journal"
	sink := &eventSink{name: pluginhost.ToolNameFor(plugin, "journal.event")}
	if err := a.toolReg.RegisterHostOp(sink, plugin); err != nil {
		t.Fatal(err)
	}
	a.startSubscriber(&pluginhost.ActivePlugin{ID: plugin, Subscriptions: []pluginhost.SubscriptionDecl{
		{Topic: pluginhost.TopicWorkGraded, Operation: "journal.event"},
	}})
	defer a.stopSubscriber(plugin)
	graded := func() []map[string]interface{} {
		deadline := time.Now().Add(5 * time.Second)
		for sink.count() == 0 && time.Now().Before(deadline) {
			time.Sleep(10 * time.Millisecond)
		}
		out := make([]map[string]interface{}, 0, sink.count())
		for _, g := range sink.got {
			if p, _ := g["payload"].(map[string]interface{}); p != nil {
				out = append(out, p)
			}
		}
		return out
	}
	if err := a.store.StartWorkSession("ws_g1", store.SubagentDescription("book the flights")); err != nil {
		t.Fatal(err)
	}
	// .
	if _, err := a.gradeResult(dashboard.GradeRequest{Session: "ws_g1", Grade: "served"}); err == nil {
		t.Fatal("a grade is the word on a result, not on running work")
	}
	if err := a.store.DeliverWorkSession("ws_g1", "served: both legs booked", store.EvidenceCompletedLocally, ""); err != nil {
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

	// .
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

	// .
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

	// .
	if err := a.store.StartWorkSession("ws_g2", "the migration"); err != nil {
		t.Fatal(err)
	}
	_ = a.store.DeliverWorkSession("ws_g2", "served: done", store.EvidenceCompletedLocally, "")
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

	// .
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

	// .
	a.enterSafe("test: the record is frozen")
	if _, err := a.gradeResult(dashboard.GradeRequest{Session: "ws_g1", Grade: "served"}); err == nil || !strings.Contains(err.Error(), "SAFE") {
		t.Fatalf("SAFE holds a grade and says so: %v", err)
	}
}

// .
// .
func gradingApp(t *testing.T) *App {
	t.Helper()
	a := liveApp(t)
	a.engine = identity.NewEngine(a.store, nil, ring.NewManager(), toolDiscovererAdapter{a.toolReg})
	return a
}
