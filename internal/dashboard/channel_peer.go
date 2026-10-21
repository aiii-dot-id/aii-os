package dashboard

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
	"github.com/pion/ice/v4"
	"github.com/pion/webrtc/v4"

	"github.com/aiii-dot-id/aii-os/internal/logsink"
)

const channelPieceBytes = 16 << 10

const (
	pieceBinary = 1 << 0
	pieceLast   = 1 << 1
)

func (c ChannelSettings) held() ChannelSettings {
	c.WaitMS = within(c.WaitMS, DefaultDirectWaitMS, MinDirectWaitMS, MaxDirectWaitMS)
	c.ReadyAllowanceMS = within(c.ReadyAllowanceMS, DefaultDirectReadyAllowanceMS, MinDirectReadyAllowanceMS, MaxReadyAllowanceMS)
	c.Sessions = within(c.Sessions, DefaultChannelSessions, MinChannelSessions, MaxChannelSessions)
	c.QueueBytes = within(c.QueueBytes, DefaultChannelQueueBytes, MinChannelQueueBytes, MaxChannelQueueBytes)
	c.StallMS = within(c.StallMS, DefaultChannelStallMS, MinChannelStallMS, MaxChannelStallMS)
	return c
}

func (s *Server) chooseCarrier(ctx context.Context, socket carrier, r *http.Request, under *atomic.Int64) (carrier, func(), bool) {
	none := func() {}
	set, ok := s.channelOffered(r)
	if !ok {
		return socket, none, true
	}
	if int(under.Add(1)) > set.Sessions {
		under.Add(-1)
		return socket, none, true
	}
	chosen, alive := s.negotiateCarrier(ctx, socket, set)
	if _, onChannel := chosen.(*channelCarrier); !alive || !onChannel {
		under.Add(-1)
		return chosen, none, alive
	}
	return chosen, func() {
		_ = chosen.CloseNow()
		under.Add(-1)
	}, true
}

func (s *Server) channelOffered(r *http.Request) (ChannelSettings, bool) {
	if r.URL.Query().Get("transport") != "1" || r.Context().Value(carriedKey{}) == nil {
		return ChannelSettings{}, false
	}
	h := s.currentHandler()
	if h == nil || h.Channels == nil {
		return ChannelSettings{}, false
	}
	set := h.Channels()
	if set.Off {
		return ChannelSettings{}, false
	}
	if !set.ICEServersNamed && h.SessionServers != nil {
		set.ICEServers = nil
		left := 0
		for _, sv := range h.SessionServers() {
			if CheckICEServers([]ICEServer{sv}) != nil {
				left++
				continue
			}
			set.ICEServers = append(set.ICEServers, sv)
		}
		if left > 0 {
			logsink.Warn("dashboard.refusal", "%d of the session servers the relay named are not STUN or TURN addresses, or carry a credential inside the address: left out, and the session uses the rest", left)
		}
	}
	return set.held(), true
}

type setupMessage struct {
	Type          string      `json:"type"`
	ICEServers    []ICEServer `json:"ice_servers,omitempty"`
	ReadyWithinMS int         `json:"ready_within_ms,omitempty"`
	SDP           string      `json:"sdp,omitempty"`
	Candidate     string      `json:"candidate,omitempty"`
	SDPMid        *string     `json:"sdp_mid,omitempty"`
	SDPMLineIndex *uint16     `json:"sdp_mline_index,omitempty"`
	Carrier       string      `json:"carrier,omitempty"`
}

func writeSetup(ctx context.Context, to carrier, m setupMessage) error {
	raw, err := json.Marshal(m)
	if err != nil {
		return err
	}
	wctx, cancel := context.WithTimeout(ctx, writeWait)
	defer cancel()
	return to.Write(wctx, websocket.MessageText, raw)
}

type socketMessage struct {
	typ  websocket.MessageType
	data []byte
	err  error
}

type pumpedSocket struct {
	carrier
	in <-chan socketMessage
}

func (p *pumpedSocket) Read(ctx context.Context) (websocket.MessageType, []byte, error) {
	select {
	case m, ok := <-p.in:
		if !ok {
			return 0, nil, io.EOF
		}
		return m.typ, m.data, m.err
	case <-ctx.Done():
		return 0, nil, ctx.Err()
	}
}

