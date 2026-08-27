package tools

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"

	"github.com/aiii-dot-id/aii-os/internal/untrusted"
	"strings"
	"time"
)

// (split from tools.go 2026-08-17 — one file per tool; registry and
// sandbox enforcement live in tools.go)

type WebFetchTool struct {
	maxBytes int
	timeout  time.Duration
	// onFetch reports each successfully fetched URL (H3: the identity
	// engine verifies note source_url citations against real fetches).
	onFetch func(url string)
}

func (t *WebFetchTool) Name() string { return "web_fetch" }
func (t *WebFetchTool) Description() string {
	return "Fetch a URL and return text content. Args: url (required)"
}

func (t *WebFetchTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"url": map[string]interface{}{"type": "string", "description": "URL to fetch"},
		},
		"required": []string{"url"},
	}
}

// FetchGuard rejects the SSRF class (finding 10, 2026-08-17 review):
// the resident's web_fetch reached localhost services and cloud metadata
// endpoints — an identity whose sandbox sits next to an unauthenticated
// local API could read it with a URL. Rules: http(s) only; the host's
// resolved addresses must be PUBLIC (no loopback, link-local — including
// 169.254.169.254 — private, or unique-local ranges); no credentials in
// the URL. DNS rebinding is closed at the dial (finding F1, external review 2026-08-17):
// guardedDialContext resolves once, vets every address, and dials the
// vetted literals — the transport never re-resolves a hostname between
// the guard's check and the connect, so an on-path attacker cannot race
// the two apart.
//
// Exported (step 4): this is THE egress policy — one guard, two
// consumers. The identity's web_fetch and the plugin broker's
// net.outbound both pass every request and every redirect hop through
// it, so a plugin can never reach an address the identity's own fetch
// could not (and a grant never outranks the guard).
func FetchGuard(rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("unparseable url: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("scheme %q not allowed (http/https only)", u.Scheme)
	}
	host := u.Hostname()
	if host == "" {
		return fmt.Errorf("url has no host")
	}
	if u.User != nil {
		return fmt.Errorf("credentials in the url are not allowed")
	}
	if ip := net.ParseIP(host); ip != nil {
		if !ip.IsGlobalUnicast() || isPrivateIP(ip) {
			return fmt.Errorf("refusing to fetch non-public address %s", host)
		}
		return nil
	}
	ips, err := ipResolver.LookupIPAddr(context.Background(), host)
	if err != nil {
		return fmt.Errorf("host lookup failed: %w", err)
	}
	addrs := make([]net.IP, len(ips))
	for i, ia := range ips {
		addrs[i] = ia.IP
	}
	for _, ip := range addrs {
		if !ip.IsGlobalUnicast() || isPrivateIP(ip) {
			return fmt.Errorf("refusing to fetch %s — resolves to non-public address %s", host, ip)
		}
	}
	return nil
}

// cgnatNet and siteLocalNet: ranges IsPrivate doesn't cover but that name
// internal infrastructure all the same (2026-08-17 external review, H4) —
// carrier-grade NAT 100.64.0.0/10 and deprecated-but-routable IPv6
// site-local fec0::/10.
var (
	cgnatNet     = mustCIDR("100.64.0.0/10")
	siteLocalNet = mustCIDR("fec0::/10")
)

func mustCIDR(s string) *net.IPNet {
	_, n, err := net.ParseCIDR(s)
	if err != nil {
		panic(err)
	}
	return n
}

func isPrivateIP(ip net.IP) bool {
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() || ip.IsUnspecified() ||
		cgnatNet.Contains(ip) || siteLocalNet.Contains(ip)
}

// GuardedClient builds the http.Client both egress consumers share
// (web_fetch and the plugin broker): the guard vets the first hop
// before the caller dials, and CheckRedirect re-runs it on EVERY hop
// (H4, found by both adversarial passes: Go follows up to 10 redirects,
// and without this a public host answering 302 →
// http://169.254.169.254/... reached exactly the targets the guard
// claims to block). guard nil means FetchGuard; transport nil means the
// default transport (tests inject a TLS-pinned transport for their
// fixture servers — production wiring passes nil).
// guardedTransport returns the production transport with a dial-time
// IP floor: DNS rebinding is closed by construction (finding F1,
// external review 2026-08-17). The pre-flight guard resolves and vets the
// hostname, but the default transport re-resolves at dial time — an
// attacker's DNS can answer public for the guard and loopback for the
// dial. The floor breaks that race: guardedDialContext resolves once,
// vets EVERY address against the same IP classes the guard applies,
// and dials the vetted literals directly — the hostname is never
// handed back to a resolver we do not control between check and
// connect. A custom transport (test seams inject fixture transports
// against loopback servers) passes through untouched: injected
// transports carry their own discipline.
func guardedTransport(guard func(string) error, transport http.RoundTripper) http.RoundTripper {
	if transport != nil {
		return transport
	}
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		return guardedDialContext(ctx, network, addr, guard)
	}
	return t
}

