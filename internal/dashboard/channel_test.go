package dashboard

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"regexp"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/coder/websocket"
	"github.com/pion/transport/v5"
	"github.com/pion/turn/v5"
	"github.com/pion/webrtc/v4"

	"github.com/aiii-dot-id/aii-os/internal/logsink"
)

// sentPieces stands in for the data channel's sending side: it keeps every
// piece, and says as much is queued as the test tells it.
type sentPieces struct {
	mu     sync.Mutex
	pieces [][]byte
	queued uint64
	fail   error
	asked  func() // when set, runs as the queue's room is asked, outside the lock
}

func (s *sentPieces) Send(p []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.fail != nil {
		return s.fail
	}
	s.pieces = append(s.pieces, append([]byte(nil), p...))
	return nil
}

func (s *sentPieces) SetBufferedAmountLowThreshold(uint64) {}

func (s *sentPieces) BufferedAmount() uint64 {
	s.mu.Lock()
	asked := s.asked
	s.mu.Unlock()
	if asked != nil {
		asked()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.queued
}

func (s *sentPieces) taken() [][]byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([][]byte(nil), s.pieces...)
}

// framed is a channel carrier over a stand-in channel: what it sends is
// kept, and what the test feeds it arrives as pieces from the page.
func framed(piece int) (*channelCarrier, *sentPieces, chan struct{}) {
	out := &sentPieces{}
	gone := make(chan struct{})
	var once sync.Once
	c := &channelCarrier{dc: out, piece: piece, queue: 1 << 20, stall: 10 * time.Second,
		in: make(chan []byte, 4096), low: make(chan struct{}, 1), gone: gone, wsem: make(chan struct{}, 1)}
	c.end = func() { once.Do(func() { close(gone) }) }
	return c, out, gone
}

func TestAMessageCrossesTheChannelInPiecesAndArrivesWhole(t *testing.T) {
	ctx := context.Background()
	for _, c := range []struct {
		what   string
		typ    websocket.MessageType
		length int
		pieces int
	}{
		{"an empty text message", websocket.MessageText, 0, 1},
		{"one byte", websocket.MessageText, 1, 1},
		{"exactly one piece", websocket.MessageBinary, 63, 1},
		{"one byte over a piece", websocket.MessageBinary, 64, 2},
		{"a long text message", websocket.MessageText, 1000, 16},
		{"a voice frame", websocket.MessageBinary, 200000, 3175},
	} {
		send, out, _ := framed(64)
		payload := bytes.Repeat([]byte("abcdefghij"), c.length/10+1)[:c.length]
		if err := send.Write(ctx, c.typ, payload); err != nil {
			t.Fatalf("%s: %v", c.what, err)
		}
		pieces := out.taken()
		if len(pieces) != c.pieces {
			t.Fatalf("%s: cut into %d pieces, want %d", c.what, len(pieces), c.pieces)
		}
		receive, _, _ := framed(64)
		for i, p := range pieces {
			if len(p) < 1 || len(p) > 64 {
				t.Fatalf("%s: piece %d is %d bytes, want 1 to 64", c.what, i, len(p))
			}
			if last := p[0]&pieceLast != 0; last != (i == len(pieces)-1) {
				t.Fatalf("%s: piece %d of %d says last=%v", c.what, i, len(pieces), last)
			}
			if binary := p[0]&pieceBinary != 0; binary != (c.typ == websocket.MessageBinary) {
				t.Fatalf("%s: piece %d says binary=%v", c.what, i, binary)
			}
			receive.in <- p
		}
		typ, got, err := receive.Read(ctx)
		if err != nil || typ != c.typ || !bytes.Equal(got, payload) {
			t.Fatalf("%s: arrived as type %v, %d bytes, %v; want type %v and the %d bytes sent", c.what, typ, len(got), err, c.typ, len(payload))
		}
	}
}

func TestTheSocketsLimitsHoldOnTheChannel(t *testing.T) {
	ctx := context.Background()
	c, _, _ := framed(16 << 10)
	piece := func(head byte, n int) []byte { return append([]byte{head}, bytes.Repeat([]byte("x"), n)...) }
	feed := func(to *channelCarrier, head byte, total int) {
		for total > 0 {
			n := min(total, 16<<10-1)
			total -= n
			h := head
			if total == 0 {
				h |= pieceLast
			}
			to.in <- piece(h, n)
		}
	}
	go func() {
		feed(c, 0, maxTextFrameBytes+50000)
		c.in <- piece(pieceLast, 5)
	}()
	typ, got, err := c.Read(ctx)
	if err != nil || typ != websocket.MessageText || len(got) != maxTextFrameBytes+1 {
		t.Fatalf("a text message over the limit arrived as %d bytes, %v; want one byte over the limit, for the session to refuse", len(got), err)
	}
	if _, got, err = c.Read(ctx); err != nil || string(got) != "xxxxx" {
		t.Fatalf("the message after an oversized one arrived as %q, %v: the rest of the large one was not dropped", got, err)
	}

	huge, _, _ := framed(16 << 10)
	go feed(huge, pieceBinary, maxVoiceFrameBytes+1)
	if _, _, err := huge.Read(ctx); !errors.Is(err, errMessageHuge) {
		t.Fatalf("a binary message over a voice frame's limit: %v, want the carrier ended", err)
	}
	mixed, _, _ := framed(64)
	mixed.in <- piece(0, 3)
	mixed.in <- piece(pieceBinary|pieceLast, 3)
	if _, _, err := mixed.Read(ctx); !errors.Is(err, errPieceMixed) {
		t.Fatalf("a message that changed kind part way: %v", err)
	}
	empty, _, _ := framed(64)
	empty.in <- []byte{}
	if _, _, err := empty.Read(ctx); !errors.Is(err, errPieceEmpty) {
		t.Fatalf("a piece with no header: %v", err)
	}
}

func TestAChannelWriteHasItsTimeAndAMessageMustKeepArriving(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c, out, gone := framed(64)
		c.queue = 1000
		out.queued = 990 // no room for a 64-byte piece
		done := make(chan error, 1)
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), writeWait)
			defer cancel()
			done <- c.Write(ctx, websocket.MessageText, []byte("after the queue drains"))
		}()
		time.Sleep(writeWait / 2)
		if len(out.taken()) != 0 {
			t.Fatal("a piece was sent into a full queue")
		}
		out.mu.Lock()
		out.queued = 0
		out.mu.Unlock()
		c.low <- struct{}{}
		if err := <-done; err != nil || len(out.taken()) != 1 {
			t.Fatalf("the write did not go on when the queue drained: %v, %d pieces", err, len(out.taken()))
		}

		out.mu.Lock()
		out.queued = 990
		out.mu.Unlock()
		ctx, cancel := context.WithTimeout(context.Background(), writeWait)
		defer cancel()
		start := time.Now()
		err := c.Write(ctx, websocket.MessageText, bytes.Repeat([]byte("y"), 500))
		if !errors.Is(err, context.DeadlineExceeded) || time.Since(start) != writeWait {
			t.Fatalf("a write into a queue that never drained: %v after %s, want its time and no more", err, time.Since(start))
		}
		select {
		case <-gone:
		default:
			t.Fatal("a write that left part of a message on the channel did not end the carrier")
		}
		if _, _, err := c.Read(context.Background()); !errors.Is(err, io.EOF) {
			t.Fatalf("a carrier whose path has ended read %v, want the end", err)
		}

		slow, _, _ := framed(64)
		slow.stall = 3 * time.Second
		slow.in <- []byte{0, 'a'}
		began := time.Now()
		if _, _, err := slow.Read(context.Background()); !errors.Is(err, errMessageStall) || time.Since(began) != 3*time.Second {
			t.Fatalf("a message that stopped part way: %v after %s, want the stall limit", err, time.Since(began))
		}
		// Between messages there is no stall: an idle session is not ended.
		idle, _, _ := framed(64)
		idle.stall = 3 * time.Second
		ctx2, cancel2 := context.WithTimeout(context.Background(), time.Minute)
		defer cancel2()
		if _, _, err := idle.Read(ctx2); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("an idle channel was ended by %v after a minute, want only the caller's own time", err)
		}
	})
}

