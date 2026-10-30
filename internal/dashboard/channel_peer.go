package dashboard

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
	"github.com/pion/ice/v4"
	"github.com/pion/transport/v5"
	"github.com/pion/transport/v5/stdnet"
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
	if len(set.ICEServers) == 0 {

		if n, err := channelNet(); err == nil {
			if ifaces, _ := n.Interfaces(); !channelOffersAnAddress(ifaces) {
				source := "no session server the relay named is in use"
				if set.ICEServersNamed {
					source = "dashboard.ice_servers names no session server"
				}
				logsink.Info("dashboard.decision", "a session stays on its socket: %s, and this machine has no interface that is up, other than loopback, with an address a channel offers, so a channel would gather nothing", source)
				return socket, none, true
			}
		}
	}
	if int(under.Add(1)) > set.Sessions {
		under.Add(-1)
		logsink.Info("dashboard.decision", "a session stays on its socket: as many attempts and channel sessions are under way as dashboard.channel_sessions allows, %d", set.Sessions)
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
		named, expire := h.SessionServers()
		set.ICEServers = nil
		left := 0
		for _, sv := range named {
			if CheckICEServers([]ICEServer{sv}) != nil {
				left++
				continue
			}
			set.ICEServers = append(set.ICEServers, sv)
		}
		if left > 0 {
			logsink.Warn("dashboard.refusal", "%d of the session servers the relay named are not STUN or TURN addresses, or carry a credential inside the address: left out, and the session uses the rest", left)
		}
		if len(set.ICEServers) > 0 {
			set.ServersExpire = expire
		}
	}
	set = set.held()
	if !set.ServersExpire.IsZero() && set.ServersExpire.Sub(s.clock()) <= set.readyWithin() {
		logsink.Info("dashboard.decision", "the session servers the relay named are not used: their credentials expire within the %d ms a channel is given to be made (dashboard.direct_wait_ms and dashboard.direct_ready_allowance_ms)", set.readyWithin().Milliseconds())
		set.ICEServers, set.ServersExpire = nil, time.Time{}
	}
	return set, true
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
	attempt, began := s.channelAttempts.Add(1), time.Now()
	gone := make(chan struct{})
	var once sync.Once
	end := func() { once.Do(func() { close(gone) }) }
	opened := make(chan *channelCarrier, 1)
	pc.OnConnectionStateChange(func(st webrtc.PeerConnectionState) {
		logsink.Info("dashboard.session", "channel %d: the peer connection is %s (ICE %s)", attempt, st, pc.ICEConnectionState())
		if st == webrtc.PeerConnectionStateFailed || st == webrtc.PeerConnectionStateClosed {
			end()
		}
	})
	pc.SCTP().Transport().ICETransport().OnSelectedCandidatePairChange(func(p *webrtc.ICECandidatePair) {
		logsink.Info("dashboard.session", "channel %d: the path in use is %s here and %s at the page", attempt, pathEnd(p.Local), pathEnd(p.Remote))
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
	if err := writeSetup(ctx, socket, setupMessage{Type: "transport_setup", ICEServers: set.ICEServers, ReadyWithinMS: int(set.readyWithin().Milliseconds())}); err != nil {
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

	var trying *channelCarrier
	var openedAfter time.Duration
	answered := make(chan error, 1)
	wait := time.NewTimer(time.Duration(set.WaitMS) * time.Millisecond)
	defer wait.Stop()

	useSocket := func(why string) (carrier, bool) {
		settled.Store(true)
		states := fmt.Sprintf("peer connection %s, ICE %s", pc.ConnectionState(), pc.ICEConnectionState())
		if trying != nil {
			_ = trying.CloseNow()
		}
		_ = pc.Close()
		if socketGone {
			logsink.Info("dashboard.decision", "channel %d: nothing carries the session: its socket closed, and %s (%s)", attempt, why, states)
			return nil, false
		}
		if err := writeSetup(ctx, socket, setupMessage{Type: "transport_ready", Carrier: "socket"}); err != nil {
			logsink.Info("dashboard.decision", "channel %d: nothing carries the session: %s, and its socket did not take the word that it carries it (%v)", attempt, why, err)
			return nil, false
		}
		logsink.Info("dashboard.decision", "channel %d: the session is carried on its socket: %s (%s)", attempt, why, states)
		return &pumpedSocket{carrier: socket, in: msgs}, true
	}
	for {
		select {
		case cc := <-opened:
			settled.Store(true)

			if err := writeSetup(ctx, cc, setupMessage{Type: "transport_ready", Carrier: "channel"}); err != nil {
				_ = cc.CloseNow()
				return useSocket(fmt.Sprintf("the channel opened and did not take its first message (%v)", err))
			}
			trying, openedAfter = cc, time.Since(began)
			go func() { answered <- pageAnswered(ctx, cc) }()
		case err := <-answered:
			if errors.Is(err, errNotAnAnswer) {

				settled.Store(true)
				s.reloadEarlierPage(ctx, trying)
				_ = pc.Close()
				close(done)
				_ = socket.CloseNow()
				logsink.Info("dashboard.decision", "channel %d: nothing carries the session: the page began it on the channel without answering, as a page from before the round trip does, and is told to load again", attempt)
				return nil, false
			}
			if err != nil {
				return useSocket(fmt.Sprintf("the channel opened and the page's answer did not come back on it (%v)", err))
			}

			close(done)
			_ = socket.CloseNow()
			logsink.Info("dashboard.decision", "channel %d: the session is carried on the channel, open %d ms and answered %d ms after the setup was sent", attempt, openedAfter.Milliseconds(), time.Since(began).Milliseconds())
			s.endBeforeExpiry(attempt, trying, set, func() (*webrtc.ICECandidatePair, []string) {
				p, _ := pc.SCTP().Transport().ICETransport().GetSelectedCandidatePair()
				var relays []string
				for _, st := range pc.GetStats() {
					if c, ok := st.(webrtc.ICECandidateStats); ok && c.Type == webrtc.StatsTypeLocalCandidate && c.CandidateType == webrtc.ICECandidateTypeRelay {
						relays = append(relays, c.IP)
					}
				}
				return p, relays
			})
			return trying, true
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
					logsink.Warn("dashboard.error", "channel %d: the page's offer was not answered (%v)", attempt, err)
					return useSocket("the page's offer was not answered")
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
			if trying != nil {
				return useSocket("the path ended before the page's answer came back on the channel")
			}
			return useSocket("the path ended before a channel opened")
		case <-wait.C:
			if trying != nil {
				return useSocket(fmt.Sprintf("the channel opened and the page's answer did not come back on it within dashboard.direct_wait_ms, %d ms", set.WaitMS))
			}
			return useSocket(fmt.Sprintf("no channel opened within dashboard.direct_wait_ms, %d ms", set.WaitMS))
		case <-ctx.Done():
			_ = pc.Close()
			logsink.Info("dashboard.decision", "channel %d: the session ended during its setup", attempt)
			return nil, false
		}
	}
}

func (s *Server) endBeforeExpiry(attempt uint64, cc *channelCarrier, set ChannelSettings, path func() (*webrtc.ICECandidatePair, []string)) {
	if set.ServersExpire.IsZero() {
		return
	}
	end := time.NewTimer(set.ServersExpire.Add(-set.readyWithin()).Sub(s.clock()))
	go func() {
		defer end.Stop()
		select {
		case <-end.C:
		case <-cc.gone:
			return
		}
		p, relays := path()
		relayed, why := s.relayedPath(p, relays, set)
		if !relayed {
			logsink.Info("dashboard.decision", "channel %d: the credentials of its session servers expire in %d ms and its path is direct (%s here and %s at the page, %s): the channel goes on, and its unused relay allocations are refused their refreshes from then until it ends", attempt, set.readyWithin().Milliseconds(), pathEnd(p.Local), pathEnd(p.Remote), why)
			return
		}
		logsink.Info("dashboard.decision", "channel %d: ended %d ms before the credentials of its session servers expire, the time a channel is given to be made (dashboard.direct_wait_ms and dashboard.direct_ready_allowance_ms), as %s; the page reconnects and is offered a channel with the servers the relay names now", attempt, set.readyWithin().Milliseconds(), why)
		_ = cc.CloseNow()
	}()
}

func (s *Server) relayedPath(p *webrtc.ICECandidatePair, relays []string, set ChannelSettings) (bool, string) {
	if p == nil {
		return true, "its path cannot be read and nothing shows it is direct"
	}
	if p.Local.Typ == webrtc.ICECandidateTypeRelay || p.Remote.Typ == webrtc.ICECandidateTypeRelay {
		return true, "its path has a relay candidate at one end"
	}
	if p.Local.Typ != webrtc.ICECandidateTypePrflx && p.Remote.Typ != webrtc.ICECandidateTypePrflx {
		return false, "no relay or peer-reflexive candidate at either end"
	}
	reflexiveOn := func(addrs []netip.Addr) bool {
		for _, c := range []*webrtc.ICECandidate{p.Local, p.Remote} {
			a, err := netip.ParseAddr(c.Address)
			if c.Typ == webrtc.ICECandidateTypePrflx && err == nil && slices.Contains(addrs, a.Unmap()) {
				return true
			}
		}
		return false
	}
	var own []netip.Addr
	for _, r := range relays {
		if a, err := netip.ParseAddr(r); err == nil {
			own = append(own, a.Unmap())
		}
	}
	if reflexiveOn(own) {
		return true, "its path has a peer-reflexive candidate at one end on the address of one of the identity's own relay candidates"
	}
	lookup := s.lookupIP
	if lookup == nil {
		lookup = net.DefaultResolver.LookupNetIP
	}
	ctx, cancel := context.WithDeadline(context.Background(), set.ServersExpire)
	defer cancel()
	var hosted []netip.Addr
	for _, sv := range set.ICEServers {
		for _, u := range sv.URLs {
			scheme, rest, _ := strings.Cut(u, ":")
			if scheme != "turn" && scheme != "turns" {
				continue
			}
			host, _, _ := strings.Cut(rest, "?")
			if h, _, err := net.SplitHostPort(host); err == nil {
				host = h
			}
			addrs, err := lookup(ctx, "ip", strings.Trim(host, "[]"))
			if err != nil {
				return true, "its path has a peer-reflexive candidate at one end and the addresses of its TURN servers cannot be read, so nothing shows it is direct"
			}
			for _, a := range addrs {
				hosted = append(hosted, a.Unmap())
			}
		}
	}
	if reflexiveOn(hosted) {
		return true, "its path has a peer-reflexive candidate at one end on an address of one of its TURN servers"
	}
	return false, "its peer-reflexive end on no relay address of its session"
}

var errNotAnAnswer = errors.New("the page's first message on the channel was not its answer")

func pageAnswered(ctx context.Context, cc *channelCarrier) error {
	typ, raw, err := cc.Read(ctx)
	if err != nil {
		return err
	}
	var m setupMessage
	if typ != websocket.MessageText || json.Unmarshal(raw, &m) != nil || m.Type != "transport_ready" || m.Carrier != "channel" {
		return errNotAnAnswer
	}
	return nil
}

func newPeer(servers []ICEServer) (*webrtc.PeerConnection, error) {
	var engine webrtc.SettingEngine

	engine.SetICEMulticastDNSMode(ice.MulticastDNSModeDisabled)

	n, err := channelNet()
	if err != nil {
		return nil, err
	}
	engine.SetNet(n)

	api := webrtc.NewAPI(webrtc.WithSettingEngine(engine), webrtc.WithMediaEngine(&webrtc.MediaEngine{}))
	var cfg webrtc.Configuration
	for _, sv := range servers {
		cfg.ICEServers = append(cfg.ICEServers, webrtc.ICEServer{URLs: sv.URLs, Username: sv.Username, Credential: sv.Credential})
	}
	return api.NewPeerConnection(cfg)
}

func pathEnd(c *webrtc.ICECandidate) string {
	if c == nil {
		return "nothing"
	}
	family := "a name"
	if ip := net.ParseIP(c.Address); ip != nil {
		family = "IPv6"
		if ip.To4() != nil {
			family = "IPv4"
		}
	}
	return c.Typ.String() + " " + c.Protocol.String() + " " + family
}

func gatheredForChannel(ip net.IP) bool {
	return !ip.IsPrivate() && (ip.To4() != nil || !ip.IsLinkLocalUnicast())
}

func channelNet() (*stdnet.Net, error) {
	n, err := stdnet.NewNet()
	if err != nil {
		return nil, err
	}
	ifaces, _ := n.Interfaces()
	keptForChannel(ifaces)
	return n, nil
}

func keptForChannel(ifaces []*transport.Interface) {
	for _, ifc := range ifaces {
		addrs, _ := ifc.Addrs()
		for _, a := range slices.Clone(addrs) {
			var ip net.IP
			switch v := a.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			}
			if ip != nil && !gatheredForChannel(ip) {
				ifc.RemoveAddress(ip)
			}
		}
	}
}

func channelOffersAnAddress(ifaces []*transport.Interface) bool {
	for _, ifc := range ifaces {
		if addrs, _ := ifc.Addrs(); len(addrs) > 0 && ifc.Flags&net.FlagUp != 0 && ifc.Flags&net.FlagLoopback == 0 {
			return true
		}
	}
	return false
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
	SetBufferedAmountLowThreshold(uint64)
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

func (c *channelCarrier) flush(ctx context.Context) {
	c.dc.SetBufferedAmountLowThreshold(0)
	for c.dc.BufferedAmount() > 0 {
		select {
		case <-c.low:
		case <-c.gone:
			return
		case <-ctx.Done():
			return
		}
	}
}

func (c *channelCarrier) Close(websocket.StatusCode, string) error { return c.CloseNow() }

func (c *channelCarrier) CloseNow() error {
	c.end()
	return nil
}
