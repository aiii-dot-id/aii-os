package dashboard

import (
	"bufio"
	"context"
	"crypto/tls"
	"crypto/x509"
	"io"
	"net"
	"net/http"
	"testing"
	"time"
)

// carriedSession hands the dashboard one end of a pipe, as a relay client
// hands it a carried connection, and returns the browser's end.
func carriedSession(t *testing.T, s *Server) net.Conn {
	t.Helper()
	browser, carried := net.Pipe()
	t.Cleanup(func() { browser.Close() })
	if !s.Carry(carried) {
		carried.Close()
		t.Fatal("the dashboard did not take the carried session")
	}
	return browser
}

func TestARelaysSessionIsServedInProcessUnderThePublicCertificate(t *testing.T) {
	const name = "ui.x.example.test"
	s := New("127.0.0.1", 0, &WSHandler{})
	addr, err := s.Start("") // plain HTTP: the operator turned nothing on
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Shutdown(context.Background()) })
	cert := selfSigned(t, name)
	s.SetPublicCertificate(name, func() *tls.Certificate { return cert })
	s.AllowHost(name)
	roots := x509.NewCertPool()
	roots.AddCert(cert.Leaf)

	get := func(serverName, host string) (*http.Response, error) {
		t.Helper()
		bc := tls.Client(carriedSession(t, s), &tls.Config{ServerName: serverName, RootCAs: roots, MinVersion: tls.VersionTLS12})
		_ = bc.SetDeadline(time.Now().Add(10 * time.Second))
		if err := bc.Handshake(); err != nil {
			return nil, err
		}
		if got := bc.ConnectionState().PeerCertificates[0]; !got.Equal(cert.Leaf) {
			t.Fatal("the certificate the browser saw is not the public name's")
		}
		req, _ := http.NewRequest("GET", "https://"+host+"/", nil)
		req.Header.Set("Accept", "text/html")
		if err := req.Write(bc); err != nil {
			return nil, err
		}
		return http.ReadResponse(bufio.NewReader(bc), req)
	}
	res, err := get(name, name)
	if err != nil {
		t.Fatalf("a session carried for the public name: %v", err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("the dashboard through a carried session answered %d", res.StatusCode)
	}
	// The Host gate is the request's own: a carried session asking for a
	// name this dashboard does not answer to is refused as any other.
	if res, err = get(name, "stranger.example.test"); err != nil || res.StatusCode != http.StatusForbidden {
		t.Fatalf("a foreign Host inside a carried session: %v %v, want 403", res, err)
	}
	res.Body.Close()

	blind := tls.Client(carriedSession(t, s), &tls.Config{ServerName: "other.example.test", InsecureSkipVerify: true, MinVersion: tls.VersionTLS12})
	began := time.Now()
	_ = blind.SetDeadline(began.Add(10 * time.Second))
	if err := blind.Handshake(); err == nil {
		t.Fatal("a carried session for another server name was answered with a certificate")
	} else if time.Since(began) > 5*time.Second {
		t.Fatalf("a carried session for another server name was not refused, only left to run out of time: %v", err)
	}
	// CLEARTEXT IS NOT A CARRIED SESSION. The listener speaks TLS and nothing
	// else, by construction: a relay that sent cleartext is not served.
	plain := carriedSession(t, s)
	_ = plain.SetDeadline(time.Now().Add(10 * time.Second))
	go func() { _, _ = plain.Write([]byte("GET / HTTP/1.1\r\nHost: " + name + "\r\n\r\n")) }()
	if res, err := http.ReadResponse(bufio.NewReader(plain), nil); err == nil && res.StatusCode < 400 {
		t.Fatalf("cleartext through a carried session was served: %d", res.StatusCode)
	}
	// And the machine's own address is what it was: plain, served.
	local, err := http.Get("http://" + addr + "/")
	if err != nil || local.StatusCode != http.StatusOK {
		t.Fatalf("plain loopback beside carried sessions: %v %v", local, err)
	}
	local.Body.Close()
	if got := s.LocalURL(); got != "http://"+addr {
		t.Fatalf("the local URL moved: %q", got)
	}
}