// A WRITE WHOSE TIME HAS RUN OUT SENDS NOTHING, ROOM OR NONE, AND A WRITER
// WAITING FOR ANOTHER KEEPS ITS OWN TIME. The deadline was asked only while
// the send queue was full, so a cancelled write with room went out, and the
// writer's lock could not be given up, so a writer behind a slow one waited
// past its own time for the other's. The
// control half: a write within its time with room goes out whole.
func TestAChannelWriteKeepsItsTimeWithRoomAndBehindAnotherWriter(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c, out, gone := framed(64)
		cancelled, cancel := context.WithCancel(context.Background())
		cancel()
		if err := c.Write(cancelled, websocket.MessageText, []byte("too late")); !errors.Is(err, context.Canceled) || len(out.taken()) != 0 {
			t.Fatalf("a cancelled write with room: %v, %d pieces sent, want the cancellation and none", err, len(out.taken()))
		}
		select {
		case <-gone:
			t.Fatal("a write that sent nothing ended the carrier")
		default:
		}

		c.queue = 1000
		out.mu.Lock()
		out.queued = 990 // the first writer holds the channel, waiting for room
		out.mu.Unlock()
		long, cancelLong := context.WithTimeout(context.Background(), time.Minute)
		defer cancelLong()
		held := make(chan error, 1)
		go func() { held <- c.Write(long, websocket.MessageText, []byte("waits for room")) }()
		synctest.Wait()
		short, cancelShort := context.WithTimeout(context.Background(), time.Second)
		defer cancelShort()
		began := time.Now()
		if err := c.Write(short, websocket.MessageText, []byte("behind it")); !errors.Is(err, context.DeadlineExceeded) || time.Since(began) != time.Second {
			t.Fatalf("a writer behind another: %v after %s, want its own second", err, time.Since(began))
		}
		out.mu.Lock()
		out.queued = 0
		out.mu.Unlock()
		c.low <- struct{}{}
		if err := <-held; err != nil || len(out.taken()) != 1 {
			t.Fatalf("the first writer goes on when the queue drains: %v, %d pieces", err, len(out.taken()))
		}
	})
}

// A WRITER WOKEN BY ROOM AFTER ITS TIME RAN OUT SENDS NOTHING. The deadline
// was asked before the wait for room, so a writer whose time ran out while
// it was woken by the queue draining, before it looked at the room again,
// went on to send and answered success. Here the writer is woken by room, and its time ends just as
// it asks the queue's room again: it sends nothing.
func TestAWriterWokenByRoomAfterItsTimeSendsNothing(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c, out, _ := framed(64)
		c.queue = 1000
		out.mu.Lock()
		out.queued = 995 // no room for the 9-byte piece
		out.mu.Unlock()
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() { done <- c.Write(ctx, websocket.MessageText, []byte("too late")) }()
		synctest.Wait()
		if len(out.taken()) != 0 {
			t.Fatal("a piece was sent into a full queue")
		}
		out.mu.Lock()
		out.queued = 0
		out.asked = cancel // the time ends as the woken writer looks again
		out.mu.Unlock()
		c.low <- struct{}{}
		if err := <-done; !errors.Is(err, context.Canceled) || len(out.taken()) != 0 {
			t.Fatalf("a writer woken by room after its time: %v, %d pieces sent", err, len(out.taken()))
		}
	})
}

// pipedSocket is a socket that goes nowhere: what the identity writes is
// kept for the test's page to read, and what the page writes is read by
// the identity. Closing it ends both.
type pipedSocket struct {
	toPage, toIdentity chan []byte
	closed             chan struct{}
	once               sync.Once
	// writesAfterClose: a write still succeeds once the socket is closed.
	// A carrier promises nothing about a write after its read has failed,
	// and the rule under test must not lean on one refusing.
	writesAfterClose bool
}

func newPipedSocket() *pipedSocket {
	return &pipedSocket{toPage: make(chan []byte, 64), toIdentity: make(chan []byte, 64), closed: make(chan struct{})}
}

func (p *pipedSocket) Read(ctx context.Context) (websocket.MessageType, []byte, error) {
	select {
	case m := <-p.toIdentity:
		return websocket.MessageText, m, nil
	case <-p.closed:
		return 0, nil, io.EOF
	case <-ctx.Done():
		return 0, nil, ctx.Err()
	}
}

