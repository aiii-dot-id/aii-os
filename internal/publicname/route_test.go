package publicname

import (
	"context"
	"errors"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/certs"
	"github.com/aiii-dot-id/aii-os/internal/cognitive"
	"github.com/aiii-dot-id/aii-os/internal/crypto"
	"github.com/aiii-dot-id/aii-os/internal/dashboard"
	"github.com/aiii-dot-id/aii-os/internal/publicname/publicnametest"
	"github.com/aiii-dot-id/aii-os/internal/relay"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

func routeCfg(host, origin, mode string) Settings {
	c := Settings{Host: host, Origin: origin}
	c.Certificate.RouteMode = mode
	return c
}

// settingsBox is a fixture's live configuration: what the runtime's
// Settings wire answers, changed by the test between passes.
type settingsBox struct {
	mu sync.Mutex
	s  Settings
}

func (b *settingsBox) get() Settings {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.s
}

func (b *settingsBox) set(change func(*Settings)) {
	b.mu.Lock()
	defer b.mu.Unlock()
	change(&b.s)
}

// fixtureRuntime is the public name with nothing live around it: no
// lifetime, store, door, dashboard, TIME or key, its configuration the
// box's.
func fixtureRuntime(s Settings) (*Runtime, *settingsBox) {
	box := &settingsBox{s: s}
	return New(Wires{
		Lifetime:    context.Background,
		Ctx:         func() context.Context { return nil },
		Go:          func(run func()) bool { go run(); return true },
		Door:        func() AppendFunc { return nil },
		Store:       func() *store.Store { return nil },
		Dashboard:   func() *dashboard.Server { return nil },
		TIME:        func() *cognitive.TIME { return nil },
		Key:         func() *crypto.KeyPair { return nil },
		IdentityDir: func() string { return "" },
		Settings:    box.get,
		Alert:       func(string, string) {},
		Stopping:    errors.New("the fixture is stopping"),
	}), box
}

func TestRouteModeRefusesWhatItCannotDo(t *testing.T) {
	for _, tc := range []struct {
		in, want string
		ok       bool
	}{
		{"", RouteDirect, true},
		{"direct", RouteDirect, true},
		{"relay", RouteRelay, true},
		{"disabled", RouteDisabled, true},
		{" direct ", RouteDirect, true},
		{"Direct", "", false},
		{"rely", "", false},
		{"off", "", false},
	} {
		got, err := RouteMode(routeCfg("", "", tc.in).Certificate)
		if tc.ok != (err == nil) || (tc.ok && got != tc.want) {
			t.Fatalf("route_mode %q: got %q %v", tc.in, got, err)
		}
	}
}

func TestDirectRouteIsTheWholeAnswerNotTheFirstInterface(t *testing.T) {
	// A concrete bind is the address, both families.
	for _, tc := range []struct{ host, kind, value string }{
		{"192.0.2.6", certs.RecordA, "192.0.2.6"},
		{"fd00::6", certs.RecordAAAA, "fd00::6"},
	} {
		got, err := directRoute(routeCfg(tc.host, "", ""))
		if err != nil || len(got) != 1 || got[0].Type != tc.kind || got[0].Value != tc.value {
			t.Fatalf("bind %s: %v %v", tc.host, got, err)
		}
	}
	// Loopback is reachable from nowhere else: an empty route, not an error.
	got, err := directRoute(routeCfg("127.0.0.1", "", ""))
	if err != nil || len(got) != 0 {
		t.Fatalf("loopback: %v %v", got, err)
	}
	// An explicit list is taken as given — including an address this
	// machine cannot see, which is what a NAT or a routed address is.
	explicit := []string{"203.0.113.9", "fd00::9"}
	cfg := routeCfg("0.0.0.0", "", "")
	cfg.Certificate.DirectAddresses = &explicit
	got, err = directRoute(cfg)
	if err != nil || len(got) != 2 {
		t.Fatalf("explicit: %v %v", got, err)
	}
	// PRESENT AND EMPTY is a real answer — an intentionally empty route —
	// and must not read the same as absent.
	empty := []string{}
	cfg.Certificate.DirectAddresses = &empty
	got, err = directRoute(cfg)
	if err != nil || len(got) != 0 {
		t.Fatalf("explicitly empty: %v %v", got, err)
	}
	// A bad address in the list is refused, not skipped.
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

	// One per family is that family's answer: dual stack publishes both.
	interfaceAddrs = mk("10.0.0.5/24", "fd00::5/64", "127.0.0.1/8", "fe80::1/64")
	got, err := directRoute(cfg)
	if err != nil || len(got) != 2 {
		t.Fatalf("dual stack: %v %v", got, err)
	}

	// TWO IPv4 addresses is a question only the operator can settle. The
	// old code took the first interface and called it the address.
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

	// A FAILED ENUMERATION IS NOT AN EMPTY SET: withdrawing a live route
	// because the OS would not answer once is the worst outcome here.
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
	a, _ := fixtureRuntime(Settings{})
	a.bound = "8181" // the dashboard's own port: where the name is reached while nothing says otherwise
	cfg := routeCfg("192.0.2.6", "", RouteRelay)

	// No choice named.
	if _, err := a.desiredRoute(cfg, RouteRelay, st); err == nil {
		t.Fatal("relay mode with no endpoint was accepted")
	}
	// A relay the service never advertised is not a choice.
	cfg.Certificate.RelayEndpoint = "relay.elsewhere.invalid"
	if _, err := a.desiredRoute(cfg, RouteRelay, st); err == nil {
		t.Fatal("an unadvertised relay was selectable")
	}
	// The advertised one, on the dashboard's own port: the name's URL does
	// not move, so the choice need not name the port.
	cfg.Certificate.RelayEndpoint = "relay.example.invalid"
	got, err := a.desiredRoute(cfg, RouteRelay, st)
	if err != nil || len(got) != 1 || got[0].Type != certs.RecordCNAME || got[0].Value != "relay.example.invalid" {
		t.Fatalf("relay: %v %v", got, err)
	}
	// A PORT MISMATCH IS AN ERROR, NOT A SILENT MOVE. DNS carries no
	// port, so a CNAME cannot take a browser from 9999 to 8181; the old
	// code rewrote the origin and changed the bookmarked URL. A choice
	// that does not name the relay's port has not accepted the move.
	a.bound = "9999"
	_, err = a.desiredRoute(cfg, RouteRelay, st)
	if err == nil {
		t.Fatal("a relay on another port was accepted by a choice that does not name the port")
	}
	if !strings.Contains(err.Error(), "9999") || !strings.Contains(err.Error(), "8181") || !strings.Contains(err.Error(), "relay.example.invalid:8181") {
		t.Fatalf("the refusal does not name both ports and the choice that accepts the move: %v", err)
	}

	cfg.Certificate.RelayEndpoint = "relay.example.invalid:8181"
	if got, err := a.desiredRoute(cfg, RouteRelay, st); err != nil || len(got) != 1 || got[0].Value != "relay.example.invalid" {
		t.Fatalf("a relay on another port, its port named: %v %v", got, err)
	}
	// A named port the service does not advertise is still refused.
	cfg.Certificate.RelayEndpoint = "relay.example.invalid:443"
	if _, err := a.desiredRoute(cfg, RouteRelay, st); err == nil {
		t.Fatal("a port the service does not advertise for the relay was accepted")
	}
	// AN ADVERTISED ORIGIN IS NOT WHERE THE NAME IS REACHED. dashboard.origin
	// is the operator's own front; set to the relay's port, it used to pass
	// this check, the route was published, and every request met a Host the
	// dashboard had never admitted.
	cfg.Certificate.RelayEndpoint = "relay.example.invalid"
	cfg.Origin = "https://front.example.invalid:8181"
	if _, err := a.desiredRoute(cfg, RouteRelay, st); err == nil {
		t.Fatal("a configured dashboard.origin on the relay's port stood in for the port the name is reached on")
	}
	cfg.Origin = ""
	// A service with no relays advertised offers no relay mode at all.
	if _, err := a.desiredRoute(cfg, RouteRelay, certs.ServiceStatus{
		Capabilities: []string{certs.CapabilityRecordSets},
	}); err == nil {
		t.Fatal("relay mode was accepted against a service with no relays")
	}
	// Disabled is an empty route, and it is not an error.
	got, err = a.desiredRoute(cfg, RouteDisabled, st)
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
	// An unparseable lease is renewed rather than trusted.
	if !routeNeedsRenewal(certs.RecordSet{Managed: true, Generation: 2,
		Records: []certs.LeasedRecord{{Type: certs.RecordA, Value: "10.0.0.2", ExpiresAt: "soon"}}}, lease) {
		t.Fatal("an unreadable lease was treated as live")
	}
	// THE FRACTION IS A BUDGET DECISION. Renewing with two thirds of the
	// lease left — which the first draft did, against its own comment —
	// is a write every 4m40s at a fourteen-minute lease: thirteen an
	// hour for ONE name, against an identity limit of twenty that a
	// second name and the ACME challenges also draw on. With a third
	// left it is six an hour, which is what the limit is planned around.
	if routeNeedsRenewal(certs.RecordSet{Managed: true, Generation: 2, Records: at(8 * time.Minute)}, lease) {
		t.Fatal("a lease with eight of fourteen minutes left was renewed — that is thirteen writes an hour for one name")
	}
	// THE FRACTION IS A BUDGET DECISION. Renewing with two thirds of the
	// lease left — which the first draft did, against its own comment —
	// is a write every 4m40s at a fourteen-minute lease: thirteen an
	// hour for ONE name, against an identity limit of twenty that a
	// second name and the ACME challenges also draw on. With a third
	// left it is six an hour, which is what the limit is planned around.
	if routeNeedsRenewal(certs.RecordSet{Managed: true, Generation: 2, Records: at(8 * time.Minute)}, lease) {
		t.Fatal("a lease with eight of fourteen minutes left was renewed — that is thirteen writes an hour for one name")
	}
	// A TOMBSTONE DOES NOT LAPSE. Renewing an empty managed route every
	// ten minutes would spend the identity's budget on nothing.
	if routeNeedsRenewal(certs.RecordSet{Managed: true, Generation: 3}, lease) {
		t.Fatal("a tombstone was renewed")
	}
	// A name that never published does need its first write.
	if !routeNeedsRenewal(certs.RecordSet{}, lease) {
		t.Fatal("an unpublished name was left alone")
	}
}

// A SETTLED PASS WAITS ON THE LEASE, NOT ON A CONSTANT. A service whose
// publication lifetime is shorter than the idle would let the route
// lapse between two passes that both thought they had time.
func TestTheNextPassIsTimedFromTheLease(t *testing.T) {
	at := func(d time.Duration) certs.RecordSet {
		return certs.RecordSet{Managed: true, Generation: 2, Records: []certs.LeasedRecord{
			{Type: certs.RecordA, Value: "10.0.0.2", ExpiresAt: time.Now().Add(d).UTC().Format(time.RFC3339)}}}
	}
	// A short service lifetime must produce a short wait.
	short := 280 * time.Second
	got := routeSettleDelay(at(short), short)
	if got > short {
		t.Fatalf("the next pass (%v) falls after the lease (%v) lapses", got, short)
	}
	if got > short-short/routeRenewOf+time.Second {
		t.Fatalf("the next pass (%v) is later than the renewal point", got)
	}
	// A long lease is still capped, so a configuration change is never
	// waited out for longer than the idle.
	long := 14 * time.Minute
	if got := routeSettleDelay(at(long), long); got > routeIdle {
		t.Fatalf("the wait exceeded the idle cap: %v", got)
	}
	// A lease already due is retried promptly, not immediately in a spin.
	if got := routeSettleDelay(at(-time.Minute), long); got < routeDebounce {
		t.Fatalf("an overdue lease spins: %v", got)
	}
	// A tombstone does not lapse and does not need an early pass.
	if got := routeSettleDelay(certs.RecordSet{Managed: true, Generation: 3}, long); got != routeIdle {
		t.Fatalf("a tombstone was scheduled early: %v", got)
	}
}

// DISABLING PUBLICATION STOPS THE TUNNEL EVEN WHEN CERTD IS DOWN. The
// withdrawal of the route needs the network; stopping the relay does
// not, and an unreachable service must never be the reason the public
// internet is still being carried to a dashboard taken out of service.
func TestDisablingStopsTheRelayWithoutTheService(t *testing.T) {
	cfg := Settings{}
	cfg.Certificate.RouteMode = RouteDisabled
	app, _ := fixtureRuntime(cfg)
	stopped := false
	app.relayCancel = func() { stopped = true }
	// A publisher that cannot be reached at all.
	app.publisher = &publicnametest.Publisher{Fail: errors.New("certificate service is unreachable")}
	app.name = &store.PublicName{Name: "x.example.test", Zone: "example.test"}

	if _, err := app.reconcileRoute(context.Background()); err == nil {
		t.Fatal("an unreachable service reported success")
	}
	if !stopped {
		t.Fatal("disabling left the relay running because certd was unavailable")
	}
	if app.relay != nil || app.relayCancel != nil {
		t.Fatal("the relay was not released")
	}
}

// A DELAY THE SERVICE NAMED IS KEPT, WHICHEVER CALL NAMED IT. A signed
// read is rate-limited against the shared source-IP allowance too, and
// reducing its Retry-After to a one-minute retry is how a limited client
// keeps knocking and starves the certificate renewal that shares it.
func TestAServiceDelayIsKeptWhicheverCallNamedIt(t *testing.T) {
	app, _ := fixtureRuntime(Settings{})
	limited := &certs.RecordSetError{Op: "read", Status: 429, Message: "slow down", RetryAfter: time.Hour}
	if got := app.serviceDelay(limited, time.Minute); got != time.Hour {
		t.Fatalf("the service asked for an hour and got %v", got)
	}
	if left := time.Until(app.routeFloor()); left < 50*time.Minute {
		t.Fatalf("the floor was not held: %v left", left)
	}
	// An ordinary failure keeps the caller's own retry.
	if got := app.serviceDelay(errors.New("connection reset"), time.Minute); got != time.Minute {
		t.Fatalf("an untimed failure changed the retry: %v", got)
	}
	// ...and did not move the floor backwards.
	if left := time.Until(app.routeFloor()); left < 50*time.Minute {
		t.Fatalf("an untimed failure cleared the service's floor: %v", left)
	}
}

// A pass that began under one intent must not act on it after the
// operator changed their mind: a slow request that started in relay mode
// cannot start a tunnel that has since been switched off.
func TestAPassCannotActOnAnIntentTheOperatorChanged(t *testing.T) {
	cfg := Settings{}
	cfg.Certificate.RouteMode = RouteDisabled
	app, _ := fixtureRuntime(cfg)
	stopped := false
	app.relay = &relay.Client{Name: "n.example.test", Relay: "relay.example.invalid:8181"}
	app.relayCancel = func() { stopped = true }

	app.afterRoute(cfg, RouteRelay, "n.example.test",
		certs.RecordSet{Managed: true, Generation: 2, Records: []certs.LeasedRecord{
			{Type: certs.RecordCNAME, Value: "relay.example.invalid",
				ExpiresAt: time.Now().Add(time.Hour).UTC().Format(time.RFC3339)}}},
		certs.ServiceStatus{RelayEndpoints: []certs.RelayEndpoint{{Name: "relay.example.invalid", Port: 8181}}}, 0)

	if !stopped {
		t.Fatal("a late pass kept a tunnel the operator had switched off")
	}
	if app.relay != nil {
		t.Fatal("the relay was not released")
	}
}

// A HEALTHY TUNNEL IS NOT DROPPED ON EVERY PASS. The owner settles on
// each reconciliation; tearing the tunnel down each time would drop
// whatever browser traffic was crossing it, every few minutes, for no
// change at all.
func TestAHealthyTunnelSurvivesASettledPass(t *testing.T) {
	app, _ := fixtureRuntime(Settings{})
	cancels := 0
	existing := &relay.Client{Name: "n.example.test", Relay: "relay.example.invalid:8181"}
	app.relay = existing
	app.relayCancel = func() { cancels++ }

	app.startRelay(Settings{}, "n.example.test", certs.RelayEndpoint{Name: "relay.example.invalid", Port: 8181})
	if cancels != 0 || app.relay != existing {
		t.Fatalf("the same name at the same relay was torn down (cancels=%d)", cancels)
	}
	// A different relay DOES replace it.
	app.startRelay(Settings{}, "n.example.test", certs.RelayEndpoint{Name: "other.example.invalid", Port: 8181})
	if cancels != 1 {
		t.Fatalf("a changed endpoint did not replace the tunnel (cancels=%d)", cancels)
	}
}

// ...AND THE READ PATH ACTUALLY USES IT. The helper being right is not
// the same as the call site calling it: the first version of this test
// exercised serviceDelay directly and stayed green while the read path
// threw the delay away.
func TestARateLimitedReadKeepsTheServicesDelay(t *testing.T) {
	// A loopback bind publishes nothing, so the pass goes straight to the
	// read. An empty host is every interface, and what the host's own
	// interfaces would publish is not this test's question.
	app, _ := fixtureRuntime(Settings{Host: "127.0.0.1"})
	app.publisher = &publicnametest.Publisher{
		Zone:     "example.test",
		ReadFail: &certs.RecordSetError{Op: "read", Status: 429, Message: "slow down", RetryAfter: time.Hour},
	}
	app.name = &store.PublicName{Name: "x.example.test", Zone: "example.test"}

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

// A FLOOR MUST NEVER LAND PAST THE DEADLINE IT PROTECTS. With a very
// short service lifetime the renewal point falls inside the debounce,
// and flooring there schedules the next look AFTER the route has
// already lapsed — a timer derived from an expiry that then ignores it.
func TestAShortLeaseIsNeverScheduledPastItsExpiry(t *testing.T) {
	at := func(d time.Duration) certs.RecordSet {
		return certs.RecordSet{Managed: true, Generation: 2, Records: []certs.LeasedRecord{
			{Type: certs.RecordA, Value: "10.0.0.2", ExpiresAt: time.Now().Add(d).UTC().Format(time.RFC3339)}}}
	}
	// Twelve seconds left, and the debounce is fifteen.
	got := routeSettleDelay(at(12*time.Second), 14*time.Second)
	if got > 12*time.Second {
		t.Fatalf("the next pass (%v) falls after the route lapses (12s)", got)
	}
	if got <= 0 {
		t.Fatalf("the next pass must still be in the future: %v", got)
	}
}

// ...and a lifetime that cannot be sustained at all is refused out
// loud, rather than published and left to lapse between passes.
func TestALifetimeTheBudgetCannotHoldIsRefused(t *testing.T) {
	// Fifteen seconds of publication against twenty writes an hour.
	short := certs.ServiceStatus{
		Zone: "example.test", MaxPublishLifetime: 15 * time.Second, MaxPublishesPerHour: 20,
		Capabilities: []string{certs.CapabilityRecordSets}, RecordSetTypes: []string{certs.RecordA},
	}
	if perHour, ok := routeSustainable(short, short.RouteLease(), false); ok {
		t.Fatalf("a fifteen-second lifetime was called sustainable (%d writes an hour)", perHour)
	}
	// The real service is comfortable: fourteen minutes, twenty an hour.
	live := certs.ServiceStatus{
		Zone: "aiios.id", MaxPublishLifetime: 900 * time.Second, MaxPublishesPerHour: 20,
		Capabilities: []string{certs.CapabilityRecordSets}, RecordSetTypes: []string{certs.RecordA},
	}
	perHour, ok := routeSustainable(live, live.RouteLease(), false)
	if !ok {
		t.Fatalf("the deployed service was called unsustainable (%d an hour)", perHour)
	}
	// A fixed hour holds one more than the average, and the ACME
	// challenges take four of the allowance.
	if perHour != 7 {
		t.Fatalf("the peak-hour count should be seven, got %d", perHour)
	}
}

// AN INTENT CHANGE STOPS LOCAL WORK ON THE SIGNAL, not on the next
// pass. A pass can be blocked on the network for a minute, and waiting
// for it leaves the public internet reaching a dashboard the operator
// has already taken out of service.
func TestDisablingStopsTheRelayWhileAPassIsStillOnTheWire(t *testing.T) {
	cfg := Settings{}
	cfg.Certificate.RouteMode = RouteRelay
	app, box := fixtureRuntime(cfg)
	stopped := make(chan struct{}, 1)
	app.relay = &relay.Client{Name: "n.example.test", Relay: "relay.example.invalid:8181"}
	app.relayCancel = func() { stopped <- struct{}{} }
	app.routeWake = make(chan struct{}, 1)
	// A pass is on the wire.
	passCancelled := make(chan struct{}, 1)
	app.routePassCancel = func() { passCancelled <- struct{}{} }

	// The operator disables publication and the owner is signalled.
	box.set(func(s *Settings) { s.Certificate.RouteMode = RouteDisabled })
	app.Signal()

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
	if app.routeRev == 0 {
		t.Fatal("the intent change did not advance the revision that fences a late answer")
	}
}

// A PASS THE INTENT MOVED UNDER IS NOT AN OUTAGE. At boot the owner's
// first pass and the first signal used to race: the signal cancelled the
// pass on the wire, and the pass reported its own supersession as the
// certificate service being unreachable — once per boot, in the log and
// on the Settings page. A superseded pass returns the cancellation and
// records nothing against the service.
func TestASupersededRoutePassIsNotReportedAsAnOutage(t *testing.T) {
	cfg := Settings{}
	cfg.Certificate.RouteMode = RouteDisabled
	app, _ := fixtureRuntime(cfg)
	app.name = &store.PublicName{Name: "x.example.test", Zone: "example.test"}
	entered := make(chan struct{})
	app.publisher = &publicnametest.BlockingStatus{Publisher: &publicnametest.Publisher{}, Entered: entered}
	done := make(chan error, 1)
	go func() {
		_, err := app.reconcileRoute(context.Background())
		done <- err
	}()
	<-entered
	app.Signal() // the intent moves under the pass on the wire
	err := <-done
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("a superseded pass must return its cancellation, got %v", err)
	}
	app.mu.Lock()
	last, route := app.lastError, app.route.LastError
	app.mu.Unlock()
	if last != "" || route != "" {
		t.Fatalf("a superseded pass was recorded as the service's failure: %q / %q", last, route)
	}
}

// The owner's first pass carries the intent that started it; only a
// later caller has something to signal.
func TestTheRouteOwnerStartsOnceAndOnlyLaterCallersSignal(t *testing.T) {
	app, _ := fixtureRuntime(Settings{})
	if !app.startRouteOwner() {
		t.Fatal("the first call did not start the owner")
	}
	if app.startRouteOwner() {
		t.Fatal("a second call claimed to start the owner again")
	}
}
