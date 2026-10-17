package dashboard

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// A KEY PASTED ON A PLUGIN'S CARD: over a private connection (this test's
// loopback) it reaches the handler whole and the page is answered with the
// configuration — stamped secrets_ok for THIS connection — and never the
// key; a clear answers the same; a request that names no setting is refused
// wearing its id.
func TestAPluginKeyCrossesAPrivateConnectionToTheHandlerOnly(t *testing.T) {
	const secret = "sk-jev-ON-THE-WIRE-42"
	var mu sync.Mutex
	var seen []string
	h := &WSHandler{
		SetPluginKey: func(plugin, key, value string) error {
			mu.Lock()
			seen = append(seen, "set:"+plugin+":"+key+":"+value)
			mu.Unlock()
			return nil
		},
		ClearPluginKey: func(plugin, key string) error {
			mu.Lock()
			seen = append(seen, "clear:"+plugin+":"+key)
			mu.Unlock()
			return nil
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
	roundTrip := func(v map[string]any) (ServerMessage, string) {
		t.Helper()
		data, _ := json.Marshal(v)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := conn.Write(ctx, websocket.MessageText, data); err != nil {
			t.Fatal(err)
		}
		for {
			_, raw, err := conn.Read(ctx)
			if err != nil {
				t.Fatal(err)
			}
			var m ServerMessage
			if json.Unmarshal(raw, &m) == nil && m.RequestID == v["request_id"] {
				return m, string(raw)
			}
		}
	}
	m, raw := roundTrip(map[string]any{"type": "plugin_key_set", "request_id": "k1",
		"plugin_key": map[string]string{"plugin": "id.aiii.codequality", "key": "api_key", "secret": secret}})
	if m.Type != "config" || m.Config == nil || !m.Config.SecretsOK {
		t.Fatalf("a kept key answers with the configuration, stamped private: %s", raw)
	}
	if strings.Contains(raw, secret) {
		t.Fatal("the key came back to the page")
	}
	if m, raw := roundTrip(map[string]any{"type": "plugin_key_clear", "request_id": "k2",
		"plugin_key": map[string]string{"plugin": "id.aiii.codequality", "key": "api_key"}}); m.Type != "config" {
		t.Fatalf("a clear answers with the configuration: %s", raw)
	}
	if m, raw := roundTrip(map[string]any{"type": "plugin_key_set", "request_id": "k3",
		"plugin_key": map[string]string{"plugin": "id.aiii.codequality", "secret": secret}}); m.Type != "error" || strings.Contains(raw, secret) {
		t.Fatalf("a key for no setting is refused by id, and not echoed: %s", raw)
	}
	mu.Lock()
	defer mu.Unlock()
	if strings.Join(seen, ",") != "set:id.aiii.codequality:api_key:"+secret+",clear:id.aiii.codequality:api_key" {
		t.Fatalf("the handlers saw the acts, the key whole: %v", seen)
	}
}

// Over a connection that is not private a key is refused before any
// handler sees it; forgetting one needs no secret and is allowed.
func TestAPluginKeyIsRefusedOverAConnectionThatIsNotPrivate(t *testing.T) {
	set := ClientMessage{Type: "plugin_key_set", PluginKey: &PluginKeyRequest{Plugin: "p", Key: "api_key", Secret: "sk-x"}}
	if r := pluginKeyRefusal(set, false); !strings.Contains(r, "not private") {
		t.Fatalf("a key over a connection that is not private is refused by name: %q", r)
	}
	if r := pluginKeyRefusal(set, true); r != "" {
		t.Fatalf("over a private connection it is taken: %q", r)
	}
	clear := ClientMessage{Type: "plugin_key_clear", PluginKey: &PluginKeyRequest{Plugin: "p", Key: "api_key"}}
	if r := pluginKeyRefusal(clear, false); r != "" {
		t.Fatalf("forgetting a key carries no secret and is allowed anywhere: %q", r)
	}
	if r := pluginKeyRefusal(ClientMessage{Type: "plugin_key_set"}, true); r == "" {
		t.Fatal("a request naming nothing is refused")
	}
}
