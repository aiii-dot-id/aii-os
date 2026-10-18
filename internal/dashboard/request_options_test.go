package dashboard

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/conversation"
	"github.com/coder/websocket"
)

func requestOptionsFixture(t *testing.T) *conversation.RequestOptions {
	t.Helper()
	var options conversation.RequestOptions
	if err := json.Unmarshal([]byte(`{"allowed_tools":["read"],"response_format":{"type":"json_schema","json_schema":{"name":"answer","strict":true,"schema":{"type":"object","properties":{"answer":{"type":"integer"}},"required":["answer"],"additionalProperties":false}}}}`), &options); err != nil {
		t.Fatal(err)
	}
	return &options
}

func TestRequestOptionsTravelWithoutReplacingSpokenWords(t *testing.T) {
	var admitted atomic.Bool
	h := &WSHandler{Speaker: "identity", HandleMessage: func(context.Context, string) (string, error) { return "", nil }, ReleaseTurn: func() {}}
	h.AdmitRequest = func(ctx context.Context, _ string) error {
		opts, present := conversation.RequestOptionsFromContext(ctx)
		if !present || opts.AllowedTools == nil || len(*opts.AllowedTools) != 1 || (*opts.AllowedTools)[0] != "read" {
			t.Error("admission lost tool scope")
		}
		admitted.Store(true)
		return nil
	}
	h.ObserveChat = func(ctx context.Context, _ string, _ func(string, string, string)) (string, error) {
		opts, present := conversation.RequestOptionsFromContext(ctx)
		if !present || opts.ResponseFormat == nil || !admitted.Load() {
			t.Error("turn lost request contract")
		}
		ChatStructuredOutput(ctx, json.RawMessage(`{"answer":42}`))
		return "I read the source.\n\n{\"answer\":42}", nil
	}
	s := New("127.0.0.1", 0, h)
	addr, err := s.Start(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Shutdown(context.Background())
	conn := dialWS(t, addr)
	sendMsg(t, conn, ClientMessage{Type: "chat", RequestID: "scoped-1", Message: "read", RequestOptions: requestOptionsFixture(t)})
	for {
		got := drainUntil(t, conn, "response")
		if !got.Done {
			continue
		}
		if got.RequestID != "scoped-1" || string(got.StructuredOutput) != `{"answer":42}` || got.Message != "I read the source.\n\n{\"answer\":42}" {
			t.Fatalf("response lost a contract: %+v", got)
		}
		break
	}
}

func TestRequestOptionsRejectAmbiguityBeforeAdmission(t *testing.T) {
	var calls atomic.Int32
	h := &WSHandler{HandleMessage: func(context.Context, string) (string, error) { calls.Add(1); return "", nil }, AdmitRequest: func(context.Context, string) error { calls.Add(1); return nil }, ReleaseTurn: func() {}}
	s := New("127.0.0.1", 0, h)
	addr, err := s.Start(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Shutdown(context.Background())
	for _, raw := range []string{
		`{"type":"chat","request_id":"x","message":"q","request_options":{"unknown":true}}`,
		`{"type":"chat","request_id":"x","message":"q","request_options":{}}`,
		`{"type":"chat","request_id":"x","message":"q","request_options":{"allowed_tools":["read"],"allowed_tools":[]}}`,
		`{"type":"chat","request_id":"x","message":"q","request_options":null}`,
		`{"type":"chat","request_id":"x","message":"q","request_options":{"allowed_tools":null}}`,
		`{"type":"chat","request_id":"x","message":"q","request_options":{"response_format":null}}`,
		`{"type":"chat","request_id":"x","message":"q","Request_Options":{"allowed_tools":[]}}`,
		`{"type":"chat","request_id":"x","message":"q","request_options":{"allowed_tools":["read","read"]}}`,
		`{"type":"chat","message":"q","request_options":{"allowed_tools":[]}}`,
		`{"type":"query","request_id":"x","query":"status","request_options":{"allowed_tools":[]}}`,
	} {
		conn := dialWS(t, addr)
		if err := conn.Write(t.Context(), websocket.MessageText, []byte(raw)); err != nil {
			t.Fatal(err)
		}
		got := drainUntil(t, conn, "error")
		if got.RequestErrorCode != "REQUEST_OPTIONS_INVALID" {
			t.Fatalf("wrong rejection: %+v", got)
		}
		conn.Close(websocket.StatusNormalClosure, "")
	}
	if calls.Load() != 0 {
		t.Fatalf("invalid contract admitted %d times", calls.Load())
	}
}

func TestRequestOptionsRequireSupportingHandler(t *testing.T) {
	var called atomic.Bool
	h := &WSHandler{HandleMessage: func(context.Context, string) (string, error) { called.Store(true); return "no", nil }}
	s := New("127.0.0.1", 0, h)
	addr, err := s.Start(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Shutdown(context.Background())
	conn := dialWS(t, addr)
	sendMsg(t, conn, ClientMessage{Type: "chat", RequestID: "unsupported", Message: "q", RequestOptions: requestOptionsFixture(t)})
	got := drainUntil(t, conn, "error")
	if got.RequestErrorCode != "REQUEST_OPTIONS_UNSUPPORTED" || called.Load() {
		t.Fatalf("silently dropped contract: %+v", got)
	}
}

// A sender tells options that can never be admitted from a turn that cannot
// begin now by the frame's code, without reading its prose.
func TestARefusedRequestSaysWhetherItsOptionsOrTheMomentRefusedIt(t *testing.T) {
	for want, refusal := range map[string]error{
		"REQUEST_OPTIONS_INVALID": fmt.Errorf("admission: %w", &conversation.RequestOptionsError{Reason: "allowed_tools names a tool this turn does not have"}),
		"REQUEST_NOT_ADMITTED":    errors.New("another turn is active"),
	} {
		t.Run(want, func(t *testing.T) {
			var called atomic.Bool
			h := &WSHandler{
				AdmitRequest:  func(context.Context, string) error { return refusal },
				ReleaseTurn:   func() {},
				HandleMessage: func(context.Context, string) (string, error) { called.Store(true); return "no", nil },
			}
			s := New("127.0.0.1", 0, h)
			addr, err := s.Start(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			defer s.Shutdown(context.Background())
			conn := dialWS(t, addr)
			sendMsg(t, conn, ClientMessage{Type: "chat", RequestID: "refused", Message: "q", RequestOptions: requestOptionsFixture(t)})
			got := drainUntil(t, conn, "error")
			if got.RequestID != "refused" || got.RequestErrorCode != want || called.Load() {
				t.Fatalf("want %s on the refused request's own frame, turn not run: %+v", want, got)
			}
		})
	}
}

func TestRequestOptionsCannotReturnUnvalidatedOutput(t *testing.T) {
	for _, output := range []string{"", `{"answer":"wrong"}`, `{"answer":1,"answer":2}`} {
		t.Run(output, func(t *testing.T) {
			h := &WSHandler{HandleMessage: func(ctx context.Context, _ string) (string, error) {
				ChatStructuredOutput(ctx, json.RawMessage(output))
				return "spoken words", nil
			}, AdmitRequest: func(context.Context, string) error { return nil }, ReleaseTurn: func() {}}
			s := New("127.0.0.1", 0, h)
			addr, err := s.Start(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			defer s.Shutdown(context.Background())
			conn := dialWS(t, addr)
			sendMsg(t, conn, ClientMessage{Type: "chat", RequestID: "bad-result", Message: "q", RequestOptions: requestOptionsFixture(t)})
			got := drainUntil(t, conn, "error")
			if got.RequestID != "bad-result" || got.RequestErrorCode != "RESPONSE_FORMAT_VIOLATION" || !strings.Contains(got.Message, "identity error") {
				t.Fatalf("not failed: %+v", got)
			}
		})
	}
}

func TestRequestOptionsSurviveDisconnectAndEndAtShutdown(t *testing.T) {
	deadline, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	started, inspect := make(chan struct{}), make(chan struct{})
	checked, released := make(chan struct{}), make(chan struct{})
	h := &WSHandler{
		AdmitRequest: func(context.Context, string) error { return nil },
		ReleaseTurn:  func() {},
		HandleMessage: func(ctx context.Context, _ string) (string, error) {
			defer close(released)
			close(started)
			select {
			case <-inspect:
			case <-ctx.Done():
				return "", ctx.Err()
			}
			opts, ok := conversation.RequestOptionsFromContext(ctx)
			if !ok || opts.AllowedTools == nil || len(*opts.AllowedTools) != 1 || opts.ResponseFormat == nil {
				t.Error("disconnect dropped admitted options")
			}
			close(checked)
			<-ctx.Done()
			return "", ctx.Err()
		},
	}
	s := New("127.0.0.1", 0, h)
	addr, err := s.Start(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Shutdown(context.Background())
	conn := dialWS(t, addr)
	sendMsg(t, conn, ClientMessage{Type: "chat", RequestID: "lifetime", Message: "q", RequestOptions: requestOptionsFixture(t)})
	select {
	case <-started:
	case <-deadline.Done():
		t.Fatal("turn did not start")
	}
	conn.CloseNow()
	waitForConnsAtMost(t, s, 0)
	close(inspect)
	select {
	case <-checked:
	case <-deadline.Done():
		t.Fatal("turn lost with connection")
	}
	s.Shutdown(context.Background())
	select {
	case <-released:
	case <-deadline.Done():
		t.Fatal("turn not released by shutdown")
	}
}
