package dashboard

import (
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"

	"github.com/aiii-dot-id/aii-os/internal/logsink"
)

func SchemeFor(tls bool) string {
	if tls {
		return "https"
	}
	return "http"
}

func (s *Server) Scheme() string { return SchemeFor(s.tls) }

func (s *Server) AdvertisedOrigin() string {
	s.hostsMu.RLock()
	defer s.hostsMu.RUnlock()
	return s.origin
}

func (s *Server) Origin() string {
	if origin := s.AdvertisedOrigin(); origin != "" {
		return origin
	}
	addr := s.boundAddr
	if addr == "" {
		addr = s.addr
	}

	if host, port, err := net.SplitHostPort(addr); err == nil && isWildcard(host) {
		addr = net.JoinHostPort("127.0.0.1", port)
	}
	return s.Scheme() + "://" + addr
}

func (s *Server) LocalURL() string {
	if s.loopbackAddr != "" {
		return "http://" + s.loopbackAddr
	}
	host, port, err := net.SplitHostPort(s.boundAddr)
	if err != nil {
		return ""
	}
	if isWildcard(host) {
		ip := net.ParseIP(host)
		if ip != nil && ip.To4() == nil {
			host = "::1"
		} else {
			host = "127.0.0.1"
		}
	}
	if IsLoopback(host) {
		return s.Scheme() + "://" + net.JoinHostPort(host, port)
	}
	return ""
}

func (s *Server) AddressURL() string {
	host, _, err := net.SplitHostPort(s.boundAddr)
	if err != nil || IsLoopback(host) || isWildcard(host) {
		return ""
	}
	return s.Scheme() + "://" + s.boundAddr
}

func requestScheme(r *http.Request) string {
	if r.TLS != nil {
		return "https"
	}
	return "http"
}

func isWildcard(host string) bool {
	if host == "0.0.0.0" || host == "::" || host == "" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsUnspecified()
}

func IsLoopback(host string) bool {
	h := strings.TrimSpace(host)
	if h == "" {
		return false
	}
	if strings.EqualFold(h, "localhost") {
		return true
	}
	if ip := net.ParseIP(strings.Trim(h, "[]")); ip != nil {
		return ip.IsLoopback()
	}
	return false
}

func (s *Server) TLSMaterial() *TLSMaterial { return s.tlsMaterial }

func (s *Server) SetOrigin(origin string) {
	s.hostsMu.Lock()
	defer s.hostsMu.Unlock()
	s.origin = strings.TrimSuffix(origin, "/")
}

func (s *Server) SetPublicCertificate(name string, source func() *tls.Certificate) {
	s.hostsMu.Lock()
	defer s.hostsMu.Unlock()
	s.publicName = strings.ToLower(name)
	s.publicCert = source
}

func (s *Server) getCertificate(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
	s.hostsMu.RLock()
	name, source := s.publicName, s.publicCert
	s.hostsMu.RUnlock()
	if name == "" || source == nil || !strings.EqualFold(hello.ServerName, name) {
		return nil, nil
	}
	return source(), nil
}

func listenDashboard(addr string, configuredPort int, listen func(string, string) (net.Listener, error)) (net.Listener, net.Listener, error) {
	const attempts = 16
	var collision error
	for attempt := 0; attempt < attempts; attempt++ {
		ln, err := listen("tcp", addr)
		if err != nil {
			return nil, nil, err
		}
		actualHost, actualPort, err := net.SplitHostPort(ln.Addr().String())
		if err != nil {
			ln.Close()
			return nil, nil, err
		}
		if IsLoopback(actualHost) || isWildcard(actualHost) {
			return ln, nil, nil
		}
		loopback, err := listen("tcp", "127.0.0.1:"+actualPort)
		if err == nil {
			return ln, loopback, nil
		}
		ln.Close()
		if configuredPort != 0 {
			return nil, nil, fmt.Errorf("the loopback companion 127.0.0.1:%s of the %s bind could not be bound: %w", actualPort, addr, err)
		}
		collision = err
	}
	return nil, nil, fmt.Errorf("could not reserve one ephemeral port for both network and loopback listeners: %w", collision)
}

