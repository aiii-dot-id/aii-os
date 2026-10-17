package dashboard

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/interaction"
	"github.com/coder/websocket"
)

func TestChatRecordingReceiptsQualifyDurabilityAndCompleteOnce(t *testing.T) {
	for _, tc := range []struct {
		name, source, code string
		failed, before     bool
	}{
		{"durable", "recorded", "", false, false},
		{"safe", "transient", "", false, false},
		{"fallback", "transient", ChatRecordingFailedCode, true, false},
		{"not invoked", "", ChatNotRecordedCode, false, true},
		{"unknown recording", "", ChatRecordingFailedCode, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var got []ChatRecording
			ctx := withChatRecording(context.Background(), func(r ChatRecording) { got = append(got, r) })
			var ref *interaction.Location
			if tc.source != "" {
				ref = &interaction.Location{Identity: "identity", Source: tc.source, Incarnation: "owner", ID: "row"}
			}
			var err error
			if tc.failed {
				err = errors.New("recording refused")
			}
			if tc.before {
				ChatNotRecorded(ctx, "wait cancelled")
			} else {
				ChatRecorded(ctx, ref, err)
			}
			// Racing cleanup and duplicate delivery never replace the first fact.
			var wg sync.WaitGroup
			for i := 0; i < 8; i++ {
				wg.Add(1)
				go func() { defer wg.Done(); ChatNotRecorded(ctx, "late cleanup") }()
			}
			wg.Wait()
			if len(got) != 1 {
				t.Fatalf("got %d receipts", len(got))
			}
			r := got[0]
			if (r.Error != nil) != (tc.code != "") || r.Error != nil && r.Error.Code != tc.code {
				t.Fatalf("wrong failure: %+v", r)
			}
			if (r.Ref != nil) != (tc.source != "") || r.Ref != nil && r.Ref.Source != tc.source {
				t.Fatalf("wrong reference: %+v", r)
			}
			data, err := json.Marshal(ServerMessage{Type: "chat_receipt", RequestID: "chat-1", ChatRecording: &r})
			if err != nil {
				t.Fatal(err)
			}
			var msg map[string]json.RawMessage
			if err := json.Unmarshal(data, &msg); err != nil || len(msg) != 3 {
				t.Fatalf("unexpected envelope: %s %v", data, err)
			}
		})
	}
}

func TestChatRecordingCannotPublishAnUnconfirmedDurableReference(t *testing.T) {
	for _, ref := range []*interaction.Location{
		nil, {}, {Identity: "identity", Source: "other", Incarnation: "owner", ID: "row"},
		{Identity: "identity", Source: "recorded", Incarnation: "owner", ID: "row"},
	} {
		ctx := withChatRecording(context.Background(), func(r ChatRecording) {
			if r.Ref != nil || r.Error == nil || r.Error.Code != ChatRecordingFailedCode {
				t.Fatalf("false confirmation: %+v", r)
			}
		})
		ChatRecorded(ctx, ref, errors.New("append outcome uncertain"))
	}
}

