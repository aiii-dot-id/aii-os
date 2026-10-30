package app

import (
	"net/http"
	"sync"
)

// pageKeyed does for a test's HTTP client what the dashboard's page does with
// its page key (internal/dashboard/pagekey.go): it keeps the key an answer
// carries in Aii-Page-Key (a sign-in's, a rotation's, turning the token on)
// and sends it in that header on every request after, beside the cookie the
// client's jar keeps. A client without it is a program holding only the
// cookie, which signs nothing in.
type pageKeyed struct {
	base http.RoundTripper
	mu   sync.Mutex
	key  string
}

// keyedTransport is a transport that keeps and sends the page key, over a
// plain one that keeps no connection between requests.
func keyedTransport() *pageKeyed {
	return &pageKeyed{base: &http.Transport{DisableKeepAlives: true}}
}

func (p *pageKeyed) RoundTrip(r *http.Request) (*http.Response, error) {
	if key := p.pageKey(); key != "" && r.Header.Get("Aii-Page-Key") == "" {
		r = r.Clone(r.Context())
		r.Header.Set("Aii-Page-Key", key)
	}
	res, err := p.base.RoundTrip(r)
	if err == nil {
		if key := res.Header.Get("Aii-Page-Key"); key != "" {
			p.mu.Lock()
			p.key = key
			p.mu.Unlock()
		}
	}
	return res, err
}

// pageKey is the key this client holds, "" before a sign-in.
func (p *pageKeyed) pageKey() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.key
}
