package tools

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
)

// .
// .

type fakeResolver struct {
	answers map[string][]net.IPAddr
	calls   atomic.Int32
}

func (f *fakeResolver) LookupIPAddr(_ context.Context, host string) ([]net.IPAddr, error) {
	f.calls.Add(1)
	if a, ok := f.answers[host]; ok {
		return a, nil
	}
	return nil, &net.DNSError{Err: "no such host", Name: host, IsNotFound: true}
}

func useResolver(t *testing.T, f *fakeResolver) {
	t.Helper()
	was := ipResolver
	ipResolver = f
	t.Cleanup(func() { ipResolver = was })
}

func addrs(ips ...string) []net.IPAddr {
	out := make([]net.IPAddr, 0, len(ips))
	for _, s := range ips {
		out = append(out, net.IPAddr{IP: net.ParseIP(s)})
	}
	return out
}

func TestLocalScopeGrammar(t *testing.T) {
	good := map[string]string{
		"192.168.1.10:8123":   "192.168.1.10:8123",
		"192.168.1.10":        "192.168.1.10:*",
		"192.168.1.0/24:*":    "192.168.1.0/24:*",
		"10.0.0.0/8":          "10.0.0.0/8:*",
		"homeassistant.local": "homeassistant.local:*",
		"NAS.home:5000":       "nas.home:5000",
		"[fe80::1]:80":        "[fe80::1]:80",
		"fd00::10":            "[fd00::10]:*",
		"127.0.0.1:11434":     "127.0.0.1:11434",
		"100.64.0.5:443":      "100.64.0.5:443",
	}
	for in, want := range good {
		sc, err := ParseLocalScope(in)
		if err != nil {
			t.Fatalf("%q: %v", in, err)
		}
		if sc.String() != want {
			t.Fatalf("%q parsed as %q, want %q", in, sc.String(), want)
		}
	}
	bad := []string{"", "*", ":8123", "8.8.8.8:53", "1.0.0.0/8", "169.254.169.254", "224.0.0.1", "0.0.0.0", "dev.local:70000", "dev.local:0", "not a name", "[fe80::1", "http://x"}
	for _, in := range bad {
		if sc, err := ParseLocalScope(in); err == nil {
			t.Fatalf("%q parsed as %q; a public address, a non-local range, a bad port or prose is not a local scope", in, sc)
		}
	}
	scopes, rejected := LocalScopes([]string{"192.168.1.10:8123", "8.8.8.8", "printer.local"})
	if len(scopes) != 2 || len(rejected) != 1 || !strings.Contains(rejected[0], "8.8.8.8") {
		t.Fatalf("a malformed entry grants nothing and is reported: %v %v", scopes, rejected)
	}
}

func TestLocalGuardAdmitsWhatTheOperatorNamedAndNothingElse(t *testing.T) {
	scopes, _ := LocalScopes([]string{"192.168.1.10:8123", "10.1.0.0/16:*", "127.0.0.1:11434"})
	g := GuardAdmitting(nil, scopes, nil)
	for _, ok := range []string{"http://192.168.1.10:8123/api", "http://10.1.7.3/x", "https://10.1.200.9:8443/", "http://127.0.0.1:11434/api/tags", "http://[::ffff:192.168.1.10]:8123/"} {
		if err := g.Guard(context.Background(), ok); err != nil {
			t.Fatalf("%s: admitted by a scope, got %v", ok, err)
		}
	}
	for _, no := range []string{
		"http://192.168.1.11:8123/",
		"http://192.168.1.10:80/",
		"http://10.2.0.1/",
		"http://127.0.0.1:8181/",
		"http://172.16.0.1/",
	} {
		err := g.Guard(context.Background(), no)
		if !errors.Is(err, ErrEgressBlocked) || !strings.Contains(err.Error(), "no local grant names it") {
			t.Fatalf("%s: refused with the key to set, got %v", no, err)
		}
	}
	// .
	if err := g.Guard(context.Background(), "https://example.com/"); err != nil {
		t.Fatalf("a public URL still passes the base guard: %v", err)
	}
	// .
	if err := GuardAdmitting(nil, nil, nil).Guard(context.Background(), "http://192.168.1.10:8123/"); !errors.Is(err, ErrEgressBlocked) {
		t.Fatalf("with no local scopes a local address is refused as before: %v", err)
	}
}