func (s *Server) negotiateCarrier(ctx context.Context, socket carrier, set ChannelSettings) (carrier, bool) {
	pc, err := newPeer(set.ICEServers)
	if err != nil {

		logsink.Warn("dashboard.error", "a channel could not be offered (%v); the session stays on its socket", err)
		return socket, true
	}
	gone := make(chan struct{})
	var once sync.Once
	end := func() { once.Do(func() { close(gone) }) }
	opened := make(chan *channelCarrier, 1)
	pc.OnConnectionStateChange(func(st webrtc.PeerConnectionState) {
		if st == webrtc.PeerConnectionStateFailed || st == webrtc.PeerConnectionStateClosed {
			end()
		}
	})

	var settled atomic.Bool
	pc.OnDataChannel(func(dc *webrtc.DataChannel) {
		if dc.Label() != "dashboard" {
			return
		}

		piece := channelPieceBytes
		if remote := pc.RemoteDescription(); remote != nil {
			if n := statedMaxMessageSize(remote.SDP); n > 1 && n < piece {
				piece = n
			}
		}
		cc := newChannelCarrier(pc, dc, piece, set, gone, end)
		dc.OnOpen(func() {
			select {
			case opened <- cc:
			default:
			}
		})
	})
	pc.OnICECandidate(func(c *webrtc.ICECandidate) {
		if settled.Load() {
			return
		}
		if c == nil {
			_ = writeSetup(ctx, socket, setupMessage{Type: "transport_candidates_done"})
			return
		}
		init := c.ToJSON()
		_ = writeSetup(ctx, socket, setupMessage{Type: "transport_candidate", Candidate: init.Candidate, SDPMid: init.SDPMid, SDPMLineIndex: init.SDPMLineIndex})
	})
	if err := writeSetup(ctx, socket, setupMessage{Type: "transport_setup", ICEServers: set.ICEServers, ReadyWithinMS: set.WaitMS + set.ReadyAllowanceMS}); err != nil {
		_ = pc.Close()
		return nil, false
	}

	msgs, done := make(chan socketMessage), make(chan struct{})
	go func() {
		defer close(msgs)
		for {
			typ, data, err := socket.Read(ctx)
			select {
			case msgs <- socketMessage{typ, data, err}:
			case <-done:
				return
			case <-ctx.Done():
				return
			}
			if err != nil {
				return
			}
		}
	}()
	in := (<-chan socketMessage)(msgs)
	socketGone := false
	wait := time.NewTimer(time.Duration(set.WaitMS) * time.Millisecond)
	defer wait.Stop()

	useSocket := func() (carrier, bool) {
		settled.Store(true)
		_ = pc.Close()
		if socketGone {
			return nil, false
		}
		if err := writeSetup(ctx, socket, setupMessage{Type: "transport_ready", Carrier: "socket"}); err != nil {
			return nil, false
		}
		return &pumpedSocket{carrier: socket, in: msgs}, true
	}
	for {
		select {
		case cc := <-opened:
			settled.Store(true)

			if err := writeSetup(ctx, cc, setupMessage{Type: "transport_ready", Carrier: "channel"}); err != nil {
				_ = cc.CloseNow()
				return useSocket()
			}

			close(done)
			_ = socket.CloseNow()
			return cc, true
		case m, ok := <-in:
			if !ok || m.err != nil {

				socketGone, in = true, nil
				continue
			}
			var msg setupMessage
			if m.typ != websocket.MessageText || json.Unmarshal(m.data, &msg) != nil {
				continue
			}
			switch msg.Type {
			case "transport_offer":
				answer, err := answerOffer(pc, msg.SDP)
				if err != nil {
					logsink.Warn("dashboard.error", "a channel's offer was not answered (%v); the session stays on its socket", err)
					return useSocket()
				}
				if err := writeSetup(ctx, socket, setupMessage{Type: "transport_answer", SDP: answer}); err != nil {
					socketGone, in = true, nil
				}
			case "transport_candidate":

				_ = pc.AddICECandidate(webrtc.ICECandidateInit{Candidate: msg.Candidate, SDPMid: msg.SDPMid, SDPMLineIndex: msg.SDPMLineIndex})
			case "transport_candidates_done":
			default:

			}
		case <-gone:
			return useSocket()
		case <-wait.C:
			return useSocket()
		case <-ctx.Done():
			_ = pc.Close()
			return nil, false
		}
	}
}

func newPeer(servers []ICEServer) (*webrtc.PeerConnection, error) {
	var engine webrtc.SettingEngine

	engine.SetICEMulticastDNSMode(ice.MulticastDNSModeDisabled)

	engine.SetIPFilter(gatheredForChannel)

	api := webrtc.NewAPI(webrtc.WithSettingEngine(engine), webrtc.WithMediaEngine(&webrtc.MediaEngine{}))
	var cfg webrtc.Configuration
	for _, sv := range servers {
		cfg.ICEServers = append(cfg.ICEServers, webrtc.ICEServer{URLs: sv.URLs, Username: sv.Username, Credential: sv.Credential})
	}
	return api.NewPeerConnection(cfg)
}

func gatheredForChannel(ip net.IP) bool {
	return ip.To4() != nil || !ip.IsLinkLocalUnicast()
}

func answerOffer(pc *webrtc.PeerConnection, sdp string) (string, error) {
	if err := pc.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeOffer, SDP: sdp}); err != nil {
		return "", err
	}
	answer, err := pc.CreateAnswer(nil)
	if err != nil {
		return "", err
	}
	if err := pc.SetLocalDescription(answer); err != nil {
		return "", err
	}
	return answer.SDP, nil
}

