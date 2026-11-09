package dashboard

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// A LOOK OR AN ADD NEVER HOLDS THE SOCKET. A look asks every usual port and
// an add asks its port again, each up to the probe's timeout; on the read
// loop, a port that holds would hold every later message on the connection.
// Each arm here is held, and a second frame on the same connection must be
// answered while it is.

// answerFor reads until a message under requestID, within d: a held read
// loop fails here fast instead of waiting out the shared 30 s read.
func answerFor(t *testing.T, conn *websocket.Conn, requestID string, d time.Duration) (ServerMessage, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), d)
	defer cancel()
	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			return ServerMessage{}, err
		}
		var m ServerMessage
		if err := json.Unmarshal(data, &m); err != nil {
			return ServerMessage{}, err
		}
		if m.RequestID == requestID {
			return m, nil
		}
	}
}

func TestALocalLookOrAddDoesNotHoldTheSocket(t *testing.T) {
	for _, arm := range []string{"local_look", "local_add"} {
		t.Run(arm, func(t *testing.T) {
			release := make(chan struct{})
			h := &WSHandler{
				LookForLocalServers: func() []LocalServerFound { <-release; return []LocalServerFound{{Port: 11434}} },
				AddLocalServer:      func(int, string) error { <-release; return nil },
				SetConfig: func(map[string]any) (*ConfigState, error) {
					return nil, fmt.Errorf("refused while the port is held")
				},
				GetConfig: func() *ConfigState { return &ConfigState{} },
			}
			s := New("127.0.0.1", 0, h)
			addr, err := s.Start(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			defer s.Shutdown(context.Background())
			conn := dialWS(t, addr)
			held := ClientMessage{RequestID: arm + "-1", Type: arm}
			if arm == "local_add" {
				held.LocalServer = &LocalServerRequest{Port: 11434, Model: "m"}
			}
			sendMsg(t, conn, held)
			sendMsg(t, conn, ClientMessage{RequestID: "config-1", Type: "config_set", Config: map[string]any{"x": 1}})
			m, err := answerFor(t, conn, "config-1", 5*time.Second)
			close(release)
			if err != nil || m.Type != "error" {
				t.Fatalf("while a %s was held, the next frame on the connection was not answered (%v): %+v", arm, err, m)
			}
			// The held request is still answered, under its own id.
			if m, err := answerFor(t, conn, arm+"-1", 5*time.Second); err != nil || m.Type == "error" {
				t.Fatalf("the %s was not answered under its own request once released (%v): %+v", arm, err, m)
			}
		})
	}
}

// A LATE ADD REACHES THE PAGE THAT CAME BACK. The add is held; its
// connection drops; the page reconnects and takes its providers answer before
// the save; then the add saves. The new connection must be sent the directory
// with the entry, with no request id, or its page goes on offering Add for a
// server that is already an entry (a review 2026-11-06 14:50Z, finding 1).
func TestALocalAddThatSavesAfterAReconnectReachesTheNewPage(t *testing.T) {
	release := make(chan struct{})
	saved := make(chan struct{})
	entry := ProviderInfo{Name: "Local server, port 11434", Endpoint: "http://127.0.0.1:11434/v1", Local: true}
	h := &WSHandler{
		AddLocalServer: func(int, string) error { <-release; close(saved); return nil },
		GetProviders: func() ProviderDirectory {
			select {
			case <-saved:
				return ProviderDirectory{Providers: []ProviderInfo{entry}}
			default:
				return ProviderDirectory{}
			}
		},
	}
	s := New("127.0.0.1", 0, h)
	addr, err := s.Start(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Shutdown(context.Background())
	released := false
	free := func() {
		if !released {
			released = true
			close(release)
		}
	}
	defer free() // a failure must not leave the add holding Shutdown
	// providersWith reads until a providers message with no request id (a
	// query's answer and a broadcast are both sent so) that has n entries.
	providersWith := func(conn *websocket.Conn, n int, d time.Duration) error {
		ctx, cancel := context.WithTimeout(context.Background(), d)
		defer cancel()
		for {
			_, data, err := conn.Read(ctx)
			if err != nil {
				return err
			}
			var m ServerMessage
			if json.Unmarshal(data, &m) == nil && m.Type == "providers" && m.RequestID == "" && len(m.Providers) == n && (n == 0 || m.Providers[0].Endpoint == entry.Endpoint) {
				return nil
			}
		}
	}
	first := dialWS(t, addr)
	sendMsg(t, first, ClientMessage{RequestID: "add-1", Type: "local_add", LocalServer: &LocalServerRequest{Port: 11434, Model: "m"}})
	time.Sleep(100 * time.Millisecond) // the add is running and held
	first.Close(websocket.StatusGoingAway, "connection lost")
	again := dialWS(t, addr)
	sendMsg(t, again, ClientMessage{Type: "query", Query: "providers"})
	if err := providersWith(again, 0, 5*time.Second); err != nil {
		t.Fatalf("setup: the reconnect's providers answer, before the save, never came (%v)", err)
	}
	free()
	if err := providersWith(again, 1, 3*time.Second); err != nil {
		t.Fatalf("the add saved after the reconnect, and the new page was never sent the providers with its entry (%v)", err)
	}
}

// Each request keeps its own errors, and both refuse as the other off-loop
// arms do when the application is stopping.
func TestALocalLookOrAddKeepsItsOwnErrors(t *testing.T) {
	h := &WSHandler{
		LookForLocalServers: func() []LocalServerFound { return nil },
		AddLocalServer:      func(int, string) error { return errors.New("nothing answers on port 11434 now") },
	}
	s := New("127.0.0.1", 0, h)
	addr, err := s.Start(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Shutdown(context.Background())
	conn := dialWS(t, addr)
	sendMsg(t, conn, ClientMessage{RequestID: "add-1", Type: "local_add", LocalServer: &LocalServerRequest{Port: 11434, Model: "m"}})
	if m, err := answerFor(t, conn, "add-1", 5*time.Second); err != nil || m.Type != "error" || m.Message != "local model: nothing answers on port 11434 now" {
		t.Fatalf("an add's own error was not answered under its request (%v): %+v", err, m)
	}
	sendMsg(t, conn, ClientMessage{RequestID: "look-1", Type: "local_look"})
	// An empty list is omitted on the wire; the page reads none as empty.
	if m, err := answerFor(t, conn, "look-1", 5*time.Second); err != nil || m.Type != "local_found" || len(m.LocalFound) != 0 {
		t.Fatalf("a look that found nothing was not answered as local_found under its request (%v): %+v", err, m)
	}

	stopping := &WSHandler{
		Background:          func(func()) bool { return false },
		LookForLocalServers: func() []LocalServerFound { t.Error("local_look ran although its job was refused"); return nil },
		AddLocalServer:      func(int, string) error { t.Error("local_add ran although its job was refused"); return nil },
	}
	s2 := New("127.0.0.1", 0, stopping)
	addr2, err := s2.Start(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Shutdown(context.Background())
	conn2 := dialWS(t, addr2)
	for _, m := range []ClientMessage{
		{RequestID: "look-s", Type: "local_look"},
		{RequestID: "add-s", Type: "local_add", LocalServer: &LocalServerRequest{Port: 11434, Model: "m"}},
	} {
		sendMsg(t, conn2, m)
		if got, err := answerFor(t, conn2, m.RequestID, 5*time.Second); err != nil || got.Message != "identity is stopping" {
			t.Errorf("%s refused as %q (%v)", m.Type, got.Message, err)
		}
	}
}