// ipAddrResolver is the resolver seam: production uses net.DefaultResolver;
// the F1 tests swap in a fake that simulates attacker-controlled DNS.
type ipAddrResolver interface {
	LookupIPAddr(ctx context.Context, host string) ([]net.IPAddr, error)
}

var ipResolver ipAddrResolver = net.DefaultResolver

// guardedDialContext vets the resolved addresses at connect time and
// dials the vetted literals. This is the substrate floor: loopback,
// link-local (169.254.169.254), private, CGNAT, and site-local ranges
// are unreachable here no matter what any earlier check believed —
// and reachable to the plugin broker's net.outbound, which shares
// GuardedClient with web_fetch, only if the dialer permits them.
func guardedDialContext(ctx context.Context, network, addr string, guard func(string) error) (net.Conn, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, fmt.Errorf("guarded dial: %w", err)
	}
	addrs, err := ipResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, fmt.Errorf("guarded dial: resolve %s: %w", host, err)
	}
	if len(addrs) == 0 {
		return nil, fmt.Errorf("guarded dial: no addresses for %s", host)
	}
	for _, a := range addrs {
		if !a.IP.IsGlobalUnicast() || isPrivateIP(a.IP) {
			if guard == nil || guard("http://"+net.JoinHostPort(a.IP.String(), port)+"/") != nil {
				return nil, fmt.Errorf("guarded dial: refusing non-public address %s for %s", a.IP, host)
			}
		}
	}
	var firstErr error
	for _, a := range addrs {
		c, derr := (&net.Dialer{}).DialContext(ctx, network, net.JoinHostPort(a.IP.String(), port))
		if derr == nil {
			return c, nil
		}
		if firstErr == nil {
			firstErr = derr
		}
	}
	return nil, firstErr
}

func GuardedClient(timeout time.Duration, guard func(string) error, transport http.RoundTripper) *http.Client {
	if guard == nil {
		guard = FetchGuard
	}
	return &http.Client{
		Timeout:   timeout,
		Transport: guardedTransport(guard, transport),
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return fmt.Errorf("too many redirects")
			}
			if err := guard(req.URL.String()); err != nil {
				return fmt.Errorf("redirect blocked: %w", err)
			}
			return nil
		},
	}
}

func (t *WebFetchTool) Execute(ctx context.Context, args map[string]interface{}) (Result, error) {
	url, _ := args["url"].(string)
	if url == "" {
		return Result{Error: "url is required"}, nil
	}
	if err := FetchGuard(strings.TrimSpace(url)); err != nil {
		return Result{Error: fmt.Sprintf("web_fetch blocked: %v", err)}, nil
	}

	// R49: external content is untrusted. Label it so the prompt composer
	// can distinguish identity content from foreign text. For a system whose
	// thesis is "the prompt IS the identity," unlabeled foreign text is an
	// injection into the self.
	//
	// GuardedClient re-runs the guard on every redirect hop (H4) — the
	// same client discipline the plugin broker shares.
	timeout := t.timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	client := GuardedClient(timeout, nil, nil)
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return Result{Error: err.Error()}, nil
	}
	req.Header.Set("User-Agent", "AII-OS/1.0")

	resp, err := client.Do(req)
	if err != nil {
		return Result{Error: err.Error()}, nil
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, int64(t.maxBytes)))
	if err != nil {
		return Result{Error: err.Error()}, nil
	}

	content := string(body)
	truncated := len(body) >= t.maxBytes

	if t.onFetch != nil {
		t.onFetch(url)
	}

	// R49: label as external/untrusted, WRAPPED between a sentinel pair
	// (M9) so the boundary where foreign text ends is as explicit as
	// where it begins — an unterminated label lets fetched text
	// impersonate what follows it.
	//
	// This used to build its own pair inline and strip nothing, so a page
	// containing the close marker ended the region early and continued as
	// the resident's own voice. The wrapping now has ONE owner, shared
	// with the cognitive facilities, and it neutralises forged sentinels
	// before wrapping.
	labeled := untrusted.Wrap(url, content)

	return Result{Output: labeled, Truncated: truncated}, nil
}