func (s *Server) Start(tlsDir string) (string, error) {
	s.tlsDir = tlsDir
	ln, pairedLoopback, err := listenDashboard(s.addr, s.port, net.Listen)
	if err != nil {
		return "", fmt.Errorf("listen failed: %w", err)
	}

	actualAddr := ln.Addr().String()
	s.boundAddr = actualAddr

	ln = newLimitListener(ln, maxConcurrentConns)

	actualHost, actualPort, err := net.SplitHostPort(actualAddr)
	if err != nil {
		ln.Close()
		if pairedLoopback != nil {
			pairedLoopback.Close()
		}
		return "", fmt.Errorf("split listen addr %q: %w", actualAddr, err)
	}

	s.hostsMu.Lock()
	if s.allowedHosts == nil {
		s.allowedHosts = map[string]bool{}
	}
	s.allowedHosts[actualAddr] = true
	s.allowedHosts[net.JoinHostPort(s.host, actualPort)] = true
	if s.host == "127.0.0.1" || s.host == "::1" || s.host == "localhost" {
		s.allowedHosts["127.0.0.1:"+actualPort] = true
		s.allowedHosts["localhost:"+actualPort] = true
	}
	if isWildcard(actualHost) {
		s.anyHostPort = actualPort
	}
	s.hostsMu.Unlock()
	if isWildcard(actualHost) {
		logsink.Warn("dashboard.decision", "bound to %s — reachable from the whole network; the Host gate matches the port only", actualAddr)
	}

	var loopbacks []net.Listener
	if !IsLoopback(actualHost) && !isWildcard(actualHost) {
		for _, la := range []string{"127.0.0.1:" + actualPort, "[::1]:" + actualPort} {
			lln := pairedLoopback
			var lerr error
			if lln == nil || lln.Addr().String() != la {
				lln, lerr = net.Listen("tcp", la)
			}
			if lerr != nil {
				logsink.Warn("dashboard.error", "loopback %s not served (%v)", la, lerr)
				continue
			}
			loopbacks = append(loopbacks, newLimitListener(lln, maxConcurrentConns))
			s.hostsMu.Lock()
			s.allowedHosts[la] = true
			if s.loopbackAddr == "" {
				s.loopbackAddr = la
				s.allowedHosts["localhost:"+actualPort] = true
			}
			s.hostsMu.Unlock()
		}
	}
	serveLoopbacks := func() {
		if len(loopbacks) == 0 {
			return
		}

		s.loopServer = &http.Server{
			Handler:           s.server.Handler,
			ReadHeaderTimeout: s.server.ReadHeaderTimeout,
			IdleTimeout:       s.server.IdleTimeout,
			MaxHeaderBytes:    s.server.MaxHeaderBytes,
		}
		for _, lln := range loopbacks {
			go func(l net.Listener) {
				if err := s.loopServer.Serve(l); err != nil && !errors.Is(err, http.ErrServerClosed) {
					logsink.Error("dashboard.error", "dashboard loopback server error: %v", err)
				}
			}(lln)
		}
	}

	s.startCarried()

	if s.tlsDir == "" {
		s.tls = false
		if !IsLoopback(actualHost) {
			logsink.Warn("dashboard.decision", "serving PLAIN HTTP on %s — every word between this identity and its operator crosses the network in the clear, and the browser will refuse the microphone. Settings → Dashboard → HTTPS.", actualAddr)
		}
		s.startTurnLifecycles()
		serveLoopbacks()
		go func() {
			if err := s.server.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
				logsink.Error("dashboard.error", "dashboard server error: %v", err)
			}
		}()
		return actualAddr, nil
	}
	s.tls = true
	mat, terr := ensureTLS(s.tlsDir, s.host)
	if terr != nil {
		ln.Close()
		for _, lln := range loopbacks {
			lln.Close()
		}
		return "", fmt.Errorf("dashboard TLS: %w", terr)
	}

	s.tlsMaterial = mat

	pair, perr := tls.LoadX509KeyPair(mat.LeafCert, mat.LeafKey)
	if perr != nil {
		ln.Close()
		for _, lln := range loopbacks {
			lln.Close()
		}
		return "", fmt.Errorf("dashboard TLS: certificate and key are not usable together (%s, %s): %w", mat.LeafCert, mat.LeafKey, perr)
	}

	s.server.TLSConfig = &tls.Config{Certificates: []tls.Certificate{pair}, GetCertificate: s.getCertificate}
	s.startTurnLifecycles()
	serveLoopbacks()

	ln = newSniffListener(ln, s.bounceTarget)
	go func() {

		if err := s.server.ServeTLS(ln, "", ""); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logsink.Error("dashboard.error", "dashboard server error: %v", err)
		}
	}()

	return actualAddr, nil
}

func (s *Server) BindHost() string { return s.host }

func (s *Server) BoundPort() string {
	_, port, err := net.SplitHostPort(s.boundAddr)
	if err != nil {
		return ""
	}
	return port
}

func (s *Server) BoundAddr() string { return s.boundAddr }

func (s *Server) bounceTarget(local net.Addr) string {
	if local != nil {
		return "https://" + local.String()
	}
	return "https://" + s.boundAddr
}
