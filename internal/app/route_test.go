package app

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/certs"
	"github.com/aiii-dot-id/aii-os/internal/relay"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

func routeCfg(host, origin, mode string) Config {
	c := Config{}
	c.Dashboard.Host = host
	c.Dashboard.Origin = origin
	c.Certificate.RouteMode = mode
	return c
}

func TestRouteModeRefusesWhatItCannotDo(t *testing.T) {
	for _, tc := range []struct {
		in, want string
		ok       bool
	}{
		{"", routeDirect, true},
		{"direct", routeDirect, true},
		{"relay", routeRelay, true},
		{"disabled", routeDisabled, true},
		{" direct ", routeDirect, true},
		{"Direct", "", false},
		{"rely", "", false},
		{"off", "", false},
	} {
		got, err := routeMode(routeCfg("", "", tc.in))
		if tc.ok != (err == nil) || (tc.ok && got != tc.want) {
			t.Fatalf("route_mode %q: got %q %v", tc.in, got, err)
		}
	}
}

func TestDirectRouteIsTheWholeAnswerNotTheFirstInterface(t *testing.T) {
	// .
	for _, tc := range []struct{ host, kind, value string }{
		{"192.0.2.6", certs.RecordA, "192.0.2.6"},
		{"fd00::6", certs.RecordAAAA, "fd00::6"},
	} {
		got, err := directRoute(routeCfg(tc.host, "", ""))
		if err != nil || len(got) != 1 || got[0].Type != tc.kind || got[0].Value != tc.value {
			t.Fatalf("bind %s: %v %v", tc.host, got, err)
		}
	}
	// .
	got, err := directRoute(routeCfg("127.0.0.1", "", ""))
	if err != nil || len(got) != 0 {
		t.Fatalf("loopback: %v %v", got, err)
	}
	// .
	// .
	explicit := []string{"203.0.113.9", "fd00::9"}
	cfg := routeCfg("0.0.0.0", "", "")
	cfg.Certificate.DirectAddresses = &explicit
	got, err = directRoute(cfg)
	if err != nil || len(got) != 2 {
		t.Fatalf("explicit: %v %v", got, err)
	}
	// .
	// .
	empty := []string{}
	cfg.Certificate.DirectAddresses = &empty
	got, err = directRoute(cfg)
	if err != nil || len(got) != 0 {
		t.Fatalf("explicitly empty: %v %v", got, err)
	}
	// .
	bad := []string{"not-an-address"}
	cfg.Certificate.DirectAddresses = &bad
	if _, err := directRoute(cfg); err == nil {
		t.Fatal("a non-address was accepted into direct_addresses")
	}
}

func TestAWildcardBindDoesNotGuessBetweenInterfaces(t *testing.T) {
	saved := interfaceAddrs
	t.Cleanup(func() { interfaceAddrs = saved })
	mk := func(cidrs ...string) func() ([]net.Addr, error) {
		return func() ([]net.Addr, error) {
			out := make([]net.Addr, 0, len(cidrs))
			for _, c := range cidrs {
				ip, n, err := net.ParseCIDR(c)
				if err != nil {
					t.Fatal(err)
				}
				n.IP = ip
				out = append(out, n)
			}
			return out, nil
		}
	}
	cfg := routeCfg("0.0.0.0", "", "")

	// .
	interfaceAddrs = mk("10.0.0.5/24", "fd00::5/64", "127.0.0.1/8", "fe80::1/64")
	got, err := directRoute(cfg)
	if err != nil || len(got) != 2 {
		t.Fatalf("dual stack: %v %v", got, err)
	}

	// .
	// .
	interfaceAddrs = mk("10.0.0.5/24", "192.168.1.5/24")
	_, err = directRoute(cfg)
	if err == nil {
		t.Fatal("a wildcard bind guessed between two IPv4 addresses")
	}
	for _, want := range []string{"10.0.0.5", "192.168.1.5", "direct_addresses"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("the error does not say what to do (%q): %v", want, err)
		}
	}

	// .
	// .
	interfaceAddrs = func() ([]net.Addr, error) { return nil, errors.New("no") }
	got, err = directRoute(cfg)
	if err == nil {
		t.Fatalf("an enumeration failure became an empty route: %v", got)
	}
}