func TestLocalGuardNeverAdmitsTheMetadataAddressOrThisHostsOwnListener(t *testing.T) {
	scopes, _ := LocalScopes([]string{"169.254.0.0/16:*", "127.0.0.0/8:*", "192.168.1.0/24:*"})
	refuse := func(ip net.IP, port int) bool {
		return port == 8181 && (ip.IsLoopback() || ip.Equal(net.ParseIP("192.168.1.2")))
	}
	g := GuardAdmitting(nil, scopes, refuse)
	if err := g.Guard(context.Background(), "http://169.254.169.254/latest/meta-data"); !errors.Is(err, ErrEgressBlocked) || !strings.Contains(err.Error(), "metadata") {
		t.Fatalf("the metadata address is never admitted, even inside a granted range: %v", err)
	}
	for _, own := range []string{"http://127.0.0.1:8181/", "http://192.168.1.2:8181/ws"} {
		if err := g.Guard(context.Background(), own); !errors.Is(err, ErrEgressBlocked) || !strings.Contains(err.Error(), "own listener") {
			t.Fatalf("%s: this host's own listener is never admitted: %v", own, err)
		}
	}
	// .
	if err := g.Guard(context.Background(), "http://192.168.1.50:8181/"); err != nil {
		t.Fatalf("a device that shares the port is not refused: %v", err)
	}
	if err := g.Guard(context.Background(), "http://127.0.0.1:11434/"); err != nil {
		t.Fatalf("another port on loopback is admitted by the range: %v", err)
	}
}

func TestAGrantedNameMustStayLocalAndIsPinnedForTheDial(t *testing.T) {
	r := &fakeResolver{answers: map[string][]net.IPAddr{
		"homeassistant.local": addrs("192.168.1.10"),
		"evil.local":          addrs("192.168.1.10", "93.184.216.34"),
		"public.local":        addrs("93.184.216.34"),
		"printer.local":       addrs("192.168.1.77"),
	}}
	useResolver(t, r)
	scopes, _ := LocalScopes([]string{"homeassistant.local:8123", "evil.local:*", "public.local:*"})
	g := GuardAdmitting(nil, scopes, nil)
	if err := g.Guard(context.Background(), "http://homeassistant.local:8123/api"); err != nil {
		t.Fatalf("a granted name resolving local is admitted: %v", err)
	}
	if err := g.Guard(context.Background(), "http://homeassistant.local:80/"); !errors.Is(err, ErrEgressBlocked) {
		t.Fatalf("the name's port is part of the grant: %v", err)
	}
	for _, off := range []string{"http://evil.local/", "http://public.local/"} {
		if err := g.Guard(context.Background(), off); !errors.Is(err, ErrEgressBlocked) || !strings.Contains(err.Error(), "not on the local network") {
			t.Fatalf("%s: a name that leaves the local network is refused: %v", off, err)
		}
	}
	// .
	if err := g.Guard(context.Background(), "http://printer.local/"); !errors.Is(err, ErrEgressBlocked) {
		t.Fatalf("an ungranted local name is refused: %v", err)
	}
	// .
	if err := g.Guard(context.Background(), "http://192.168.1.10:8123/"); err != nil {
		t.Fatalf("the address a granted name resolved to is admitted at the dial: %v", err)
	}
	if err := g.Guard(context.Background(), "http://192.168.1.10:9999/"); !errors.Is(err, ErrEgressBlocked) {
		t.Fatalf("the pin is the address AND the port: %v", err)
	}
	ips, ok := g.Pinned("homeassistant.local")
	if !ok || len(ips) != 1 || !ips[0].Equal(net.ParseIP("192.168.1.10")) {
		t.Fatalf("the name is pinned to what it resolved: %v %v", ips, ok)
	}
}

// .
// .
// .
// .
// .
func TestThePinnedDialDoesNotResolveAgain(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			c.Close()
		}
	}()
	port := ln.Addr().(*net.TCPAddr).Port
	r := &fakeResolver{answers: map[string][]net.IPAddr{"device.local": addrs("127.0.0.1")}}
	useResolver(t, r)
	scopes, _ := LocalScopes([]string{"device.local:*"})
	g := GuardAdmitting(nil, scopes, nil)
	if err := g.Guard(context.Background(), "http://device.local:"+itoa(port)+"/"); err != nil {
		t.Fatal(err)
	}
	// .
	r.answers["device.local"] = addrs("93.184.216.34")
	ctx := WithPins(context.Background(), g)
	c, err := guardedDialContext(ctx, "tcp", net.JoinHostPort("device.local", itoa(port)), g.Guard)
	if err != nil {
		t.Fatalf("the pinned dial reaches the address the guard saw: %v", err)
	}
	c.Close()
	if n := r.calls.Load(); n != 1 {
		t.Fatalf("the resolver was consulted %d times; the dial must use the pin, not resolve again", n)
	}
	// .
	// .
	// .
	// .
	rebound := &fakeResolver{answers: map[string][]net.IPAddr{"device.local": addrs("10.9.9.9")}}
	useResolver(t, rebound)
	if _, err := guardedDialContext(context.Background(), "tcp", "device.local:1", g.Guard); !errors.Is(err, ErrEgressBlocked) {
		t.Fatalf("an unpinned dial to a local address no scope covers is refused by the guard: %v", err)
	}
	if ip, _ := netip.ParseAddr("10.9.9.9"); !IsLocalIP(ip.AsSlice()) {
		t.Fatal("10.9.9.9 is a local address for this test's premise")
	}
}

func itoa(n int) string { return strconv.Itoa(n) }
