//go:build !windows

package dashboard

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/pion/turn/v5"
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

func TestAPageThatIsLeftEndsItsChannelAtOnce(t *testing.T) {
	engines := resolveEngines()
	if len(engines) == 0 {
		skipWithoutEngines(t)
	}
	servers, ok := turnOnThisMachine(t)
	if !ok {
		t.Skip("this machine has no IPv4 address outside loopback for a TURN server the browser would use")
	}
	for _, found := range engines {
		t.Run(found.engine.name, func(t *testing.T) {
			t.Run("navigated away", func(t *testing.T) {
				address, results := channelRig(t, false, leavingProbe(true), servers, nil)
				runServerInOneEngine(t, found.engine, found.path, address, results, 0, 0, 0)
			})
			t.Run("tab closed", func(t *testing.T) {
				if !strings.HasPrefix(found.engine.name, "blink-") {
					t.Skip("the tab is closed through Chrome's DevTools endpoint, which this engine does not serve")
				}
				closedTab(t, found.engine, found.path, servers)
			})
		})
	}
}

// turnOnThisMachine is a TURN server on one of this machine's own IPv4
// addresses outside loopback, and the session servers that name it; false
// when the machine has no such address.
func turnOnThisMachine(t *testing.T) ([]ICEServer, bool) {
	var ip net.IP
	addrs, _ := net.InterfaceAddrs()
	for _, a := range addrs {
		if ipn, ok := a.(*net.IPNet); ok && ipn.IP.To4() != nil && !ipn.IP.IsLoopback() && !ipn.IP.IsLinkLocalUnicast() {
			ip = ipn.IP
			break
		}
	}
	if ip == nil {
		return nil, false
	}
	const realm, user, pass = "aii.example.test", "the-user", "the-credential"
	udp, err := net.ListenPacket("udp4", net.JoinHostPort(ip.String(), "0"))
	if err != nil {
		t.Fatal(err)
	}
	key := turn.GenerateAuthKey(user, realm, pass)
	server, err := turn.NewServer(turn.ServerConfig{
		Realm:       realm,
		AuthHandler: func(ra *turn.RequestAttributes) (string, []byte, bool) { return ra.Username, key, ra.Username == user },
		PacketConnConfigs: []turn.PacketConnConfig{{PacketConn: udp,
			RelayAddressGenerator: &turn.RelayAddressGeneratorStatic{RelayAddress: ip, Address: ip.String()}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Close() })
	return []ICEServer{{URLs: []string{"turn:" + udp.LocalAddr().String() + "?transport=udp"}, Username: user, Credential: pass}}, true
}

// closedTab opens the rig's page in a new tab of a Chrome-family browser and
// closes that tab through the browser's DevTools endpoint when the page says
// it is leaving.
func closedTab(t *testing.T, engine browserEngine, path string, servers []ICEServer) {
	root := t.TempDir()
	profile := filepath.Join(root, "profile")
	if err := os.Mkdir(profile, 0o700); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(path, append([]string{"--remote-debugging-port=0"}, engine.args(profile, "about:blank")...)...)
	cmd.Env = append(browserEnv(engine, root, os.Environ()), "DBUS_SESSION_BUS_ADDRESS=disabled:")
	outputPath := filepath.Join(root, "output.log")
	output, err := os.Create(outputPath)
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stdout, cmd.Stderr = output, output
	browser, err := startBrowserGroup(cmd)
	_ = output.Close()
	if err != nil {
		t.Fatalf("%s failed to start: %v", engine.name, err)
	}
	t.Cleanup(func() {
		if err := browser.stop(); err != nil {
			t.Errorf("%s cleanup: %v", engine.name, err)
		}
	})
	// The browser writes the port it chose on the first line of this file.
	var port string
	for limit := time.Now().Add(60 * time.Second); port == ""; time.Sleep(50 * time.Millisecond) {
		if raw, err := os.ReadFile(filepath.Join(profile, "DevToolsActivePort")); err == nil {
			if first, _, ok := strings.Cut(string(raw), "\n"); ok {
				port = first
			}
		}
		if time.Now().After(limit) {
			t.Fatalf("%s served no DevTools endpoint within 60 s; it said:\n%s", engine.name, browserSaid(outputPath))
		}
	}
	devtools := "http://127.0.0.1:" + port
	tab := make(chan string, 1)
	closeTab := func() error {
		var id string
		select {
		case id = <-tab:
		case <-time.After(time.Minute):
			return errors.New("the tab was never named")
		}
		resp, err := http.Get(devtools + "/json/close/" + id)
		if err != nil {
			return err
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("the DevTools endpoint answered %s", resp.Status)
		}
		return nil
	}
	address, results := channelRig(t, false, leavingProbe(false), servers, closeTab)
	req, err := http.NewRequest(http.MethodPut, devtools+"/json/new?"+address+"/", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s opened no tab: %v", engine.name, err)
	}
	var opened struct {
		ID string `json:"id"`
	}
	err = json.NewDecoder(resp.Body).Decode(&opened)
	_ = resp.Body.Close()
	if err != nil || opened.ID == "" {
		t.Fatalf("%s opened no tab it named (%v)", engine.name, err)
	}
	tab <- opened.ID
	select {
	case result := <-results:
		if measured, ok := strings.CutPrefix(result, "OK\n"); ok {
			t.Logf("%s measured:\n%s", engine.name, measured)
		} else {
			t.Fatalf("%s: %s\nthe browser said:\n%s", engine.name, result, browserSaid(outputPath))
		}
	case <-time.After(180 * time.Second):
		t.Fatalf("%s: no verdict within 180 s; the browser said:\n%s", engine.name, browserSaid(outputPath))
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
	address, results := channelRig(t, candidateFirst, realBrowserChannelProbe, carriedServers(t), nil)
	runServerInOneEngine(t, engine, path, address, results, 0, 0, 0)
}

// channelRig serves the shipped page script, a probe and the identity's own
// negotiation, with the session servers given, over a real socket, and
// returns its address and where verdicts arrive. The session echoes what it
// is sent. A page that says it is leaving (/__leaving) is given a verdict by
// the identity: whether its channel ended within a second; leaving, when
// given, is called then.
func channelRig(t *testing.T, candidateFirst bool, probe string, servers []ICEServer, leaving func() error) (string, <-chan string) {
	s := New("127.0.0.1", 0, &WSHandler{})
	modules := wsProbeModules(t, probe)
	results := make(chan string, 4)
	verdict := func(v string) {
		select {
		case results <- v:
		default:
		}
	}
	ended := make(chan time.Time, 1) // when the session's carrier ended
	ctx, cancel := context.WithCancel(context.Background())
	session := func(w http.ResponseWriter, r *http.Request) {
		socket, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		socket.SetReadLimit(maxVoiceFrameBytes)
		set := channelTest
		set.WaitMS, set.ReadyAllowanceMS = MaxDirectWaitMS, MaxReadyAllowanceMS
		set.ICEServers = servers
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
				select {
				case ended <- time.Now():
				default:
				}
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
		case r.URL.Path == "/__leaving":
			said := time.Now()
			w.WriteHeader(http.StatusNoContent)
			go func() {
				if leaving != nil {
					if err := leaving(); err != nil {
						verdict("FAIL: the page could not be left: " + err.Error())
						return
					}
				}
				select {
				case at := <-ended:
					switch after := at.Sub(said); {
					case after < 0:
						verdict("FAIL: the identity's channel ended before the page said it was leaving")
					case after > time.Second:
						verdict(fmt.Sprintf("FAIL: the identity's channel ended %d ms after the page said it was leaving, not within a second", after.Milliseconds()))
					default:
						verdict(fmt.Sprintf("OK\nthe identity's channel ended %d ms after the page said it was leaving", after.Milliseconds()))
					}
				case <-time.After(40 * time.Second):
					verdict("FAIL: the identity's channel was still open 40 s after the page said it was leaving")
				case <-ctx.Done():
				}
			}()
		case r.URL.Path == "/__left":
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			fmt.Fprint(w, "<!DOCTYPE html><title>left</title>")
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
	t.Cleanup(server.Close)
	t.Cleanup(cancel) // the sessions end before the server waits for them
	return server.URL, results
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

// leavingProbe holds a session on a channel whose path at the page is only
// through the TURN server, says it is leaving, and then navigates away or
// waits to be closed.
func leavingProbe(navigate bool) string {
	return strings.Replace(realBrowserLeavingProbe, "NAVIGATE", strconv.FormatBool(navigate), 1)
}

const realBrowserLeavingProbe = `
import { report } from '/__harness.js';
try {
  const { settle } = await import('/ws.js');
  const socket = new WebSocket((location.protocol === 'https:' ? 'wss://' : 'ws://') + location.host + '/ws?transport=1');
  socket.binaryType = 'arraybuffer';
  let carried = false;
  const carrier = await new Promise((resolve, reject) => settle(socket,
    c => { c.onmessage = e => { if (typeof e.data === 'string' && e.data.includes('"carried"')) carried = true; }; resolve(c); },
    () => reject(Error('neither the channel nor the socket came to carry the session')),
    config => new RTCPeerConnection(Object.assign(config, { iceTransportPolicy: 'relay' }))));
  if (carrier === socket) throw Error('the session stayed on its socket: the browser and the identity found no path through the TURN server');
  for (const end = Date.now() + 60000; !carried; await new Promise(resolve => setTimeout(resolve, 50))) if (Date.now() > end) throw Error('the identity did not say, on the channel, what carries the session');
  await fetch('/__leaving', { method: 'POST' });
  if (NAVIGATE) location.href = '/__left';
} catch (e) { report('FAIL: ' + ((e && e.message) || String(e))); }
`
