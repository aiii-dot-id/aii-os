//go:build !windows

package dashboard

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func routedAddress() bool {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return false
	}
	for _, a := range addrs {
		if ipnet, ok := a.(*net.IPNet); ok && !ipnet.IP.IsLoopback() && !ipnet.IP.IsLinkLocalUnicast() {
			return true
		}
	}
	return false
}

func TestARealBrowserCarriesItsSessionOnAChannel(t *testing.T) {
	engines := resolveEngines()
	if len(engines) == 0 {
		skipWithoutEngines(t)
	}
	if !routedAddress() {
		t.Skip("this machine has no address but its own loopback, and the identity offers a browser none: the two cannot find a path here")
	}
	for _, found := range engines {
		t.Run(found.engine.name, func(t *testing.T) {
			t.Run("ordinary setup", func(t *testing.T) { realBrowserChannel(t, found.engine, found.path, false) })
			t.Run("candidate before answer", func(t *testing.T) { realBrowserChannel(t, found.engine, found.path, true) })
		})
	}
}

type answerAfterCandidate struct {
	carrier
	candidate chan struct{}
	once      sync.Once
}

func (s *answerAfterCandidate) Write(ctx context.Context, typ websocket.MessageType, raw []byte) error {
	var message setupMessage
	_ = json.Unmarshal(raw, &message)
	if message.Type == "transport_answer" {
		select {
		case <-s.candidate:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	err := s.carrier.Write(ctx, typ, raw)
	if err == nil && message.Type == "transport_candidate" {
		s.once.Do(func() { close(s.candidate) })
	}
	return err
}

func realBrowserChannel(t *testing.T, engine browserEngine, path string, candidateFirst bool) {
	s := New("127.0.0.1", 0, &WSHandler{})
	modules := wsProbeModules(t, realBrowserChannelProbe)
	results := make(chan string, 4)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	session := func(w http.ResponseWriter, r *http.Request) {
		socket, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		socket.SetReadLimit(maxVoiceFrameBytes)
		set := channelTest
		set.WaitMS, set.ReadyAllowanceMS = 20000, 2000
		var setup carrier = socket
		if candidateFirst {
			setup = &answerAfterCandidate{carrier: socket, candidate: make(chan struct{})}
		}
		chosen, alive := s.negotiateCarrier(ctx, setup, set.held())
		if !alive {
			return
		}
		defer chosen.CloseNow()
		say := func(typ websocket.MessageType, p []byte) bool {
			wctx, done := context.WithTimeout(ctx, 10*time.Second)
			defer done()
			return chosen.Write(wctx, typ, p) == nil
		}
		_, isChannel := chosen.(*channelCarrier)
		if !say(websocket.MessageText, fmt.Appendf(nil, `{"type":"carried","channel":%v}`, isChannel)) {
			return
		}
		for {
			typ, data, err := chosen.Read(ctx)
			if err != nil {
				return
			}
			if !say(typ, data) {
				return
			}
		}
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/__result":
			body, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
			select {
			case results <- string(body):
			default:
			}
			w.WriteHeader(http.StatusNoContent)
		case r.URL.Path == "/__harness.js":
			w.Header().Set("Content-Type", "text/javascript")
			fmt.Fprint(w, harnessJS)
		case r.URL.Path == "/ws":
			session(w, r)
		case modules[r.URL.Path] != nil:
			w.Header().Set("Content-Type", "text/javascript")
			_, _ = w.Write(modules[r.URL.Path])
		case strings.HasSuffix(r.URL.Path, ".js"):
			real, err := staticFS.ReadFile("static" + r.URL.Path)
			if err != nil {
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Content-Type", "text/javascript")
			_, _ = w.Write(real)
		default:
			w.Header().Set("Content-Security-Policy", uiCSP)
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			fmt.Fprint(w, tokenPromptProbePage)
		}
	}))
	defer server.Close()
	defer cancel() // the sessions end before the server waits for them
	runServerInOneEngine(t, engine, path, server.URL, results, 0, 0, 0)
}

const realBrowserChannelProbe = `
import { report } from '/__harness.js';
try {
  const assert = (ok, why) => { if (!ok) throw Error(why); };
  const until = async (what, ok, ms) => {
    for (const end = Date.now() + (ms || 60000); !(await ok()); await new Promise(resolve => setTimeout(resolve, 50))) if (Date.now() > end) throw Error(what);
  };
  const { settle } = await import('/ws.js');

  const seen = [];
  const texts = type => seen.filter(m => typeof m === 'string').map(m => { try { return JSON.parse(m); } catch (_) { return {}; } }).filter(m => m.type === type);
  const socket = new WebSocket((location.protocol === 'https:' ? 'wss://' : 'ws://') + location.host + '/ws?transport=1');
  socket.binaryType = 'arraybuffer';
  const carrier = await new Promise((resolve, reject) => settle(socket,
    c => { c.onmessage = e => seen.push(e.data); c.onclose = () => seen.push('{"type":"closed"}'); resolve(c); },
    () => reject(Error('neither the channel nor the socket came to carry the session')),
    config => new RTCPeerConnection(config)));
  assert(carrier !== socket && carrier.readyState === 1, 'the session stayed on its socket: the browser and the identity found no path between themselves');
  await until('the identity did not say, on the channel, what carries the session', () => texts('carried').length === 1);
  assert(texts('carried')[0].channel === true, 'the identity says the session is not on the channel');

  const long = 'the session\'s own words. '.repeat(4000);
  carrier.send(JSON.stringify({ type: 'echo', text: long }));
  await until('a long text message did not cross the channel and come back whole', () => texts('echo').some(m => m.text === long));
  const bytes = new Uint8Array(100000).map((_, i) => i % 251);
  carrier.send(bytes.buffer);
  await until('a binary message did not cross the channel and come back whole', () => seen.some(m => m instanceof ArrayBuffer && m.byteLength === bytes.length && new Uint8Array(m).every((b, i) => b === i % 251)));

  carrier.close();
  report('OK');
} catch (e) { report('FAIL: ' + ((e && e.message) || String(e))); }
`
