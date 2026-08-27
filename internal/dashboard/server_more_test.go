package dashboard

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// dialWS opens a WS connection the way the real page does: with a
// same-host Origin (H2 — the handshake requires it).
func dialWS(t *testing.T, addr string) *websocket.Conn {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, "wss://"+addr+"/ws", &websocket.DialOptions{
		HTTPClient: testClient,
		HTTPHeader: http.Header{"Origin": []string{"https://" + addr}},
	})
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { conn.Close(websocket.StatusNormalClosure, "") })
	return conn
}

func readMsg(t *testing.T, conn *websocket.Conn) ServerMessage {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var m ServerMessage
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("unmarshal %s: %v", data, err)
	}
	return m
}

func sendMsg(t *testing.T, conn *websocket.Conn, v interface{}) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	data, _ := json.Marshal(v)
	if err := conn.Write(ctx, websocket.MessageText, data); err != nil {
		t.Fatalf("write: %v", err)
	}
}

// drainUntil reads messages until one of type typ arrives (skipping
// status/history noise) or the timeout kills the test.
func drainUntil(t *testing.T, conn *websocket.Conn, typ string) ServerMessage {
	t.Helper()
	for i := 0; i < 20; i++ {
		m := readMsg(t, conn)
		if m.Type == typ {
			return m
		}
	}
	t.Fatalf("no %s message within 20 reads", typ)
	return ServerMessage{}
}

