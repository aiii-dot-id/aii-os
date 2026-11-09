package dashboard

import (
	"context"
	"crypto/hkdf"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"maps"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/aiii-dot-id/aii-os/internal/logsink"
)

const pageKeyHeader = "Aii-Page-Key"

const missingHeader = "Aii-Missing"

const (
	pageKeyPrefix     = "pk1."
	pageKeyNonceBytes = 16
	pageKeyBytes      = pageKeyNonceBytes + sha256.Size

	pageKeyInfo = "aii-os dashboard page key 1"
)

func pageSecret(token string) []byte {
	if token == "" {
		return nil
	}
	secret, err := hkdf.Key(sha256.New, []byte(token), nil, pageKeyInfo, sha256.Size)
	if err != nil {
		return nil
	}
	return secret
}

func pageKeyMAC(secret, nonce []byte) []byte {
	mac := hmac.New(sha256.New, secret)
	mac.Write(nonce)
	return mac.Sum(nil)
}

func mintPageKey(p *accessPolicy) string {
	if p == nil || len(p.secret) == 0 {
		return ""
	}
	raw := make([]byte, pageKeyBytes)
	rand.Read(raw[:pageKeyNonceBytes])
	copy(raw[pageKeyNonceBytes:], pageKeyMAC(p.secret, raw[:pageKeyNonceBytes]))
	return pageKeyPrefix + base64.RawURLEncoding.EncodeToString(raw)
}

func pageKeyValid(p *accessPolicy, key string) bool {
	rest, ok := strings.CutPrefix(key, pageKeyPrefix)
	if p == nil || len(p.secret) == 0 || !ok {
		return false
	}
	raw, err := base64.RawURLEncoding.DecodeString(rest)
	if err != nil || len(raw) != pageKeyBytes {
		return false
	}
	return hmac.Equal(raw[pageKeyNonceBytes:], pageKeyMAC(p.secret, raw[:pageKeyNonceBytes]))
}

func givePageKey(w http.ResponseWriter, p *accessPolicy) {
	if key := mintPageKey(p); key != "" {
		w.Header().Set(pageKeyHeader, key)
	}
}

type signInMissing string

const (
	signedInBoth   signInMissing = ""
	missingCookie  signInMissing = "cookie"
	missingPageKey signInMissing = "page-key"
)

const (
	refusedKeyless = "signed in by cookie only: this dashboard also asks for the page key its sign-in answered"
	refusedTicket  = "This address was good once, for a short time, and does not open its file again. Open the file from the dashboard."
)

func (s *Server) signInLacks(r *http.Request, tickets bool) signInMissing {
	p := s.auth.Load()
	if p == nil || !p.required {
		return signedInBoth
	}
	if !tokenAuthorizedUnder(p, r) {
		if s.guessed(r) {
			s.credentialRefused(r)
		}
		return missingCookie
	}
	if key := r.Header.Get(pageKeyHeader); key != "" {
		if pageKeyValid(p, key) {
			return signedInBoth
		}
		s.credentialRefused(r)
		return missingPageKey
	}
	if tickets && r.URL.Query().Has(ticketParam) {
		if s.tickets.spend(p, r, s.clock()) {
			return signedInBoth
		}
		return missingPageKey
	}
	s.keyless(r)
	return missingPageKey
}

func (s *Server) keyless(r *http.Request) {
	if s.keylessLines.own(s.refusalLines(), func(n uint64) {
		logsink.Warn("dashboard.refusal", "%d more request(s) carried the sign-in cookie without the page key in the last minute", n)
	}) {
		logsink.Warn("dashboard.refusal", "refused a request %s that carried the sign-in cookie without the page key: a page from before the page key, or a program holding the cookie", cameFrom(r))
	}
}

func refuseSignIn(w http.ResponseWriter, r *http.Request, missing signInMissing) {
	w.Header().Set(missingHeader, string(missing))
	words := "this page isn't signed in. Sign in, then try again"
	if missing == missingPageKey {
		words = refusedKeyless
		if r.URL.Query().Has(ticketParam) {
			words = refusedTicket
		}
	}
	http.Error(w, words, http.StatusUnauthorized)
}

type reach int

const (
	reachPage reach = iota + 1

	reachBrowser

	reachMinted

	reachDoor

	reachOutside

	reachFrame
	reachSection
	reachSocket
	reachHook
)

func (s *Server) walled(asks reach, next http.HandlerFunc) http.HandlerFunc {
	switch asks {
	case reachFrame, reachSection, reachSocket, reachHook:
		return next
	}
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Referrer-Policy", "no-referrer")
		var missing signInMissing
		switch asks {
		case reachPage:
			missing = s.signInLacks(r, false)
		case reachBrowser:
			missing = s.signInLacks(r, true)
		case reachMinted:
			if !s.tokenAuthorized(r) {
				missing = missingCookie
			}
		}
		if missing != signedInBoth {
			refuseSignIn(w, r, missing)
			return
		}
		next(w, r)
	}
}

const closeSignIn websocket.StatusCode = 4401

const refusedSocket = "sign in again: this page does not hold this dashboard's page key"

type helloMessage struct {
	Type    string `json:"type"`
	PageKey string `json:"page_key"`
}

