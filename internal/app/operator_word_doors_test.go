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
	"github.com/aiii-dot-id/aii-os/internal/interaction"
	"github.com/aiii-dot-id/aii-os/internal/store"
	"github.com/coder/websocket"
)

// saidOn reads the mark the record kept beside the row whose words begin
// with prefix (store.AnnotationSaid): how the operator said them, or "" for
// none.
func saidOn(t *testing.T, st *store.Store, prefix string) string {
	t.Helper()
	var said string
	if err := st.DB().QueryRow(`SELECT COALESCE((SELECT m.key FROM turn_annotations m WHERE m.turn_seq = c.turn_seq AND m.kind = ?), '')
		FROM conversations c WHERE c.role = 'operator' AND substr(c.content, 1, length(?)) = ? ORDER BY c.turn_seq DESC LIMIT 1`,
		store.AnnotationSaid, prefix, prefix).Scan(&said); err != nil {
		t.Fatalf("the row %q: %v", prefix, err)
	}
	return said
}

func TestTheOperatorsWordIsMarkedAtTheDoorAndAnAskPressIsNot(t *testing.T) {
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
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	send := func(m dashboard.ClientMessage) {
		t.Helper()
		raw, _ := json.Marshal(m)
		if err := conn.Write(ctx, websocket.MessageText, raw); err != nil {
			t.Fatal(err)
		}
		readUntil(t, conn, func(m *dashboard.ServerMessage) bool { return m.Type == "steered" })
		if said := a.DrainSteering(); len(said) != 1 {
			t.Fatalf("the turn read %q", said)
		}
	}
	latest := func() string {
		t.Helper()
		turn, err := st.LatestOperatorWord()
		if err != nil || turn == nil {
			t.Fatalf("no latest operator word: %v", err)
		}
		return turn.Content
	}
	ask := func(question string) string {
		t.Helper()
		if err := a.store.UpdateWorkPlan(t.Context(), "ws_w", nil, nil, nil, nil, nil, &question); err != nil {
			t.Fatal(err)
		}
		if err := a.proposeAsk("ws_w", question, []string{"Yes, I affirm", "No"}, ""); err != nil {
			t.Fatal(err)
		}
		return a.askViews()[0].ID
	}

	startSession(t, a, "ws_w", "write the charter")
	if !a.TryBeginTurn() {
		t.Fatal("no running turn to speak into")
	}
	defer a.EndTurn()

	send(dashboard.ClientMessage{Type: "chat", Message: "go ahead and draft it"})
	if got := saidOn(t, st, "go ahead"); got != interaction.SaidTyped {
		t.Fatalf("a typed message is marked %q, want typed", got)
	}

	id := ask("May I record this charter?")
	send(dashboard.ClientMessage{Type: "ask", Ask: &dashboard.AskAnswer{ID: id, Answer: "no"}})
	if got := saidOn(t, st, store.AskMarker+id+"] no"); got != interaction.SaidAnswer {
		t.Fatalf("a press of No is marked %q, want answer", got)
	}
	if got := latest(); got != "go ahead and draft it" {
		t.Fatalf("the latest operator word is %q: a press on an ask card, in the host's words, stood as theirs", got)
	}

	id = ask("May I record it now?")
	send(dashboard.ClientMessage{Type: "ask", Ask: &dashboard.AskAnswer{ID: id, Answer: "chose", Choice: "Yes, I affirm"}})
	if got := latest(); got != "go ahead and draft it" {
		t.Fatalf("the latest operator word is %q: the identity's own choice label stood as theirs", got)
	}

	id = ask("Anything to add?")
	send(dashboard.ClientMessage{Type: "ask", Ask: &dashboard.AskAnswer{ID: id, Answer: "said", Text: "yes, record it as written"}})
	if got := latest(); !strings.HasSuffix(got, "yes, record it as written") {
		t.Fatalf("words the operator typed into the card are not their word: latest is %q", got)
	}
}

func TestTheOperatorsSpokenWordsAreMarkedAtTheDoor(t *testing.T) {
	a := newVoiceApp(t)
	ctx := context.Background()
	if !a.TryBeginTurn() {
		t.Fatal("no running turn to speak into")
	}
	if err := a.observeOperatorVoice(ctx, "use the second draft", heardUtterance{Answer: true}); err != nil {
		t.Fatal(err)
	}
	if said := a.DrainSteering(); len(said) != 1 {
		t.Fatalf("the turn read %q", said)
	}
	a.EndTurn()
	if got := saidOn(t, a.store, voiceMarker+"use the second"); got != interaction.SaidSpoken {
		t.Fatalf("spoken words steered into a turn are marked %q, want spoken", got)
	}

	var woke string
	saved := voiceWake
	voiceWake = func(_ *App, ctx context.Context, _, _ string) (string, error) {
		woke = interaction.SaidOf(ctx)
		return "", nil
	}
	t.Cleanup(func() { voiceWake = saved })
	if err := a.observeOperatorVoice(ctx, "and send it", heardUtterance{Answer: true}); err != nil {
		t.Fatal(err)
	}
	if woke != interaction.SaidSpoken {
		t.Fatalf("spoken words that open a turn carry the mark %q to it, want spoken", woke)
	}
}
