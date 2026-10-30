package dashboard

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"net"
	"testing"
	"time"

	"github.com/pion/turn/v5"
)

// carriedChannels is the channel setting of a handler whose test opens a
// page carried through a relay: the wait is long enough for the channel to
// open, so the session is held on it and not on the socket, and the servers
// are carriedServers.
func carriedChannels(t *testing.T) func() ChannelSettings {
	servers := carriedServers(t)
	return func() ChannelSettings {
		return ChannelSettings{WaitMS: 8000, ReadyAllowanceMS: 100, ICEServers: servers, ICEServersNamed: true, Sessions: 4}
	}
}

// machineIPv4 is an IPv4 address outside loopback on one of this machine's
// interfaces that is up; nil when it has none.
func machineIPv4() net.IP {
	ifaces, _ := net.Interfaces()
	for _, ifc := range ifaces {
		if ifc.Flags&net.FlagUp == 0 || ifc.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, _ := ifc.Addrs()
		for _, a := range addrs {
			if ipn, ok := a.(*net.IPNet); ok && ipn.IP.To4() != nil && !ipn.IP.IsLinkLocalUnicast() {
				return ipn.IP
			}
		}
	}
	return nil
}

func carriedServers(t *testing.T) []ICEServer {
	t.Helper()
	ip := machineIPv4()
	if ip == nil {
		t.Log("this machine has no IPv4 address outside loopback on an interface that is up: no STUN server is named, so no channel can open")
		return nil
	}
	udp, err := net.ListenPacket("udp4", net.JoinHostPort(ip.String(), "0"))
	if err != nil {
		t.Fatal(err)
	}
	server, err := turn.NewServer(turn.ServerConfig{
		AuthHandler: func(*turn.RequestAttributes) (string, []byte, bool) { return "", nil, false },
		PacketConnConfigs: []turn.PacketConnConfig{{PacketConn: udp,
			RelayAddressGenerator: &turn.RelayAddressGeneratorStatic{RelayAddress: ip, Address: ip.String()}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Close() })
	return []ICEServer{{URLs: []string{"stun:" + udp.LocalAddr().String()}}}
}

type carriedPage struct {
	pieces <-chan []byte
	join   *channelCarrier
}

// openCarriedChannelPage opens one page through a relay for the public name, on a
// channel, on a started server whose handler offers channels
// (carriedChannels).
func openCarriedChannelPage(t *testing.T, s *Server) *carriedPage {
	t.Helper()
	const name = "ui.carried.example.test"
	cert := selfSigned(t, name)
	s.SetPublicCertificate(name, func() *tls.Certificate { return cert })
	s.AllowHost(name)
	roots := x509.NewCertPool()
	roots.AddCert(cert.Leaf)
	_, pieces := channelPage(t, carriedSocket(t, s, name, roots, "&transport=1"))
	join, _, _ := framed(16 << 10)
	return &carriedPage{pieces: pieces, join: join}
}

// next reads the next whole message the identity sent the carried page.
func (p *carriedPage) next(t *testing.T) (ServerMessage, bool) {
	t.Helper()
	for {
		select {
		case piece := <-p.pieces:
			p.join.in <- piece
			if piece[0]&pieceLast == 0 {
				continue
			}
			_, whole, err := p.join.Read(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			var m ServerMessage
			if err := json.Unmarshal(whole, &m); err != nil {
				t.Fatalf("the carried page was sent what is not a message: %q", whole)
			}
			return m, true
		case <-time.After(10 * time.Second):
			return ServerMessage{}, false
		}
	}
}

// until reads the carried page's messages until one of type typ arrives,
// and fails the test with why when none does.
func (p *carriedPage) until(t *testing.T, typ, why string) ServerMessage {
	t.Helper()
	for {
		m, ok := p.next(t)
		if !ok {
			t.Fatalf("the page carried through a relay was never sent %q: %s", typ, why)
		}
		if m.Type == typ {
			return m
		}
	}
}
