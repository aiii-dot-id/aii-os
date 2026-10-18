package dashboard

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/pion/webrtc/v4"
)

const carriedSwitchToken = "carried-switch-token-long-enough-to-be-one"

type heardSwitch struct {
	mu    sync.Mutex
	heard []bool
}

func (h *heardSwitch) answer(t *testing.T, n int) bool {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		h.mu.Lock()
		if len(h.heard) >= n {
			v := h.heard[n-1]
			h.mu.Unlock()
			return v
		}
		h.mu.Unlock()
		if time.Now().After(deadline) {
			t.Fatalf("the host never heard switch request %d", n)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func carriedSwitchServer(t *testing.T, required, onChannel bool) (*Server, *heardSwitch, string, *x509.CertPool) {
	t.Helper()
	const name = "ui.x.example.test"
	wait := 300
	if onChannel {
		wait = 8000
	}
	heard := &heardSwitch{}
	s := New("127.0.0.1", 0, &WSHandler{
		Channels: func() ChannelSettings {
			return ChannelSettings{WaitMS: wait, ReadyAllowanceMS: 100, ICEServersNamed: true, Sessions: 4}
		},
		SetSandboxOff: func(_, signedIn bool) error {
			heard.mu.Lock()
			heard.heard = append(heard.heard, signedIn)
			heard.mu.Unlock()
			return nil
		},
		GetSandbox: func() (*SandboxState, error) { return &SandboxState{Root: "/identity"}, nil },
	})
	if required {
		s.SetAccessToken(true, carriedSwitchToken)
	}
	if _, err := s.Start(""); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Shutdown(context.Background()) })
	cert := selfSigned(t, name)
	s.SetPublicCertificate(name, func() *tls.Certificate { return cert })
	s.AllowHost(name)
	roots := x509.NewCertPool()
	roots.AddCert(cert.Leaf)
	return s, heard, name, roots
}

const switchOff = `{"type":"sandbox_switch","request_id":"r","sandbox_off":true}`

func openCarriedPage(t *testing.T, s *Server, name string, roots *x509.CertPool, onChannel bool, cookie ...string) func() {
	t.Helper()
	conn := carriedSocket(t, s, name, roots, "&transport=1", cookie...)
	if onChannel {
		dc := channelPage(t, conn)
		return func() {
			if err := dc.Send(append([]byte{pieceLast}, switchOff...)); err != nil {
				t.Fatal(err)
			}
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	for {
		_, raw, err := conn.Read(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(raw), `"transport_ready"`) {
			break
		}
	}
	return func() {
		if err := conn.Write(ctx, websocket.MessageText, []byte(switchOff)); err != nil {
			t.Fatal(err)
		}
	}
}

func heldOn(s *Server) (channel, socket int) {
	s.wsMu.Lock()
	defer s.wsMu.Unlock()
	for c := range s.wsConns {
		switch c.(type) {
		case *channelCarrier:
			channel++
		case *pumpedSocket:
			socket++
		}
	}
	return channel, socket
}

func waitHeldOn(t *testing.T, s *Server, onChannel bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		ch, so := heldOn(s)
		if onChannel && ch == 1 || !onChannel && so == 1 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the session is not on the carrier meant (onChannel %v): channel %d, socket %d", onChannel, ch, so)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func channelPage(t *testing.T, conn *websocket.Conn) *webrtc.DataChannel {
	t.Helper()
	return channelPageSetup(t, conn, false)
}

type pageSetupSocket interface {
	Read(context.Context) (websocket.MessageType, []byte, error)
	Write(context.Context, websocket.MessageType, []byte) error
}

func channelPageSetup(t *testing.T, conn pageSetupSocket, gatherBeforeOffer bool) *webrtc.DataChannel {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(cancel)
	var channelOpen atomic.Bool
	var wmu sync.Mutex
	setupErr := make(chan error, 1)
	report := func(err error) {
		if err != nil {
			select {
			case setupErr <- err:
			default:
			}
		}
	}
	say := func(m setupMessage) {
		raw, _ := json.Marshal(m)
		wmu.Lock()
		defer wmu.Unlock()
		if err := conn.Write(ctx, websocket.MessageText, raw); err != nil && !channelOpen.Load() {
			report(err)
		}
	}
	_, raw, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("socket read: %v", err)
	}
	var first setupMessage
	_ = json.Unmarshal(raw, &first)
	if first.Type != "transport_setup" {
		t.Fatalf("first message %q, want transport_setup", first.Type)
	}
	var engine webrtc.SettingEngine

	engine.SetIPFilter(gatheredForChannel)

	engine.SetNetworkTypes([]webrtc.NetworkType{webrtc.NetworkTypeUDP4})
	pc, err := webrtc.NewAPI(webrtc.WithSettingEngine(engine)).NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pc.Close() })
	var localCandidateCount, remoteCandidateCount atomic.Int32
	var answerSeen atomic.Bool
	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("channel setup: peer=%s ice=%s gathering=%s answer=%v local_candidates=%d remote_candidates=%d open=%v",
				pc.ConnectionState(), pc.ICEConnectionState(), pc.ICEGatheringState(), answerSeen.Load(),
				localCandidateCount.Load(), remoteCandidateCount.Load(), channelOpen.Load())
		}
	})
	dc, err := pc.CreateDataChannel("dashboard", nil)
	if err != nil {
		t.Fatal(err)
	}
	dc.OnOpen(func() { channelOpen.Store(true) })
	ready := make(chan []byte, 64)
	dc.OnMessage(func(m webrtc.DataChannelMessage) { ready <- m.Data })
	var candidateMu sync.Mutex
	var localCandidates []setupMessage
	offered := false
	pc.OnICECandidate(func(c *webrtc.ICECandidate) {
		if c == nil {
			return
		}
		localCandidateCount.Add(1)
		init := c.ToJSON()
		message := setupMessage{Type: "transport_candidate", Candidate: init.Candidate, SDPMid: init.SDPMid, SDPMLineIndex: init.SDPMLineIndex}
		candidateMu.Lock()
		defer candidateMu.Unlock()
		if !offered {
			localCandidates = append(localCandidates, message)
			return
		}
		say(message)
	})

	go func() {
		var early []webrtc.ICECandidateInit
		answered := false
		for {
			_, raw, err := conn.Read(ctx)
			if err != nil {
				return
			}
			var m setupMessage
			_ = json.Unmarshal(raw, &m)
			switch m.Type {
			case "transport_answer":
				if err := pc.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeAnswer, SDP: m.SDP}); err != nil {
					report(err)
					return
				}
				answered = true
				answerSeen.Store(true)
				for _, c := range early {
					report(pc.AddICECandidate(c))
				}
			case "transport_candidate":
				remoteCandidateCount.Add(1)
				c := webrtc.ICECandidateInit{Candidate: m.Candidate, SDPMid: m.SDPMid, SDPMLineIndex: m.SDPMLineIndex}
				if answered {
					report(pc.AddICECandidate(c))
				} else {
					early = append(early, c)
				}
			case "transport_ready":
				report(fmt.Errorf("the identity chose the socket: %s", raw))
				return
			}
		}
	}()
	offer, err := pc.CreateOffer(nil)
	if err != nil {
		t.Fatal(err)
	}
	gathered := webrtc.GatheringCompletePromise(pc)
	if err := pc.SetLocalDescription(offer); err != nil {
		t.Fatal(err)
	}
	if gatherBeforeOffer {
		select {
		case <-gathered:
		case <-ctx.Done():
			t.Fatal("local candidates did not finish gathering")
		}
	}

	candidateMu.Lock()
	say(setupMessage{Type: "transport_offer", SDP: offer.SDP})
	offered = true
	for _, candidate := range localCandidates {
		say(candidate)
	}
	localCandidates = nil
	candidateMu.Unlock()

	select {
	case first := <-ready:
		if len(first) < 2 || first[0]&pieceLast == 0 || !strings.Contains(string(first[1:]), `"channel"`) {
			t.Fatalf("first piece on the channel %q", first)
		}
	case err := <-setupErr:
		t.Fatal(err)
	case <-time.After(15 * time.Second):
		t.Fatal("the channel never said ready")
	}
	return dc
}

