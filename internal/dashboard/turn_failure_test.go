package dashboard

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/interaction"
	"github.com/aiii-dot-id/aii-os/internal/llm"
	"github.com/aiii-dot-id/aii-os/internal/store"
	"github.com/coder/websocket"
)

// A PROVIDER'S FAILURE IS CLASSED FOR ITS TURN (spec step 4, "a provider's
// failure shown on its turn"). A provider that did not answer in time is
// "provider_timeout"; one that could not be reached, or refused the
// request, is "provider_failed" with its short cause, however deeply the
// error is wrapped; a deadline met outside the request, and anything else,
// is unclassed and shown in the daemon's own words.
func TestATurnsProviderFailureIsClassed(t *testing.T) {
	dial := &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connection refused")}
	request := &url.Error{Op: "Post", URL: "https://api.example.test/v1/chat", Err: dial}
	slow := &url.Error{Op: "Post", URL: "https://api.example.test/v1/chat", Err: context.DeadlineExceeded}
	for _, c := range []struct {
		name            string
		err             error
		failure, reason string
	}{
		{"no connection", dial, "provider_failed", "connection refused"},
		{"no connection, through the request, wrapped", &llm.CallError{Err: fmt.Errorf("chat: %w", request)}, "provider_failed", "connection refused"},
		{"no answer in time", fmt.Errorf("chat: %w", slow), "provider_timeout", ""},
		{"unavailable", &llm.HTTPError{StatusCode: 503}, "provider_failed", "Service Unavailable"},
		{"a refused key", fmt.Errorf("turn: %w", &llm.HTTPError{StatusCode: 401}), "provider_failed", "Unauthorized"},
		{"a deadline outside the request", fmt.Errorf("tool: %w", context.DeadlineExceeded), "", ""},
		{"anything else", errors.New("the record could not be written"), "", ""},
	} {
		if failure, reason := turnFailure(c.err); failure != c.failure || reason != c.reason {
			t.Errorf("%s: %q %q, want %q %q", c.name, failure, reason, c.failure, c.reason)
		}
	}
}

// A TURN'S ERROR FRAME NAMES ITS TURN (spec step 4, amendment 7). The
// daemon keeps the recorded operator message a turn answers, from its
// recording, and carries it on the broadcast error as turn_ref, so every
// page, not only the one that sent it, puts the failure under that message.
func TestATurnsErrorFrameNamesItsTurn(t *testing.T) {
	st, err := store.NewMemory()
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	var recorded interaction.Location
	s := New("127.0.0.1", 0, &WSHandler{Speaker: "identity", Interactions: st, HandleMessage: func(ctx context.Context, text string) (string, error) {
		ref, err := st.RecordRelatedConversation(ctx, "operator", text, interaction.TurnID(ctx), "", interaction.Details{})
		if err != nil {
			return "", err
		}
		recorded = interaction.Location{Identity: "fixture-identity", Source: "recorded", Incarnation: st.InteractionIncarnation(), ID: ref.ID}
		ChatRecorded(ctx, &recorded, nil)
		return "", &llm.HTTPError{StatusCode: 503}
	}})
	addr, err := s.Start(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Shutdown(context.Background())
	sender, other := dialWS(t, addr), dialWS(t, addr)
	sendMsg(t, sender, ClientMessage{Type: "chat", RequestID: "q1", Message: "Are you there?"})
	for _, conn := range []*websocket.Conn{sender, other} {
		frame := drainUntil(t, conn, "error")
		if frame.TurnFailure != "provider_failed" || frame.TurnReason != "Service Unavailable" {
			t.Fatalf("the error frame is not classed: %q %q", frame.TurnFailure, frame.TurnReason)
		}
		if frame.TurnRef == nil || *frame.TurnRef != recorded || recorded.ID == "" {
			t.Fatalf("the error frame does not name the message its turn answered: %+v, want %+v", frame.TurnRef, recorded)
		}
	}
}
