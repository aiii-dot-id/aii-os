package dashboard

import (
	"context"
	"net"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/pion/turn/v5"
	"github.com/pion/webrtc/v4"

	"github.com/aiii-dot-id/aii-os/internal/logsink"
)

// cuttable is a TURN server's UDP socket that can be cut: from then on
// nothing it receives is read and nothing it sends leaves.
type cuttable struct {
	net.PacketConn
	cut atomic.Bool
}

func (c *cuttable) ReadFrom(p []byte) (int, net.Addr, error) {
	for {
		n, from, err := c.PacketConn.ReadFrom(p)
		if err != nil || !c.cut.Load() {
			return n, from, err
		}
	}
}

func (c *cuttable) WriteTo(p []byte, to net.Addr) (int, error) {
	if c.cut.Load() {
		return len(p), nil
	}
	return c.PacketConn.WriteTo(p, to)
}

// unreadNomination is the attribute the page's nomination number rides in:
// one the identity does not read (comprehension-optional, and not the
// library's default 0x0030), so what the identity acts on is USE-CANDIDATE
// alone, as from a browser that negotiated no renomination.
const unreadNomination = 0xC0A1

func TestAChannelWhosePathGoesQuietFollowsThePageOntoItsOtherPath(t *testing.T) {
	ip := machineIPv4()
	if ip == nil {
		t.Skip("this machine has no IPv4 address outside loopback on an interface that is up, for the TURN server the page's relayed paths run through")
	}
	const realm, user, pass = "aii.example.test", "the-user", "the-credential"
	rawUDP, err := net.ListenPacket("udp4", net.JoinHostPort(ip.String(), "0"))
	if err != nil {
		t.Fatal(err)
	}
	udp := &cuttable{PacketConn: rawUDP}
	tcp, err := net.Listen("tcp4", net.JoinHostPort(ip.String(), "0"))
	if err != nil {
		t.Fatal(err)
	}
	var tcpOpen atomic.Bool
	var allocMu sync.Mutex
	relayed := map[int]string{} // relay port -> the protocol the page reaches the server by
	key := turn.GenerateAuthKey(user, realm, pass)
	relay := &turn.RelayAddressGeneratorStatic{RelayAddress: ip, Address: ip.String()}
	server, err := turn.NewServer(turn.ServerConfig{
		Realm:       realm,
		AuthHandler: func(ra *turn.RequestAttributes) (string, []byte, bool) { return ra.Username, key, ra.Username == user },
		EventHandler: turn.EventHandler{OnAllocationCreated: func(client, _ net.Addr, _, _, _ string, relayAddr net.Addr, _ int) {
			by := "UDP"
			if _, ok := client.(*net.TCPAddr); ok {
				by = "TCP"
			}
			if ua, ok := relayAddr.(*net.UDPAddr); ok {
				allocMu.Lock()
				relayed[ua.Port] = by
				allocMu.Unlock()
			}
		}},
		PacketConnConfigs: []turn.PacketConnConfig{{PacketConn: udp, RelayAddressGenerator: relay}},
		ListenerConfigs: []turn.ListenerConfig{{Listener: tcp, RelayAddressGenerator: relay,
			PermissionHandler: func(net.Addr, net.IP) bool { return tcpOpen.Load() }}},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Close() })
	pageConfig := webrtc.Configuration{ICETransportPolicy: webrtc.ICETransportPolicyRelay, ICEServers: []webrtc.ICEServer{{
		URLs:     []string{"turn:" + rawUDP.LocalAddr().String() + "?transport=udp", "turn:" + tcp.Addr().String() + "?transport=tcp"},
		Username: user, Credential: pass,
	}}}
	// The page moves its nomination when another pair answers and its own
	// has not for a while; the library does that only as renomination. Its
	// pairs over TCP are checked for as long as the test runs, so they are
	// there to move to once their path is let through.
	var engine webrtc.SettingEngine
	if err := engine.SetICERenomination(webrtc.WithRenominationNominationAttribute(unreadNomination)); err != nil {
		t.Fatal(err)
	}
	engine.SetICEMaxBindingRequests(1 << 15)
	pageAPI := webrtc.NewAPI(webrtc.WithSettingEngine(engine))

	said := logsink.CaptureForTest(t)
	s := New("127.0.0.1", 0, &WSHandler{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	d := dialChannelThrough(ctx, t, s, channelTest.held(), pageAPI, pageConfig, pageReady, nil)
	on, isChannel := d.chosen.(*channelCarrier)
	if !d.alive || !isChannel {
		t.Fatalf("the session was given %T (alive %v), want the channel", d.chosen, d.alive)
	}
	defer on.CloseNow()
	join, _, _ := framed(16 << 10)
	toPage := func(what string, within time.Duration) {
		t.Helper()
		wctx, wcancel := context.WithTimeout(ctx, writeWait)
		defer wcancel()
		if err := on.Write(wctx, websocket.MessageText, []byte(what)); err != nil {
			t.Fatalf("the identity could not write %q on the channel: %v", what, err)
		}
		limit := time.After(within)
		for {
			select {
			case p := <-d.fromIdentity:
				join.in <- p
				if p[0]&pieceLast == 0 {
					continue
				}
				_, whole, err := join.Read(ctx)
				if err == nil && strings.Contains(string(whole), `"transport_ready"`) {
					continue // the identity's ready, before the session's first message
				}
				if err != nil || string(whole) != what {
					t.Fatalf("the page read %q (%v), want %q", whole, err, what)
				}
				return
			case <-limit:
				t.Fatalf("%q did not reach the page in %v", what, within)
			}
		}
	}
	toIdentity := func(what string) {
		t.Helper()
		pieces, out, _ := framed(16 << 10)
		if err := pieces.Write(ctx, websocket.MessageText, []byte(what)); err != nil {
			t.Fatal(err)
		}
		for _, p := range out.taken() {
			if err := d.dc.Send(p); err != nil {
				t.Fatal(err)
			}
		}
		rctx, rcancel := context.WithTimeout(ctx, 10*time.Second)
		defer rcancel()
		if _, got, err := on.Read(rctx); err != nil || string(got) != what {
			t.Fatalf("the identity read %q (%v), want %q", got, err, what)
		}
	}
	// pageRelayedBy is the protocol the page's selected pair reaches the
	// TURN server by.
	pageRelayedBy := func() string {
		p, err := d.page.SCTP().Transport().ICETransport().GetSelectedCandidatePair()
		if err != nil || p == nil || p.Local == nil {
			return "nothing"
		}
		allocMu.Lock()
		defer allocMu.Unlock()
		if by, ok := relayed[int(p.Local.Port)]; ok {
			return by
		}
		return "an unknown relay port " + strconv.Itoa(int(p.Local.Port))
	}

	// selectedPriority is the priority of the page's selected pair (RFC 8445
	// 6.1.2.3, as the library computes it), the same at both ends: G is the
	// page's candidate, the page controlling, and D the identity's.
	selectedPriority := func() uint64 {
		p, err := d.page.SCTP().Transport().ICETransport().GetSelectedCandidatePair()
		if err != nil || p == nil || p.Local == nil || p.Remote == nil {
			t.Fatalf("the page has no selected pair (%v)", err)
		}
		g, dd := uint64(p.Local.Priority), uint64(p.Remote.Priority)
		var gd uint64
		if g > dd {
			gd = 1
		}
		return (1<<32-1)*min(g, dd) + 2*max(g, dd) + gd
	}

	toPage("before", 10*time.Second)
	toIdentity("before")
	if by := pageRelayedBy(); by != "UDP" {
		t.Fatalf("the page's first path reaches the TURN server by %s, want UDP: this shows nothing of a failover from it", by)
	}
	cutPriority := selectedPriority()

	udp.cut.Store(true)
	quiet := regexp.MustCompile(`dashboard\.session: channel \d+: the peer connection is disconnected`)
	for limit := time.Now().Add(20 * time.Second); !quiet.MatchString(said.String()); {
		if time.Now().After(limit) {
			t.Fatalf("the identity never said its path had gone quiet:\n%s", said.String())
		}
		time.Sleep(50 * time.Millisecond)
	}
	tcpOpen.Store(true)
	for limit := time.Now().Add(15 * time.Second); pageRelayedBy() != "TCP"; {
		if time.Now().After(limit) {
			t.Fatalf("the page did not move to its path over TCP (it is on %s): this shows nothing of the identity following it", pageRelayedBy())
		}
		time.Sleep(50 * time.Millisecond)
	}
	// The pair the page moves to is of lower priority than the one cut, so
	// the published rule (strictly higher priority only) would refuse it.
	if moved := selectedPriority(); moved >= cutPriority {
		t.Fatalf("the page moved to a pair of priority %d, not below the cut pair's %d: this shows nothing of a lower-priority nomination", moved, cutPriority)
	}

	toPage("after", 10*time.Second)
	toIdentity("after")
}
