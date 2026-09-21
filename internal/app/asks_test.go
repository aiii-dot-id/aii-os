package app

import (
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/dashboard"
	"github.com/aiii-dot-id/aii-os/internal/store"

	"github.com/aiii-dot-id/aii-os/internal/logsink"
)

// .

func startSession(t *testing.T, a *App, id, desc string) {
	t.Helper()
	if err := a.store.StartWorkSession(id, desc); err != nil {
		t.Fatal(err)
	}
}

func decisionOf(t *testing.T, a *App, id string) string {
	t.Helper()
	ws, err := a.store.WorkSessionByID(id)
	if err != nil || ws == nil {
		t.Fatalf("session %s: %v", id, err)
	}
	return ws.DecisionNeeded
}

func TestAnOwedDecisionBecomesACardAndItsAnswerAnOperatorMessage(t *testing.T) {
	a := liveApp(t)
	startSession(t, a, "ws_1", "book the flights")
	owed := "Aisle or window for the long leg?"
	if err := a.store.UpdateWorkPlan("ws_1", nil, nil, nil, nil, nil, &owed); err != nil {
		t.Fatal(err)
	}
	if err := a.proposeAsk("ws_1", owed, []string{"aisle", "window"}, ""); err != nil {
		t.Fatal(err)
	}
	views := a.askViews()
	if len(views) != 1 || views[0].Kind != "choose" || views[0].From != "identity" || len(views[0].Choices) != 2 || views[0].Session != "ws_1" {
		t.Fatalf("one choose card from the identity: %+v", views)
	}
	id := views[0].ID
	// .
	if err := a.proposeAsk("ws_1", owed, []string{"aisle", "window"}, ""); err != nil || len(a.askViews()) != 1 {
		t.Fatalf("a pending ask is not duplicated: %v %d", err, len(a.askViews()))
	}
	text, err := a.answerAsk(dashboard.AskAnswer{ID: id, Answer: "chose", Choice: "window"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(text, "[ask "+id+"] chose: window") {
		t.Fatalf("the answer is an operator message marked with its ask: %q", text)
	}
	if len(a.askViews()) != 0 {
		t.Fatal("an answered ask leaves the page")
	}
	if got := decisionOf(t, a, "ws_1"); got != "" {
		t.Fatalf("a chosen answer clears the owed decision, got %q", got)
	}
	if _, err := a.answerAsk(dashboard.AskAnswer{ID: id, Answer: "chose", Choice: "aisle"}); err == nil {
		t.Fatal("an ask is answered once")
	}
}

func TestNotNowKeepsTheDecisionOwedAndNoClosesIt(t *testing.T) {
	a := liveApp(t)
	startSession(t, a, "ws_2", "renew the domain")
	owed := "Renew for one year or five?"
	_ = a.store.UpdateWorkPlan("ws_2", nil, nil, nil, nil, nil, &owed)
	if err := a.proposeAsk("ws_2", owed, nil, ""); err != nil {
		t.Fatal(err)
	}
	id := a.askViews()[0].ID
	text, err := a.answerAsk(dashboard.AskAnswer{ID: id, Answer: "not_now"})
	if err != nil || !strings.Contains(text, "not now") || !strings.Contains(text, "stays owed") {
		t.Fatalf("not now tells the identity to proceed and keep the decision owed: %q %v", text, err)
	}
	if got := decisionOf(t, a, "ws_2"); got != owed {
		t.Fatalf("not now keeps the decision owed on the session, got %q", got)
	}
	// .
	if err := a.proposeAsk("ws_2", owed, nil, ""); err != nil || len(a.askViews()) != 1 {
		t.Fatalf("asking again after not now opens a new card: %v", err)
	}
	id = a.askViews()[0].ID
	if text, err := a.answerAsk(dashboard.AskAnswer{ID: id, Answer: "no"}); err != nil || !strings.Contains(text, "no") {
		t.Fatalf("no: %q %v", text, err)
	}
	if got := decisionOf(t, a, "ws_2"); got != "" {
		t.Fatalf("no closes the question and clears the decision, got %q", got)
	}
}

func TestANewerAskSupersedesTheOldAndASessionEndWithdrawsIt(t *testing.T) {
	a := liveApp(t)
	startSession(t, a, "ws_3", "draft the reply")
	if err := a.proposeAsk("ws_3", "Formal or friendly?", []string{"formal", "friendly"}, ""); err != nil {
		t.Fatal(err)
	}
	first := a.askViews()[0].ID
	if err := a.proposeAsk("ws_3", "Sign it from you or from the team?", []string{"me", "the team"}, ""); err != nil {
		t.Fatal(err)
	}
	views := a.askViews()
	if len(views) != 1 || views[0].ID == first {
		t.Fatalf("one open ask per session; the newer supersedes: %+v", views)
	}
	a.workObserved(store.WorkEvent{Kind: store.WorkDelivered, ID: "ws_3", Outcome: "served", Evidence: "completed_locally"})
	if len(a.askViews()) != 0 {
		t.Fatal("a delivered session withdraws its ask")
	}
	// .
	if err := a.proposeAsk("ws_3", "One more?", nil, ""); err != nil {
		t.Fatal(err)
	}
	if err := a.proposeAsk("ws_3", "", nil, ""); err != nil || len(a.askViews()) != 0 {
		t.Fatalf("an emptied decision withdraws the card: %v", err)
	}
}

func TestConnectAsksPointAtThePluginsPageAndGrantNothing(t *testing.T) {
	a := liveApp(t)
	startSession(t, a, "ws_4", "summarize the inbox")
	if err := a.proposeAsk("ws_4", "I need your email to read the inbox.", nil, "email"); err != nil {
		t.Fatal(err)
	}
	v := a.askViews()[0]
	if v.Kind != "connect" || v.Connector != "email" {
		t.Fatalf("a connector names a connect ask: %+v", v)
	}
	text, err := a.answerAsk(dashboard.AskAnswer{ID: v.ID, Answer: "connect", Scope: "read"})
	if err != nil || !strings.Contains(text, "connect email (read only)") || !strings.Contains(text, "Plugins page") {
		t.Fatalf("the answer points at the Plugins page and grants nothing itself: %q %v", text, err)
	}
	if got := decisionOf(t, a, "ws_4"); got != "" {
		t.Fatalf("a connect answer clears the owed decision, got %q", got)
	}
}

func TestAsksAreBoundedAndTyped(t *testing.T) {
	a := liveApp(t)
	for i := 0; i < asksPending; i++ {
		startSession(t, a, "ws_many_"+string(rune('a'+i)), "work")
		if err := a.proposeAsk("ws_many_"+string(rune('a'+i)), "Question?", nil, ""); err != nil {
			t.Fatalf("ask %d: %v", i, err)
		}
	}
	startSession(t, a, "ws_over", "work")
	if err := a.proposeAsk("ws_over", "One too many?", nil, ""); err == nil || !strings.Contains(err.Error(), "ceiling") {
		t.Fatalf("the ceiling holds and names itself: %v", err)
	}
	if err := a.proposeAsk("ws_over", strings.Repeat("x", askTextMax+1), nil, ""); err == nil {
		t.Fatal("a card carries a bounded text")
	}
	if err := a.proposeAsk("ws_over", "Pick", []string{"1", "2", "3", "4", "5", "6", "7"}, ""); err == nil {
		t.Fatal("a card offers at most six choices")
	}
	views := a.askViews()
	for _, v := range views {
		if v.Kind != "clarify" {
			t.Fatalf("no choices and no connector is a clarify ask: %+v", v)
		}
	}
	if _, err := a.answerAsk(dashboard.AskAnswer{ID: views[0].ID, Answer: "shrug"}); err == nil {
		t.Fatal("an unknown answer is refused")
	}
	if len(a.askViews()) != asksPending {
		t.Fatal("a refused answer leaves the ask on the page")
	}
}

// .
// .
// .
// .
// .
func TestASupersedeAndAWithdrawalAreOnTheRecord(t *testing.T) {
	lines := logsink.CaptureForTest(t)

	a := liveApp(t)
	startSession(t, a, "ws_log", "draft the reply")
	if err := a.proposeAsk("ws_log", "Formal or friendly?", nil, ""); err != nil {
		t.Fatal(err)
	}
	first := a.askViews()[0].ID
	if err := a.proposeAsk("ws_log", "Sign it from you or the team?", nil, ""); err != nil {
		t.Fatal(err)
	}
	second := a.askViews()[0].ID
	a.workObserved(store.WorkEvent{Kind: store.WorkDelivered, ID: "ws_log", Outcome: "served", Evidence: "completed_locally"})

	whole := lines.String()
	if !strings.Contains(whole, second+" superseded "+first) || !strings.Contains(whole, "ask.decision") {
		t.Fatalf("the supersede names both cards:\n%s", whole)
	}
	if !strings.Contains(whole, second+" withdrawn (session ws_log): the session it belonged to has delivered") || !strings.Contains(whole, "ask.end") {
		t.Fatalf("the withdrawal names the card and the reason:\n%s", whole)
	}
}