func TestRelayModeWillNotBridgeAPortOrInventARelay(t *testing.T) {
	st := certs.ServiceStatus{
		Capabilities:   []string{certs.CapabilityRecordSets, certs.CapabilityRelayV2},
		RecordSetTypes: []string{certs.RecordA, certs.RecordAAAA, certs.RecordCNAME},
		RelayEndpoints: []certs.RelayEndpoint{{Name: "relay.example.invalid", Port: 8181}},
	}
	a := &App{}
	cfg := routeCfg("192.0.2.6", "https://name.aiios.id:8181", routeRelay)

	// .
	if _, err := a.desiredRoute(cfg, routeRelay, st); err == nil {
		t.Fatal("relay mode with no endpoint was accepted")
	}
	// .
	cfg.Certificate.RelayEndpoint = "relay.elsewhere.invalid"
	if _, err := a.desiredRoute(cfg, routeRelay, st); err == nil {
		t.Fatal("an unadvertised relay was selectable")
	}
	// .
	cfg.Certificate.RelayEndpoint = "relay.example.invalid"
	got, err := a.desiredRoute(cfg, routeRelay, st)
	if err != nil || len(got) != 1 || got[0].Type != certs.RecordCNAME || got[0].Value != "relay.example.invalid" {
		t.Fatalf("relay: %v %v", got, err)
	}
	// .
	// .
	// .
	cfg.Dashboard.Origin = "https://name.aiios.id:9999"
	_, err = a.desiredRoute(cfg, routeRelay, st)
	if err == nil {
		t.Fatal("a relay on the wrong port was accepted")
	}
	if !strings.Contains(err.Error(), "9999") || !strings.Contains(err.Error(), "8181") {
		t.Fatalf("the error does not name both ports: %v", err)
	}
	// .
	if _, err := a.desiredRoute(cfg, routeRelay, certs.ServiceStatus{
		Capabilities: []string{certs.CapabilityRecordSets},
	}); err == nil {
		t.Fatal("relay mode was accepted against a service with no relays")
	}
	// .
	got, err = a.desiredRoute(cfg, routeDisabled, st)
	if err != nil || got == nil || len(got) != 0 {
		t.Fatalf("disabled: %v %v", got, err)
	}
}

func TestSameRouteIgnoresOrderAndNotContent(t *testing.T) {
	have := []certs.LeasedRecord{
		{Type: certs.RecordAAAA, Value: "fd00::2", ExpiresAt: "2026-09-13T12:00:00Z"},
		{Type: certs.RecordA, Value: "10.0.0.2", ExpiresAt: "2026-09-13T12:00:00Z"},
	}
	if !sameRoute(have, []certs.Record{
		{Type: certs.RecordA, Value: "10.0.0.2"}, {Type: certs.RecordAAAA, Value: "fd00::2"},
	}) {
		t.Fatal("the same route in another order read as a change")
	}
	if sameRoute(have, []certs.Record{{Type: certs.RecordA, Value: "10.0.0.2"}}) {
		t.Fatal("a dropped member read as no change")
	}
	if sameRoute(nil, []certs.Record{{Type: certs.RecordA, Value: "10.0.0.2"}}) {
		t.Fatal("an empty route matched a populated one")
	}
	if !sameRoute(nil, []certs.Record{}) {
		t.Fatal("two empty routes differed")
	}
}

func TestALeaseIsRenewedBeforeItLapsesAndATombstoneIsNot(t *testing.T) {
	lease := 14 * time.Minute
	at := func(d time.Duration) []certs.LeasedRecord {
		return []certs.LeasedRecord{{Type: certs.RecordA, Value: "10.0.0.2",
			ExpiresAt: time.Now().Add(d).UTC().Format(time.RFC3339)}}
	}
	if routeNeedsRenewal(certs.RecordSet{Managed: true, Generation: 2, Records: at(13 * time.Minute)}, lease) {
		t.Fatal("a fresh lease was renewed")
	}
	if !routeNeedsRenewal(certs.RecordSet{Managed: true, Generation: 2, Records: at(2 * time.Minute)}, lease) {
		t.Fatal("a lease about to lapse was not renewed")
	}
	if !routeNeedsRenewal(certs.RecordSet{Managed: true, Generation: 2, Records: at(-time.Minute)}, lease) {
		t.Fatal("an expired record satisfied the route")
	}
	// .
	if !routeNeedsRenewal(certs.RecordSet{Managed: true, Generation: 2,
		Records: []certs.LeasedRecord{{Type: certs.RecordA, Value: "10.0.0.2", ExpiresAt: "soon"}}}, lease) {
		t.Fatal("an unreadable lease was treated as live")
	}
	// .
	// .
	// .
	// .
	// .
	// .
	if routeNeedsRenewal(certs.RecordSet{Managed: true, Generation: 2, Records: at(8 * time.Minute)}, lease) {
		t.Fatal("a lease with eight of fourteen minutes left was renewed — that is thirteen writes an hour for one name")
	}
	// .
	// .
	// .
	// .
	// .
	// .
	if routeNeedsRenewal(certs.RecordSet{Managed: true, Generation: 2, Records: at(8 * time.Minute)}, lease) {
		t.Fatal("a lease with eight of fourteen minutes left was renewed — that is thirteen writes an hour for one name")
	}
	// .
	// .
	if routeNeedsRenewal(certs.RecordSet{Managed: true, Generation: 3}, lease) {
		t.Fatal("a tombstone was renewed")
	}
	// .
	if !routeNeedsRenewal(certs.RecordSet{}, lease) {
		t.Fatal("an unpublished name was left alone")
	}
}

