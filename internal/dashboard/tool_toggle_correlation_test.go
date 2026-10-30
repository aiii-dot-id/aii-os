package dashboard

import (
	"context"
	"errors"
	"testing"
)

// A tool switch is answered to the request that asked, on a refusal as on
// a change, and a change reaches every other page, one carried through a
// relay among them: their switches say what the identity can reach too.
func TestAToolSwitchAnswersItsRequestAndTellsEveryPage(t *testing.T) {
	enabled := true
	h := &WSHandler{
		SetToolFunc: func(name string, on bool) error {
			if name != "shell" {
				return errors.New("fixture refusal")
			}
			enabled = on
			return nil
		},
		GetTools: func() []ToolState { return []ToolState{{Name: "shell", Enabled: enabled}} },
		Channels: carriedChannels(t),
	}
	s := New("127.0.0.1", 0, h)
	addr, err := s.Start(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Shutdown(context.Background()) })
	asker, other := dialWS(t, addr), dialWS(t, addr)
	carried := openCarriedChannelPage(t, s)
	waitForConns(t, s, 3)

	for _, refused := range []ClientMessage{
		{Type: "tool_toggle", RequestID: "no-such", Tool: "absent"},
		{Type: "tool_toggle", RequestID: "no-name"},
	} {
		sendMsg(t, asker, refused)
		if got := readMsg(t, asker); got.Type != "error" || got.RequestID != refused.RequestID {
			t.Fatalf("a refused switch answered type=%q id=%q, want error/%s: the page reads an error with no id as a turn's", got.Type, got.RequestID, refused.RequestID)
		}
	}

	sendMsg(t, asker, ClientMessage{Type: "tool_toggle", RequestID: "off", Tool: "shell", Enabled: false})
	if got := readMsg(t, asker); got.Type != "tools" || got.RequestID != "off" || len(got.Tools) != 1 || got.Tools[0].Enabled {
		t.Fatalf("the asking page got type=%q id=%q tools=%+v, want the new list under its own id", got.Type, got.RequestID, got.Tools)
	}
	// The refusals above reached nobody else, so this is the other page's
	// first frame.
	if got := readMsg(t, other); got.Type != "tools" || got.RequestID != "" || len(got.Tools) != 1 || got.Tools[0].Enabled {
		t.Fatalf("the other page got type=%q id=%q tools=%+v, want the new list: it still shows shell on", got.Type, got.RequestID, got.Tools)
	}
	if got := carried.until(t, "tools", "it still shows shell on"); got.RequestID != "" || len(got.Tools) != 1 || got.Tools[0].Enabled {
		t.Fatalf("the carried page got id=%q tools=%+v, want the new list: it still shows shell on", got.RequestID, got.Tools)
	}
}