func TestChatRecordingPrecedesAdmissionAckAndStaysOnItsSocket(t *testing.T) {
	h := &WSHandler{Speaker: "identity", HandleMessage: func(context.Context, string) (string, error) { return "", nil }}
	h.AdmitChat = func(ctx context.Context, text string) (bool, error) {
		ChatRecorded(ctx, &interaction.Location{Identity: "identity", Source: "recorded", Incarnation: "owner", ID: text}, nil)
		return true, nil
	}
	s := New("127.0.0.1", 0, h)
	addr, err := s.Start(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Shutdown(context.Background())
	a, b := dialWS(t, addr), dialWS(t, addr)
	for _, c := range []struct {
		id   string
		send func()
	}{
		{"first", func() { sendMsg(t, a, ClientMessage{Type: "chat", RequestID: "same-id", Message: "first"}) }},
		{"second", func() { sendMsg(t, b, ClientMessage{Type: "chat", RequestID: "same-id", Message: "second"}) }},
	} {
		c.send()
	}
	for _, pair := range []struct {
		expected string
		read     func(string) ServerMessage
	}{
		{"first", func(kind string) ServerMessage { return drainUntil(t, a, kind) }},
		{"second", func(kind string) ServerMessage { return drainUntil(t, b, kind) }},
	} {
		r := pair.read("chat_receipt")
		if r.RequestID != "same-id" || r.ChatRecording == nil || r.ChatRecording.Ref.ID != pair.expected {
			t.Fatalf("cross-client receipt: %+v", r)
		}
		if ack := pair.read("chat_ack"); ack.Message != "accepted" {
			t.Fatalf("wrong admission: %+v", ack)
		}
	}
}

func TestChatRecordingRefusedJobReturnsItsTurnClaim(t *testing.T) {
	var held atomic.Bool
	h := &WSHandler{HandleMessage: func(context.Context, string) (string, error) { t.Error("refused job ran"); return "", nil },
		AdmitChat: func(context.Context, string) (bool, error) { held.Store(true); return false, nil }, ReleaseTurn: func() { held.Store(false) }}
	s := New("127.0.0.1", 0, h)
	addr, err := s.Start(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Shutdown(context.Background())
	conn := dialWS(t, addr)
	// The client handshake can finish before the server registers this
	// connection. Establish registration before setting the stopping state
	// so the request tests runJob's refusal path instead of handshake refusal.
	waitForConns(t, s, 1)
	s.jobsMu.Lock()
	s.jobsStopping = true
	s.jobsMu.Unlock()
	sendMsg(t, conn, ClientMessage{Type: "chat", RequestID: "refused", Message: "words"})
	r := drainUntil(t, conn, "chat_receipt")
	if held.Load() || r.ChatRecording == nil || r.ChatRecording.Ref != nil || r.ChatRecording.Error.Code != ChatNotRecordedCode {
		t.Fatalf("claim leaked or false receipt: held=%v %+v", held.Load(), r)
	}
}

func TestChatRecordingQueuedWaitFailureReportsNoRecording(t *testing.T) {
	h := &WSHandler{HandleMessage: func(context.Context, string) (string, error) {
		t.Error("failed wait reached recording")
		return "", nil
	},
		AdmitChat: func(context.Context, string) (bool, error) { return false, ErrBusyInternal }, AcquireTurn: func(context.Context) error { return context.Canceled }}
	s := New("127.0.0.1", 0, h)
	addr, err := s.Start(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Shutdown(context.Background())
	conn := dialWS(t, addr)
	sendMsg(t, conn, ClientMessage{Type: "chat", RequestID: "queued", Message: "words"})
	r := drainUntil(t, conn, "chat_receipt")
	if r.RequestID != "queued" || r.ChatRecording == nil || r.ChatRecording.Ref != nil || r.ChatRecording.Error.Code != ChatNotRecordedCode {
		t.Fatalf("false queued recording: %+v", r)
	}
}

func TestChatRecordingSocketLossDoesNotCancelTheAdmittedTurn(t *testing.T) {
	wait, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	started, proceed := make(chan struct{}), make(chan struct{})
	done := make(chan error, 1)
	h := &WSHandler{Speaker: "identity", HandleMessage: func(ctx context.Context, _ string) (string, error) {
		close(started)
		<-proceed
		ChatRecorded(ctx, &interaction.Location{Identity: "fixture", Source: "recorded", Incarnation: "disk", ID: "row"}, nil)
		done <- ctx.Err()
		return "answer", nil
	}}
	s := New("127.0.0.1", 0, h)
	addr, err := s.Start(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Shutdown(context.Background())
	var release sync.Once
	defer release.Do(func() { close(proceed) })
	conn := dialWS(t, addr)
	sendMsg(t, conn, ClientMessage{Type: "chat", RequestID: "disconnected", Message: "words"})
	select {
	case <-started:
	case <-wait.Done():
		t.Fatal("turn did not start")
	}
	conn.CloseNow()
	release.Do(func() { close(proceed) })
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("socket cancelled admitted work: %v", err)
		}
	case <-wait.Done():
		t.Fatal("recording blocked on dead socket")
	}
}

func TestChatRecordingMissingObservationCannotClaimNothingWasRecorded(t *testing.T) {
	h := &WSHandler{HandleMessage: func(context.Context, string) (string, error) {
		// The server cannot observe whether this handler wrote a record.
		return "answer without a receipt", nil
	}}
	s := New("127.0.0.1", 0, h)
	addr, err := s.Start(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Shutdown(context.Background())
	conn := dialWS(t, addr)
	sendMsg(t, conn, ClientMessage{Type: "chat", RequestID: "unknown", Message: "words"})
	r := drainUntil(t, conn, "chat_receipt")
	if r.ChatRecording == nil || r.ChatRecording.Ref != nil || r.ChatRecording.Error == nil || r.ChatRecording.Error.Code != ChatRecordingFailedCode {
		t.Fatalf("missing confirmation was turned into proof of absence: %+v", r)
	}
}

// A refusal belongs to one submission and socket; it must not masquerade as
// completion of the already-running turn on either screen.
func TestChatRecordingRejectedSteerDoesNotEndActiveTurn(t *testing.T) {
	h := &WSHandler{TurnActive: func() bool { return true },
		AdmitChat: func(context.Context, string) (bool, error) { return false, errors.New("steer queue full") }}
	s := New("127.0.0.1", 0, h)
	addr, err := s.Start(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Shutdown(context.Background())
	a, b := dialWS(t, addr), dialWS(t, addr)
	// Drain startup messages with a round trip before testing isolation.
	for _, c := range []*websocket.Conn{a, b} {
		sendMsg(t, c, ClientMessage{Type: "probe", RequestID: "before"})
		drainUntil(t, c, "probe")
	}
	sendMsg(t, a, ClientMessage{Type: "chat", RequestID: "rejected", Message: "words"})
	receipt := drainUntil(t, a, "chat_receipt")
	if receipt.ChatRecording == nil || receipt.ChatRecording.Error == nil || receipt.ChatRecording.Error.Code != ChatNotRecordedCode {
		t.Fatalf("bad receipt: %+v", receipt)
	}
	failure := drainUntil(t, a, "error")
	if failure.RequestID != "rejected" {
		t.Fatalf("admission refusal presented as turn failure: %+v", failure)
	}
	if ack := drainUntil(t, a, "chat_ack"); ack.RequestID != "rejected" || ack.Message != "refused" {
		t.Fatalf("bad ack: %+v", ack)
	}
	for _, c := range []*websocket.Conn{a, b} {
		sendMsg(t, c, ClientMessage{Type: "probe", RequestID: "after"})
		reply := readMsg(t, c)
		if reply.Type != "probe" || reply.RequestID != "after" || !reply.TurnActive {
			t.Fatalf("rejection changed another turn or socket: %+v", reply)
		}
	}
}