// .
// .
// .
func TestTheNextPassIsTimedFromTheLease(t *testing.T) {
	at := func(d time.Duration) certs.RecordSet {
		return certs.RecordSet{Managed: true, Generation: 2, Records: []certs.LeasedRecord{
			{Type: certs.RecordA, Value: "10.0.0.2", ExpiresAt: time.Now().Add(d).UTC().Format(time.RFC3339)}}}
	}
	// .
	short := 280 * time.Second
	got := routeSettleDelay(at(short), short)
	if got > short {
		t.Fatalf("the next pass (%v) falls after the lease (%v) lapses", got, short)
	}
	if got > short-short/routeRenewOf+time.Second {
		t.Fatalf("the next pass (%v) is later than the renewal point", got)
	}
	// .
	// .
	long := 14 * time.Minute
	if got := routeSettleDelay(at(long), long); got > routeIdle {
		t.Fatalf("the wait exceeded the idle cap: %v", got)
	}
	// .
	if got := routeSettleDelay(at(-time.Minute), long); got < routeDebounce {
		t.Fatalf("an overdue lease spins: %v", got)
	}
	// .
	if got := routeSettleDelay(certs.RecordSet{Managed: true, Generation: 3}, long); got != routeIdle {
		t.Fatalf("a tombstone was scheduled early: %v", got)
	}
}

// .
// .
// .
// .
func TestDisablingStopsTheRelayWithoutTheService(t *testing.T) {
	cfg := &Config{}
	cfg.Certificate.RouteMode = routeDisabled
	app := &App{cfg: cfg}
	stopped := false
	app.pn.relayCancel = func() { stopped = true }
	// .
	app.pn.publisher = &stubNamePublisher{fail: errors.New("certificate service is unreachable")}
	app.pn.name = &store.PublicName{Name: "x.example.test", Zone: "example.test"}

	if _, err := app.reconcileRoute(context.Background()); err == nil {
		t.Fatal("an unreachable service reported success")
	}
	if !stopped {
		t.Fatal("disabling left the relay running because certd was unavailable")
	}
	if app.pn.relay != nil || app.pn.relayCancel != nil {
		t.Fatal("the relay was not released")
	}
}

// .
// .
// .
// .
func TestAServiceDelayIsKeptWhicheverCallNamedIt(t *testing.T) {
	app := &App{cfg: &Config{}}
	limited := &certs.RecordSetError{Op: "read", Status: 429, Message: "slow down", RetryAfter: time.Hour}
	if got := app.serviceDelay(limited, time.Minute); got != time.Hour {
		t.Fatalf("the service asked for an hour and got %v", got)
	}
	if left := time.Until(app.routeFloor()); left < 50*time.Minute {
		t.Fatalf("the floor was not held: %v left", left)
	}
	// .
	if got := app.serviceDelay(errors.New("connection reset"), time.Minute); got != time.Minute {
		t.Fatalf("an untimed failure changed the retry: %v", got)
	}
	// .
	if left := time.Until(app.routeFloor()); left < 50*time.Minute {
		t.Fatalf("an untimed failure cleared the service's floor: %v", left)
	}
}

// .
// .
// .
func TestAPassCannotActOnAnIntentTheOperatorChanged(t *testing.T) {
	cfg := &Config{}
	cfg.Certificate.RouteMode = routeDisabled
	app := &App{cfg: cfg}
	stopped := false
	app.pn.relay = &relay.Client{Name: "n.example.test", Relay: "relay.example.invalid:8181"}
	app.pn.relayCancel = func() { stopped = true }

	app.afterRoute(*cfg, routeRelay, "n.example.test",
		certs.RecordSet{Managed: true, Generation: 2, Records: []certs.LeasedRecord{
			{Type: certs.RecordCNAME, Value: "relay.example.invalid",
				ExpiresAt: time.Now().Add(time.Hour).UTC().Format(time.RFC3339)}}},
		certs.ServiceStatus{RelayEndpoints: []certs.RelayEndpoint{{Name: "relay.example.invalid", Port: 8181}}}, 0)

	if !stopped {
		t.Fatal("a late pass kept a tunnel the operator had switched off")
	}
	if app.pn.relay != nil {
		t.Fatal("the relay was not released")
	}
}

