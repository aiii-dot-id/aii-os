package app

import (
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/dashboard"
	"github.com/aiii-dot-id/aii-os/internal/publicname"
	"github.com/aiii-dot-id/aii-os/internal/publicname/publicnametest"
)

// The construction path hands the configured origin to the dashboard, and
// every reader of the advertised origin says it.
func TestAConfiguredOriginIsTheOneTheDashboardAdvertises(t *testing.T) {
	build := func(origin string) *App {
		cfg := defaultConfig()
		cfg.SourcePath = filepath.Join(t.TempDir(), "config.json")
		cfg.Identity.KeyPath = filepath.Join(filepath.Dir(cfg.SourcePath), "identity.sec")
		cfg.Dashboard = DashboardConfig{Host: "127.0.0.1", Port: 8180, Origin: origin}
		a := newBareApp(t, cfg)
		d, err := a.newDashboard(&dashboard.WSHandler{})
		if err != nil {
			t.Fatal(err)
		}
		a.dashboard = d
		return a
	}

	a := build("https://front.example.test/")
	const want = "https://front.example.test"
	if got := a.dashboard.Origin(); got != want {
		t.Errorf("the dashboard advertises %q, the operator configured %q", got, want)
	}
	if got := a.advertisedOrigin(); got != want {
		t.Errorf("the app's advertised origin is %q, want %q", got, want)
	}
	if got := a.webhookURL("com.example.mail", "notify"); got != want+"/hooks/com.example.mail/notify" {
		t.Errorf("a plugin is told its webhook is at %q", got)
	}
	if got := a.configState().Dashboard.Origin; got != want {
		t.Errorf("the page is told the origin is %q, want %q", got, want)
	}

	// The control: with none configured, the origin is the socket's, as it
	// was. The fix adds an origin only where the operator wrote one.
	if got := build("").dashboard.Origin(); got != "http://127.0.0.1:8180" {
		t.Errorf("with no origin configured the dashboard advertises %q, want its socket", got)
	}
}

// The public name leaves a configured origin alone (it sets its own only
// when none is configured), so the two together used to advertise neither.
// Claimed beside a configured origin, the name is still served and still an
// allowed host; what is advertised is the operator's origin, before the
// claim and after it.
func TestAClaimedPublicNameLeavesAConfiguredOriginInPlace(t *testing.T) {
	const front = "https://front.example.test:8443"
	dir := t.TempDir()
	cfg := birthNamed(t, dir)
	cfg.Dashboard.Origin = front
	pub := &publicnametest.Publisher{Zone: "example.test"}
	var managers []*publicnametest.Manager
	app := bootNamed(t, cfg, pub, &managers)
	defer stopApp(t, app)

	if got := app.dashboard.Origin(); got != front {
		t.Fatalf("before the claim the dashboard advertises %q, the operator configured %q", got, front)
	}
	st, err := app.pn.Claim()
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	waitStatus(t, app, publicname.StatusIssued)
	if got := app.dashboard.Origin(); got != front {
		t.Errorf("after the claim the dashboard advertises %q, the operator configured %q", got, front)
	}
	addr := app.dashboard.BoundAddr()
	_, port, _ := net.SplitHostPort(addr)
	if got := servedCert(t, addr, st.Name); got.VerifyHostname(st.Name) != nil {
		t.Error("the dashboard does not serve the public certificate for the claimed name")
	}
	if !app.dashboard.HostAllowedForTest(net.JoinHostPort(st.Name, port)) {
		t.Error("the claimed name is not an allowed host")
	}
	// The host gate is unchanged by a configured origin: its host is not
	// let through on that account.
	if app.dashboard.HostAllowedForTest("front.example.test:8443") {
		t.Error("a configured origin's host was added to the host gate")
	}
}

