package dashboard

import (
	"context"
	"errors"
	"net"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// dialOperator opens the page's own connection (the interaction version it
// declares included) with the session cookie given, or none.
func dialOperator(addr, cookie string) (*websocket.Conn, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	h := http.Header{"Origin": []string{"https://" + addr}}
	if cookie != "" {
		_, port, _ := net.SplitHostPort(addr)
		h.Set("Cookie", "aii_token_"+port+"="+cookie)
	}
	conn, _, err := websocket.Dial(ctx, "wss://"+addr+"/ws?interaction_version=1", &websocket.DialOptions{HTTPClient: testClient, HTTPHeader: h})
	return conn, err
}

// WHAT A PLUGIN STORED IS READ, AND REMOVED, ONLY THROUGH THE OPERATOR'S
// SESSION. The read rides the same connection the removal rides: with a
// token required, a connection without the operator's cookie is never
// opened, so neither can be asked; the operator's connection is answered
// with the page it asked for, wearing the plugin, the kind and the cursor,
// and the removal reaches the host through it. A request that names no
// plugin or no kind is refused before the host is asked; a read that
// failed, and a host with no such read, are answers with their reason.
func TestWhatAPluginStoredIsReadAndRemovedThroughTheOperatorsSessionOnly(t *testing.T) {
	var reads, removals atomic.Int32
	h := &WSHandler{
		PluginStored: func(req PluginStoredRequest) (*PluginStoredPage, error) {
			reads.Add(1)
			if req.Plugin == "org.example.broken" {
				return nil, errors.New("plugins: what org.example.broken remembered could not be read: disk I/O error")
			}
			return &PluginStoredPage{Total: 41, Next: 17,
				Memories: []PluginStoredMemory{{ID: "pm_1", Text: "the whole text of a memory", StoredAt: "2026-10-13T12:00:00Z"}}}, nil
		},
		PluginAct: func(act PluginAction) error {
			if act.Action == "remove_stored" {
				removals.Add(1)
			}
			return nil
		},
		GetConfig: func() *ConfigState { return &ConfigState{} },
	}
	s := New("127.0.0.1", 0, h)
	s.SetAccessToken(true, "the-token")
	addr, err := s.Start(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Shutdown(context.Background())

	for _, cookie := range []string{"", "wrong-token"} {
		if conn, err := dialOperator(addr, cookie); err == nil {
			conn.CloseNow()
			t.Fatalf("a connection with the cookie %q was opened: what a plugin stored could be asked for without the operator's session", cookie)
		}
	}
	conn, err := dialOperator(addr, s.accessHash())
	if err != nil {
		t.Fatalf("the operator's session was refused: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")
	answer := func(id string) ServerMessage {
		t.Helper()
		for i := 0; i < 40; i++ {
			if m := readMsg(t, conn); m.RequestID == id {
				return m
			}
		}
		t.Fatalf("no answer to %s", id)
		return ServerMessage{}
	}

	sendMsg(t, conn, ClientMessage{RequestID: "r1", Type: "plugin_stored", Stored: &PluginStoredRequest{Plugin: "org.example.notes", Kind: "memories", Before: 38}})
	m := answer("r1")
	if p := m.PluginStored; m.Type != "plugin_stored" || p == nil || p.Plugin != "org.example.notes" || p.Kind != "memories" || p.Before != 38 ||
		p.Total != 41 || p.Next != 17 || len(p.Memories) != 1 || p.Memories[0].Text != "the whole text of a memory" || p.Error != "" {
		t.Fatalf("the page comes back wearing what was asked, with its count and its next page: %+v", m.PluginStored)
	}
	sendMsg(t, conn, ClientMessage{RequestID: "r2", Type: "plugin_stored", Stored: &PluginStoredRequest{Plugin: "org.example.broken", Kind: "memories"}})
	if p := answer("r2").PluginStored; p == nil || p.Plugin != "org.example.broken" || p.Error == "" || len(p.Memories) != 0 {
		t.Fatalf("a read that failed is an answer with its reason: %+v", p)
	}
	before := reads.Load()
	for id, req := range map[string]*PluginStoredRequest{
		"r3": nil, "r4": {Kind: "memories"}, "r5": {Plugin: "org.example.notes", Kind: "everything"},
	} {
		sendMsg(t, conn, ClientMessage{RequestID: id, Type: "plugin_stored", Stored: req})
		if m := answer(id); m.Type != "error" || m.Message != "plugin stored: name the plugin, and memories or keys" {
			t.Fatalf("%s: a request that names no plugin or no kind is refused before the host is asked: %+v", id, m)
		}
	}
	if reads.Load() != before {
		t.Fatal("a refused request reached the host's read")
	}

	sendMsg(t, conn, ClientMessage{RequestID: "r6", Type: "plugin", Plugin: &PluginAction{Action: "remove_stored", ID: "org.example.notes"}})
	if m := answer("r6"); m.Type != "config" || removals.Load() != 1 {
		t.Fatalf("the removal rides the same session: answered %q, the host heard %d", m.Type, removals.Load())
	}

	// A host with no such read (first boot's handler) says so.
	bare := New("127.0.0.1", 0, &WSHandler{})
	bareAddr, err := bare.Start(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer bare.Shutdown(context.Background())
	conn = dialWS(t, bareAddr)
	sendMsg(t, conn, ClientMessage{RequestID: "r7", Type: "plugin_stored", Stored: &PluginStoredRequest{Plugin: "org.example.notes", Kind: "keys"}})
	if p := answer("r7").PluginStored; p == nil || p.Error != "not available" || p.Kind != "keys" {
		t.Fatalf("a host with no such read: %+v", p)
	}
}

// A PLUGIN'S KEYS ARE SHOWN ONLY ON A PRIVATE CONNECTION. Their values are
// whatever the plugin kept, a token among them; a pasted key is taken only
// on a connection that is private, and the values are shown on no
// other. Its memories are read on any of the
// operator's connections.
func TestAPluginsKeysAreShownOnlyOnAPrivateConnection(t *testing.T) {
	read := func(kind string) ClientMessage {
		return ClientMessage{Type: "plugin_stored", Stored: &PluginStoredRequest{Plugin: "org.example.p", Kind: kind}}
	}
	if r := pluginStoredRefusal(read("keys"), false); r == "" || !strings.Contains(r, "not private") {
		t.Fatalf("keys over a connection that is not private: %q", r)
	}
	for _, c := range []struct {
		kind    string
		private bool
	}{{"keys", true}, {"memories", false}, {"memories", true}} {
		if r := pluginStoredRefusal(read(c.kind), c.private); r != "" {
			t.Fatalf("%s, private %v: refused %q", c.kind, c.private, r)
		}
	}
	if r := pluginStoredRefusal(read("files"), true); r == "" {
		t.Fatal("a kind that is neither memories nor keys was taken")
	}
}