func (p *pipedSocket) Write(ctx context.Context, _ websocket.MessageType, m []byte) error {
	if !p.writesAfterClose {
		select {
		case <-p.closed:
			return io.ErrClosedPipe
		default:
		}
	}
	select {
	case p.toPage <- append([]byte(nil), m...):
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (p *pipedSocket) Close(websocket.StatusCode, string) error { return p.CloseNow() }
func (p *pipedSocket) CloseNow() error                          { p.once.Do(func() { close(p.closed) }); return nil }

// page reads the next setup message the identity wrote on the socket.
func (p *pipedSocket) page(t *testing.T, within time.Duration) (setupMessage, bool) {
	t.Helper()
	select {
	case raw := <-p.toPage:
		var m setupMessage
		if err := json.Unmarshal(raw, &m); err != nil {
			t.Fatalf("the identity wrote what is not a setup message: %q", raw)
		}
		return m, true
	case <-time.After(within):
		return setupMessage{}, false
	}
}

func (p *pipedSocket) say(t *testing.T, m setupMessage) {
	t.Helper()
	raw, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	p.toIdentity <- raw
}

var channelTest = ChannelSettings{WaitMS: 20000, ReadyAllowanceMS: 2000, Sessions: 4, QueueBytes: 1 << 20, StallMS: 5000, ICEServersNamed: true}

// unaskedServers names a session server for a test whose page makes no
// offer: a channel is offered on any machine (chooseCarrier), and the
// server is never asked.
var unaskedServers = []ICEServer{{URLs: []string{"stun:stun.example.test:3478"}}}

// pageReady is the page's answer to the identity's ready on the channel.
const pageReady = `{"type":"transport_ready","carrier":"channel"}`

// dialed is a page that offered a channel to an identity, and what came of it.
type dialed struct {
	socket       *pipedSocket
	page         *webrtc.PeerConnection
	dc           *webrtc.DataChannel
	fromIdentity chan []byte  // the pieces the identity sent on the channel
	setup        setupMessage // the identity's first message on the socket
	answer       string       // the identity's answer to the page's offer
	chosen       carrier
	alive        bool
}

// dialChannel plays the page against negotiateCarrier, with two real peers in
// one process: a peer connection of its own, one channel, an offer, and the
// candidates passed each way on the socket. prepare, when given, adds to the
// page's peer connection before its offer is made. When the identity says
// ready on the channel the page sends answer back on it: pageReady, as the
// shipped page does; nothing, when answer is empty; or what a page from
// before the round trip sends, its session's first message. It returns once
// a carrier is chosen.
func dialChannel(ctx context.Context, t *testing.T, s *Server, set ChannelSettings, answer string, prepare func(page *webrtc.PeerConnection)) *dialed {
	t.Helper()
	return dialChannelWith(ctx, t, s, set, webrtc.Configuration{}, answer, prepare)
}

// dialChannelWith is dialChannel with the page's peer connection made from
// pageConfig: its servers, and whether it may use only a relayed path.
func dialChannelWith(ctx context.Context, t *testing.T, s *Server, set ChannelSettings, pageConfig webrtc.Configuration, answer string, prepare func(page *webrtc.PeerConnection)) *dialed {
	t.Helper()
	return dialChannelThrough(ctx, t, s, set, webrtc.NewAPI(), pageConfig, answer, prepare)
}

// dialChannelThrough is dialChannelWith with the page's peer connection made
// by api, whose settings are the page's own. The identity is named
// carriedServers beside the set's own servers, so the page finds it at its
// server-reflexive address.
func dialChannelThrough(ctx context.Context, t *testing.T, s *Server, set ChannelSettings, api *webrtc.API, pageConfig webrtc.Configuration, answer string, prepare func(page *webrtc.PeerConnection)) *dialed {
	t.Helper()
	d := &dialed{socket: newPipedSocket(), fromIdentity: make(chan []byte, 64)}
	set.ICEServers = append(slices.Clone(set.ICEServers), carriedServers(t)...)
	type result struct {
		c     carrier
		alive bool
	}
	chosen := make(chan result, 1)
	go func() {
		c, alive := s.negotiateCarrier(ctx, d.socket, set)
		chosen <- result{c, alive}
	}()
	var ok bool
	if d.setup, ok = d.socket.page(t, 5*time.Second); !ok {
		t.Fatal("the identity did not open with the setup")
	}
	var err error
	if d.page, err = api.NewPeerConnection(pageConfig); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.page.Close() })
	if d.dc, err = d.page.CreateDataChannel("dashboard", nil); err != nil {
		t.Fatal(err)
	}
	var answeredOnce sync.Once
	d.dc.OnMessage(func(m webrtc.DataChannelMessage) {
		if answer != "" && strings.Contains(string(m.Data), `"transport_ready"`) {
			answeredOnce.Do(func() { _ = d.dc.Send(append([]byte{pieceLast}, answer...)) })
		}
		d.fromIdentity <- m.Data
	})
	if prepare != nil {
		prepare(d.page)
	}
	var candidatesMu sync.Mutex
	var heldCandidates []setupMessage
	offered := false
	d.page.OnICECandidate(func(c *webrtc.ICECandidate) {
		if c == nil {
			return
		}
		init := c.ToJSON()
		m := setupMessage{Type: "transport_candidate", Candidate: init.Candidate, SDPMid: init.SDPMid, SDPMLineIndex: init.SDPMLineIndex}
		candidatesMu.Lock()
		defer candidatesMu.Unlock()
		if !offered {
			heldCandidates = append(heldCandidates, m)
			return
		}
		d.socket.say(t, m)
	})
	offer, err := d.page.CreateOffer(nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := d.page.SetLocalDescription(offer); err != nil {
		t.Fatal(err)
	}
	candidatesMu.Lock()
	d.socket.say(t, setupMessage{Type: "transport_offer", SDP: offer.SDP})
	offered = true
	for _, m := range heldCandidates {
		d.socket.say(t, m)
	}
	heldCandidates = nil
	candidatesMu.Unlock()
	var early []webrtc.ICECandidateInit // the identity's candidates that came before its answer
	for d.answer == "" {
		m, ok := d.socket.page(t, 10*time.Second)
		if !ok {
			t.Fatal("the identity did not answer the offer")
		}
		switch m.Type {
		case "transport_answer":
			if err := d.page.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeAnswer, SDP: m.SDP}); err != nil {
				t.Fatal(err)
			}
			d.answer = m.SDP
		case "transport_candidate":
			early = append(early, webrtc.ICECandidateInit{Candidate: m.Candidate, SDPMid: m.SDPMid, SDPMLineIndex: m.SDPMLineIndex})
		}
	}
	for _, c := range early {
		_ = d.page.AddICECandidate(c)
	}
	go func() {
		for {
			// Candidates may trail the answer on a loaded host: keep
			// reading for the whole wait the identity itself allows.
			m, ok := d.socket.page(t, time.Duration(set.WaitMS+set.ReadyAllowanceMS)*time.Millisecond)
			if !ok {
				return
			}
			if m.Type == "transport_candidate" {
				_ = d.page.AddICECandidate(webrtc.ICECandidateInit{Candidate: m.Candidate, SDPMid: m.SDPMid, SDPMLineIndex: m.SDPMLineIndex})
			}
		}
	}()
	// The identity waits for the channel as long as the setting it was given
	// says (WaitMS and ReadyAllowanceMS), so the page waits that long and a
	// little more before it calls the choice missing. A fixed 15 s here was
	// shorter than channelTest's own 22 s, and a loaded gate failed it on a
	// channel the identity would still have taken.
	wait := time.Duration(set.WaitMS+set.ReadyAllowanceMS)*time.Millisecond + 5*time.Second
	select {
	case got := <-chosen:
		d.chosen, d.alive = got.c, got.alive
	case <-time.After(wait):
		t.Fatalf("no carrier was chosen in %v", wait)
	}
	return d
}

