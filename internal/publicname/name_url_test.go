package publicname

import (
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/certs"
	"github.com/aiii-dot-id/aii-os/internal/dashboard"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

const fixtureName = "abcdefghijklmnopqrstuvwxyz.id.example.test"

func relayCfg(endpoint string) Settings {
	c := Settings{Host: "192.0.2.6"}
	c.Certificate.RouteMode = RouteRelay
	c.Certificate.RelayEndpoint = endpoint
	return c
}

// THE NAME'S URL IS THE NAME AND THE PORT OF WHAT THE NAME LEADS TO: the
// dashboard's own port while the route is direct or disabled, the relay's
// while a relay carries it and the choice names that port. Derived from the
// configuration and the bound port, never stored.
func TestTheNamesPortIsTheRoutes(t *testing.T) {
	direct := Settings{Host: "192.0.2.6"}
	for _, c := range []struct {
		what  string
		cfg   Settings
		bound string
		want  string
	}{
		{"direct", direct, "8180", "8180"},
		{"a relay on the dashboard's own port, no port named", relayCfg("relay.example.test"), "8180", "8180"},
		{"a relay whose port the choice names", relayCfg("relay.example.test:443"), "8180", "443"},
		{"a relay on another named port", relayCfg("relay.example.test:9443"), "8180", "9443"},
		{"disabled keeps the dashboard's port", func() Settings {
			c := relayCfg("relay.example.test:443")
			c.Certificate.RouteMode = RouteDisabled
			return c
		}(), "8180", "8180"},
		{"a mode nobody knows moves nothing", func() Settings {
			c := relayCfg("relay.example.test:443")
			c.Certificate.RouteMode = "sideways"
			return c
		}(), "8180", "8180"},
		{"a configured origin is not the name's", func() Settings { c := direct; c.Origin = "https://front.example.test"; return c }(), "8180", "8180"},
	} {
		if got := namePort(c.cfg, c.bound); got != c.want {
			t.Errorf("%s: the name is reached on %q, want %q", c.what, got, c.want)
		}
	}
}

// THE NAME'S URL AND THE HOST THE DASHBOARD ADMITS FOLLOW THE ROUTE. With a
// relay on 443 and a dashboard on another port, a browser sends the bare
// name: the public-name wiring admitted the name at the dashboard's port
// only, so that request was refused as a foreign Host and no socket's
// Origin passed.
func TestTheNamesURLAndItsHostFollowTheRoute(t *testing.T) {
	dash := dashboard.New("127.0.0.1", 0, &dashboard.WSHandler{})
	r, box := fixtureRuntime(Settings{Host: "192.0.2.6"})
	r.w.Dashboard = func() *dashboard.Server { return dash }
	r.ServeForTest(store.PublicName{Name: fixtureName, Zone: "id.example.test"}, "https://"+fixtureName+":8180", nil)

	r.applyNameURL()
	if got := r.State().Origin; got != "https://"+fixtureName+":8180" {
		t.Fatalf("direct: the name's URL is %q", got)
	}
	if !dash.HostAllowedForTest(fixtureName+":8180") || dash.HostAllowedForTest(fixtureName) {
		t.Fatal("direct: the dashboard admits the name at its own port, and not the bare name")
	}

	box.set(func(s *Settings) {
		s.Certificate.RouteMode, s.Certificate.RelayEndpoint = RouteRelay, "relay.example.test:443"
	})
	r.Signal()
	if got := r.State().Origin; got != "https://"+fixtureName {
		t.Fatalf("a relay on 443: the name's URL is %q, want the bare name", got)
	}
	if got := dash.AdvertisedOrigin(); got != "https://"+fixtureName {
		t.Fatalf("a relay on 443: the dashboard advertises %q", got)
	}
	if !dash.HostAllowedForTest(fixtureName) || !dash.HostAllowedForTest(fixtureName+":443") {
		t.Fatal("a relay on 443: the bare name is what a browser sends, and it is refused")
	}
	if !dash.HostAllowedForTest(fixtureName + ":8180") {
		t.Fatal("the name at the dashboard's own port is this identity's either way, and it stopped being admitted")
	}
	if dash.HostAllowedForTest("relay.example.test") || dash.HostAllowedForTest("stranger.example.test") {
		t.Fatal("a route change admitted a host that is not the name")
	}

	// The way back: the URL returns to the dashboard's port.
	box.set(func(s *Settings) { s.Certificate.RouteMode = RouteDirect })
	r.Signal()
	if got := r.State().Origin; got != "https://"+fixtureName+":8180" {
		t.Fatalf("back to direct: the name's URL is %q", got)
	}
}

func TestARouteChangeLeavesAConfiguredOriginAlone(t *testing.T) {
	dash := dashboard.New("127.0.0.1", 0, &dashboard.WSHandler{})
	dash.SetOrigin("https://front.example.test")
	r, box := fixtureRuntime(Settings{Host: "192.0.2.6", Origin: "https://front.example.test"})
	r.w.Dashboard = func() *dashboard.Server { return dash }
	r.ServeForTest(store.PublicName{Name: fixtureName, Zone: "id.example.test"}, "https://"+fixtureName+":8180", nil)
	box.set(func(s *Settings) {
		s.Certificate.RouteMode, s.Certificate.RelayEndpoint = RouteRelay, "relay.example.test:443"
	})
	r.Signal()
	if got := dash.AdvertisedOrigin(); got != "https://front.example.test" {
		t.Fatalf("the configured origin was overwritten with %q", got)
	}
	if dash.HostAllowedForTest("front.example.test") || dash.HostAllowedForTest("front.example.test:443") {
		t.Fatal("the configured origin's host joined the allowed list")
	}
	// The name's own URL still follows the route: the sign-in link and the
	// card read it, whatever the dashboard advertises.
	if got := r.State().Origin; got != "https://"+fixtureName {
		t.Fatalf("the name's URL is %q", got)
	}
}

func TestARelayChoiceThatMovesTheNamesURLNamesThePort(t *testing.T) {
	service := certs.ServiceStatus{
		Capabilities:   []string{certs.CapabilityRecordSets, certs.CapabilityRelayV2},
		RecordSetTypes: []string{certs.RecordA, certs.RecordAAAA, certs.RecordCNAME},
		RelayEndpoints: []certs.RelayEndpoint{{Name: "relay.example.test", Port: 443}, {Name: "near.example.test", Port: 8180}},
	}
	r, _ := fixtureRuntime(Settings{})
	r.ServeForTest(store.PublicName{Name: fixtureName, Zone: "id.example.test"}, "https://"+fixtureName+":8180", &service)
	direct := Settings{Host: "192.0.2.6"}

	// 8180 -> the relay on 443.
	err := r.RelayChoiceRefusal(direct, relayCfg("relay.example.test"))
	if err == nil {
		t.Fatal("a relay on 443 was chosen from a dashboard on 8180 without naming the port")
	}
	for _, want := range []string{"https://" + fixtureName + ":8180", "https://" + fixtureName, "relay.example.test:443"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("the refusal does not say %q: %v", want, err)
		}
	}
	if err := r.RelayChoiceRefusal(direct, relayCfg("relay.example.test:443")); err != nil {
		t.Fatalf("the move, its port named: %v", err)
	}
	// A relay on the dashboard's own port moves nothing and names nothing.
	if err := r.RelayChoiceRefusal(direct, relayCfg("near.example.test")); err != nil {
		t.Fatalf("a relay on the dashboard's own port: %v", err)
	}
	// The relay on 443 -> the relay on 8180: the second move.
	on443 := relayCfg("relay.example.test:443")
	if err := r.RelayChoiceRefusal(on443, relayCfg("near.example.test")); err == nil {
		t.Fatal("the name moved from the relay on 443 to a relay on 8180 with nothing said")
	}
	if err := r.RelayChoiceRefusal(on443, relayCfg("near.example.test:8180")); err != nil {
		t.Fatalf("the second move, its port named: %v", err)
	}
	// The same relay chosen again is no move.
	if err := r.RelayChoiceRefusal(on443, on443); err != nil {
		t.Fatalf("the route already in force: %v", err)
	}
}