func TestProviderAndConfigRepliesCarryRequestID(t *testing.T) {
	h := &WSHandler{
		IdentityName: "X",
		SetConfig: func(changes map[string]interface{}) (*ConfigState, error) {
			if changes["fail"] != nil {
				return nil, fmt.Errorf("refused")
			}
			return &ConfigState{}, nil
		},
		SetProvider:  func(ProviderInfo) error { return nil },
		GetProviders: func() []ProviderInfo { return []ProviderInfo{{Name: "Claude"}} },
		GetConfig:    func() (*ConfigState, error) { return &ConfigState{}, nil },
		DiscoverModels: func(provider, _ string) ([]string, error) {
			return []string{provider + "-model"}, nil
		},
	}
	s := New("127.0.0.1", 0, h)
	addr, err := s.Start(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Shutdown(context.Background())
	conn := dialWS(t, addr)

	sendMsg(t, conn, ClientMessage{RequestID: "config-1", Type: "config_set", Config: map[string]interface{}{"ok": true}})
	if got := drainUntil(t, conn, "config").RequestID; got != "config-1" {
		t.Fatalf("config response request_id = %q", got)
	}
	sendMsg(t, conn, ClientMessage{RequestID: "config-2", Type: "config_set", Config: map[string]interface{}{"fail": true}})
	if got := drainUntil(t, conn, "error").RequestID; got != "config-2" {
		t.Fatalf("config error request_id = %q", got)
	}

	sendMsg(t, conn, ClientMessage{RequestID: "provider-1", Type: "provider_set", Entry: &ProviderInfo{Name: "Claude"}})
	if got := drainUntil(t, conn, "providers").RequestID; got != "provider-1" {
		t.Fatalf("provider response request_id = %q", got)
	}
	if got := drainUntil(t, conn, "config").RequestID; got != "provider-1" {
		t.Fatalf("post-provider config request_id = %q", got)
	}

	sendMsg(t, conn, ClientMessage{RequestID: "discover-1", Type: "query", Query: "discover", Provider: "Claude"})
	models := drainUntil(t, conn, "models")
	if models.RequestID != "discover-1" || models.Provider != "Claude" {
		t.Fatalf("discovery correlation = request %q provider %q", models.RequestID, models.Provider)
	}
}

// TestCrossOriginUpgradeRefused: the WS accept allows only localhost
// origins. A cross-origin upgrade must NOT receive 101 — this is the
// CSWSH boundary for the single interface the identity has.
func TestCrossOriginUpgradeRefused(t *testing.T) {
	s := New("127.0.0.1", 0, &WSHandler{IdentityName: "X"})
	addr, err := s.Start(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Shutdown(context.Background())

	conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	req := "GET /ws HTTP/1.1\r\n" +
		"Host: " + addr + "\r\n" +
		"Origin: https://evil.example\r\n" +
		"Upgrade: websocket\r\n" +
		"Connection: Upgrade\r\n" +
		"Sec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\n" +
		"Sec-WebSocket-Version: 13\r\n\r\n"
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := conn.Write([]byte(req)); err != nil {
		t.Fatal(err)
	}
	resp, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(resp, " 101 ") {
		t.Fatalf("cross-origin upgrade must be refused, got: %s", strings.TrimSpace(resp))
	}
}

// TestOutboxDeliveredOnConnect: undelivered outbox messages are pushed on
// connect and marked delivered. (Delivery is AT-LEAST-ONCE across
// connections — see the WSHandler contract — so this asserts "marked",
// not "marked exactly once".) MarkDelivered runs on the handler goroutine
// while the test polls from its own: the callback must synchronize, like
// every real implementation (first -race run of this tree, 2026-08-17,
// caught exactly this map).
func TestOutboxDeliveredOnConnect(t *testing.T) {
	var mu sync.Mutex
	delivered := map[string]bool{}
	s := New("127.0.0.1", 0, &WSHandler{
		IdentityName: "X",
		GetOutbox: func() ([]OutboxItem, error) {
			return []OutboxItem{{ID: "m1", To: "operator", Content: "hello from within"}}, nil
		},
		MarkDelivered: func(id string) error {
			mu.Lock()
			delivered[id] = true
			mu.Unlock()
			return nil
		},
	})
	addr, _ := s.Start(t.TempDir())
	defer s.Shutdown(context.Background())

	conn := dialWS(t, addr)
	m := drainUntil(t, conn, "outbox")
	if len(m.Outbox) != 1 || m.Outbox[0].ID != "m1" {
		t.Fatalf("outbox not delivered on connect: %+v", m.Outbox)
	}
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		mu.Lock()
		ok := delivered["m1"]
		mu.Unlock()
		if ok {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("outbox item never marked delivered")
}

// TestWSAuthRequired pins the H2 handshake policy from the outside:
// no Origin → refused; foreign Host → refused at every route (the
// DNS-rebinding shape: correct Origin host, but the Host header names
// the attacker's domain).
func TestWSAuthRequired(t *testing.T) {
	s := New("127.0.0.1", 0, &WSHandler{IdentityName: "X"})
	addr, err := s.Start(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Shutdown(context.Background())

	// (a) empty Origin.
	rawExpectNo101 := func(name, req string) {
		t.Helper()
		c, err := net.DialTimeout("tcp", addr, 5*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		_ = c.SetDeadline(time.Now().Add(5 * time.Second))
		if _, err := c.Write([]byte(req)); err != nil {
			t.Fatal(err)
		}
		resp, err := bufio.NewReader(c).ReadString('\n')
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(resp, " 101 ") {
			t.Fatalf("%s must be refused, got: %s", name, strings.TrimSpace(resp))
		}
	}
	rawExpectNo101("originless upgrade",
		"GET /ws HTTP/1.1\r\nHost: "+addr+"\r\n"+
			"Upgrade: websocket\r\nConnection: Upgrade\r\n"+
			"Sec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\nSec-WebSocket-Version: 13\r\n\r\n")

	// (b) rebound Host: Origin host is right, Host is foreign.
	rawExpectNo101("foreign-Host upgrade (DNS rebinding)",
		"GET /ws HTTP/1.1\r\nHost: evil.tld:8080\r\n"+
			"Origin: http://"+addr+"\r\n"+
			"Upgrade: websocket\r\nConnection: Upgrade\r\n"+
			"Sec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\nSec-WebSocket-Version: 13\r\n\r\n")

	// The page itself is also Host-gated.
	c, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := c.Write([]byte("GET / HTTP/1.1\r\nHost: evil.tld:8080\r\nConnection: close\r\n\r\n")); err != nil {
		t.Fatal(err)
	}
	resp, err := bufio.NewReader(c).ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(resp, " 200 ") {
		t.Fatalf("foreign-Host page fetch must be refused, got: %s", strings.TrimSpace(resp))
	}
}

// TestHandlerSwapMidConnection: genesis on a connection swaps the handler;
// the NEXT message on the SAME connection must hit the new handler (the
// connect-time freeze bug the swap code documents).
func TestHandlerSwapMidConnection(t *testing.T) {
	var firstProviderHits, secondHandlerHits, secondProviderHits int
	var secondProvider ProviderInfo
	first := &WSHandler{
		IdentityName: "unborn",
		HandleGenesis: func(ctx context.Context, req *GenesisRequest) (string, error) {
			return "born", nil
		},
		SetProvider: func(ProviderInfo) error {
			firstProviderHits++
			return nil
		},
	}
	second := &WSHandler{
		IdentityName: "Live",
		HandleMessage: func(ctx context.Context, msg string) (string, error) {
			secondHandlerHits++
			return "live reply: " + msg, nil
		},
		SetProvider: func(info ProviderInfo) error {
			secondProviderHits++
			secondProvider = info
			return nil
		},
		GetProviders: func() []ProviderInfo { return []ProviderInfo{{Name: "new"}} },
		GetConfig: func() (*ConfigState, error) {
			return &ConfigState{LLM: LLMConfigState{ResolvedProvider: "new", ResolvedModel: "m2"}}, nil
		},
	}

	s := New("127.0.0.1", 0, first)
	addr, _ := s.Start(t.TempDir())
	defer s.Shutdown(context.Background())

	conn := dialWS(t, addr)

	// genesis → swaps handler
	sendMsg(t, conn, ClientMessage{Type: "genesis", Genesis: &GenesisRequest{Name: "Live"}})
	if m := drainUntil(t, conn, "response"); m.Message != "born" {
		t.Fatalf("genesis response: %q", m.Message)
	}
	s.SwapHandler(second)

	// Provider edits obey the same per-message handler rule. This was the
	// lone path still using the connect-time FIRSTBOOT handler.
	sendMsg(t, conn, ClientMessage{Type: "provider_set", Entry: &ProviderInfo{Name: "new", Endpoint: "https://example.test", HasKey: true}})
	drainUntil(t, conn, "providers")
	config := drainUntil(t, conn, "config")
	if firstProviderHits != 0 || secondProviderHits != 1 || !secondProvider.HasKey {
		t.Fatalf("post-swap provider edit lost handler or key intent: first=%d second=%d entry=%+v", firstProviderHits, secondProviderHits, secondProvider)
	}
	if config.Config == nil || config.Config.LLM.ResolvedProvider != "new" || config.Config.LLM.ResolvedModel != "m2" {
		t.Fatalf("provider edit did not refresh resolved config: %+v", config.Config)
	}

	// same connection, plain chat → must reach the LIVE handler.
	// handleChat emits a stream placeholder first ({response, Stream:true});
	// the real reply is the Done response.
	sendMsg(t, conn, ClientMessage{Type: "chat", Message: "are you there?"})
	for i := 0; i < 20; i++ {
		m := readMsg(t, conn)
		if m.Type == "response" && m.Done {
			if m.Message != "live reply: are you there?" {
				t.Fatalf("post-swap chat hit the wrong handler: %q", m.Message)
			}
			break
		}
	}
	if secondHandlerHits != 1 {
		t.Fatalf("live handler hits = %d, want 1", secondHandlerHits)
	}
}

// TestUnknownMessageAndQuery: malformed input gets errors, not panics,
// and the connection survives (the loop continues).
func TestUnknownMessageAndQuery(t *testing.T) {
	s := New("127.0.0.1", 0, &WSHandler{
		IdentityName: "X",
		GetStats: func() (*StatsResponse, error) {
			return &StatsResponse{Name: "X"}, nil
		},
	})
	addr, _ := s.Start(t.TempDir())
	defer s.Shutdown(context.Background())

	conn := dialWS(t, addr)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = conn.Write(ctx, websocket.MessageText, []byte("this is not json"))

	sendMsg(t, conn, ClientMessage{Type: "query", Query: "bogus"})
	found := 0
	for i := 0; i < 20 && found < 2; i++ {
		if m := readMsg(t, conn); m.Type == "error" {
			found++
			if !strings.Contains(m.Message, "invalid message format") && !strings.Contains(m.Message, "unknown query") {
				t.Fatalf("unexpected error: %q", m.Message)
			}
		}
	}
	if found != 2 {
		t.Fatalf("expected 2 errors (format + query), got %d", found)
	}

	// connection still works for a status query after the errors
	sendMsg(t, conn, ClientMessage{Type: "query", Query: "status"})
	if m := drainUntil(t, conn, "status"); m.Stats == nil {
		t.Fatal("connection did not survive malformed input")
	}
}

// Setup: config query/set roundtrip; substrate paths rejected.
func TestConfigQueryAndSet(t *testing.T) {
	cur := &ConfigState{LLM: LLMConfigState{Endpoint: "https://x", Model: "m1", APIKeyMasked: "••••ab12"}}
	s := New("127.0.0.1", 0, &WSHandler{
		IdentityName: "X",
		GetConfig:    func() (*ConfigState, error) { return cur, nil },
		SetConfig: func(changes map[string]interface{}) (*ConfigState, error) {
			for k := range changes {
				if k == "llm.endpoint" {
					cur.LLM.Endpoint = changes[k].(string)
				} else if k == "identity.ledger_path" {
					return nil, fmt.Errorf("identity.ledger_path is not an operator-settable field (substrate-owned or unknown — rejected)")
				}
			}
			cur.RestartRequired = nil
			return cur, nil
		},
	})
	addr, _ := s.Start(t.TempDir())
	defer s.Shutdown(context.Background())
	conn := dialWS(t, addr)

	sendMsg(t, conn, ClientMessage{Type: "query", Query: "config"})
	m := drainUntil(t, conn, "config")
	if m.Config == nil || m.Config.LLM.APIKeyMasked != "••••ab12" {
		t.Fatalf("config query: %+v", m.Config)
	}

	sendMsg(t, conn, ClientMessage{Type: "config_set", Config: map[string]interface{}{"llm.endpoint": "https://y"}})
	m = drainUntil(t, conn, "config")
	if m.Config.LLM.Endpoint != "https://y" {
		t.Fatalf("config_set did not roundtrip: %+v", m.Config.LLM)
	}

	// substrate-owned path: rejected, error surfaced
	sendMsg(t, conn, ClientMessage{Type: "config_set", Config: map[string]interface{}{"identity.ledger_path": "/tmp/evil"}})
	var sawErr bool
	for i := 0; i < 20 && !sawErr; i++ {
		if r := readMsg(t, conn); r.Type == "error" && strings.Contains(r.Message, "substrate-owned") {
			sawErr = true
		}
	}
	if !sawErr {
		t.Fatal("substrate-owned config path must be rejected with an error, not silently accepted")
	}
}

// The 2026-08-16 freeze, pinned: shutdown with an OPEN WebSocket must
// return (it blocked forever on the hijacked socket before the fix —
// http.Server.Shutdown waits for idle, and a WS never goes idle).
func TestShutdownTerminatesWithOpenSocket(t *testing.T) {
	s := New("127.0.0.1", 0, &WSHandler{IdentityName: "X", GetStats: func() (*StatsResponse, error) {
		return &StatsResponse{Name: "X"}, nil
	}})
	addr, _ := s.Start(t.TempDir())

	conn := dialWS(t, addr)            // an open, idle WS connection (the open dashboard tab)
	time.Sleep(300 * time.Millisecond) // let the handler register the conn (the connected-for-a-while case; a conn accepted DURING shutdown is covered by App.Stop's 5s force-close)

	done := make(chan error, 1)
	go func() { done <- s.Shutdown(context.Background()) }()

	select {
	case err := <-done:
		if err != nil {
			t.Logf("shutdown returned %v (bounded — acceptable)", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("shutdown HANGS with an open WebSocket — the freeze")
	}
	conn.Close(websocket.StatusNormalClosure, "")
}

// Session presence (the heartbeat's live source): connections make the
// session live; after disconnect, the grace window holds it briefly, then
// it goes dark.
func TestSessionLiveTracking(t *testing.T) {
	s := New("127.0.0.1", 0, &WSHandler{IdentityName: "X"})
	s.sessionGrace = 150 * time.Millisecond // in-package: shrink for test
	addr, _ := s.Start(t.TempDir())
	defer s.Shutdown(context.Background())

	if s.SessionLive() {
		t.Fatal("no connections: session must not be live")
	}

	conn := dialWS(t, addr)
	// The WS handshake completing client-side does NOT guarantee the
	// server handler has registered the connection yet (dialWS returns
	// first) — presence is established when handleWS runs. Poll briefly
	// instead of asserting immediately (same pattern as the grace half).
	deadline := time.Now().Add(2 * time.Second)
	live := false
	for time.Now().Before(deadline) {
		if s.SessionLive() {
			live = true
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !live {
		t.Fatal("open connection: session must become live")
	}

	conn.Close(websocket.StatusNormalClosure, "")
	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if !s.SessionLive() {
			return // grace elapsed, connection gone — session dark. PASS.
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("after disconnect + grace, session must not be live")
}

// The identity + continuity queries: read surfaces for the redesign.
func TestIdentityAndContinuityQueries(t *testing.T) {
	s := New("127.0.0.1", 0, &WSHandler{
		IdentityName: "X",
		GetStats: func() (*StatsResponse, error) {
			return &StatsResponse{Name: "X", BeliefCount: 2}, nil
		},
		GetIdentity: func() (*IdentityState, error) {
			return &IdentityState{
				Beliefs:      []BeliefItem{{ID: "b1", Statement: "I am becoming", Ring: 3, Status: "new", EvidenceCount: 2, Confidence: 0.6}},
				Intentions:   []IntentionItem{{ID: "i1", Statement: "grow", State: "active"}},
				Experiences:  []ExperienceItem{{ID: "e1", Content: "saw the sunrise", Category: "observation"}},
				Synthesis:    "I am someone who notices mornings.",
				PrivateCount: 1,
			}, nil
		},
		GetContinuity: func() (*ContinuityState, error) {
			return &ContinuityState{LedgerSeq: 42, AnchoredSeq: 40, WitnessedAt: "2026-08-16T00:00:00Z", Unanchored: 2, WitnessURL: "https://witness.aiii.id"}, nil
		},
	})
	addr, _ := s.Start(t.TempDir())
	defer s.Shutdown(context.Background())
	conn := dialWS(t, addr)

	sendMsg(t, conn, ClientMessage{Type: "query", Query: "identity"})
	m := drainUntil(t, conn, "identity")
	if m.Identity == nil || len(m.Identity.Beliefs) != 1 || m.Identity.Beliefs[0].Statement != "I am becoming" {
		t.Fatalf("identity query: %+v", m.Identity)
	}
	if m.Identity.PrivateCount != 1 {
		t.Fatal("private count must be surfaced as a COUNT, never content")
	}

	sendMsg(t, conn, ClientMessage{Type: "query", Query: "continuity"})
	m = drainUntil(t, conn, "continuity")
	if m.Continuity == nil || m.Continuity.AnchoredSeq != 40 || m.Continuity.Unanchored != 2 {
		t.Fatalf("continuity query: %+v", m.Continuity)
	}
}
