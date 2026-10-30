package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/dashboard"
	"github.com/aiii-dot-id/aii-os/internal/interaction"
	"github.com/aiii-dot-id/aii-os/internal/store"
	"github.com/coder/websocket"
)

// spokenTurn records words the operator spoke at the door they speak at,
// with the speech engine's observation that identified them as the
// operator's enrolled voice under the UUID named here.
func spokenTurn(t *testing.T, st *store.Store, words, uuid string) uint64 {
	t.Helper()
	ref, err := st.RecordConversation(t.Context(), roleOperator, voiceMarker+words, "", interaction.Details{Said: interaction.SaidSpoken})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.AnnotateTurn(ref.Sequence, store.AnnotationSpeaker, fmt.Sprintf("vs-1/%d", ref.Sequence),
		`{"decision":"known","speaker_id":"quinn-desk","speaker_uuid":"`+uuid+`","continuity":"matched"}`); err != nil {
		t.Fatal(err)
	}
	return ref.Sequence
}

func TestTheWordCardShowsTheSpokenWordsAndOnlyItsConfirmRecordsTheConfirmation(t *testing.T) {
	a := liveApp(t)
	const atDesk = "6f0c2c1e-8a41-4f6e-9a55-0d1c2b3a4e5f"
	confirmed := func(turn uint64) bool {
		t.Helper()
		notes, err := a.store.TurnAnnotations(store.AnnotationConfirmed, []uint64{turn})
		if err != nil {
			t.Fatal(err)
		}
		_, ok := notes[turn]
		return ok
	}
	words := func() []dashboard.AskView {
		var out []dashboard.AskView
		for _, v := range a.askViews() {
			if v.Kind == askKindWord {
				out = append(out, v)
			}
		}
		return out
	}

	startSession(t, a, "ws_1", "write the charter")
	owed := "Which name goes on the charter?"
	if err := a.store.UpdateWorkPlan(t.Context(), "ws_1", nil, nil, nil, nil, nil, &owed); err != nil {
		t.Fatal(err)
	}
	if err := a.proposeAsk("ws_1", owed, nil, ""); err != nil {
		t.Fatal(err)
	}
	first := spokenTurn(t, a.store, `Yes, record it as our "charter".`, atDesk)
	if err := a.confirmSpokenWord(first); err != nil {
		t.Fatal(err)
	}
	if err := a.confirmSpokenWord(first); err != nil {
		t.Fatal(err)
	}
	cards := words()
	if len(cards) != 1 || cards[0].From != "host" || len(a.askViews()) != 2 {
		t.Fatalf("one word card from the host beside the identity's own ask, however often it is asked for: %+v", a.askViews())
	}
	want := fmt.Sprintf(`At turn %d you said: "Yes, record it as our "charter"."`, first)
	if !strings.HasPrefix(cards[0].Text, want) || strings.Contains(cards[0].Text, voiceMarker) {
		t.Fatalf("the card shows the words as the record holds them, without the host's marker:\n%s\nwant it to begin %s", cards[0].Text, want)
	}

	// Words longer than a card holds are cut, and the card says so and
	// where the whole is.
	long := spokenTurn(t, a.store, strings.Repeat("é", askWordsMax+10), atDesk)
	if err := a.confirmSpokenWord(long); err != nil {
		t.Fatal(err)
	}
	if cards = words(); len(cards) != 1 || !strings.Contains(cards[0].Text, fmt.Sprintf("… (the first %d of %d characters; the whole is in the conversation, at turn %d)", askWordsMax, askWordsMax+10, long)) {
		t.Fatalf("a cut is said on the card: %+v", cards)
	}

	// A newer spoken word takes the older card's place.
	second := spokenTurn(t, a.store, "and we meet in the mornings", atDesk)
	if err := a.confirmSpokenWord(second); err != nil {
		t.Fatal(err)
	}
	if cards = words(); len(cards) != 1 || !strings.Contains(cards[0].Text, fmt.Sprintf("At turn %d", second)) {
		t.Fatalf("the newer words' card takes the older's place: %+v", cards)
	}
	id := cards[0].ID

	// Nothing but confirm or no answers it, and a refused press keeps it.
	for _, other := range []dashboard.AskAnswer{{ID: id, Answer: "said", Text: "yes"}, {ID: id, Answer: "chose", Choice: "These are my words"}, {ID: id, Answer: "not_now"}} {
		if _, _, err := a.answerAsk(other); err == nil {
			t.Fatalf("a word card answered %q", other.Answer)
		}
	}
	if len(words()) != 1 || confirmed(second) {
		t.Fatal("a refused press took the card away or confirmed the words")
	}

	// No: the answer goes to the identity, and nothing is confirmed.
	text, settle, err := a.answerAsk(dashboard.AskAnswer{ID: id, Answer: "no"})
	if err != nil {
		t.Fatal(err)
	}
	settle(true)
	if !strings.HasPrefix(text, store.AskMarker+id+"] no") || confirmed(second) || len(words()) != 0 {
		t.Fatalf("no is an answer and confirms nothing: %q, confirmed %v", text, confirmed(second))
	}

	// Confirm: recorded on that turn, and the answer goes to the identity.
	if err := a.confirmSpokenWord(second); err != nil {
		t.Fatal(err)
	}
	id = words()[0].ID
	text, settle, err = a.answerAsk(dashboard.AskAnswer{ID: id, Answer: "confirm"})
	if err != nil {
		t.Fatal(err)
	}
	if !confirmed(second) || confirmed(first) {
		t.Fatalf("the press of confirm records the confirmation on its own turn and no other")
	}
	if !strings.HasPrefix(text, store.AskMarker+id+"] confirmed") {
		t.Fatalf("the confirmation is an answer to the identity: %q", text)
	}
	settle(true)
	if decisionOf(t, a, "ws_1") != owed || len(a.askViews()) != 1 {
		t.Fatalf("the word card's answer leaves the identity's own ask as it was: %+v", a.askViews())
	}
}

