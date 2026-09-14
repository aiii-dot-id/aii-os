package tools

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
)

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
type LocalScope struct {
	Addr   netip.Addr
	Prefix netip.Prefix
	Name   string
	Port   int
}

// .
func (s LocalScope) String() string {
	var host string
	switch {
	case s.Addr.IsValid():
		host = s.Addr.String()
		if s.Addr.Is6() {
			host = "[" + host + "]"
		}
	case s.Prefix.IsValid():
		host = s.Prefix.String()
	default:
		host = s.Name
	}
	if s.Port == 0 {
		return host + ":*"
	}
	return host + ":" + strconv.Itoa(s.Port)
}

// .
// .
var reLocalName = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)*$`)

// .
// .
// .
// .
func ParseLocalScope(entry string) (LocalScope, error) {
	s := strings.TrimSpace(entry)
	if s == "" {
		return LocalScope{}, fmt.Errorf("an empty local scope names nothing")
	}
	hostPart, portPart := s, ""
	switch {
	case strings.HasPrefix(s, "["):
		end := strings.IndexByte(s, ']')
		if end < 0 {
			return LocalScope{}, fmt.Errorf("%q: unterminated IPv6 literal", entry)
		}
		hostPart = s[1:end]
		if rest := s[end+1:]; rest != "" {
			if !strings.HasPrefix(rest, ":") {
				return LocalScope{}, fmt.Errorf("%q: expected :port after the IPv6 literal", entry)
			}
			portPart = rest[1:]
		}
	case strings.Count(s, ":") == 1:
		i := strings.IndexByte(s, ':')
		hostPart, portPart = s[:i], s[i+1:]
	case strings.Count(s, ":") > 1:
		hostPart = s
	}
	sc := LocalScope{}
	switch portPart {
	case "", "*":
		sc.Port = 0
	default:
		p, err := strconv.Atoi(portPart)
		if err != nil || p < 1 || p > 65535 {
			return LocalScope{}, fmt.Errorf("%q: port %q is not 1-65535 or *", entry, portPart)
		}
		sc.Port = p
	}
	if hostPart == "" || hostPart == "*" {
		return LocalScope{}, fmt.Errorf("%q: a local scope names an address, a range or a name", entry)
	}
	if pfx, err := netip.ParsePrefix(hostPart); err == nil {
		if pfx.Addr().Is4In6() {
			return LocalScope{}, fmt.Errorf("%q: write an IPv4 range as IPv4", entry)
		}
		if !IsLocalIP(pfx.Addr().AsSlice()) {
			return LocalScope{}, fmt.Errorf("%q: %s is not a range on the local network", entry, pfx)
		}
		sc.Prefix = pfx.Masked()
		return sc, nil
	}
	if a, err := netip.ParseAddr(hostPart); err == nil {
		a = a.Unmap()
		if !IsLocalIP(a.AsSlice()) {
			return LocalScope{}, fmt.Errorf("%q: %s is not an address on the local network (the public internet is plugins.grants hosts)", entry, a)
		}
		sc.Addr = a
		return sc, nil
	}
	name := strings.ToLower(strings.TrimSuffix(hostPart, "."))
	if !reLocalName.MatchString(name) {
		return LocalScope{}, fmt.Errorf("%q: %q is not an address, a range or a hostname", entry, hostPart)
	}
	sc.Name = name
	return sc, nil
}

// .
// .
// .
func LocalScopes(entries []string) (scopes []LocalScope, rejected []string) {
	for _, e := range entries {
		sc, err := ParseLocalScope(e)
		if err != nil {
			rejected = append(rejected, err.Error())
			continue
		}
		scopes = append(scopes, sc)
	}
	return scopes, rejected
}

// .
func (s LocalScope) CoversAddr(ip netip.Addr, port int) bool {
	if s.Port != 0 && s.Port != port {
		return false
	}
	ip = ip.Unmap()
	switch {
	case s.Addr.IsValid():
		return s.Addr == ip
	case s.Prefix.IsValid():
		return s.Prefix.Contains(ip)
	}
	return false
}

// .
func (s LocalScope) CoversName(name string, port int) bool {
	return s.Name != "" && s.Name == strings.ToLower(strings.TrimSuffix(name, ".")) && (s.Port == 0 || s.Port == port)
}

// .
// .
var metadataIPs = []net.IP{net.ParseIP("169.254.169.254"), net.ParseIP("fd00:ec2::254")}

func isMetadataIP(ip net.IP) bool {
	for _, m := range metadataIPs {
		if m.Equal(ip) {
			return true
		}
	}
	return false
}

// .
// .
// .
// .
func IsLocalIP(ip net.IP) bool {
	if ip == nil || ip.IsUnspecified() || ip.IsMulticast() || isMetadataIP(ip) {
		return false
	}
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
		cgnatNet.Contains(ip) || siteLocalNet.Contains(ip)
}

// .
// .
// .
// .
type LocalGuard struct {
	base   func(context.Context, string) error
	scopes []LocalScope
	refuse func(ip net.IP, port int) bool
	mu     sync.Mutex
	pinned map[string][]netip.AddrPort
}

// .
// .
// .
func GuardAdmitting(base func(context.Context, string) error, scopes []LocalScope, refuse func(net.IP, int) bool) *LocalGuard {
	if base == nil {
		base = FetchGuard
	}
	return &LocalGuard{base: base, scopes: scopes, refuse: refuse, pinned: map[string][]netip.AddrPort{}}
}

func urlPort(u *url.URL) int {
	if p := u.Port(); p != "" {
		n, err := strconv.Atoi(p)
		if err == nil {
			return n
		}
		return 0
	}
	if u.Scheme == "https" {
		return 443
	}
	return 80
}

// .
// .
// .
func (g *LocalGuard) Guard(ctx context.Context, rawURL string) error {
	if len(g.scopes) == 0 {
		return g.base(ctx, rawURL)
	}
	u, err := url.Parse(rawURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" {
		return g.base(ctx, rawURL)
	}
	host := strings.ToLower(strings.TrimSuffix(u.Hostname(), "."))
	port := urlPort(u)
	if ip, perr := netip.ParseAddr(host); perr == nil {
		ip = ip.Unmap()
		if isMetadataIP(ip.AsSlice()) {
			return fmt.Errorf("%w: %s is the cloud metadata address", ErrEgressBlocked, ip)
		}
		if !IsLocalIP(ip.AsSlice()) {
			return g.base(ctx, rawURL)
		}
		return g.admitAddr(ip, port)
	}
	if !g.nameGranted(host, port) {
		return g.base(ctx, rawURL)
	}
	addrs, err := ipResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return fmt.Errorf("local egress: resolve %s: %w", host, err)
	}
	if len(addrs) == 0 {
		return fmt.Errorf("%w: %s resolves to nothing", ErrEgressBlocked, host)
	}
	pins := make([]netip.AddrPort, 0, len(addrs))
	for _, a := range addrs {
		ip, ok := netip.AddrFromSlice(a.IP)
		if !ok {
			continue
		}
		ip = ip.Unmap()
		if !IsLocalIP(ip.AsSlice()) {
			return fmt.Errorf("%w: %s resolves to %s, which is not on the local network", ErrEgressBlocked, host, ip)
		}
		if g.refuse != nil && g.refuse(ip.AsSlice(), port) {
			return fmt.Errorf("%w: %s resolves to %s:%d, this host's own listener", ErrEgressBlocked, host, ip, port)
		}
		pins = append(pins, netip.AddrPortFrom(ip, uint16(port)))
	}
	g.mu.Lock()
	g.pinned[host] = pins
	g.mu.Unlock()
	return nil
}

func (g *LocalGuard) nameGranted(host string, port int) bool {
	for _, s := range g.scopes {
		if s.CoversName(host, port) {
			return true
		}
	}
	return false
}

func (g *LocalGuard) admitAddr(ip netip.Addr, port int) error {
	if g.refuse != nil && g.refuse(ip.AsSlice(), port) {
		return fmt.Errorf("%w: %s:%d is this host's own listener", ErrEgressBlocked, ip, port)
	}
	for _, s := range g.scopes {
		if s.CoversAddr(ip, port) {
			return nil
		}
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	want := netip.AddrPortFrom(ip, uint16(port))
	for _, pins := range g.pinned {
		for _, p := range pins {
			if p == want {
				return nil
			}
		}
	}
	return fmt.Errorf("%w: %s:%d is on the local network and no local grant names it", ErrEgressBlocked, ip, port)
}

// .
// .
// .
// .
func (g *LocalGuard) Pinned(host string) ([]net.IP, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	pins, ok := g.pinned[strings.ToLower(strings.TrimSuffix(host, "."))]
	if !ok {
		return nil, false
	}
	out := make([]net.IP, 0, len(pins))
	for _, p := range pins {
		out = append(out, p.Addr().AsSlice())
	}
	return out, true
}

type pinsKey struct{}

// .
// .
func WithPins(ctx context.Context, g *LocalGuard) context.Context {
	if g == nil {
		return ctx
	}
	return context.WithValue(ctx, pinsKey{}, g)
}

func pinsFrom(ctx context.Context) *LocalGuard {
	g, _ := ctx.Value(pinsKey{}).(*LocalGuard)
	return g
}
