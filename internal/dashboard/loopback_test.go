package dashboard

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"net/http"
	"strings"
	"testing"
)

// nonLoopbackIPv4 finds an address of this host a network bind can use:
// on an interface that is up and running, and proved by binding it. The
// host's address list is not that. Windows lists the addresses of an
// adapter that is down as well — an autoconfiguration 169.254 address on
// a disconnected one is the usual case — and binding one fails with "The
// requested address is not valid in its context". The product never picks
// an interface itself: the dashboard binds the host it is configured with.
func nonLoopbackIPv4(t *testing.T) string {
	t.Helper()
	ifaces, err := net.Interfaces()
	if err != nil {
		t.Skip("no interfaces")
	}
	for _, ifi := range ifaces {
		if ifi.Flags&net.FlagUp == 0 || ifi.Flags&net.FlagRunning == 0 || ifi.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := ifi.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			ipn, ok := a.(*net.IPNet)
			if !ok || ipn.IP.IsLoopback() || ipn.IP.To4() == nil {
				continue
			}
			ln, err := net.Listen("tcp", net.JoinHostPort(ipn.IP.String(), "0"))
			if err != nil {
				continue
			}
			ln.Close()
			return ipn.IP.String()
		}
	}
	t.Skip("this host has no bindable non-loopback IPv4 address for a network bind")
	return ""
}

func TestEphemeralNetworkBindRetriesUntilLoopbackSharesItsPort(t *testing.T) {
	ip := nonLoopbackIPv4(t)
	networkBinds, loopbackBinds := 0, 0
	listen := func(network, address string) (net.Listener, error) {
		if strings.HasPrefix(address, "127.0.0.1:") {
			loopbackBinds++
			if loopbackBinds == 1 {
				return nil, errors.New("occupied")
			}
		} else {
			networkBinds++
		}
		return net.Listen(network, address)
	}
	ln, loopback, err := listenDashboard(net.JoinHostPort(ip, "0"), 0, listen)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ln.Close()
		loopback.Close()
	})
	_, networkPort, _ := net.SplitHostPort(ln.Addr().String())
	_, loopbackPort, _ := net.SplitHostPort(loopback.Addr().String())

	if networkBinds < 2 || loopbackBinds != networkBinds || networkPort != loopbackPort {
		t.Fatalf("did not discard the collided pair and reserve a shared port: network=%d loopback=%d ports=%s/%s",
			networkBinds, loopbackBinds, networkPort, loopbackPort)
	}
}

// Beside a network bind, loopback is served in the clear: the person at
// the machine has a URL that needs no name, no certificate and nothing
// installed. The TLS bind still serves TLS; the origin check takes each
// request's own scheme; the URLs say which is which.
func TestLoopbackIsServedInTheClearBesideANetworkBind(t *testing.T) {
	ip := nonLoopbackIPv4(t)
	s := New(ip, 0, &WSHandler{})
	addr, err := s.Start(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Shutdown(context.Background()) })
	_, port, _ := net.SplitHostPort(addr)
	if got := s.LocalURL(); got != "http://127.0.0.1:"+port {
		t.Fatalf("the local URL is plain loopback beside the bind: %q", got)
	}
	if got := s.AddressURL(); got != "https://"+addr {
		t.Fatalf("the address URL is the TLS bind: %q", got)
	}
	res, err := http.Get("http://127.0.0.1:" + port + "/")
	if err != nil {
		t.Fatalf("loopback in the clear: %v", err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("loopback must be served: %d", res.StatusCode)
	}
	res, err = testClient.Get("https://" + addr + "/")
	if err != nil {
		t.Fatalf("the network bind still serves TLS: %v", err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("the TLS bind must be served: %d", res.StatusCode)
	}
	// The socket's origin check follows the request's scheme.
	plain, _ := http.NewRequest("GET", "http://127.0.0.1:"+port+"/ws", nil)
	plain.Header.Set("Origin", "http://127.0.0.1:"+port)
	if u := plain.Header.Get("Origin"); !s.hostAllowed(strings.TrimPrefix(u, "http://")) || requestScheme(plain) != "http" {
		t.Fatal("a plain loopback origin is this server's own")
	}
	secure, _ := http.NewRequest("GET", "https://"+addr+"/ws", nil)
	secure.TLS = &tls.ConnectionState{}
	if requestScheme(secure) != "https" {
		t.Fatal("a TLS request is https")
	}
}

func TestTheNetworkBindServesHTTP2BesideLoopback(t *testing.T) {
	ip := nonLoopbackIPv4(t)
	s := New(ip, 0, &WSHandler{})
	addr, err := s.Start(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Shutdown(context.Background()) })
	c := &http.Client{Transport: &http.Transport{
		ForceAttemptHTTP2: true,
		TLSClientConfig:   &tls.Config{InsecureSkipVerify: true, NextProtos: []string{"h2", "http/1.1"}},
	}}
	res, err := c.Get("https://" + addr + "/")
	if err != nil {
		t.Fatalf("an HTTP/2 GET to the bind failed: %v", err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("the bind must serve HTTP/2: %d", res.StatusCode)
	}
	if res.ProtoMajor != 2 {
		t.Fatalf("the bind must negotiate HTTP/2, got HTTP/%d.%d", res.ProtoMajor, res.ProtoMinor)
	}
	// Loopback beside it is plain HTTP/1.1 and still answers.
	_, port, _ := net.SplitHostPort(addr)
	lres, err := http.Get("http://127.0.0.1:" + port + "/")
	if err != nil {
		t.Fatalf("loopback: %v", err)
	}
	lres.Body.Close()
	if lres.StatusCode != http.StatusOK {
		t.Fatalf("loopback must answer: %d", lres.StatusCode)
	}
}