// A SPOKEN AFFIRMATION IS RECORDED ONCE THE OPERATOR CONFIRMS IT IN THE
// PAGE, through the whole identity: its own commit verb is refused and the
// card is put on the page; the operator's press arrives through the page's
// socket as an answer and not as their word; and the identity's commit
// then cites the words they spoke.
func TestASpokenAffirmationIsRecordedOnceTheOperatorConfirmsItInThePage(t *testing.T) {
	c := newCharterIdentity(t)
	a := c.app
	const atDesk = "6f0c2c1e-8a41-4f6e-9a55-0d1c2b3a4e5f"
	if _, err := a.applyConfigChange(map[string]any{"speech.operator_speakers": []any{atDesk}}); err != nil {
		t.Fatal(err)
	}
	spoken := spokenTurn(t, a.store, "Yes, record it as our charter.", atDesk)
	args := func() map[string]any {
		return map[string]any{"variant": "relationship.upsert", "id": "rel_spoken_charter", "counterpart_name": "Quinn",
			"counterpart_role": "operator", "charter_text": "Quinn is my operator; we work in the mornings."}
	}
	ctx := context.Background()
	if _, err := a.engine.ExecuteAction(ctx, "verb", "commit", args()); err == nil || !strings.Contains(err.Error(), "a card on their page now shows them those words") {
		t.Fatalf("the identity's commit citing an unconfirmed spoken word must be refused, saying the card is on the page: %v", err)
	}
	var card dashboard.AskView
	for _, v := range a.askViews() {
		if v.Kind == askKindWord {
			card = v
		}
	}
	if card.ID == "" || !strings.Contains(card.Text, fmt.Sprintf(`At turn %d you said: "Yes, record it as our charter."`, spoken)) {
		t.Fatalf("the card for the spoken words is on the page: %+v", a.askViews())
	}

	if !a.TryBeginTurn() {
		t.Fatal("no running turn to answer into")
	}
	conn := dialWSS(t, a.dashboard.Origin())
	raw, _ := json.Marshal(dashboard.ClientMessage{Type: "ask", Ask: &dashboard.AskAnswer{ID: card.ID, Answer: "confirm"}})
	wctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := conn.Write(wctx, websocket.MessageText, raw); err != nil {
		t.Fatal(err)
	}
	readUntil(t, conn, func(m *dashboard.ServerMessage) bool { return m.Type == "steered" })
	a.DrainSteering()
	a.EndTurn()
	if got := saidOn(t, a.store, store.AskMarker+card.ID+"] confirmed"); got != interaction.SaidAnswer {
		t.Fatalf("the press is recorded as %q, want an answer", got)
	}
	turn, err := a.store.LatestOperatorWord()
	if err != nil || turn == nil || turn.TurnSeq != spoken {
		t.Fatalf("the spoken words stand as the operator's latest word, the press after them none of it: %+v, %v", turn, err)
	}
	out, err := a.engine.ExecuteAction(ctx, "verb", "commit", args())
	if err != nil {
		t.Fatalf("the commit once the operator confirmed: %v", err)
	}
	if !strings.Contains(out, fmt.Sprintf("citing the operator's words at turn %d", spoken)) {
		t.Fatalf("the mint cites the spoken words:\n%s", out)
	}
	var waiting *store.UnconfirmedSpeechError
	again := spokenTurn(t, a.store, "and add the afternoons", atDesk)
	if _, err := a.store.LatestOperatorWord(); !errors.As(err, &waiting) || waiting.TurnSeq != again {
		t.Fatalf("newer spoken words wait for their own confirmation: %v", err)
	}
}