// .
// .
// .
// .
func TestAHealthyTunnelSurvivesASettledPass(t *testing.T) {
	app := &App{cfg: &Config{}}
	cancels := 0
	existing := &relay.Client{Name: "n.example.test", Relay: "relay.example.invalid:8181"}
	app.pn.relay = existing
	app.pn.relayCancel = func() { cancels++ }

	app.startRelay(*app.cfg, "n.example.test", certs.RelayEndpoint{Name: "relay.example.invalid", Port: 8181})
	if cancels != 0 || app.pn.relay != existing {
		t.Fatalf("the same name at the same relay was torn down (cancels=%d)", cancels)
	}
	// .
	app.startRelay(*app.cfg, "n.example.test", certs.RelayEndpoint{Name: "other.example.invalid", Port: 8181})
	if cancels != 1 {
		t.Fatalf("a changed endpoint did not replace the tunnel (cancels=%d)", cancels)
	}
}

// .
// .
// .
// .
func TestARateLimitedReadKeepsTheServicesDelay(t *testing.T) {
	cfg := &Config{}
	app := &App{cfg: cfg}
	app.pn.publisher = &stubNamePublisher{
		zone:     "example.test",
		readFail: &certs.RecordSetError{Op: "read", Status: 429, Message: "slow down", RetryAfter: time.Hour},
	}
	app.pn.name = &store.PublicName{Name: "x.example.test", Zone: "example.test"}

	next, err := app.reconcileRoute(context.Background())
	if err == nil {
		t.Fatal("a rate-limited read reported success")
	}
	if next < 50*time.Minute {
		t.Fatalf("the service asked for an hour and the next pass is in %v", next)
	}
	if left := time.Until(app.routeFloor()); left < 50*time.Minute {
		t.Fatalf("no floor was held against a local wake: %v", left)
	}
}

// .
// .
// .
// .
func TestAShortLeaseIsNeverScheduledPastItsExpiry(t *testing.T) {
	at := func(d time.Duration) certs.RecordSet {
		return certs.RecordSet{Managed: true, Generation: 2, Records: []certs.LeasedRecord{
			{Type: certs.RecordA, Value: "10.0.0.2", ExpiresAt: time.Now().Add(d).UTC().Format(time.RFC3339)}}}
	}
	// .
	got := routeSettleDelay(at(12*time.Second), 14*time.Second)
	if got > 12*time.Second {
		t.Fatalf("the next pass (%v) falls after the route lapses (12s)", got)
	}
	if got <= 0 {
		t.Fatalf("the next pass must still be in the future: %v", got)
	}
}

// .
// .
func TestALifetimeTheBudgetCannotHoldIsRefused(t *testing.T) {
	// .
	short := certs.ServiceStatus{
		Zone: "example.test", MaxPublishLifetime: 15 * time.Second, MaxPublishesPerHour: 20,
		Capabilities: []string{certs.CapabilityRecordSets}, RecordSetTypes: []string{certs.RecordA},
	}
	if perHour, ok := routeSustainable(short); ok {
		t.Fatalf("a fifteen-second lifetime was called sustainable (%d writes an hour)", perHour)
	}
	// .
	live := certs.ServiceStatus{
		Zone: "aiios.id", MaxPublishLifetime: 900 * time.Second, MaxPublishesPerHour: 20,
		Capabilities: []string{certs.CapabilityRecordSets}, RecordSetTypes: []string{certs.RecordA},
	}
	perHour, ok := routeSustainable(live)
	if !ok {
		t.Fatalf("the deployed service was called unsustainable (%d an hour)", perHour)
	}
	// .
	// .
	if perHour != 7 {
		t.Fatalf("the peak-hour count should be seven, got %d", perHour)
	}
}

// .
// .
// .
// .
func TestDisablingStopsTheRelayWhileAPassIsStillOnTheWire(t *testing.T) {
	cfg := &Config{}
	cfg.Certificate.RouteMode = routeRelay
	app := &App{cfg: cfg}
	stopped := make(chan struct{}, 1)
	app.pn.relay = &relay.Client{Name: "n.example.test", Relay: "relay.example.invalid:8181"}
	app.pn.relayCancel = func() { stopped <- struct{}{} }
	app.pn.routeWake = make(chan struct{}, 1)
	// .
	passCancelled := make(chan struct{}, 1)
	app.pn.routePassCancel = func() { passCancelled <- struct{}{} }

	// .
	app.cfgMu.Lock()
	app.cfg.Certificate.RouteMode = routeDisabled
	app.cfgMu.Unlock()
	app.signalRoute()

	select {
	case <-stopped:
	default:
		t.Fatal("the tunnel was left running until the blocked pass returned")
	}
	select {
	case <-passCancelled:
	default:
		t.Fatal("the in-flight pass was not cancelled by the intent change")
	}
	if app.pn.routeRev == 0 {
		t.Fatal("the intent change did not advance the revision that fences a late answer")
	}
}