func TestAnIdentityWithNoPublicOriginAdvertisesNone(t *testing.T) {
	cfg := defaultConfig()
	cfg.SourcePath = filepath.Join(t.TempDir(), "config.json")
	cfg.Identity.KeyPath = filepath.Join(filepath.Dir(cfg.SourcePath), "identity.sec")
	cfg.Dashboard = DashboardConfig{Host: "127.0.0.1", Port: 8180}
	a := newBareApp(t, cfg)
	d, err := a.newDashboard(&dashboard.WSHandler{})
	if err != nil {
		t.Fatal(err)
	}
	a.dashboard = d

	if got := a.dashboard.Origin(); got != "http://127.0.0.1:8180" {
		t.Errorf("the dashboard answers at %q, want its socket", got)
	}
	if got := a.dashboard.AdvertisedOrigin(); got != "" {
		t.Errorf("the dashboard was given no origin to advertise, yet advertises %q", got)
	}
	if got := a.advertisedOrigin(); got != "" {
		t.Errorf("the app's advertised origin is %q; a socket is not a public origin", got)
	}
	if got := a.webhookURL("com.example.mail", "notify"); got != "" {
		t.Errorf("a plugin is told its webhook is at %q; with no public origin it is told none", got)
	}
	if got := a.configState().Dashboard.Origin; got != "" {
		t.Errorf("the page is told the advertised origin is %q; none is advertised", got)
	}
}

// A dashboard.origin that is not an origin is refused where the file is
// read, by name, and says what is wrong without repeating the value. It is
// advertised as written — the page, a plugin's webhook URL and the audience
// a push is verified against are built from it — so nothing that is not
// scheme://host[:port] may get as far as being advertised. One closing
// slash is admitted: SetOrigin drops it.
func TestADashboardOriginThatIsNotAnOriginIsRefusedAtLoad(t *testing.T) {
	load := func(origin string) (*Config, error) {
		t.Helper()
		raw, err := json.Marshal(map[string]any{"dashboard": map[string]any{"origin": origin}})
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(t.TempDir(), "config.json")
		if err := os.WriteFile(path, raw, 0o600); err != nil {
			t.Fatal(err)
		}
		return ReadConfig(path)
	}

	for _, origin := range []string{
		"",
		"https://front.example.test",
		"https://front.example.test/",
		"https://front.example.test:8443",
		"http://10.0.0.7:8180",
		"https://[2001:db8::1]:8443",
		"https://xn--bcher-kva.example.test",
	} {
		cfg, err := load(origin)
		if err != nil {
			t.Errorf("%q is an origin, yet the file was refused: %v", origin, err)
			continue
		}
		if cfg.Dashboard.Origin != origin {
			t.Errorf("%q was read as %q", origin, cfg.Dashboard.Origin)
		}
	}

	for origin, why := range map[string]string{
		"front.example.test":                  "does not begin http:// or https://",
		"ftp://front.example.test":            "does not begin http:// or https://",
		"HTTPS://front.example.test":          "does not begin http:// or https://",
		"https:front.example.test":            "does not begin http:// or https://",
		" https://front.example.test":         "has space around it",
		"https://front.example.test ":         "has space around it",
		"https://":                            "names no host",
		"https://:8443":                       "names no host",
		"https://user@front.example.test":     "carries a user name",
		"https://u:secret@front.example.test": "carries a user name",
		"https://front.example.test/ui":       "has a path",
		"https://front.example.test//":        "has a path",
		"https://front.example.test?x=1":      "has a query",
		"https://front.example.test/?":        "has a query",
		"https://front.example.test#top":      "has a fragment",
		"https://front.example.test/#":        "has a fragment",
		"https://front.example.test:":         "has a port that is not a plain number from 1 to 65535",
		"https://front.example.test:0":        "has a port that is not a plain number from 1 to 65535",
		"https://front.example.test:70000":    "has a port that is not a plain number from 1 to 65535",
		"https://front.example.test:08443":    "has a port that is not a plain number from 1 to 65535",
		"https://front.example.test:8443x":    "does not parse as a URL",
		"https://front example.test":          "does not parse as a URL",
	} {
		cfg, err := load(origin)
		if err == nil {
			t.Errorf("%q is not an origin, yet the file was read (origin %q)", origin, cfg.Dashboard.Origin)
			continue
		}
		msg := err.Error()
		if !strings.HasPrefix(msg, "dashboard.origin must be an origin") || !strings.HasSuffix(msg, "the configured value "+why) {
			t.Errorf("%q: the refusal is %q, want it to name dashboard.origin and end %q", origin, msg, why)
		}
		if strings.Contains(msg, "secret") || strings.Contains(msg, "front") {
			t.Errorf("%q: the refusal repeats the value: %q", origin, msg)
		}
	}
}
