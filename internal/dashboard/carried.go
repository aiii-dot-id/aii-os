package dashboard

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/logsink"
)

const (
	DefaultCarriedIdleSeconds = 15
	MinCarriedIdleSeconds     = 5
	MaxCarriedIdleSeconds     = 600
)

func (s *Server) SetCarriedIdleSeconds(n int) {
	switch {
	case n <= 0:
		n = DefaultCarriedIdleSeconds
	case n < MinCarriedIdleSeconds:
		n = MinCarriedIdleSeconds
	case n > MaxCarriedIdleSeconds:
		n = MaxCarriedIdleSeconds
	}
	s.carriedIdleSeconds = n
}

func (s *Server) carriedIdle() time.Duration {
	n := s.carriedIdleSeconds
	if n <= 0 {
		n = DefaultCarriedIdleSeconds
	}
	return time.Duration(n) * time.Second
}

type carriedListener struct {
	conns chan net.Conn
	done  chan struct{}
	once  sync.Once
}

func newCarriedListener() *carriedListener {
	return &carriedListener{conns: make(chan net.Conn), done: make(chan struct{})}
}

func (l *carriedListener) Accept() (net.Conn, error) {
	select {
	case c := <-l.conns:
		return c, nil
	case <-l.done:
		return nil, net.ErrClosed
	}
}

func (l *carriedListener) Close() error   { l.once.Do(func() { close(l.done) }); return nil }
func (l *carriedListener) Addr() net.Addr { return carriedAddr{} }

type carriedAddr struct{}

func (carriedAddr) Network() string { return "relay" }
func (carriedAddr) String() string  { return "carried by a relay" }

func (s *Server) Carry(c net.Conn) bool {
	l := s.carried
	if l == nil || !s.carrying.Load() {
		return false
	}
	select {
	case l.conns <- c:
		return true
	case <-l.done:
		return false
	}
}

func (s *Server) startCarried() {
	s.carried = newCarriedListener()
	s.carriedServer = &http.Server{
		Handler:           s.server.Handler,
		ReadHeaderTimeout: s.server.ReadHeaderTimeout,
		IdleTimeout:       s.carriedIdle(),
		MaxHeaderBytes:    s.server.MaxHeaderBytes,
		TLSConfig:         &tls.Config{GetCertificate: s.carriedCertificate, MinVersion: tls.VersionTLS12},

		ConnContext: func(ctx context.Context, _ net.Conn) context.Context {
			return context.WithValue(ctx, carriedKey{}, true)
		},
	}
	ln := newLimitListener(s.carried, maxConcurrentConns)
	s.carrying.Store(true)
	go func() {
		if err := s.carriedServer.ServeTLS(ln, "", ""); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logsink.Error("dashboard.error", "dashboard relay-session server error: %v", err)
		}
	}()
}

func (s *Server) carriedCertificate(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
	s.hostsMu.RLock()
	name, source := s.publicName, s.publicCert
	s.hostsMu.RUnlock()
	if name == "" || source == nil || !strings.EqualFold(hello.ServerName, name) {
		return nil, errors.New("dashboard: a carried session that is not for the public name")
	}
	cert := source()
	if cert == nil {
		return nil, errors.New("dashboard: the public name holds no certificate yet")
	}
	return cert, nil
}