func TestACarriedPageIsSignedInForTheSwitchOnlyWithTheTokenInForce(t *testing.T) {
	for _, carrier := range []struct {
		name      string
		onChannel bool
	}{{"a WebRTC channel", true}, {"the fallback socket", false}} {
		t.Run(carrier.name, func(t *testing.T) {
			t.Run("with the cookie, then the requirement dropped", func(t *testing.T) {
				s, heard, name, roots := carriedSwitchServer(t, true, carrier.onChannel)
				send := openCarriedPage(t, s, name, roots, carrier.onChannel, s.accessHash())
				waitHeldOn(t, s, carrier.onChannel)
				send()
				if !heard.answer(t, 1) {
					t.Fatal("a page that signed in with the token in force was heard as not signed in")
				}
				s.SetAccessToken(false, carriedSwitchToken)
				send()
				if heard.answer(t, 2) {
					t.Fatal("after the token stopped being asked for, the page was heard as signed in")
				}
			})
			t.Run("no token asked for", func(t *testing.T) {
				s, heard, name, roots := carriedSwitchServer(t, false, carrier.onChannel)
				send := openCarriedPage(t, s, name, roots, carrier.onChannel)
				send()
				if heard.answer(t, 1) {
					t.Fatal("a page under no token was heard as signed in")
				}
			})
			t.Run("no cookie under a required token", func(t *testing.T) {
				s, _, name, roots := carriedSwitchServer(t, true, carrier.onChannel)
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				client := &http.Client{Transport: &http.Transport{
					DialTLSContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
						c := tls.Client(carriedSession(t, s), &tls.Config{ServerName: name, RootCAs: roots, MinVersion: tls.VersionTLS12})
						return c, c.HandshakeContext(ctx)
					},
				}}
				c, _, err := websocket.Dial(ctx, "wss://"+name+"/ws?interaction_version=1&transport=1", &websocket.DialOptions{
					HTTPClient: client, HTTPHeader: http.Header{"Origin": []string{"https://" + name}}})
				if err == nil {
					c.CloseNow()
					t.Fatal("a carried page with no cookie was admitted under a required token")
				}
			})
		})
	}
}

func TestADeviceSignedInByAnAccessCodeIsSignedInForTheSwitch(t *testing.T) {
	s, heard, name, roots := carriedSwitchServer(t, true, false)
	st, err := s.issueAccessCode(s.auth.Load(), "")
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "https://"+name+"/auth/token", nil)
	req.TLS = &tls.ConnectionState{}
	granted, ok, _ := s.redeemAccessCode(st.Code, req)
	if !ok {
		t.Fatal("the code was not redeemed")
	}
	if granted.hash != s.accessHash() {
		t.Fatal("the code granted a cookie other than the token's")
	}
	send := openCarriedPage(t, s, name, roots, false, granted.hash)
	send()
	if !heard.answer(t, 1) {
		t.Fatal("a device signed in by an access code was heard as not signed in")
	}
}
