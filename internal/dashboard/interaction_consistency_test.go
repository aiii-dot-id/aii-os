package dashboard

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/interaction"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

func TestInteractionTwoClientsAndReloadShareRecordedFacts(t *testing.T) {
	st, err := store.NewMemory()
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	h := &WSHandler{Speaker: "identity", Interactions: st, HandleMessage: func(ctx context.Context, text string) (string, error) {
		if err := st.AddConversationTurn("operator", text); err != nil {
			return "", err
		}
		if err := st.AddConversationTurn("resident", "answer"); err != nil {
			return "", err
		}
		return "answer", nil
	}}
	s := New("127.0.0.1", 0, h)
	st.SetInteractionObserver(s.PokeInteractions)
	addr, err := s.Start(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Shutdown(context.Background())
	a, b := dialWS(t, addr), dialWS(t, addr)
	drainUntil(t, a, "interaction_changed")
	drainUntil(t, b, "interaction_changed")
	sendMsg(t, a, ClientMessage{Type: "chat", RequestID: "operator-message", Message: "question"})
	ack := drainUntil(t, a, "chat_ack")
	if ack.RequestID != "operator-message" || ack.Message != "accepted" {
		t.Fatal(ack)
	}
	// The expected oracle is the two actual stored facts, not streamed reply text.
	for {
		m := readMsg(t, b)
		if m.Type == "response" && m.Done {
			break
		}
	}
	read := func(id string) []byte {
		sendMsg(t, b, ClientMessage{Type: "query", Query: "interaction", RequestID: id, InteractionQuery: &interaction.Query{Version: 1}})
		m := drainUntil(t, b, "interaction_page")
		if m.RequestID != id || m.InteractionPage == nil {
			t.Fatal(m)
		}
		rows := m.InteractionPage.Rows
		if len(rows) != 2 || rows[0].Role != interaction.Operator || rows[0].Content != "question" || rows[1].Role != interaction.Resident || rows[1].Content != "answer" || rows[0].ID == rows[1].ID {
			t.Fatal(rows)
		}
		raw, _ := json.Marshal(m.InteractionPage)
		return raw
	}
	first := read("read-one")
	// The dashboard window is an envelope extension; the same latest read still
	// supplies exactly the legacy facts and binds its navigation metadata.
	sendMsg(t, b, ClientMessage{Type: "query", Query: "interaction", RequestID: "window", InteractionQuery: &interaction.Query{Version: 1}, InteractionWindow: &interaction.WindowQuery{Mode: "latest"}})
	window := drainUntil(t, b, "interaction_page")
	windowRaw, _ := json.Marshal(window.InteractionPage)
	if string(windowRaw) != string(first) || window.InteractionWindow == nil || window.InteractionWindow.Fingerprint != window.InteractionPage.Fingerprint {
		t.Fatalf("dashboard window diverged from public history: %+v", window)
	}

	c := dialWS(t, addr)
	drainUntil(t, c, "interaction_changed")
	sendMsg(t, c, ClientMessage{Type: "query", Query: "interaction", RequestID: "fresh", InteractionQuery: &interaction.Query{Version: 1}})
	fresh := drainUntil(t, c, "interaction_page")
	raw, _ := json.Marshal(fresh.InteractionPage)
	if string(raw) != string(first) {
		t.Fatal("fresh client and observer disagree")
	}
}
