package dashboard

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// BenchmarkBroadcast is one fan-out to four open pages: the tool event a
// turn emits on every call, and the configuration, whose secrets_ok is each
// page's own. allocs/op is the evidence; the wall time includes the socket
// writes and moves with the host's load.
func BenchmarkBroadcast(b *testing.B) {
	s := New("127.0.0.1", 0, &WSHandler{})
	addr, err := s.Start(b.TempDir())
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { s.Shutdown(context.Background()) })
	ctx, cancel := context.WithCancel(context.Background())
	b.Cleanup(cancel)
	const pages = 4
	for i := 0; i < pages; i++ {
		dctx, dcancel := context.WithTimeout(ctx, 30*time.Second)
		conn, _, err := websocket.Dial(dctx, "wss://"+addr+"/ws?interaction_version=1", &websocket.DialOptions{
			HTTPClient: testClient,
			HTTPHeader: http.Header{"Origin": []string{"https://" + addr}},
		})
		dcancel()
		if err != nil {
			b.Fatal(err)
		}
		conn.SetReadLimit(1 << 20)
		go func() { // the page reads until the benchmark ends
			for {
				if _, _, err := conn.Read(ctx); err != nil {
					return
				}
			}
		}()
	}
	for {
		s.wsMu.Lock()
		n := len(s.wsConns)
		s.wsMu.Unlock()
		if n == pages {
			break
		}
		time.Sleep(time.Millisecond)
	}
	for _, bc := range []struct {
		name string
		msg  ServerMessage
	}{
		{"event", ServerMessage{Type: "event", Kind: "tool_call", Name: "shell", Args: strings.Repeat(`{"cmd":"ls -la"}`, 64)}},
		{"config", ServerMessage{Type: "config", Config: &ConfigState{
			LLM:     LLMConfigState{ResolvedProvider: "Claude", ResolvedModel: "claude-opus", Endpoint: "https://example.test"},
			Plugins: PluginsState{Autoload: "T3", CatalogURL: "https://example.test/catalog"},
		}}},
	} {
		b.Run(bc.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				s.broadcast(bc.msg)
			}
		})
	}
}
