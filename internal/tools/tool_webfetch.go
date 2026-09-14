package tools

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"

	"github.com/aiii-dot-id/aii-os/internal/untrusted"
	"strings"
	"time"
)

// .
// .

type WebFetchTool struct {
	maxBytes int
	timeout  time.Duration
	// .
	// .
	// .
	local  []LocalScope
	refuse func(net.IP, int) bool
	// .
	// .
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

// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
var ErrEgressBlocked = errors.New("egress blocked by policy")

func FetchGuard(ctx context.Context, rawURL string) error {
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
			return fmt.Errorf("refusing to fetch non-public address %s: %w", host, ErrEgressBlocked)
		}
		return nil
	}
	// .
	// .
	// .
	// .
	// .
	ips, err := ipResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return fmt.Errorf("host lookup failed: %w", err)
	}
	addrs := make([]net.IP, len(ips))
	for i, ia := range ips {
		addrs[i] = ia.IP
	}
	for _, ip := range addrs {
		if !ip.IsGlobalUnicast() || isPrivateIP(ip) {
			return fmt.Errorf("refusing to fetch %s — resolves to non-public address %s: %w", host, ip, ErrEgressBlocked)
		}
	}
	return nil
}

// .
// .
// .
// .
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

// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
func guardedTransport(guard func(context.Context, string) error, transport http.RoundTripper) http.RoundTripper {
	if transport != nil {
		return transport
	}
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		return guardedDialContext(ctx, network, addr, guard)
	}
	return t
}

// .
// .
type ipAddrResolver interface {
	LookupIPAddr(ctx context.Context, host string) ([]net.IPAddr, error)
}

var ipResolver ipAddrResolver = net.DefaultResolver

// .
// .
// .
// .
// .
// .
func guardedDialContext(ctx context.Context, network, addr string, guard func(context.Context, string) error) (net.Conn, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, fmt.Errorf("guarded dial: %w", err)
	}
	// .
	// .
	// .
	var addrs []net.IPAddr
	if g := pinsFrom(ctx); g != nil {
		if ips, ok := g.Pinned(host); ok {
			for _, ip := range ips {
				addrs = append(addrs, net.IPAddr{IP: ip})
			}
		}
	}
	if addrs == nil {
		addrs, err = ipResolver.LookupIPAddr(ctx, host)
		if err != nil {
			return nil, fmt.Errorf("guarded dial: resolve %s: %w", host, err)
		}
	}
	if len(addrs) == 0 {
		return nil, fmt.Errorf("guarded dial: no addresses for %s", host)
	}
	for _, a := range addrs {
		if !a.IP.IsGlobalUnicast() || isPrivateIP(a.IP) {
			if guard == nil || guard(ctx, "http://"+net.JoinHostPort(a.IP.String(), port)+"/") != nil {
				return nil, fmt.Errorf("guarded dial: refusing non-public address %s for %s: %w", a.IP, host, ErrEgressBlocked)
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

func GuardedClient(timeout time.Duration, guard func(context.Context, string) error, transport http.RoundTripper) *http.Client {
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
			if err := guard(req.Context(), req.URL.String()); err != nil {
				// .
				// .
				// .
				// .
				return fmt.Errorf("redirect refused: %w", err)
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
	lg := GuardAdmitting(nil, t.local, t.refuse)
	if err := lg.Guard(ctx, strings.TrimSpace(url)); err != nil {
		return Result{Error: fmt.Sprintf("web_fetch blocked: %v", err)}, nil
	}

	// .
	// .
	// .
	// .
	// .
	// .
	// .
	timeout := t.timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	client := GuardedClient(timeout, lg.Guard, nil)
	ctx = WithPins(ctx, lg)
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

	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	labeled := untrusted.Wrap(url, content)

	return Result{Output: labeled, Truncated: truncated}, nil
}