func statedMaxMessageSize(sdp string) int {
	for _, line := range strings.Split(sdp, "\n") {
		if v, ok := strings.CutPrefix(strings.TrimSpace(line), "a=max-message-size:"); ok {
			n, err := strconv.Atoi(strings.TrimSpace(v))
			if err == nil && n > 0 {
				return n
			}
		}
	}
	return 0
}

type pieceSender interface {
	Send([]byte) error
	BufferedAmount() uint64
}

type channelCarrier struct {
	dc    pieceSender
	piece int
	queue uint64
	stall time.Duration
	in    chan []byte
	low   chan struct{}
	gone  <-chan struct{}
	end   func()
	wsem  chan struct{}
}

var (
	errPieceEmpty   = errors.New("dashboard: a piece with no header arrived on the channel")
	errPieceMixed   = errors.New("dashboard: a message changed between text and binary part way")
	errMessageHuge  = errors.New("dashboard: a binary message on the channel is larger than a voice frame may be")
	errMessageStall = errors.New("dashboard: a message stopped arriving part way")
)

func newChannelCarrier(pc *webrtc.PeerConnection, dc *webrtc.DataChannel, piece int, set ChannelSettings, gone <-chan struct{}, end func()) *channelCarrier {
	c := &channelCarrier{dc: dc, piece: piece, queue: uint64(set.QueueBytes), stall: time.Duration(set.StallMS) * time.Millisecond,
		in: make(chan []byte), low: make(chan struct{}, 1), gone: gone, wsem: make(chan struct{}, 1)}
	var once sync.Once
	c.end = func() {
		once.Do(func() {
			end()
			_ = dc.Close()
			_ = pc.Close()
		})
	}
	dc.SetBufferedAmountLowThreshold(c.queue / 2)
	dc.OnBufferedAmountLow(func() {
		select {
		case c.low <- struct{}{}:
		default:
		}
	})
	dc.OnMessage(func(m webrtc.DataChannelMessage) {
		select {
		case c.in <- m.Data:
		case <-gone:
		}
	})
	dc.OnClose(end)
	return c
}

func (c *channelCarrier) Read(ctx context.Context) (websocket.MessageType, []byte, error) {
	var buf []byte
	typ, started, over := websocket.MessageText, false, false
	var stalled <-chan time.Time
	var timer *time.Timer
	defer func() {
		if timer != nil {
			timer.Stop()
		}
	}()
	for {
		select {
		case p := <-c.in:
			if len(p) < 1 {
				return 0, nil, errPieceEmpty
			}
			binary := p[0]&pieceBinary != 0
			switch {
			case !started:
				started = true
				if binary {
					typ = websocket.MessageBinary
				}
			case binary != (typ == websocket.MessageBinary):
				return 0, nil, errPieceMixed
			}
			if binary && len(buf)+len(p)-1 > maxVoiceFrameBytes {
				return 0, nil, errMessageHuge
			}
			if !over {
				buf = append(buf, p[1:]...)
				if !binary && len(buf) > maxTextFrameBytes {
					buf, over = buf[:maxTextFrameBytes+1], true
				}
			}
			if p[0]&pieceLast != 0 {
				return typ, buf, nil
			}
			if timer == nil {
				timer = time.NewTimer(c.stall)
				stalled = timer.C
			} else {
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
				timer.Reset(c.stall)
			}
		case <-stalled:
			return 0, nil, errMessageStall
		case <-c.gone:
			return 0, nil, io.EOF
		case <-ctx.Done():
			return 0, nil, ctx.Err()
		}
	}
}

func (c *channelCarrier) Write(ctx context.Context, typ websocket.MessageType, p []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case c.wsem <- struct{}{}:
	case <-c.gone:
		return io.ErrClosedPipe
	case <-ctx.Done():
		return ctx.Err()
	}
	defer func() { <-c.wsem }()
	head := byte(0)
	if typ == websocket.MessageBinary {
		head = pieceBinary
	}
	room := c.piece - 1
	for first := true; first || len(p) > 0; first = false {
		n := min(len(p), room)
		piece := make([]byte, 1+n)
		piece[0] = head
		if n == len(p) {
			piece[0] |= pieceLast
		}
		copy(piece[1:], p[:n])
		p = p[n:]
		for c.dc.BufferedAmount()+uint64(len(piece)) > c.queue {
			select {
			case <-c.low:
			case <-c.gone:
				return io.ErrClosedPipe
			case <-ctx.Done():
				c.end()
				return ctx.Err()
			}
		}

		if err := ctx.Err(); err != nil {
			c.end()
			return err
		}
		if err := c.dc.Send(piece); err != nil {
			c.end()
			return err
		}
	}
	return nil
}

func (c *channelCarrier) Close(websocket.StatusCode, string) error { return c.CloseNow() }

func (c *channelCarrier) CloseNow() error {
	c.end()
	return nil
}
