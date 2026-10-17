package app

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/crypto"
	"github.com/aiii-dot-id/aii-os/internal/dashboard"
	"github.com/aiii-dot-id/aii-os/internal/identity"
	"github.com/aiii-dot-id/aii-os/internal/store"
	"github.com/coder/websocket"

	"github.com/aiii-dot-id/aii-os/internal/logsink"
)

// The identity asks; the operator answers.

func startSession(t *testing.T, a *App, id, desc string) {
	t.Helper()
	if err := a.store.StartWorkSession(t.Context(), id, desc); err != nil {
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
	if err := a.store.UpdateWorkPlan(t.Context(), "ws_1", nil, nil, nil, nil, nil, &owed); err != nil {
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
	// The same question again is not a second card.
	if err := a.proposeAsk("ws_1", owed, []string{"aisle", "window"}, ""); err != nil || len(a.askViews()) != 1 {
		t.Fatalf("a pending ask is not duplicated: %v %d", err, len(a.askViews()))
	}
	text, settle, err := a.answerAsk(dashboard.AskAnswer{ID: id, Answer: "chose", Choice: "window"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(text, "[ask "+id+"] chose: window") {
		t.Fatalf("the answer is an operator message marked with its ask: %q", text)
	}
	if len(a.askViews()) != 0 {
		t.Fatal("an answered ask leaves the page")
	}
	// The decision is the record's to clear: the press has only sent the
	// answer on its way.
	if got := decisionOf(t, a, "ws_1"); got != owed {
		t.Fatalf("the press cleared the owed decision before its answer was recorded: %q", got)
	}
	settle(true)
	if got := decisionOf(t, a, "ws_1"); got != "" {
		t.Fatalf("a chosen answer clears the owed decision, got %q", got)
	}
	if _, _, err := a.answerAsk(dashboard.AskAnswer{ID: id, Answer: "chose", Choice: "aisle"}); err == nil {
		t.Fatal("an ask is answered once")
	}
}

// AN ANSWER THE RECORD NEVER TOOK HAS NOT ANSWERED THE ASK. The press sends
// an operator message, and that message can be refused on its way as any
// typed one can: here the running turn already holds eight unread messages.
// The press used to close the card and clear the session's decision first,
// so a refused answer left no card, no decision owed in the identity's work
// state, and an identity that had never read it. Through the page's own
// socket: the refusal is said, the question is back, the decision is still
// owed; pressed again once the turn has room, the answer is recorded at the
// next tool boundary, and only then is the decision cleared.
func TestAnAnswerRefusedOnItsWayLeavesTheQuestionOpenAndTheDecisionOwed(t *testing.T) {
	st, err := store.NewMemory()
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	a := newSteerApp(t)
	a.store, a.engine = st, identity.NewEngine(st, nil, nil, nil, identity.Ports{})
	if a.keyPair, err = crypto.GenerateKeyPair(); err != nil {
		t.Fatal(err)
	}
	defer func() { a.bgCancel(); a.bgWG.Wait() }()
	h := &dashboard.WSHandler{Speaker: "identity", AdmitChat: a.admitOperatorChat, ReleaseTurn: a.EndTurn, AcquireTurn: a.acquireTurn, PendingSteers: a.PendingSteers,
		GetAsks: a.askViews, AnswerAsk: a.answerAsk,
		HandleMessage: func(context.Context, string) (string, error) { return "", nil }}
	s := dashboard.New("127.0.0.1", 0, h)
	if _, err := s.Start(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	defer s.Shutdown(context.Background())
	a.dashboard = s
	conn := dialWSS(t, s.Origin())

	startSession(t, a, "ws_5", "ship the release")
	owed := "Deploy to A or B?"
	if err := a.store.UpdateWorkPlan(t.Context(), "ws_5", nil, nil, nil, nil, nil, &owed); err != nil {
		t.Fatal(err)
	}
	if err := a.proposeAsk("ws_5", owed, []string{"A", "B"}, ""); err != nil {
		t.Fatal(err)
	}
	id := a.askViews()[0].ID

	// A turn is running and holds as many unread messages as it takes.
	if !a.TryBeginTurn() {
		t.Fatal("no running turn to answer into")
	}
	defer a.EndTurn()
	for i := 0; i < maxPendingSteers; i++ {
		if steered, err := a.Steer("filler"); err != nil || !steered {
			t.Fatalf("filler %d was not queued: steered=%v err=%v", i, steered, err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	press, _ := json.Marshal(dashboard.ClientMessage{Type: "ask", Ask: &dashboard.AskAnswer{ID: id, Answer: "chose", Choice: "B"}})
	if err := conn.Write(ctx, websocket.MessageText, press); err != nil {
		t.Fatal(err)
	}
	// The refusal is sent after the ask is settled, on the socket's own loop.
	readUntil(t, conn, func(m *dashboard.ServerMessage) bool {
		return m.Type == "error" && strings.Contains(m.Message, "unread messages")
	})
	if views := a.askViews(); len(views) != 1 || views[0].ID != id {
		t.Fatalf("the answer was refused and its question is gone from the page: %+v", views)
	}
	if got := decisionOf(t, a, "ws_5"); got != owed {
		t.Fatalf("the answer was refused and the decision is no longer owed: %q", got)
	}

	// The turn reaches a tool boundary; the operator presses again.
	a.DrainSteering()
	if err := conn.Write(ctx, websocket.MessageText, press); err != nil {
		t.Fatal(err)
	}
	readUntil(t, conn, func(m *dashboard.ServerMessage) bool { return m.Type == "steered" })
	if got := decisionOf(t, a, "ws_5"); got != owed {
		t.Fatalf("the decision was cleared while its answer still waited for the turn: %q", got)
	}
	if said := a.DrainSteering(); len(said) != 1 || !strings.Contains(said[0], "[ask "+id+"] chose: B") {
		t.Fatalf("the answer did not reach the turn as the operator's marked message: %q", said)
	}
	if views := a.askViews(); len(views) != 0 {
		t.Fatalf("a recorded answer ends the ask: %+v", views)
	}
	if got := decisionOf(t, a, "ws_5"); got != "" {
		t.Fatalf("a recorded answer clears the owed decision, got %q", got)
	}
}

func TestNotNowKeepsTheDecisionOwedAndNoClosesIt(t *testing.T) {
	a := liveApp(t)
	startSession(t, a, "ws_2", "renew the domain")
	owed := "Renew for one year or five?"
	_ = a.store.UpdateWorkPlan(t.Context(), "ws_2", nil, nil, nil, nil, nil, &owed)
	if err := a.proposeAsk("ws_2", owed, nil, ""); err != nil {
		t.Fatal(err)
	}
	id := a.askViews()[0].ID
	text, settle, err := a.answerAsk(dashboard.AskAnswer{ID: id, Answer: "not_now"})
	if err != nil || !strings.Contains(text, "not now") || !strings.Contains(text, "stays owed") {
		t.Fatalf("not now tells the identity to proceed and keep the decision owed: %q %v", text, err)
	}
	settle(true)
	if got := decisionOf(t, a, "ws_2"); got != owed {
		t.Fatalf("not now keeps the decision owed on the session, got %q", got)
	}
	// The identity may ask again when the work needs it: a new card, no clock.
	if err := a.proposeAsk("ws_2", owed, nil, ""); err != nil || len(a.askViews()) != 1 {
		t.Fatalf("asking again after not now opens a new card: %v", err)
	}
	id = a.askViews()[0].ID
	text, settle, err = a.answerAsk(dashboard.AskAnswer{ID: id, Answer: "no"})
	if err != nil || !strings.Contains(text, "no") {
		t.Fatalf("no: %q %v", text, err)
	}
	settle(true)
	if got := decisionOf(t, a, "ws_2"); got != "" {
		t.Fatalf("no closes the question and clears the decision, got %q", got)
	}
}

func TestAQuestionAskedAnewBeforeTheAnswerSettlesKeepsItsDecisionAndItsCard(t *testing.T) {
	a := liveApp(t)
	startSession(t, a, "ws_4", "plan the trip")
	owe := func(text string) string {
		t.Helper()
		if err := a.store.UpdateWorkPlan(t.Context(), "ws_4", nil, nil, nil, nil, nil, &text); err != nil {
			t.Fatal(err)
		}
		if err := a.proposeAsk("ws_4", text, nil, ""); err != nil {
			t.Fatal(err)
		}
		views := a.askViews()
		if len(views) != 1 || views[0].Text != text {
			t.Fatalf("rig: one card, %q: %+v", text, views)
		}
		return views[0].ID
	}

	first := owe("Train or plane?")
	_, settle, err := a.answerAsk(dashboard.AskAnswer{ID: first, Answer: "said", Text: "train"})
	if err != nil {
		t.Fatal(err)
	}
	owe("Which day?")
	settle(true)
	if got := decisionOf(t, a, "ws_4"); got != "Which day?" {
		t.Fatalf("the older question's answer, recorded, cleared the newer decision: %q", got)
	}

	second := a.askViews()[0].ID
	_, settle, err = a.answerAsk(dashboard.AskAnswer{ID: second, Answer: "said", Text: "Friday"})
	if err != nil {
		t.Fatal(err)
	}
	owe("Morning or evening?")
	settle(false)
	if views := a.askViews(); len(views) != 1 || views[0].Text != "Morning or evening?" {
		t.Fatalf("an answer the record refused put its older card back beside the newer one: %+v", views)
	}
	if got := decisionOf(t, a, "ws_4"); got != "Morning or evening?" {
		t.Fatalf("the decision owed is %q, want the newer question's", got)
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
	// Clearing the decision withdraws too.
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
	text, settle, err := a.answerAsk(dashboard.AskAnswer{ID: v.ID, Answer: "connect", Scope: "read"})
	if err != nil || !strings.Contains(text, "connect email (read only)") || !strings.Contains(text, "Plugins page") {
		t.Fatalf("the answer points at the Plugins page and grants nothing itself: %q %v", text, err)
	}
	settle(true)
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
	if _, _, err := a.answerAsk(dashboard.AskAnswer{ID: views[0].ID, Answer: "shrug"}); err == nil {
		t.Fatal("an unknown answer is refused")
	}
	if len(a.askViews()) != asksPending {
		t.Fatal("a refused answer leaves the ask on the page")
	}
}

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
