package dashboard

import (
	"context"
	"errors"
	"testing"
)

func TestSandboxRepliesEchoRequestOnSuccessAndFailure(t *testing.T) {
	for _, kind := range []string{"save", "refused", "unavailable", "read-failed", "read-unavailable", "query"} {
		t.Run(kind, func(t *testing.T) {
			h := &WSHandler{
				SetSandbox: func([]string) error {
					if kind == "refused" {
						return errors.New("fixture refusal")
					}
					return nil
				},
				GetSandbox: func() (*SandboxState, error) {
					if kind == "read-failed" {
						return nil, errors.New("fixture read failure")
					}
					return &SandboxState{Root: "/identity"}, nil
				},
			}
			if kind == "unavailable" {
				h.SetSandbox = nil
			}
			if kind == "read-unavailable" {
				h.GetSandbox = nil
			}
			s := New("127.0.0.1", 0, h)
			addr, err := s.Start(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { s.Shutdown(context.Background()) })
			conn := dialWS(t, addr)
			msg := ClientMessage{Type: "sandbox_set", RequestID: "sandbox-" + kind, Roots: []string{"/project"}}
			if kind == "query" {
				msg.Type = "query"
				msg.Query = "sandbox"
			}
			sendMsg(t, conn, msg)
			answer := readMsg(t, conn)
			want := "error"
			if kind == "save" || kind == "query" {
				want = "sandbox"
			}
			if answer.Type != want || answer.RequestID != msg.RequestID {
				t.Fatalf("reply: type=%q id=%q, want %s/%s", answer.Type, answer.RequestID, want, msg.RequestID)
			}
		})
	}
}
