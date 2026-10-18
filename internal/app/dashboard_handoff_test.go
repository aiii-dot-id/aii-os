package app

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/dashboard"
)

func TestDashboardHandoffUsesTheServingListenerNotFutureConfig(t *testing.T) {
	for _, host := range []string{"127.0.0.1", "::1", "0.0.0.0", "::"} {
		for _, secure := range []bool{false, true} {
			t.Run(host+dashboard.SchemeFor(secure), func(t *testing.T) {
				cfg := defaultConfig()
				cfg.Dashboard.Host = host
				cfg.Dashboard.Port = 0
				cfg.Dashboard.TLS = secure
				a := New(cfg)
				defer stopApp(t, a)
				if got := a.DashboardURL(); got != "" {
					t.Errorf("unstarted runtime advertised %q", got)
				}
				a.dashboard = dashboard.New(host, 0, &dashboard.WSHandler{})
				if got := a.DashboardURL(); got != "" {
					t.Errorf("unbound runtime advertised %q", got)
				}
				dir := ""
				if secure {
					dir = t.TempDir()
				}
				if _, err := a.dashboard.Start(dir); err != nil {
					t.Fatal(err)
				}
				a.dashboard.SetOrigin("https://public.example.invalid")
				got := a.DashboardURL()
				u, err := url.Parse(got)
				if err != nil || u.Scheme != dashboard.SchemeFor(secure) || u.Port() != a.dashboard.BoundPort() || net.ParseIP(u.Hostname()) == nil || !net.ParseIP(u.Hostname()).IsLoopback() {
					t.Fatalf("handoff %q does not identify serving local listener %q: %v", got, a.dashboard.BoundAddr(), err)
				}
				boundHost, _, _ := net.SplitHostPort(a.dashboard.BoundAddr())
				if net.ParseIP(boundHost).To4() == nil && net.ParseIP(u.Hostname()).To4() != nil {
					t.Fatal("IPv6 listener handoff assumed IPv4 availability")
				}
				// Saved next-start settings cannot move this listener or its URL.
				a.cfgMu.Lock()
				a.cfg.Dashboard.Host = "192.0.2.1"
				a.cfg.Dashboard.Port = 1
				a.cfg.Dashboard.TLS = !secure
				a.cfgMu.Unlock()
				if after := a.DashboardURL(); after != got {
					t.Fatalf("saved future config moved URL from %q to %q", got, after)
				}
				if secure && (host == "0.0.0.0" || host == "::") {
					return
				}
				assertHandoffResponds(t, a, secure)
			})
		}
	}
}

func assertHandoffResponds(t *testing.T, a *App, secure bool) {
	t.Helper()
	transport := &http.Transport{}
	if secure {
		root, err := os.ReadFile(a.dashboard.TLSMaterial().CACertPath)
		if err != nil {
			t.Fatal(err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(root) {
			t.Fatal("invalid fixture root")
		}
		transport.TLSClientConfig = &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}
	}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 5 * time.Second}
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, a.DashboardURL()+"auth/token", nil)
	if err != nil {
		t.Fatal(err)
	}
	res, err := client.Do(req)
	if err != nil {
		t.Fatalf("shell URL is not reachable: %v", err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusNoContent && res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("shell URL did not reach dashboard authentication: %d", res.StatusCode)
	}
}

// bindableIPv4 is an IPv4 address of this host that a listener can bind:
// on an interface that is up and running, and proved by binding it, or ""
// when there is none. The host's address list is not that — Windows lists
// a disconnected adapter's 169.254 address too, and binding it fails with
// "The requested address is not valid in its context".
func bindableIPv4(t *testing.T) string {
	t.Helper()
	ifaces, err := net.Interfaces()
	if err != nil {
		t.Fatal(err)
	}
	for _, ifi := range ifaces {
		if ifi.Flags&net.FlagUp == 0 || ifi.Flags&net.FlagRunning == 0 || ifi.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := ifi.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			n, ok := a.(*net.IPNet)
			if !ok || n.IP.To4() == nil || n.IP.IsLoopback() {
				continue
			}
			if ln, err := net.Listen("tcp", net.JoinHostPort(n.IP.String(), "0")); err == nil {
				ln.Close()
				return n.IP.String()
			}
		}
	}
	return ""
}

