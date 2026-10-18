package dashboard

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/logsink"
)

const (
	AccessCodeLength = 12

	accessCodeAlphabet = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

	maxAccessCodeBytes = 128

	DefaultAccessCodeSeconds = 120
	MinAccessCodeSeconds     = 30
	MaxAccessCodeSeconds     = 600
)

type accessCode struct {
	id      string
	hash    [sha256.Size]byte
	expires time.Time
	policy  *accessPolicy
}

type AccessCodeState struct {
	ID        string `json:"id"`
	Code      string `json:"code"`
	Link      string `json:"link,omitempty"`
	QR        string `json:"qr,omitempty"`
	ExpiresAt string `json:"expires_at"`
	Seconds   int    `json:"seconds"`
	Own       bool   `json:"own,omitempty"`
}

var (
	ErrAccessCodeNeedsToken = errors.New("an access code signs a device in to a dashboard that asks for its access token, and this one asks for none — Ask for an access token (Settings → Dashboard) first")

	ErrAccessCodeNotPrivate = errors.New("this connection is not private, so no access code was made: it would cross the network in the clear. Open this page on the machine itself, or over HTTPS")

	ErrAccessCodeSeen = errors.New("this connection is not private, so the access code sent over it has been seen in the clear: it is ended and signed nothing in. Make another and sign in with it over HTTPS")

	ErrAccessCodeStale = errors.New("the access token changed while the code was being made — sign in again, then ask for a code")
)

type AccessCodeShort struct{ Length, Line int }

func (e *AccessCodeShort) Error() string {
	return fmt.Sprintf("your own access code is %d characters long and must be at least %d: anyone who guesses it while it is valid is signed in, and no count of tries is kept", e.Length, e.Line)
}

