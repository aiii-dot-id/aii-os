package dashboard

import (
	"context"
	"strings"
	"testing"

	"github.com/coder/websocket"
)

func TestCopyNotesIsAnsweredWithWhatTheHostDid(t *testing.T) {
	for _, c := range []struct {
		name     string
		refusal  *AgentNotesRefusal
		wrote    bool
		missing  bool
		wantType string
		wantCode string
		wantSays string
	}{
		{name: "copied", wrote: true, wantType: "sandbox", wantSays: "2 notes"},
		{name: "refused", refusal: &AgentNotesRefusal{Code: AgentNotesExists, Reason: "fixture: already there"}, wantType: "error", wantCode: AgentNotesExists, wantSays: "fixture: already there"},
		{name: "did not finish", refusal: &AgentNotesRefusal{Code: AgentNotesFailed, Reason: "fixture: the copy did not finish"}, wrote: true, wantType: "error", wantCode: AgentNotesFailed, wantSays: "did not finish"},
		{name: "no such hook", missing: true, wantType: "error", wantSays: "not available"},
	} {
		t.Run(c.name, func(t *testing.T) {
			copied := false
			h := &WSHandler{
				CopyAgentNotes: func(context.Context) (string, error) {
					copied = c.wrote
					if c.refusal != nil {
						return "", c.refusal
					}
					return "2 notes that other AI agents kept on this machine were copied", nil
				},
				GetSandbox: func() (*SandboxState, error) {
					return &SandboxState{Root: "/identity", AgentNotes: &AgentNotesState{Copied: copied}}, nil
				},
			}
			if c.missing {
				h.CopyAgentNotes = nil
			}
			s := New("127.0.0.1", 0, h)
			addr, err := s.Start(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { s.Shutdown(context.Background()) })
			conn, other := dialWS(t, addr), dialWS(t, addr)
			// Each page has been answered once, so each is being served.
			for id, page := range map[string]*websocket.Conn{"hello-asking": conn, "hello-other": other} {
				sendMsg(t, page, ClientMessage{Type: "query", Query: "sandbox", RequestID: id})
				if answer := readMsg(t, page); answer.Type != "sandbox" || answer.RequestID != id {
					t.Fatalf("a page reads the sandbox: %+v", answer)
				}
			}
			sendMsg(t, conn, map[string]any{"type": "agent_notes_copy", "request_id": "n1"})
			answer := readMsg(t, conn)
			if answer.Type != c.wantType || answer.RequestID != "n1" || answer.RequestErrorCode != c.wantCode || !strings.Contains(answer.Message, c.wantSays) {
				t.Fatalf("reply: type=%q id=%q code=%q message=%q, want %s %q saying %q", answer.Type, answer.RequestID, answer.RequestErrorCode, answer.Message, c.wantType, c.wantCode, c.wantSays)
			}
			if c.missing {
				return
			}
			if c.wantType == "error" {
				answer = readMsg(t, conn)
				if answer.Type != "sandbox" || answer.RequestID != "" {
					t.Fatalf("the refused page is not given the sandbox after the reason: %+v", answer)
				}
			}
			if answer.Sandbox == nil || answer.Sandbox.AgentNotes == nil || answer.Sandbox.AgentNotes.Copied != c.wrote {
				t.Fatalf("the answer is not the sandbox as the host holds it: %+v", answer.Sandbox)
			}
			heard := readMsg(t, other)
			if heard.Type != "sandbox" || heard.RequestID != "" || heard.Sandbox == nil || heard.Sandbox.AgentNotes.Copied != c.wrote {
				t.Fatalf("another open page was not told: %+v", heard)
			}
		})
	}
}