func (s *Server) admitHello(ctx context.Context, socket carrier, p *accessPolicy, r *http.Request) bool {
	if p == nil || !p.required {
		return true
	}
	wait := s.strangerBodyTime()
	type read struct {
		typ  websocket.MessageType
		data []byte
		err  error
	}
	got := make(chan read, 1)
	go func() {
		typ, data, err := socket.Read(ctx)
		got <- read{typ, data, err}
	}()
	var first read
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case first = <-got:
	case <-timer.C:

		s.reloadEarlierPage(ctx, socket)
		return false
	}
	if first.err != nil {
		return false
	}
	var hello helloMessage
	if first.typ != websocket.MessageText || json.Unmarshal(first.data, &hello) != nil || hello.Type != "hello" {
		s.reloadEarlierPage(ctx, socket)
		return false
	}
	switch {
	case hello.PageKey == "":
		s.keyless(r)
	case pageKeyValid(p, hello.PageKey):
		return true
	default:
		s.credentialRefused(r)
	}
	_ = socket.Close(closeSignIn, refusedSocket)
	return false
}

func (s *Server) reloadEarlierPage(ctx context.Context, socket carrier) {
	frame, err := json.Marshal(ServerMessage{Type: "overlay_changed", Token: uint64(s.clock().UnixMilli()), Paths: []string{"/ws.js"}})
	if err == nil {
		wctx, cancel := context.WithTimeout(ctx, writeWait)

		if socket.Write(wctx, websocket.MessageText, frame) == nil {
			if ch, ok := socket.(*channelCarrier); ok {
				ch.flush(wctx)
			}
		}
		cancel()
	}
	_ = socket.Close(closeSignIn, refusedSocket)
}

const ticketParam = "ticket"

const (
	DefaultPageTicketSeconds, MinPageTicketSeconds, MaxPageTicketSeconds = 60, 5, 600
	DefaultPageTickets, MinPageTickets, MaxPageTickets                   = 256, 16, 4096
)

const ticketBytes = 16

type ticketFor struct {
	method, path, query string
	verifier            string
	minted, expires     time.Time
}

type ticketStore struct {
	mu   sync.Mutex
	held map[[sha256.Size]byte]ticketFor
}

func canonicalQuery(q url.Values) string {
	q = maps.Clone(q)
	q.Del(ticketParam)
	return q.Encode()
}

func (t *ticketStore) mint(p *accessPolicy, method, path string, q url.Values, now time.Time, life time.Duration, most int) string {
	raw := make([]byte, ticketBytes)
	rand.Read(raw)
	ticket := base64.RawURLEncoding.EncodeToString(raw)
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.held == nil {
		t.held = map[[sha256.Size]byte]ticketFor{}
	}
	for h, f := range t.held {
		if !now.Before(f.expires) {
			delete(t.held, h)
		}
	}
	for len(t.held) >= most {
		var oldest [sha256.Size]byte
		first := true
		for h, f := range t.held {
			if first || f.minted.Before(t.held[oldest].minted) {
				oldest, first = h, false
			}
		}
		delete(t.held, oldest)
	}
	t.held[sha256.Sum256([]byte(ticket))] = ticketFor{method: method, path: path, query: canonicalQuery(q), verifier: p.hash, minted: now, expires: now.Add(life)}
	return ticket
}

func (t *ticketStore) spend(p *accessPolicy, r *http.Request, now time.Time) bool {
	q := r.URL.Query()
	h := sha256.Sum256([]byte(q.Get(ticketParam)))
	t.mu.Lock()
	f, ok := t.held[h]
	delete(t.held, h)
	t.mu.Unlock()
	return ok && now.Before(f.expires) && f.verifier == p.hash &&
		f.method == r.Method && f.path == r.URL.Path && f.query == canonicalQuery(q)
}

type ticketAsk struct {
	Method  string `json:"method"`
	Address string `json:"address"`
}

func (s *Server) handleTicket(w http.ResponseWriter, r *http.Request) {
	p := s.auth.Load()
	if p == nil || !p.required {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	var ask ticketAsk
	if err := json.NewDecoder(io.LimitReader(r.Body, maxTicketAskBytes)).Decode(&ask); err != nil {
		http.Error(w, "a ticket is asked for with {\"method\", \"address\"}", http.StatusBadRequest)
		return
	}
	u, err := url.Parse(ask.Address)
	if err != nil || u.Scheme != "" || u.Host != "" || !strings.HasPrefix(u.Path, "/") || (ask.Method != http.MethodGet && ask.Method != http.MethodPost) {
		http.Error(w, "a ticket opens one GET or POST of an address on this dashboard", http.StatusBadRequest)
		return
	}
	probe := &http.Request{Method: ask.Method, URL: u, Host: r.Host, Header: http.Header{}}
	if _, pattern := s.mux.Handler(probe); s.reaches[pattern] != reachBrowser {
		http.Error(w, "that address takes no ticket: a page reaches it with its page key", http.StatusBadRequest)
		return
	}
	seconds, most := 0, 0
	if h := s.currentHandler(); h != nil && h.PageTickets != nil {
		seconds, most = h.PageTickets()
	}
	seconds = within(seconds, DefaultPageTicketSeconds, MinPageTicketSeconds, MaxPageTicketSeconds)
	most = within(most, DefaultPageTickets, MinPageTickets, MaxPageTickets)
	ticket := s.tickets.mint(p, ask.Method, u.Path, u.Query(), s.clock(), time.Duration(seconds)*time.Second, most)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"ticket": ticket})
}

const maxTicketAskBytes = 16 << 10
