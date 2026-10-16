package dashboard

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/logsink"
)

const AccessTokenMaxBytes = 4096

const legacyDashboardCookieName = "aii_token"

func (s *Server) hostAllowed(hostPort string) bool {
	s.hostsMu.RLock()
	defer s.hostsMu.RUnlock()
	if s.allowedHosts[hostPort] {
		return true
	}
	if s.anyHostPort == "" {
		return false
	}
	_, p, err := net.SplitHostPort(hostPort)
	return err == nil && p == s.anyHostPort
}

func (s *Server) SetWebhookHandler(fn func(w http.ResponseWriter, r *http.Request, pluginID, hookPath string)) {
	s.hostsMu.Lock()
	defer s.hostsMu.Unlock()
	s.webhook = fn
}

func (s *Server) handleWebhook(w http.ResponseWriter, r *http.Request) {
	s.hostsMu.RLock()
	fn := s.webhook
	s.hostsMu.RUnlock()
	if fn == nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	fn(w, r, r.PathValue("plugin"), r.PathValue("path"))
}

func (s *Server) AllowHost(hostPort string) {
	s.hostsMu.Lock()
	defer s.hostsMu.Unlock()
	if s.allowedHosts == nil {
		s.allowedHosts = map[string]bool{}
	}
	s.allowedHosts[hostPort] = true
}

func (s *Server) hostGate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.hostAllowed(r.Host) {
			if s.foreignHosts.own(refusalLinesAMinute, func(n uint64) {
				logsink.Warn("dashboard.refusal", "%d more request(s) with a foreign Host refused in the last minute", n)
			}) {
				logsink.Warn("dashboard.refusal", "refused request with foreign Host %q", r.Host)
			}
			http.Error(w, "forbidden host", http.StatusForbidden)
			return
		}
		if scrubTokenQuery(w, r) {
			return
		}
		next.ServeHTTP(w, r)
	})
}

func scrubTokenQuery(w http.ResponseWriter, r *http.Request) bool {

	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		return false
	}
	q := r.URL.Query()
	if _, present := q["token"]; !present {
		return false
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	q.Del("token")
	clean := *r.URL
	clean.RawQuery = q.Encode()
	http.Redirect(w, r, clean.RequestURI(), http.StatusSeeOther)
	return true
}