func TestTheChannelOpensAndCarriesTheSession(t *testing.T) {
	s := New("127.0.0.1", 0, &WSHandler{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	d := dialChannel(ctx, t, s, channelTest.held(), pageReady, nil)
	if want := channelTest.WaitMS + channelTest.ReadyAllowanceMS; d.setup.Type != "transport_setup" || d.setup.ReadyWithinMS != want {
		t.Fatalf("the identity did not open with the setup and the time to wait: %+v", d.setup)
	}
	if !strings.Contains(d.answer, "a=fingerprint:") {
		t.Fatal("the answer carries no certificate fingerprint for the page to verify")
	}
	socket, page, dc, fromIdentity := d.socket, d.page, d.dc, d.fromIdentity
	on, isChannel := d.chosen.(*channelCarrier)
	if !d.alive || !isChannel {
		t.Fatalf("with a path between the two, the session was given %T (alive %v), want the channel", d.chosen, d.alive)
	}
	defer on.CloseNow()
	select {
	case <-socket.closed:
	case <-time.After(5 * time.Second):
		t.Fatal("the channel carries the session and the socket was not let go")
	}
	join, _, _ := framed(16 << 10)
	next := func() string {
		t.Helper()
		for {
			select {
			case p := <-fromIdentity:
				join.in <- p
				if p[0]&pieceLast != 0 {
					_, whole, err := join.Read(context.Background())
					if err != nil {
						t.Fatal(err)
					}
					return string(whole)
				}
			case <-time.After(10 * time.Second):
				t.Fatal("nothing arrived on the channel")
			}
		}
	}
	if first := next(); !strings.Contains(first, `"transport_ready"`) || !strings.Contains(first, `"channel"`) {
		t.Fatalf("the first message on the channel is %q, want the identity saying the session is ready there", first)
	}
	// A long message each way, whole.
	long := strings.Repeat("the session's own words. ", 4000)
	wctx, wcancel := context.WithTimeout(ctx, writeWait)
	defer wcancel()
	if err := on.Write(wctx, websocket.MessageText, []byte(long)); err != nil {
		t.Fatal(err)
	}
	if arrived := next(); arrived != long {
		t.Fatalf("a %d-byte message arrived on the page as %d bytes", len(long), len(arrived))
	}
	page2identity, out, _ := framed(16 << 10)
	if err := page2identity.Write(ctx, websocket.MessageBinary, []byte(long)); err != nil {
		t.Fatal(err)
	}
	for _, p := range out.taken() {
		if err := dc.Send(p); err != nil {
			t.Fatal(err)
		}
	}
	rctx, rcancel := context.WithTimeout(ctx, 10*time.Second)
	defer rcancel()
	if typ, arrived, err := on.Read(rctx); err != nil || typ != websocket.MessageBinary || string(arrived) != long {
		t.Fatalf("a %d-byte binary message from the page arrived as type %v, %d bytes, %v", len(long), typ, len(arrived), err)
	}
	// The path ends: the session reads the end, and does not wait.
	_ = page.Close()
	ectx, ecancel := context.WithTimeout(ctx, 40*time.Second)
	defer ecancel()
	if _, _, err := on.Read(ectx); err == nil || errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("the page went away and the session read %v, want the carrier's end", err)
	}
}

func TestTheIdentityWritesDownWhatCarriesASessionAndItsPath(t *testing.T) {
	said := logsink.CaptureForTest(t)
	s := New("127.0.0.1", 0, &WSHandler{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	waitFor := func(what string, re *regexp.Regexp) {
		t.Helper()
		for deadline := time.Now().Add(10 * time.Second); !re.MatchString(said.String()); {
			if time.Now().After(deadline) {
				t.Fatalf("no line says %s:\n%s", what, said.String())
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
	d := dialChannel(ctx, t, s, channelTest.held(), pageReady, nil)
	on, isChannel := d.chosen.(*channelCarrier)
	if !d.alive || !isChannel {
		t.Fatalf("with a path between the two, the session was given %T, want the channel", d.chosen)
	}
	waitFor("the session is carried on the channel", regexp.MustCompile(`dashboard\.decision: channel \d+: the session is carried on the channel, open \d+ ms and answered \d+ ms after the setup was sent`))
	waitFor("the peer connection connected", regexp.MustCompile(`dashboard\.session: channel \d+: the peer connection is connected \(ICE connected\)`))
	waitFor("the path in use", regexp.MustCompile(`dashboard\.session: channel \d+: the path in use is (host|srflx|prflx|relay) (udp|tcp) (IPv4|IPv6|a name) here and (host|srflx|prflx|relay) (udp|tcp) (IPv4|IPv6|a name) at the page`))
	var addresses []string
	for _, st := range d.page.GetStats() {
		if c, ok := st.(webrtc.ICECandidateStats); ok && c.IP != "" {
			addresses = append(addresses, c.IP)
		}
	}
	_ = on.CloseNow()
	waitFor("the peer connection closed", regexp.MustCompile(`dashboard\.session: channel \d+: the peer connection is closed`))
	if len(addresses) == 0 {
		t.Fatal("the page gathered no address, so nothing here shows none is written")
	}
	for _, line := range refusalLines(said.String(), "channel ") {
		for _, a := range addresses {
			if strings.Contains(line, a) {
				t.Fatalf("a channel's line carries the address %s: %q", a, line)
			}
		}
	}

	// With no channel in time: the socket, and why, and no credential.
	said.Reset()
	const credential = "the-servers-credential"
	quick := ChannelSettings{WaitMS: 500, ReadyAllowanceMS: 100, ICEServersNamed: true,
		ICEServers: []ICEServer{{URLs: []string{"turn:turn.example.test:3478"}, Username: "the-user", Credential: credential}}}.held()
	socket := newPipedSocket()
	go func() { _, _ = s.negotiateCarrier(ctx, socket, quick) }()
	waitFor("the socket was chosen at the wait", regexp.MustCompile(`dashboard\.decision: channel \d+: the session is carried on its socket: no channel opened within dashboard\.direct_wait_ms, 500 ms \(peer connection \w+, ICE \w+\)`))
	if said.Contains(credential) || said.Contains("the-user") {
		t.Fatalf("a server's name or credential was written down:\n%s", said.String())
	}
}

func TestAChannelThatOpensAndCarriesNoAnswerLeavesTheSessionOnItsSocket(t *testing.T) {
	s := New("127.0.0.1", 0, &WSHandler{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	set := channelTest
	set.WaitMS = 8000
	d := dialChannel(ctx, t, s, set.held(), "", nil)
	select {
	case first := <-d.fromIdentity:
		if !strings.Contains(string(first), `"transport_ready"`) {
			t.Fatalf("the identity's first message on the channel is %q, want ready", first)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the channel never opened in the wait, so nothing here asks what an open channel with no answer is given")
	}
	if _, pumped := d.chosen.(*pumpedSocket); !d.alive || !pumped {
		t.Fatalf("a channel that opened and never carried the page's answer was given the session (%T, alive %v), want the socket", d.chosen, d.alive)
	}
	select {
	case <-d.socket.closed:
		t.Fatal("the socket the session is left on was closed")
	default:
	}
	for deadline := time.Now().Add(10 * time.Second); d.dc.ReadyState() != webrtc.DataChannelStateClosed; {
		if time.Now().After(deadline) {
			t.Fatalf("the channel that carried no answer was left %s", d.dc.ReadyState())
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestAPageFromBeforeTheRoundTripIsToldToLoadAgain(t *testing.T) {
	s := New("127.0.0.1", 0, &WSHandler{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	d := dialChannel(ctx, t, s, channelTest.held(), `{"type":"query","query":"config","request_id":"first"}`, nil)
	if d.alive || d.chosen != nil {
		t.Fatalf("a page that began its session on the channel without answering was given %T (alive %v), want nothing", d.chosen, d.alive)
	}
	select {
	case <-d.socket.closed:
	case <-time.After(5 * time.Second):
		t.Fatal("the socket of a page from before the round trip was left open")
	}
	for deadline := time.After(10 * time.Second); ; {
		select {
		case p := <-d.fromIdentity:
			if strings.Contains(string(p), `"overlay_changed"`) {
				return
			}
		case <-deadline:
			t.Fatal("the page was not sent the frame that makes an earlier page load again")
		}
	}
}

func TestAMediaLineAPageOffersIsRejectedAndTheChannelStillOpens(t *testing.T) {
	s := New("127.0.0.1", 0, &WSHandler{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	d := dialChannel(ctx, t, s, channelTest.held(), pageReady, func(page *webrtc.PeerConnection) {
		for _, kind := range []webrtc.RTPCodecType{webrtc.RTPCodecTypeAudio, webrtc.RTPCodecTypeVideo} {
			if _, err := page.AddTransceiverFromKind(kind, webrtc.RTPTransceiverInit{Direction: webrtc.RTPTransceiverDirectionRecvonly}); err != nil {
				t.Fatal(err)
			}
		}
	})
	on, isChannel := d.chosen.(*channelCarrier)
	if !d.alive || !isChannel {
		t.Fatalf("a page that offered media lines beside its channel was given %T (alive %v), want the channel", d.chosen, d.alive)
	}
	defer on.CloseNow()
	for _, kind := range []string{"audio", "video"} {
		if !strings.Contains(d.answer, "\nm="+kind+" 0 ") {
			t.Fatalf("the answer does not reject the %s line the page offered:\n%s", kind, d.answer)
		}
	}
}

// answerAndGather has pc answer a page's offer of a channel, as the identity
// does, and waits until pc has gathered every candidate it will.
func answerAndGather(t *testing.T, pc *webrtc.PeerConnection) {
	t.Helper()
	page, err := webrtc.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = page.Close() })
	if _, err := page.CreateDataChannel("dashboard", nil); err != nil {
		t.Fatal(err)
	}
	offer, err := page.CreateOffer(nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := page.SetLocalDescription(offer); err != nil {
		t.Fatal(err)
	}
	gathered := webrtc.GatheringCompletePromise(pc)
	if _, err := answerOffer(pc, offer.SDP); err != nil {
		t.Fatal(err)
	}
	select {
	case <-gathered:
	case <-time.After(10 * time.Second):
		t.Fatal("the answering peer did not finish gathering")
	}
}

func TestTheIdentityGathersNoLinkLocalAddressForAChannel(t *testing.T) {
	linkLocal := func(pc *webrtc.PeerConnection) []string {
		var found []string
		for _, st := range pc.GetStats() {
			c, ok := st.(webrtc.ICECandidateStats)
			if !ok || c.Type != webrtc.StatsTypeLocalCandidate {
				continue
			}
			if a, err := netip.ParseAddr(c.IP); err == nil && a.Is6() && a.IsLinkLocalUnicast() {
				found = append(found, c.IP)
			}
		}
		return found
	}

	plain, err := webrtc.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = plain.Close() })
	answerAndGather(t, plain)
	if len(linkLocal(plain)) == 0 {
		t.Skip("this machine has no IPv6 link-local address for the library to gather, so nothing here tells the identity's peer from the library's defaults")
	}

	identity, err := newPeer(nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = identity.Close() })
	answerAndGather(t, identity)
	if got := linkLocal(identity); len(got) != 0 {
		t.Fatalf("the identity gathered %d IPv6 link-local address(es) for a channel, %v: sockets that send checks no page can answer where they were sent", len(got), got)
	}
	if !gatheredForChannel(net.ParseIP("169.254.1.1")) || gatheredForChannel(net.ParseIP("fe80::1")) {
		t.Fatal("the rule does not keep an IPv4 link-local address and refuse an IPv6 one")
	}
}

func TestTheIdentityAsksATURNServerForOneAllocation(t *testing.T) {
	addresses := 0
	ifaces, _ := net.Interfaces()
	for _, ifc := range ifaces {
		if ifc.Flags&net.FlagUp == 0 || ifc.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, _ := ifc.Addrs()
		for _, a := range addrs {
			if ipn, ok := a.(*net.IPNet); ok && ipn.IP.To4() != nil {
				addresses++
			}
		}
	}
	if addresses < 2 {
		t.Skip("this machine has fewer than two IPv4 addresses outside loopback, so one allocation for each would also be one")
	}
	const realm, user, pass = "aii.example.test", "the-user", "the-credential"
	udp, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var allocations atomic.Int64
	key := turn.GenerateAuthKey(user, realm, pass)
	server, err := turn.NewServer(turn.ServerConfig{
		Realm: realm,
		AuthHandler: func(ra *turn.RequestAttributes) (string, []byte, bool) {
			return ra.Username, key, ra.Username == user
		},
		EventHandler: turn.EventHandler{OnAllocationCreated: func(_, _ net.Addr, _, _, _ string, _ net.Addr, _ int) { allocations.Add(1) }},
		PacketConnConfigs: []turn.PacketConnConfig{{PacketConn: udp,
			RelayAddressGenerator: &turn.RelayAddressGeneratorStatic{RelayAddress: net.ParseIP("127.0.0.1"), Address: "127.0.0.1"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Close() })

	identity, err := newPeer([]ICEServer{{URLs: []string{"turn:" + udp.LocalAddr().String() + "?transport=udp"}, Username: user, Credential: pass}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = identity.Close() })
	page, err := webrtc.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = page.Close() })
	if _, err := page.CreateDataChannel("dashboard", nil); err != nil {
		t.Fatal(err)
	}
	offer, err := page.CreateOffer(nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := page.SetLocalDescription(offer); err != nil {
		t.Fatal(err)
	}
	gathered := webrtc.GatheringCompletePromise(identity)
	if _, err := answerOffer(identity, offer.SDP); err != nil {
		t.Fatal(err)
	}
	select {
	case <-gathered:
	case <-time.After(15 * time.Second):
		t.Fatal("the identity did not finish gathering")
	}
	if n := allocations.Load(); n != 1 {
		t.Fatalf("the identity asked the TURN server for %d allocations on a machine with %d IPv4 addresses, want one", n, addresses)
	}
}

func TestAChannelGathersOnNoPrivateAddress(t *testing.T) {
	for _, a := range []string{"10.0.0.1", "172.16.0.1", "192.168.1.1", "fd00::1", "fe80::1"} {
		if gatheredForChannel(net.ParseIP(a)) {
			t.Fatalf("%s is gathered on for a channel: it is private, or IPv6 link-local", a)
		}
	}
	for _, a := range []string{"203.0.113.1", "2001:db8::1"} {
		if !gatheredForChannel(net.ParseIP(a)) {
			t.Fatalf("%s is not gathered on for a channel: it is public", a)
		}
	}
}

func TestTheIdentitysChannelOffersNoPrivateHostCandidate(t *testing.T) {
	const upAndRunning = net.FlagUp | net.FlagRunning
	private := 0
	ifaces, _ := net.Interfaces()
	for _, ifc := range ifaces {
		addrs, _ := ifc.Addrs()
		for _, a := range addrs {
			if ipn, ok := a.(*net.IPNet); ok && ifc.Flags&upAndRunning == upAndRunning && ipn.IP.IsPrivate() {
				private++
			}
		}
	}
	if private == 0 {
		t.Skip("this machine has no private address on an interface that is up and running, so there is none for the identity to leave out")
	}
	identity, err := newPeer(nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = identity.Close() })
	answerAndGather(t, identity)
	var found []string
	for _, st := range identity.GetStats() {
		c, ok := st.(webrtc.ICECandidateStats)
		if ok && c.Type == webrtc.StatsTypeLocalCandidate && c.CandidateType == webrtc.ICECandidateTypeHost && net.ParseIP(c.IP).IsPrivate() {
			found = append(found, c.IP)
		}
	}
	if len(found) != 0 {
		t.Fatalf("the identity's channel peer gathered %d host candidate(s) on a private address, %v: a page through a relay cannot know it shares that network, and a phone forbidden local addresses loses the socket it sends to them from", len(found), found)
	}
}

func TestAMachineWithNoAddressAChannelOffersIsSeenAsOne(t *testing.T) {
	iface := func(flags net.Flags, ip string) *transport.Interface {
		ifc := transport.NewInterface(net.Interface{Flags: flags})
		ifc.AddAddress(&net.IPNet{IP: net.ParseIP(ip), Mask: net.CIDRMask(24, 32)})
		return ifc
	}
	reading := func(more ...*transport.Interface) []*transport.Interface {
		ifaces := append([]*transport.Interface{
			iface(net.FlagUp|net.FlagRunning, "192.168.1.10"),
			iface(net.FlagUp|net.FlagRunning, "fd00::10"),
			iface(net.FlagUp|net.FlagRunning|net.FlagLoopback, "127.0.0.1"),
			iface(net.FlagBroadcast, "203.0.113.10"),
		}, more...)
		keptForChannel(ifaces)
		return ifaces
	}
	if channelOffersAnAddress(reading()) {
		t.Fatal("a machine with no address but private, loopback and down ones is read as offering one: with no session server, every page would wait out the setup for a channel that cannot open")
	}
	if !channelOffersAnAddress(reading(iface(net.FlagUp|net.FlagRunning, "203.0.113.11"))) {
		t.Fatal("a machine with a public address on an interface that is up is read as offering none")
	}
}

func TestWithNoChannelInTimeTheSocketCarriesTheSession(t *testing.T) {
	s := New("127.0.0.1", 0, &WSHandler{})
	quick := ChannelSettings{WaitMS: 500, ReadyAllowanceMS: 100, ICEServersNamed: true}.held()
	socket := newPipedSocket()
	began := time.Now()
	type result struct {
		c     carrier
		alive bool
	}
	chosen := make(chan result, 1)
	go func() {
		c, alive := s.negotiateCarrier(context.Background(), socket, quick)
		chosen <- result{c, alive}
	}()
	if m, ok := socket.page(t, 5*time.Second); !ok || m.Type != "transport_setup" {
		t.Fatalf("no setup: %+v", m)
	}
	socket.say(t, setupMessage{Type: "chat"}) // not a setup message: not acted on, and not lost to the session either way
	var ready setupMessage
	for ready.Type != "transport_ready" {
		m, ok := socket.page(t, 5*time.Second)
		if !ok {
			t.Fatal("the wait ran out and the page was not told which carrier it has")
		}
		ready = m
	}
	if waited := time.Since(began); ready.Carrier != "socket" || waited < 500*time.Millisecond || waited > 3*time.Second {
		t.Fatalf("ready on %q after %s, want the socket, at the wait", ready.Carrier, waited)
	}
	got := <-chosen
	if _, pumped := got.c.(*pumpedSocket); !got.alive || !pumped {
		t.Fatalf("with no channel the session was given %T (alive %v), want its socket", got.c, got.alive)
	}
	socket.say(t, setupMessage{Type: "after ready"})
	rctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, raw, err := got.c.Read(rctx); err != nil || !strings.Contains(string(raw), "after ready") {
		t.Fatalf("the session did not read its socket after the choice: %q, %v", raw, err)
	}

	gone := newPipedSocket()
	gone.writesAfterClose = true
	go func() {
		c, alive := s.negotiateCarrier(context.Background(), gone, quick)
		chosen <- result{c, alive}
	}()
	if _, ok := gone.page(t, 5*time.Second); !ok {
		t.Fatal("no setup on the second socket")
	}
	_ = gone.CloseNow()
	select {
	case got = <-chosen:
		if got.alive || got.c != nil {
			t.Fatalf("with the socket gone and no channel, the session was given %T", got.c)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a session with nothing to carry it was never ended")
	}
}

func TestChannelSettingsAreHeldToTheirBounds(t *testing.T) {
	if got := (ChannelSettings{}).held(); got.WaitMS != DefaultDirectWaitMS || got.ReadyAllowanceMS != DefaultDirectReadyAllowanceMS ||
		got.Sessions != DefaultChannelSessions || got.QueueBytes != DefaultChannelQueueBytes || got.StallMS != DefaultChannelStallMS {
		t.Fatalf("unset, the settings are %+v, want every default", got)
	}
	if got := (ChannelSettings{WaitMS: 1, ReadyAllowanceMS: 1, Sessions: 100000, QueueBytes: 1, StallMS: 9999999}).held(); got.WaitMS != MinDirectWaitMS ||
		got.ReadyAllowanceMS != MinDirectReadyAllowanceMS || got.Sessions != MaxChannelSessions || got.QueueBytes != MinChannelQueueBytes || got.StallMS != MaxChannelStallMS {
		t.Fatalf("out of bounds, the settings are %+v, want each at its nearer bound", got)
	}
	if err := CheckICEServers([]ICEServer{{URLs: []string{"stun:turn.example.test:3478"}}, {URLs: []string{"turn:turn.example.test:3478?transport=udp", "turns:[2001:db8::2]:443"}, Username: "u", Credential: "c"}}); err != nil {
		t.Fatalf("well-formed servers were refused: %v", err)
	}
	for _, bad := range [][]ICEServer{{{}}, {{URLs: []string{"https://turn.example.test"}}}, {{URLs: []string{"turn:user:secret@turn.example.test"}}}, {{URLs: []string{"stun:"}}}} {
		if err := CheckICEServers(bad); err == nil || !strings.Contains(err.Error(), "dashboard.ice_servers") {
			t.Errorf("%+v was taken as a list of session servers: %v", bad, err)
		}
	}
	if statedMaxMessageSize("v=0\r\na=max-message-size:1200\r\n") != 1200 || statedMaxMessageSize("v=0\r\n") != 0 {
		t.Error("the page's stated largest message was not read from its offer")
	}
}

// carriedSocket opens the page's socket through a relay-carried session, as
// a browser that reached the identity's public name does: given a cookie, it
// sends first the page key a sign-in on s answers, as that page would.
func carriedSocket(t *testing.T, s *Server, name string, roots *x509.CertPool, query string, cookie ...string) *websocket.Conn {
	t.Helper()
	header := http.Header{"Origin": []string{"https://" + name}}
	if len(cookie) == 1 {
		header.Set("Cookie", tlsCookieName("443")+"="+cookie[0]) // a carried request is TLS to the bare name
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	client := &http.Client{Transport: &http.Transport{
		DialTLSContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			c := tls.Client(carriedSession(t, s), &tls.Config{ServerName: name, RootCAs: roots, MinVersion: tls.VersionTLS12})
			return c, c.HandshakeContext(ctx)
		},
	}}
	conn, _, err := websocket.Dial(ctx, "wss://"+name+"/ws?interaction_version=1"+query, &websocket.DialOptions{
		HTTPClient: client, HTTPHeader: header})
	if err != nil {
		t.Fatalf("the page's socket through a carried session: %v", err)
	}
	t.Cleanup(func() { conn.CloseNow() })
	if len(cookie) == 1 {
		if err := sendHello(ctx, conn, pageKeyFor(s)); err != nil {
			t.Fatalf("the page key: %v", err)
		}
	}
	return conn
}

// answered sends the page's own liveness question and returns the type of
// the next message: a session that has begun answers it.
func answered(t *testing.T, conn *websocket.Conn) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := conn.Write(ctx, websocket.MessageText, []byte(`{"type":"probe","request_id":"fixture"}`)); err != nil {
		t.Fatalf("the page could not ask: %v", err)
	}
	return firstType(t, conn)
}

func firstType(t *testing.T, conn *websocket.Conn) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, raw, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("nothing arrived on the socket: %v", err)
	}
	var m struct {
		Type string `json:"type"`
	}
	_ = json.Unmarshal(raw, &m)
	return m.Type
}

func TestAChannelIsOfferedOnlyThroughARelayToAPageThatAsked(t *testing.T) {
	const name = "ui.x.example.test"
	settings := ChannelSettings{WaitMS: 500, ReadyAllowanceMS: 100, ICEServers: unaskedServers, ICEServersNamed: true}
	var off atomic.Bool
	s := New("127.0.0.1", 0, &WSHandler{Channels: func() ChannelSettings {
		set := settings
		set.Off = off.Load()
		return set
	}})
	addr, err := s.Start("")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Shutdown(context.Background()) })
	cert := selfSigned(t, name)
	s.SetPublicCertificate(name, func() *tls.Certificate { return cert })
	s.AllowHost(name)
	roots := x509.NewCertPool()
	roots.AddCert(cert.Leaf)

	asked := carriedSocket(t, s, name, roots, "&transport=1")
	began := time.Now()
	if got := firstType(t, asked); got != "transport_setup" {
		t.Fatalf("through a relay, a page that asked was first told %q, want the setup", got)
	}
	// A question asked during the setup is not a setup message: it is not
	// acted on, and the next thing the page hears is which carrier it has.
	if got := answered(t, asked); got != "transport_ready" {
		t.Fatalf("before its carrier was chosen the page was sent %q: a session was begun during setup", got)
	}
	if waited := time.Since(began); waited < 400*time.Millisecond {
		t.Fatalf("ready came after %s, before the wait was up and with no channel", waited)
	}
	if got := answered(t, asked); got == "" || strings.HasPrefix(got, "transport_") {
		t.Fatalf("after ready on the socket the session did not begin there: its question was answered with %q", got)
	}
	if n := s.channelSessions.Load(); n != 0 {
		t.Fatalf("a session that stayed on its socket still counts as %d channel session(s)", n)
	}

	if got := answered(t, carriedSocket(t, s, name, roots, "")); strings.HasPrefix(got, "transport_") || got == "" {
		t.Fatalf("a page that did not ask was told %q", got)
	}
	direct, _, err := websocket.Dial(context.Background(), "ws://"+addr+"/ws?interaction_version=1&transport=1", &websocket.DialOptions{HTTPHeader: http.Header{"Origin": []string{"http://" + addr}}})
	if err != nil {
		t.Fatal(err)
	}
	defer direct.CloseNow()
	if got := answered(t, direct); strings.HasPrefix(got, "transport_") || got == "" {
		t.Fatalf("a page on the machine's own address was told %q: its socket is already direct", got)
	}
	off.Store(true)
	if got := answered(t, carriedSocket(t, s, name, roots, "&transport=1")); strings.HasPrefix(got, "transport_") || got == "" {
		t.Fatalf("with dashboard.channels false a page was told %q", got)
	}
}

func TestTheServersARelayNamesAreHeldToTheirShape(t *testing.T) {
	const name = "ui.x.example.test"
	good := ICEServer{URLs: []string{"turn:turn.example.test:3478?transport=udp"}, Username: "1700000000:x", Credential: "fixture"}
	s := New("127.0.0.1", 0, &WSHandler{
		Channels: func() ChannelSettings { return ChannelSettings{WaitMS: 500, ReadyAllowanceMS: 100} },
		SessionServers: func() ([]ICEServer, time.Time) {
			return []ICEServer{
				{URLs: []string{"http://collector.example.test/"}},
				good,
				{URLs: []string{"turn:name:password@turn.example.test:3478"}},
				{},
				{URLs: []string{"stun:stun.example.test:3478", "file:///etc/passwd"}},
			}, time.Time{}
		},
	})
	if _, err := s.Start(""); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Shutdown(context.Background()) })
	cert := selfSigned(t, name)
	s.SetPublicCertificate(name, func() *tls.Certificate { return cert })
	s.AllowHost(name)
	roots := x509.NewCertPool()
	roots.AddCert(cert.Leaf)
	conn := carriedSocket(t, s, name, roots, "&transport=1")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, raw, err := conn.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var setup setupMessage
	if err := json.Unmarshal(raw, &setup); err != nil || setup.Type != "transport_setup" {
		t.Fatalf("a page through a relay was first told %q: %v", raw, err)
	}
	if len(setup.ICEServers) != 1 || len(setup.ICEServers[0].URLs) != 1 || setup.ICEServers[0].URLs[0] != good.URLs[0] || setup.ICEServers[0].Credential != good.Credential {
		t.Fatalf("the page was told %d servers, want the one well-formed server the relay named and none of the four others: %+v", len(setup.ICEServers), setup.ICEServers)
	}
}

func TestATokenRotatedDuringASetupAdmitsNothing(t *testing.T) {
	const name = "ui.x.example.test"
	const token = "fixture-token-long-enough-to-be-one"
	s := New("127.0.0.1", 0, &WSHandler{
		Channels: func() ChannelSettings {
			return ChannelSettings{WaitMS: 800, ReadyAllowanceMS: 100, ICEServers: unaskedServers, ICEServersNamed: true}
		},
		GetConfig: func() *ConfigState { return &ConfigState{} },
	})
	s.SetAccessToken(true, token)
	if _, err := s.Start(""); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Shutdown(context.Background()) })
	cert := selfSigned(t, name)
	s.SetPublicCertificate(name, func() *tls.Certificate { return cert })
	s.AllowHost(name)
	roots := x509.NewCertPool()
	roots.AddCert(cert.Leaf)
	sum := sha256.Sum256([]byte(token))
	conn := carriedSocket(t, s, name, roots, "&transport=1", hex.EncodeToString(sum[:]))
	if got := firstType(t, conn); got != "transport_setup" {
		t.Fatalf("a signed-in page through a relay was first told %q, want the setup", got)
	}
	// The rotation lands while the attempt is waiting.
	s.SetAccessToken(true, "another-fixture-token-long-enough")
	s.closeAdmitted()
	deadline := time.Now().Add(10 * time.Second)
	ctx, cancel := context.WithDeadline(context.Background(), deadline)
	defer cancel()
	asked := false
	for {
		_, raw, err := conn.Read(ctx)
		if err != nil {
			// The host closed it. A read that ended only because this
			// test stopped waiting is not that, whatever its error says.
			if !time.Now().Before(deadline) {
				t.Fatal("a session whose token was rotated during setup was left open: nothing closed it in ten seconds")
			}
			return
		}
		var m struct {
			Type string `json:"type"`
		}
		_ = json.Unmarshal(raw, &m)
		if !strings.HasPrefix(m.Type, "transport_") {
			t.Fatalf("a session was begun under a token rotated during its setup: the page was sent %q", m.Type)
		}
		if m.Type == "transport_ready" && !asked {
			asked = true
			// It may already be closed: then the write fails and the next
			// read says so.
			_ = conn.Write(ctx, websocket.MessageText, []byte(`{"type":"query","query":"config","request_id":"asked"}`))
		}
	}
}

func TestAChannelIsEndedTheTimeAChannelTakesBeforeItsCredentialsExpire(t *testing.T) {
	type path = func() (*webrtc.ICECandidatePair, []string)
	pair := func(here, there webrtc.ICECandidateType, at string, relays ...string) path {
		return func() (*webrtc.ICECandidatePair, []string) {
			return &webrtc.ICECandidatePair{Local: &webrtc.ICECandidate{Typ: here, Address: "203.0.113.1"}, Remote: &webrtc.ICECandidate{Typ: there, Address: at}}, relays
		}
	}
	turn := func(urls ...string) []ICEServer {
		return []ICEServer{{URLs: urls, Username: "1700000000:x", Credential: "fixture"}}
	}
	const host, srflx, prflx, relay = webrtc.ICECandidateTypeHost, webrtc.ICECandidateTypeSrflx, webrtc.ICECandidateTypePrflx, webrtc.ICECandidateTypeRelay
	synctest.Test(t, func(t *testing.T) {
		s := &Server{lookupIP: func(ctx context.Context, network, name string) ([]netip.Addr, error) {
			switch name {
			case "turn.example.test":
				return []netip.Addr{netip.MustParseAddr("198.51.100.10")}, nil
			case "down.example.test":
				return nil, errors.New("no answer")
			}
			return net.DefaultResolver.LookupNetIP(ctx, network, name) // only IP literals reach it
		}}
		set := ChannelSettings{}.held()
		for _, c := range []struct {
			why     string
			servers []ICEServer
			path    path
		}{
			{"a relay candidate here", nil, pair(relay, host, "198.51.100.1")},
			{"a relay candidate at the page", nil, pair(srflx, relay, "198.51.100.1")},
			{"a path that cannot be read", nil, func() (*webrtc.ICECandidatePair, []string) { return nil, nil }},
			{"a peer-reflexive page on the identity's own relay address", nil, pair(host, prflx, "198.51.100.7", "198.51.100.7")},
			{"a peer-reflexive page on a TURN server's literal address", turn("turn:198.51.100.9:3478?transport=udp"), pair(host, prflx, "198.51.100.9")},
			{"a peer-reflexive page on a TURN server's resolved name", turn("stun:198.51.100.2", "turns:turn.example.test:443?transport=tcp"), pair(host, prflx, "198.51.100.10")},
			{"a peer-reflexive page whose TURN server cannot be resolved", turn("turn:down.example.test"), pair(host, prflx, "198.51.100.20")},
		} {
			set.ServersExpire, set.ICEServers = time.Now().Add(12*time.Hour), c.servers
			cc, _, gone := framed(16 << 10)
			defer cc.CloseNow() // a failure leaves no goroutine in the bubble
			s.endBeforeExpiry(1, cc, set, c.path)
			time.Sleep(12*time.Hour - set.readyWithin() - time.Millisecond)
			synctest.Wait()
			select {
			case <-gone:
				t.Fatalf("a channel with %s was ended before the time a channel is given to be made ahead of its credentials' expiry", c.why)
			default:
			}
			time.Sleep(time.Millisecond)
			synctest.Wait()
			select {
			case <-gone:
			default:
				t.Fatalf("a channel with %s was still open %v before its credentials' expiry, the time the next channel is given to be made", c.why, set.readyWithin())
			}
		}

		for _, c := range []struct {
			why     string
			expire  time.Time
			servers []ICEServer
			path    path
		}{
			{"a channel on a host and a server-reflexive end", time.Now().Add(time.Hour), turn("turn:down.example.test"), pair(host, srflx, "198.51.100.20")},
			{"a channel on a peer-reflexive end with no TURN server", time.Now().Add(time.Hour), turn("stun:down.example.test"), pair(host, prflx, "198.51.100.20")},
			{"a channel on a peer-reflexive end on no relay address", time.Now().Add(time.Hour), turn("turn:198.51.100.9", "turn:turn.example.test"), pair(host, prflx, "198.51.100.20", "198.51.100.7")},
			{"a channel whose servers stated no time", time.Time{}, nil, pair(relay, relay, "198.51.100.1")},
		} {
			cc, _, gone := framed(16 << 10)
			defer cc.CloseNow()
			set.ServersExpire, set.ICEServers = c.expire, c.servers
			s.endBeforeExpiry(2, cc, set, c.path)
			time.Sleep(30 * 24 * time.Hour)
			synctest.Wait()
			select {
			case <-gone:
				t.Fatalf("%s was ended", c.why)
			default:
			}
		}
	})
}

// A RELAYED CHANNEL ENDED FOR ITS CREDENTIALS IS ENDED AT BOTH ENDS, AND THE
// SESSION GOES ON ON A NEW ONE. Two real peers and a TURN server in the
// process, the page allowed only a relayed path: the identity's channel,
// made with a set that expires soon, ends before the expiry, the page's end
// of it closes, and the page, reconnecting as on any channel's end, is given
// a new channel (here with a set that states no time).
func TestAChannelEndedForItsCredentialsIsFollowedByANewOne(t *testing.T) {
	const realm, user, pass = "aii.example.test", "the-user", "the-credential"
	ip := machineIPv4()
	if ip == nil {
		t.Skip("this machine has no IPv4 address outside loopback on an interface that is up, for the TURN server the page's relayed path runs through")
	}
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
	servers := []ICEServer{{URLs: []string{"turn:" + udp.LocalAddr().String() + "?transport=udp"}, Username: user, Credential: pass}}
	pageConfig := webrtc.Configuration{ICETransportPolicy: webrtc.ICETransportPolicyRelay}
	for _, sv := range servers {
		pageConfig.ICEServers = append(pageConfig.ICEServers, webrtc.ICEServer{URLs: sv.URLs, Username: sv.Username, Credential: sv.Credential})
	}

	said := logsink.CaptureForTest(t)
	s := New("127.0.0.1", 0, &WSHandler{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	set := channelTest.held()
	set.ICEServers = servers
	margin := set.readyWithin()
	set.ServersExpire = time.Now().Add(margin + 2*time.Second)
	deadline := set.ServersExpire.Add(-margin)
	d := dialChannelWith(ctx, t, s, set, pageConfig, pageReady, nil)
	chosenAt := time.Now()
	on, isChannel := d.chosen.(*channelCarrier)
	if !d.alive || !isChannel {
		t.Fatalf("the session was given %T (alive %v), want the channel", d.chosen, d.alive)
	}
	defer on.CloseNow()
	pageEnd := make(chan struct{})
	var once sync.Once
	d.dc.OnClose(func() { once.Do(func() { close(pageEnd) }) })
	if d.dc.ReadyState() == webrtc.DataChannelStateClosed {
		once.Do(func() { close(pageEnd) })
	}
	// The end is due at the deadline, or at once when the setup took longer
	// than the time left before it; half the margin is slack for a loaded
	// host, counted from whichever is later.
	due := deadline
	if chosenAt.After(due) {
		due = chosenAt
	}
	rctx, rcancel := context.WithDeadline(ctx, due.Add(margin/2))
	defer rcancel()
	for {
		_, _, err := on.Read(rctx)
		if err == nil {
			continue
		}
		if errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("a relayed channel was still open %v after its end was due:\n%s", margin/2, said.String())
		}
		break
	}
	select {
	case <-pageEnd:
	case <-time.After(10 * time.Second):
		t.Fatal("the identity ended the channel and the page's end of it stayed open")
	}
	if !regexp.MustCompile(`dashboard\.session: channel \d+: the path in use is [^\n]*relay`).MatchString(said.String()) {
		t.Fatalf("the channel's path was not relayed, so this shows nothing of one:\n%s", said.String())
	}

	next := dialChannelWith(ctx, t, s, channelTest.held(), pageConfig, pageReady, nil)
	again, isChannel := next.chosen.(*channelCarrier)
	if !next.alive || !isChannel {
		t.Fatalf("the reconnected session was given %T (alive %v), want a new channel", next.chosen, next.alive)
	}
	_ = again.CloseNow()
}

// A CHANNEL ON A DIRECT PATH IS NOT ENDED WHEN ITS CREDENTIALS EXPIRE. Two
// real peers on this machine's own interfaces find a direct path; the set
// the channel was made with expires soon, and at the deadline the identity
// reads the path from the library, leaves the channel open, and says why.
func TestAChannelOnADirectPathOutlivesItsCredentials(t *testing.T) {
	said := logsink.CaptureForTest(t)
	s := New("127.0.0.1", 0, &WSHandler{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	set := channelTest.held()
	margin := set.readyWithin()
	set.ServersExpire = time.Now().Add(margin + time.Second)
	d := dialChannel(ctx, t, s, set, pageReady, nil)
	on, isChannel := d.chosen.(*channelCarrier)
	if !d.alive || !isChannel {
		t.Fatalf("the session was given %T (alive %v), want the channel", d.chosen, d.alive)
	}
	defer on.CloseNow()
	direct := regexp.MustCompile(`dashboard\.decision: channel \d+: the credentials of its session servers expire in \d+ ms and its path is direct \((host|srflx|prflx) `)
	for limit := time.Now().Add(margin / 2); !direct.MatchString(said.String()); {
		if time.Now().After(limit) {
			t.Fatalf("no line says the direct path goes on:\n%s", said.String())
		}
		time.Sleep(20 * time.Millisecond)
	}
	rctx, rcancel := context.WithTimeout(ctx, 2*time.Second)
	defer rcancel()
	if _, _, err := on.Read(rctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("a channel on a direct path read %v after its credentials' deadline, want it open", err)
	}
}

// THE TIME THE RELAY STATES RIDES WITH ITS SERVERS, AND ONLY WITH THEM.
// channelOffered keeps it for the servers it hands on; the operator's own
// servers and a set with no time have none; and a set whose time comes
// within the time a channel is given to be made is not used at all, as the
// relay client hands none past its time.
func TestTheRelaysCredentialTimeRidesWithItsServers(t *testing.T) {
	turn := ICEServer{URLs: []string{"turn:turn.example.test:3478"}, Username: "1700000000:x", Credential: "fixture"}
	var expire atomic.Pointer[time.Time]
	named := atomic.Bool{}
	s := New("127.0.0.1", 0, &WSHandler{
		Channels: func() ChannelSettings {
			return ChannelSettings{WaitMS: 3000, ReadyAllowanceMS: 1500, ICEServersNamed: named.Load()}
		},
		SessionServers: func() ([]ICEServer, time.Time) { return []ICEServer{turn}, *expire.Load() },
	})
	r := httptest.NewRequest(http.MethodGet, "/ws?transport=1", nil)
	r = r.WithContext(context.WithValue(r.Context(), carriedKey{}, "relay.example.test"))
	offered := func(at time.Time) ChannelSettings {
		t.Helper()
		expire.Store(&at)
		set, ok := s.channelOffered(r)
		if !ok {
			t.Fatal("a page through a relay that asked was offered no channel")
		}
		return set
	}
	later := time.Now().Add(12 * time.Hour).Truncate(time.Second)
	if set := offered(later); len(set.ICEServers) != 1 || !set.ServersExpire.Equal(later) {
		t.Fatalf("the relay's servers were handed on with %d servers and time %v, want its one server and %v", len(set.ICEServers), set.ServersExpire, later)
	}
	if set := offered(time.Time{}); len(set.ICEServers) != 1 || !set.ServersExpire.IsZero() {
		t.Fatalf("a set with no stated time was handed on with time %v", set.ServersExpire)
	}
	if set := offered(time.Now().Add(4 * time.Second)); len(set.ICEServers) != 0 || !set.ServersExpire.IsZero() {
		t.Fatalf("a set expiring within the 4.5 s a channel is given was used: %d servers, time %v", len(set.ICEServers), set.ServersExpire)
	}
	named.Store(true)
	if set := offered(later); !set.ServersExpire.IsZero() {
		t.Fatalf("the operator's own servers were given the relay's time %v", set.ServersExpire)
	}
}