// A dashboard that is not serving takes no carried session, and says so, so
// the relay client closes it: before Start, after the stop, and with no
// public name (the handshake is refused; nothing is served).
func TestCarriedSessionsNeedARunningDashboardAndAPublicName(t *testing.T) {
	s := New("127.0.0.1", 0, &WSHandler{})
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()
	if s.Carry(b) {
		t.Fatal("a dashboard that never started took a carried session")
	}
	if _, err := s.Start(""); err != nil {
		t.Fatal(err)
	}
	// No public name: taken, and refused at the handshake.
	bc := tls.Client(carriedSession(t, s), &tls.Config{ServerName: "ui.x.example.test", InsecureSkipVerify: true})
	_ = bc.SetDeadline(time.Now().Add(10 * time.Second))
	if err := bc.Handshake(); err == nil {
		t.Fatal("a dashboard with no public name completed a carried handshake")
	}
	done := make(chan struct{})
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		s.Shutdown(ctx)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(15 * time.Second):
		t.Fatal("the dashboard did not stop: carried sessions held it")
	}
	c, d := net.Pipe()
	defer c.Close()
	defer d.Close()
	if s.Carry(d) {
		t.Fatal("a stopped dashboard took a carried session")
	}
}

func TestAnIdleCarriedConnectionIsClosedAtItsOwnLimit(t *testing.T) {
	const name = "ui.x.example.test"
	limit := time.Duration(MinCarriedIdleSeconds) * time.Second
	s := New("127.0.0.1", 0, &WSHandler{})
	s.SetCarriedIdleSeconds(MinCarriedIdleSeconds)
	addr, err := s.Start("")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Shutdown(context.Background()) })
	if s.carriedServer.IdleTimeout != limit || s.server.IdleTimeout == limit {
		t.Fatalf("carried sessions idle for %v and the bind for %v: want %v for the first and the bind's own for the second", s.carriedServer.IdleTimeout, s.server.IdleTimeout, limit)
	}
	cert := selfSigned(t, name)
	s.SetPublicCertificate(name, func() *tls.Certificate { return cert })
	s.AllowHost(name)
	roots := x509.NewCertPool()
	roots.AddCert(cert.Leaf)

	ask := func(c net.Conn, r *bufio.Reader, scheme, host string) {
		t.Helper()
		req, _ := http.NewRequest("GET", scheme+"://"+host+"/", nil)
		req.Header.Set("Accept", "text/html")
		if err := req.Write(c); err != nil {
			t.Fatalf("%s: the request was not taken: %v", scheme, err)
		}
		res, err := http.ReadResponse(r, req)
		if err != nil || res.StatusCode != http.StatusOK {
			t.Fatalf("%s: %v %v", scheme, res, err)
		}
		_, _ = io.Copy(io.Discard, res.Body)
		res.Body.Close()
	}
	carried := tls.Client(carriedSession(t, s), &tls.Config{ServerName: name, RootCAs: roots, MinVersion: tls.VersionTLS12})
	_ = carried.SetDeadline(time.Now().Add(4 * limit))
	if err := carried.Handshake(); err != nil {
		t.Fatal(err)
	}
	local, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer local.Close()
	_ = local.SetDeadline(time.Now().Add(4 * limit))
	carriedReader, localReader := bufio.NewReader(carried), bufio.NewReader(local)
	ask(carried, carriedReader, "https", name)
	ask(local, localReader, "http", addr)
	idleFrom := time.Now()

	// Nothing more is asked. The dashboard ends the carried one, at its
	// limit and not before.
	if _, err := carriedReader.ReadByte(); err == nil {
		t.Fatal("the dashboard sent something on an idle carried connection")
	}
	if idle := time.Since(idleFrom); idle < limit-time.Second || idle > 3*limit {
		t.Fatalf("the idle carried connection ended after %v, want its limit, %v", idle, limit)
	}
	// The machine's own connection has been idle exactly as long, and is
	// still answered on.
	ask(local, localReader, "http", addr)

	// The setter holds what it is given to the key's bounds.
	for set, want := range map[int]int{0: DefaultCarriedIdleSeconds, -3: DefaultCarriedIdleSeconds, 1: MinCarriedIdleSeconds, 90: 90, 100000: MaxCarriedIdleSeconds} {
		other := New("127.0.0.1", 0, &WSHandler{})
		other.SetCarriedIdleSeconds(set)
		if got := other.carriedIdle(); got != time.Duration(want)*time.Second {
			t.Errorf("set to %d seconds, an idle carried connection is kept %v, want %d seconds", set, got, want)
		}
	}
	if got := New("127.0.0.1", 0, &WSHandler{}).carriedIdle(); got != DefaultCarriedIdleSeconds*time.Second {
		t.Errorf("never set, an idle carried connection is kept %v, want the default", got)
	}
}