func TestDashboardHandoffUsesExistingPlainNetworkCompanion(t *testing.T) {
	host := bindableIPv4(t)
	if host == "" {
		t.Skip("no IPv4 network interface for the companion-listener probe")
	}
	cfg := defaultConfig()
	cfg.Dashboard.Host = host
	cfg.Dashboard.Port = 0
	cfg.Dashboard.TLS = true
	a := New(cfg)
	defer stopApp(t, a)
	a.dashboard = dashboard.New(host, 0, &dashboard.WSHandler{})
	if _, err := a.dashboard.Start(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	want := "http://" + net.JoinHostPort("127.0.0.1", a.dashboard.BoundPort()) + "/"
	if got := a.DashboardURL(); got != want {
		t.Fatalf("shell ignored existing plain companion: got %q want %q", got, want)
	}
	assertHandoffResponds(t, a, false)
	if !cfg.Dashboard.TLS || a.dashboard.Scheme() != "https" {
		t.Fatal("local handoff disabled network TLS")
	}
}

func TestDashboardHandoffAcrossEmbeddedBootModesAndRestart(t *testing.T) {
	for _, mode := range []string{"firstboot", "live", "safe"} {
		t.Run(mode, func(t *testing.T) {
			dir := t.TempDir()
			var cfg *Config
			if mode == "firstboot" {
				cfg = defaultConfig()
				cfg.SourcePath = filepath.Join(dir, "config.json")
				cfg.Logs.Dir = filepath.Join(dir, "log") // beside the rig's data, not in the package directory
				cfg.Identity.KeyPath = filepath.Join(dir, "identity.sec")
				cfg.Identity.LedgerPath = filepath.Join(dir, "ledger.jsonl")
				cfg.Identity.DBPath = filepath.Join(dir, "aii.db")
				cfg.Tools.CWD = dir
				// Genesis refuses at once — the client never retries a 4xx —
				// so this measures the handoff, not the client's backoff:
				// an unreachable port slept through 2+4 s of retries per
				// fetch, about 24 s across the two starts.
				refuse := httptest.NewServer(http.NotFoundHandler())
				t.Cleanup(refuse.Close)
				cfg.Genesis = GenesisConfig{ServerURL: refuse.URL, FirewallURL: refuse.URL, BootstrapURL: refuse.URL}
			} else {
				key, record, db := birthFixture(t, dir, "URL fixture")
				buildPriorProjection(t, record, db)
				cfg = safebootConfig(t, dir, "URL fixture", key, record, db)
				if mode == "safe" {
					tamperChain(t, key, record)
				}
			}
			cfg.Dashboard.Host = "127.0.0.1"
			cfg.Dashboard.Port = 0
			cfg.Dashboard.TLS = false
			cfg.Certificate.ServerURL = "none"
			for start := 0; start < 2; start++ {
				a := New(cfg)
				if err := a.StartEmbedded(); err != nil {
					stopApp(t, a)
					t.Fatal(err)
				}
				func() {
					defer stopApp(t, a)
					assertHandoffResponds(t, a, cfg.Dashboard.TLS)
					if mode == "live" && start == 0 {
						before := a.DashboardURL()
						port, _ := strconv.Atoi(a.dashboard.BoundPort())
						if _, err := a.applyConfigChange(map[string]any{"dashboard.port": float64(port), "dashboard.tls": true}); err != nil {
							t.Fatal(err)
						}
						if got := a.DashboardURL(); got != before {
							t.Fatalf("successful next-start save moved live URL: %q -> %q", before, got)
						}
						if _, err := a.applyConfigChange(map[string]any{"dashboard.port": float64(-1)}); err == nil {
							t.Fatal("invalid port was accepted")
						}
						if got := a.DashboardURL(); got != before {
							t.Fatal("failed settings change moved live URL")
						}
						assertHandoffResponds(t, a, false)
					}
					if mode == "safe" {
						if _, safe := a.SafeMode(); !safe {
							t.Fatal("SAFE fixture did not boot SAFE")
						}
					}
				}()
			}
		})
	}
}

func TestFailedEmbeddedBindDoesNotInventADashboardURL(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	_, p, _ := net.SplitHostPort(ln.Addr().String())
	port, _ := strconv.Atoi(p)
	dir := t.TempDir()
	key, record, db := birthFixture(t, dir, "Failed URL")
	buildPriorProjection(t, record, db)
	cfg := safebootConfig(t, dir, "Failed URL", key, record, db)
	cfg.Dashboard.Port = port
	a := New(cfg)
	defer stopApp(t, a)
	if err := a.StartEmbedded(); err == nil {
		t.Fatal("occupied listener unexpectedly started")
	}
	if got := a.DashboardURL(); got != "" {
		t.Fatalf("failed startup advertised %q", got)
	}
}