func normalizeAccessCode(s string) string {
	var b strings.Builder
	for _, r := range strings.ToUpper(strings.TrimSpace(s)) {
		switch r {
		case ' ', '-', '\t':
		case 'O':
			b.WriteByte('0')
		case 'I', 'L':
			b.WriteByte('1')
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

func newAccessCode() (string, error) {
	var raw [AccessCodeLength]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("access code: %w", err)
	}
	out := make([]byte, AccessCodeLength)
	for i, b := range raw {
		out[i] = accessCodeAlphabet[b&31]
	}
	return string(out), nil
}

func groupAccessCode(code string) string {
	var parts []string
	for len(code) > 4 {
		parts, code = append(parts, code[:4]), code[4:]
	}
	return strings.Join(append(parts, code), "-")
}

func (s *Server) SetAccessCodeSeconds(n int) {
	switch {
	case n <= 0:
		n = DefaultAccessCodeSeconds
	case n < MinAccessCodeSeconds:
		n = MinAccessCodeSeconds
	case n > MaxAccessCodeSeconds:
		n = MaxAccessCodeSeconds
	}
	s.accessCodeSeconds.Store(int64(n))
}

func (s *Server) accessCodeLife() time.Duration {
	n := s.accessCodeSeconds.Load()
	if n <= 0 {
		n = DefaultAccessCodeSeconds
	}
	return time.Duration(n) * time.Second
}

func (s *Server) issueAccessCode(policy *accessPolicy, own string) (AccessCodeState, error) {
	if policy == nil || !policy.required || policy.hash == "" {
		return AccessCodeState{}, ErrAccessCodeNeedsToken
	}
	if s.auth.Load() != policy {
		return AccessCodeState{}, ErrAccessCodeStale
	}
	shown, isOwn := "", strings.TrimSpace(own) != ""
	var secret string
	if isOwn {
		secret = normalizeAccessCode(own)
		if len(own) > maxAccessCodeBytes {
			return AccessCodeState{}, fmt.Errorf("your own access code is longer than %d characters", maxAccessCodeBytes)
		}
		if n := len([]rune(secret)); n < AccessCodeLength {
			return AccessCodeState{}, &AccessCodeShort{Length: n, Line: AccessCodeLength}
		}
		shown = strings.TrimSpace(own)
	} else {
		code, err := newAccessCode()
		if err != nil {
			return AccessCodeState{}, err
		}
		secret, shown = code, groupAccessCode(code)
	}
	var idRaw [8]byte
	if _, err := rand.Read(idRaw[:]); err != nil {
		return AccessCodeState{}, fmt.Errorf("access code: %w", err)
	}
	life := s.accessCodeLife()
	c := &accessCode{id: hex.EncodeToString(idRaw[:]), hash: sha256.Sum256([]byte(secret)), expires: s.clock().Add(life), policy: policy}
	s.accessCode.Store(c)

	if s.auth.Load() != policy {
		s.accessCode.CompareAndSwap(c, nil)
		return AccessCodeState{}, ErrAccessCodeStale
	}
	return AccessCodeState{ID: c.id, Code: shown, ExpiresAt: c.expires.UTC().Format(time.RFC3339), Seconds: int(life / time.Second), Own: isOwn}, nil
}

func (s *Server) cancelAccessCode(id string) {
	if cur := s.accessCode.Load(); cur != nil && id != "" && cur.id == id {
		s.accessCode.CompareAndSwap(cur, nil)
	}
}

func (s *Server) redeemAccessCode(offered string, r *http.Request) (granted *accessPolicy, ok, seen bool) {
	p := s.auth.Load()
	cur := s.accessCode.Load()
	if p == nil || cur == nil || cur.policy != p {
		return nil, false, false
	}
	sum := sha256.Sum256([]byte(normalizeAccessCode(offered)))
	if subtle.ConstantTimeCompare(sum[:], cur.hash[:]) != 1 {
		return nil, false, false
	}
	if !privateConnection(r) {
		if s.accessCode.CompareAndSwap(cur, nil) {
			logsink.Warn("dashboard.refusal", "an access code was offered from %s over a connection that is not private, so it has been seen in the clear: it is ended and signed nothing in", clientHost(r))
		}
		return nil, false, true
	}
	if !s.clock().Before(cur.expires) {
		s.accessCode.CompareAndSwap(cur, nil)
		return nil, false, false
	}
	if !s.accessCode.CompareAndSwap(cur, nil) {
		return nil, false, false
	}
	return p, true, false
}

func (s *Server) handleLauncherLink(w http.ResponseWriter, r *http.Request) {
	if !remoteIsLoopback(r.RemoteAddr) {
		http.Error(w, "a sign-in link is made here only for a program on this machine", http.StatusForbidden)
		return
	}
	if r.Header.Get("Origin") != "" || r.Header.Get("Sec-Fetch-Site") != "" {
		http.Error(w, "a sign-in link is made here for the launcher on this machine, never for a page: a page that is signed in makes an access code with Settings → Dashboard → Open on another device", http.StatusForbidden)
		return
	}
	p := s.auth.Load()
	if p == nil || !p.required || p.hash == "" {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	wait := s.strangerBodyWait
	if wait <= 0 {
		wait = strangerBodyWait
	}
	_ = http.NewResponseController(w).SetReadDeadline(time.Now().Add(wait))
	body, err := io.ReadAll(io.LimitReader(r.Body, AccessTokenMaxBytes+1))
	if err != nil || len(body) > AccessTokenMaxBytes || !tokenIsVerifier(strings.TrimSpace(string(body)), p.hash) {
		s.credentialRefused(r)
		http.Error(w, "dashboard access token refused", http.StatusUnauthorized)
		return
	}
	if h := s.currentHandler(); h != nil && h.AccessCodeSeconds != nil {
		s.SetAccessCodeSeconds(h.AccessCodeSeconds())
	}
	st, err := s.issueAccessCode(p, "")
	if err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	logsink.Info("dashboard.decision", "the launcher on this machine was given a sign-in link, good once for %d seconds", st.Seconds)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(LauncherLink{
		Link:      accessCodeLink(requestScheme(r)+"://"+r.Host, st.Code),
		ExpiresAt: st.ExpiresAt,
		Seconds:   st.Seconds,
	})
}

type LauncherLink struct {
	Link      string `json:"link"`
	ExpiresAt string `json:"expires_at"`
	Seconds   int    `json:"seconds"`
}

func tokenIsVerifier(offered, hash string) bool {
	want, err := hex.DecodeString(hash)
	if err != nil || len(want) != sha256.Size {
		return false
	}
	sum := sha256.Sum256([]byte(offered))
	return subtle.ConstantTimeCompare(sum[:], want) == 1
}

func (s *Server) clock() time.Time {
	if s.now != nil {
		return s.now()
	}
	return time.Now()
}