func (s *Server) handleAccessToken(w http.ResponseWriter, r *http.Request) {
	if !s.accessRequired() {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Method == http.MethodGet || r.Method == http.MethodHead {
		if s.tokenAuthorized(r) {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if s.upgradeAccessCookie(w, r) {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		http.Error(w, "dashboard access token required", http.StatusUnauthorized)
		return
	}

	wait := s.strangerBodyWait
	if wait <= 0 {
		wait = strangerBodyWait
	}
	_ = http.NewResponseController(w).SetReadDeadline(time.Now().Add(wait))
	body, err := io.ReadAll(io.LimitReader(r.Body, AccessTokenMaxBytes+1))
	offered := strings.TrimSpace(string(body))
	status, refusal := http.StatusUnauthorized, "dashboard access token refused"
	if err == nil && len(body) <= AccessTokenMaxBytes && !s.validAccessToken(offered) {

		p, ok, seen := s.redeemAccessCode(offered, r)
		if ok {
			if s.accessCodeSpent != nil {
				s.accessCodeSpent()
			}
			logsink.Info("dashboard.decision", "an access code signed a device in from %s", clientHost(r))
			s.setAccessCookieFor(w, r, p.hash)
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if seen {

			status, refusal = http.StatusForbidden, ErrAccessCodeSeen.Error()
		}
	}
	if err != nil || len(body) > AccessTokenMaxBytes || !s.validAccessToken(offered) {
		s.credentialRefused(r)
		http.Error(w, refusal, status)
		return
	}
	s.setAccessCookie(w, r)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleRotateAccessToken(w http.ResponseWriter, r *http.Request) {
	s.handleTokenAct(w, r, false)
}

func (s *Server) handleRequireAccessToken(w http.ResponseWriter, r *http.Request) {
	s.handleTokenAct(w, r, true)
}

func (s *Server) handleTokenAct(w http.ResponseWriter, r *http.Request, require bool) {
	done, where := "rotated", "rotate it there"
	if require {
		done, where = "turned on", "turn it on there"
	}
	switch {
	case require && s.accessRequired():
		http.Error(w, "this dashboard already asks for an access token — there is nothing to turn on; Rotate access token replaces it", http.StatusConflict)
		return
	case !require && !s.AccessTokenRequired():
		http.Error(w, "this dashboard is not asking for an access token, so there is none to rotate", http.StatusConflict)
		return
	case !require && !s.tokenAuthorized(r):
		http.Error(w, "dashboard access token required", http.StatusUnauthorized)
		return
	}

	if !sameSiteAs(r.Header.Get("Origin"), r.Host) {
		http.Error(w, "request came from another site", http.StatusForbidden)
		return
	}
	if site := r.Header.Get("Sec-Fetch-Site"); site != "" && site != "same-origin" {
		http.Error(w, "request came from another site", http.StatusForbidden)
		return
	}
	if !privateConnection(r) {
		http.Error(w, "This connection is not private, so the access token was not "+done+": its cookie would cross the network in the clear. Open this page on the machine itself, or serve the dashboard over HTTPS (Settings → Dashboard), and "+where+".", http.StatusForbidden)
		return
	}
	var act func() error
	if h := s.currentHandler(); h != nil {
		act = h.RotateDashboardToken
		if require {
			act = h.RequireDashboardToken
		}
	}
	if act == nil {
		http.Error(w, "the access token cannot be "+done+" here", http.StatusNotFound)
		return
	}
	before := s.accessHash()
	err := act()

	if s.accessRequired() && s.accessHash() != before {
		s.setAccessCookie(w, r)
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

const strangerBodyWait = 10 * time.Second

const refusedLoginDelay = 400 * time.Millisecond

func (s *Server) credentialRefused(r *http.Request) {
	limit := 0
	if h := s.currentHandler(); h != nil && h.RefusedPauses != nil {
		limit = h.RefusedPauses()
	}
	limit = within(limit, DefaultRefusedPauses, MinRefusedPauses, MaxRefusedPauses)
	if s.refused.lines.own(refusalLinesAMinute, func(n uint64) {
		logsink.Warn("dashboard.refusal", "%d more access token(s) refused in the last minute, each answered and none written down one by one", n)
	}) {
		logsink.Warn("dashboard.refusal", "access token refused from %s", clientHost(r))
	}
	g := &s.refused
	g.mu.Lock()
	slow := g.pausing < limit
	if slow {
		g.pausing++
	}
	g.mu.Unlock()
	if !slow {
		return
	}
	pause := time.Sleep
	if s.refusedPause != nil {
		pause = s.refusedPause
	}
	pause(refusedLoginDelay)
	g.mu.Lock()
	g.pausing--
	g.mu.Unlock()
}

const (
	DefaultRefusedPauses, MinRefusedPauses, MaxRefusedPauses = 32, 1, 1024
	refusalLinesAMinute                                      = 120
	refusalMinute                                            = time.Minute
)

type refusalGate struct {
	mu      sync.Mutex
	pausing int
	lines   minuteLines
}

type minuteLines struct {
	mu       sync.Mutex
	counting bool
	said     int
	unsaid   uint64
}

func (m *minuteLines) own(most int, rest func(uncounted uint64)) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.counting {
		m.counting = true
		time.AfterFunc(refusalMinute, func() {
			m.mu.Lock()
			n := m.unsaid
			m.counting, m.said, m.unsaid = false, 0, 0
			m.mu.Unlock()
			if n > 0 {
				rest(n)
			}
		})
	}
	if m.said < most {
		m.said++
		return true
	}
	m.unsaid++
	return false
}

const (
	DefaultUnsignedRequestsPerSecond, MinUnsignedRequestsPerSecond, MaxUnsignedRequestsPerSecond = 20, 1, 1000
	DefaultUnsignedRequestBurst, MinUnsignedRequestBurst, MaxUnsignedRequestBurst                = 100, 1, 10000
)

type unsignedGate struct {
	mu     sync.Mutex
	tokens float64
	at     time.Time
	lines  minuteLines
}

func (g *unsignedGate) take(now time.Time, perSecond, burst int) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.at.IsZero() {
		g.tokens = float64(burst)
	} else if since := now.Sub(g.at); since > 0 {
		g.tokens += since.Seconds() * float64(perSecond)
	}
	g.tokens = min(g.tokens, float64(burst))
	g.at = now
	if g.tokens < 1 {
		return false
	}
	g.tokens--
	return true
}

func (s *Server) unsignedRate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Context().Value(carriedKey{}) == nil || tokenAuthorizedUnder(s.auth.Load(), r) {
			next.ServeHTTP(w, r)
			return
		}
		perSecond, burst := 0, 0
		if h := s.currentHandler(); h != nil && h.UnsignedRequests != nil {
			perSecond, burst = h.UnsignedRequests()
		}
		perSecond = within(perSecond, DefaultUnsignedRequestsPerSecond, MinUnsignedRequestsPerSecond, MaxUnsignedRequestsPerSecond)
		burst = within(burst, DefaultUnsignedRequestBurst, MinUnsignedRequestBurst, MaxUnsignedRequestBurst)
		if s.unsigned.take(time.Now(), perSecond, burst) {
			next.ServeHTTP(w, r)
			return
		}

		s.unsigned.lines.own(0, func(n uint64) {
			logsink.Warn("dashboard.refusal", "%d unsigned request(s) through the relay were answered 429 in the last minute: more arrived than dashboard.unsigned_requests_per_second (%d, in bursts of %d) allows", n, perSecond, burst)
		})
		w.Header().Set("Retry-After", "1")
		http.Error(w, "too many requests", http.StatusTooManyRequests)
	})
}

func (s *Server) guessed(r *http.Request) bool {
	if s.accessHash() == "" {
		return false
	}
	guess := false
	for _, name := range [...]string{dashboardCookieName(r), legacyDashboardCookieName} {
		c, err := r.Cookie(name)
		if err != nil {
			continue
		}
		if s.validAccessToken(c.Value) {
			return false
		}
		guess = true
	}
	return guess
}

func clientHost(r *http.Request) string {
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}

func (s *Server) upgradeAccessCookie(w http.ResponseWriter, r *http.Request) bool {
	if s.accessHash() == "" {
		return false
	}
	if current, err := r.Cookie(dashboardCookieName(r)); err == nil && s.validAccessToken(current.Value) {
		s.setAccessCookie(w, r)
		return true
	}
	legacy, err := r.Cookie(legacyDashboardCookieName)
	if err != nil || !s.validAccessToken(legacy.Value) {
		return false
	}
	s.setAccessCookie(w, r)
	http.SetCookie(w, &http.Cookie{
		Name:     legacyDashboardCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   r.TLS != nil,
		MaxAge:   -1,
		Expires:  time.Unix(1, 0),
	})
	return true
}

func (s *Server) setAccessCookie(w http.ResponseWriter, r *http.Request) {
	s.setAccessCookieFor(w, r, s.accessHash())
}

func (s *Server) setAccessCookieFor(w http.ResponseWriter, r *http.Request, hash string) {
	http.SetCookie(w, &http.Cookie{
		Name:     dashboardCookieName(r),
		Value:    hash,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,

		Secure: r.TLS != nil,
		MaxAge: 365 * 24 * 60 * 60,
	})
}

func dashboardCookieName(r *http.Request) string {
	_, port, err := net.SplitHostPort(r.Host)
	if err != nil || port == "" {
		if r.TLS != nil {
			port = "443"
		} else {
			port = "80"
		}
	}
	return "aii_token_" + port
}

func (s *Server) validAccessToken(offered string) bool {
	return tokenIsVerifier(offered, s.accessHash())
}

func (s *Server) wsAuthorized(r *http.Request) bool {
	return s.wsAuthorizedUnder(s.auth.Load(), r)
}

func (s *Server) wsAuthorizedUnder(p *accessPolicy, r *http.Request) bool {
	return s.wsOriginAllowed(r) && tokenAuthorizedUnder(p, r)
}

func (s *Server) wsOriginAllowed(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return false
	}
	u, err := url.Parse(origin)
	return err == nil && u.Scheme == requestScheme(r) && s.hostAllowed(u.Host) && sameSiteAs(origin, r.Host)
}

func (s *Server) tokenAuthorized(r *http.Request) bool {
	if tokenAuthorizedUnder(s.auth.Load(), r) {
		return true
	}
	if s.guessed(r) {
		s.credentialRefused(r)
	}
	return false
}

func tokenAuthorizedUnder(p *accessPolicy, r *http.Request) bool {
	required, hash := false, ""
	if p != nil {
		required, hash = p.required, p.hash
	}
	if !required {
		return true
	}
	if hash == "" {
		return false
	}
	c, cerr := r.Cookie(dashboardCookieName(r))
	if cerr != nil {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(c.Value), []byte(hash)) == 1
}

const ShortAccessToken = 16

type accessPolicy struct {
	required bool
	hash     string
	length   int
}

func (s *Server) accessPolicy() (required bool, hash string) {
	p := s.auth.Load()
	if p == nil {
		return false, ""
	}
	return p.required, p.hash
}

func (s *Server) accessRequired() bool { required, _ := s.accessPolicy(); return required }
func (s *Server) accessHash() string   { _, hash := s.accessPolicy(); return hash }

func (s *Server) SetAccessToken(required bool, token string) {
	p := &accessPolicy{required: required, length: len(token)}
	if token != "" {
		sum := sha256.Sum256([]byte(token))
		p.hash = hex.EncodeToString(sum[:])
	}

	if old := s.auth.Swap(p); cutsOff(old, p) {
		if n := s.closeAdmitted(); n > 0 {
			logsink.Info("dashboard.decision", "access policy changed — %d open session(s) closed; each signs in again", n)
		}
	}
}

func cutsOff(before, after *accessPolicy) bool {
	if before == nil {
		before = &accessPolicy{}
	}
	if after == nil {
		after = &accessPolicy{}
	}
	return after.required && (!before.required || before.hash != after.hash)
}

func (s *Server) closeAdmitted() int {
	conns := s.conns(nil)
	for _, c := range conns {
		c.CloseNow()
	}
	return len(conns)
}

func (s *Server) AccessTokenRequired() bool {
	required, hash := s.accessPolicy()
	return required && hash != ""
}

func (s *Server) AccessTokenLength() int {
	if p := s.auth.Load(); p != nil {
		return p.length
	}
	return 0
}

func (s *Server) AccessTokenShort() bool {
	return s.AccessTokenRequired() && s.AccessTokenLength() < ShortAccessToken
}

func (s *Server) HostAllowedForTest(hostPort string) bool { return s.hostAllowed(hostPort) }
