package dashboard

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

// "pass_now" is answered under its own member, correlated by the request
// id the page sent, with the reason typed; without a daemon to ask it is
// an error frame, and so is a store that could not be read (F-COG-110).
func TestPassNowAnswersOnTheWire(t *testing.T) {
	calls := 0
	h := &WSHandler{PassNow: func(ctx context.Context) (PassNowResult, error) {
		calls++
		switch calls {
		case 1:
			return PassNowResult{Moved: true, AskedAt: "2026-10-31T11:30:00Z"}, nil
		case 2:
			return PassNowResult{Reason: "turn_holding"}, nil
		default:
			return PassNowResult{}, errors.New("read alarm rhythm: disk gone")
		}
	}}
	s := New("127.0.0.1", 0, h)
	addr, err := s.Start(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Shutdown(context.Background())
	conn := dialWS(t, addr)

	sendMsg(t, conn, ClientMessage{RequestID: "press-1", Type: "pass_now"})
	m := drainUntil(t, conn, "pass_now")
	if m.RequestID != "press-1" || m.PassNow == nil || !m.PassNow.Moved || m.PassNow.AskedAt != "2026-10-31T11:30:00Z" || m.PassNow.Reason != "" {
		t.Fatalf("a moved press must answer under pass_now with its id and asked_at: %+v", m)
	}
	sendMsg(t, conn, ClientMessage{RequestID: "press-2", Type: "pass_now"})
	m = drainUntil(t, conn, "pass_now")
	if m.RequestID != "press-2" || m.PassNow == nil || m.PassNow.Moved || m.PassNow.Reason != "turn_holding" || m.PassNow.AskedAt != "" {
		t.Fatalf("a refusal must answer under pass_now with its typed reason and no asked_at: %+v", m)
	}
	sendMsg(t, conn, ClientMessage{RequestID: "press-3", Type: "pass_now"})
	if e := drainUntil(t, conn, "error"); e.RequestID != "press-3" || e.PassNow != nil {
		t.Fatalf("a store error must be an error frame for the same request, not a pass_now answer: %+v", e)
	}
}

func TestPassNowWithoutADaemonIsAnError(t *testing.T) {
	s := New("127.0.0.1", 0, &WSHandler{})
	addr, err := s.Start(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Shutdown(context.Background())
	conn := dialWS(t, addr)
	sendMsg(t, conn, ClientMessage{RequestID: "press-0", Type: "pass_now"})
	if e := drainUntil(t, conn, "error"); e.RequestID != "press-0" {
		t.Fatalf("without PassNow the request must be refused as not available: %+v", e)
	}
}

// The answer's literal shape on the wire: the member is named pass_now and
// holds moved, reason and asked_at; nothing of the answer sits at the
// frame's top. Decoded as raw JSON, not through ServerMessage, so a renamed
// or lifted member cannot hide behind a shared struct.
func TestPassNowIsNestedUnderItsOwnMemberOnTheWire(t *testing.T) {
	s := New("127.0.0.1", 0, &WSHandler{PassNow: func(ctx context.Context) (PassNowResult, error) {
		return PassNowResult{Moved: true, AskedAt: "2026-10-31T11:30:00.250Z"}, nil
	}})
	raw, err := json.Marshal(ServerMessage{Type: "pass_now", RequestID: "press-9", PassNow: &PassNowResult{Moved: true, AskedAt: "2026-10-31T11:30:00.250Z"}})
	if err != nil {
		t.Fatal(err)
	}
	_ = s
	var frame map[string]json.RawMessage
	if err := json.Unmarshal(raw, &frame); err != nil {
		t.Fatal(err)
	}
	member, ok := frame["pass_now"]
	if !ok {
		t.Fatalf("the answer must travel under a member named pass_now: %s", raw)
	}
	for _, top := range []string{"moved", "reason", "asked_at"} {
		if _, lifted := frame[top]; lifted {
			t.Fatalf("%q must not sit at the frame's top: %s", top, raw)
		}
	}
	var inner map[string]any
	if err := json.Unmarshal(member, &inner); err != nil {
		t.Fatal(err)
	}
	if inner["moved"] != true || inner["asked_at"] != "2026-10-31T11:30:00.250Z" {
		t.Fatalf("pass_now must carry moved and asked_at under their own names: %s", member)
	}
	if _, has := inner["reason"]; !has {
		t.Fatalf("pass_now must carry reason, empty on a move: %s", member)
	}
}
