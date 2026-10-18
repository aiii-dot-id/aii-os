package publicname

import (
	"context"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/certs"
	"github.com/aiii-dot-id/aii-os/internal/publicname/publicnametest"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

// carriedFixture is a name whose route is a relay's, with the publisher
// stating the ceiling it is given for relay routes (zero: none).
func carriedFixture(ceiling time.Duration) (*Runtime, *settingsBox, *publicnametest.Publisher) {
	app, box := fixtureRuntime(relayCfg("relay.example.test:443"))
	pub := &publicnametest.Publisher{Zone: "example.test", Relays: []string{"relay.example.test:443"}, RelayLifetime: ceiling}
	app.publisher = pub
	app.name = &store.PublicName{Name: "x.example.test", Zone: "example.test"}
	app.bound = "443"
	return app, box, pub
}

// A RELAY ROUTE IS HELD FOR THE RELAY'S CEILING, AND EVERYTHING ELSE FOR THE
// SHORT ONE. The write that publishes one CNAME to a relay is signed for
// what the service allows a relay route, a day; the read before it is
// signed for the short lease, because the service holds every other request
// to that and a read good for a day is a request to replay for a day. The
// long lease is signed for a random nine to ten tenths of what is allowed,
// so a fleet that publishes together does not renew together.
func TestARelayRouteIsHeldForTheRelaysCeilingAndReadUnderTheShortLease(t *testing.T) {
	app, box, pub := carriedFixture(24 * time.Hour)
	app.random = func() float64 { return 0.5 }
	if _, err := app.reconcileRoute(context.Background()); err != nil {
		t.Fatal(err)
	}
	whole := 24*time.Hour - time.Minute
	if len(pub.ReadLeases) != 1 || pub.ReadLeases[0] != 14*time.Minute {
		t.Fatalf("the read was signed for %v, want the short lease", pub.ReadLeases)
	}
	if len(pub.WriteLeases) != 1 || pub.WriteLeases[0] != whole-whole/20 {
		t.Fatalf("the relay route was signed for %v, want %v: nineteen twentieths of a day less a minute at this draw", pub.WriteLeases, whole-whole/20)
	}
	if writes, last := pub.LastRoute(); writes != 1 || len(last) != 1 || last[0].Type != certs.RecordCNAME {
		t.Fatalf("what was published is not one CNAME: %d %v", writes, last)
	}
	// The next pass finds it standing and writes nothing: a day's lease is
	// renewed with a third of it left, sixteen hours on.
	if _, err := app.reconcileRoute(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(pub.WriteLeases) != 1 {
		t.Fatalf("a relay route with nearly a day left was written again: %v", pub.WriteLeases)
	}

	// The same name gone direct is signed for the short lease, whatever
	// the relay ceiling says.
	box.set(func(s *Settings) { s.Certificate.RouteMode = RouteDirect })
	if _, err := app.reconcileRoute(context.Background()); err != nil {
		t.Fatal(err)
	}
	if n := len(pub.WriteLeases); n != 2 || pub.WriteLeases[1] != 14*time.Minute {
		t.Fatalf("an address was signed for %v, want the short lease", pub.WriteLeases)
	}

	// A service that states no ceiling for relay routes: the short lease,
	// whole, as before.
	older, _, was := carriedFixture(0)
	if _, err := older.reconcileRoute(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(was.WriteLeases) != 1 || was.WriteLeases[0] != 14*time.Minute {
		t.Fatalf("with no relay ceiling stated the route was signed for %v, want the short lease whole", was.WriteLeases)
	}
}

// A ROUTE THAT IS HELD AND CARRIED IS NOT ASKED ABOUT. A pass costs the
// service two requests, one of them signed, and the owner made one every
// ten minutes whatever had or had not changed. While the intent has not
// moved since the pass that settled it, that pass left a relay route whose
// lease is not in its last third, and the relay is carrying the name, the
// owner asks nothing. Each of those, lost, makes the next look a
// real pass again.
func TestARouteThatIsHeldAndCarriedIsNotAskedAbout(t *testing.T) {
	hold := 24*time.Hour - time.Minute
	held := func() (*Runtime, *settingsBox) {
		app, box, _ := carriedFixture(24 * time.Hour)
		app.route = routeState{Mode: RouteRelay, Managed: true, Generation: 2, LeaseUntil: time.Now().Add(20 * time.Hour)}
		app.routeSettled, app.routeSettledRev, app.routeHold = true, app.routeRev, hold
		app.CarryForTest("x.example.test", func() {}, nil).MarkConnectedForTest(true)
		return app, box
	}
	app, _ := held()
	if wait, quiet := app.routeQuiet(); !quiet || wait != routeIdle {
		t.Fatalf("a held and carried route: quiet %v for %v, want quiet for the idle interval and no longer", quiet, wait)
	}
	// Close to its renewal the quiet ends at the renewal, not after it.
	app.route.LeaseUntil = time.Now().Add(hold/routeRenewOf + 3*time.Minute)
	if wait, quiet := app.routeQuiet(); !quiet || wait > 3*time.Minute || wait < 2*time.Minute {
		t.Fatalf("three minutes before its renewal: quiet %v for %v", quiet, wait)
	}

	for what, lose := range map[string]func(*Runtime, *settingsBox){
		"the intent moved":                  func(a *Runtime, _ *settingsBox) { a.routeRev++ },
		"the last pass did not settle":      func(a *Runtime, _ *settingsBox) { a.routeSettled = false },
		"the relay is not carrying":         func(a *Runtime, _ *settingsBox) { a.relay.MarkConnectedForTest(false) },
		"there is no relay":                 func(a *Runtime, _ *settingsBox) { a.relay = nil },
		"the route is to be direct":         func(_ *Runtime, b *settingsBox) { b.set(func(s *Settings) { s.Certificate.RouteMode = RouteDirect }) },
		"the route is to be withdrawn":      func(_ *Runtime, b *settingsBox) { b.set(func(s *Settings) { s.Certificate.RouteMode = RouteDisabled }) },
		"what was published is not a relay": func(a *Runtime, _ *settingsBox) { a.route.Mode = RouteDirect },
		"the lease is in its last third":    func(a *Runtime, _ *settingsBox) { a.route.LeaseUntil = time.Now().Add(hold/routeRenewOf - time.Minute) },
		"the lease has lapsed":              func(a *Runtime, _ *settingsBox) { a.route.LeaseUntil = time.Now().Add(-time.Minute) },
		"the lease is not known":            func(a *Runtime, _ *settingsBox) { a.route.LeaseUntil = time.Time{} },
	} {
		app, box := held()
		lose(app, box)
		if _, quiet := app.routeQuiet(); quiet {
			t.Errorf("%s, and the service was still not asked", what)
		}
	}
}

func TestANameWithNothingPublishedAndNothingWantedIsNotAskedAbout(t *testing.T) {
	for _, mode := range []string{"", RouteDisabled} {
		set := Settings{Host: "127.0.0.1"}
		set.Certificate.RouteMode = mode
		app, box := fixtureRuntime(set)
		pub := &publicnametest.Publisher{Zone: "example.test", Relays: []string{"relay.example.test:443"}}
		app.publisher = pub
		app.name = &store.PublicName{Name: "x.example.test", Zone: "example.test"}
		app.bound = "443"
		app.status = StatusIssued // the operator's own name: it has its certificate, so it is not merely prepared
		look := func() {
			t.Helper()
			if _, err := app.lookAtRoute(context.Background()); err != nil {
				t.Fatalf("route mode %q: %v", mode, err)
			}
		}
		look()
		statuses, reads, _ := pub.Asked()
		if statuses != 1 || reads != 1 {
			t.Fatalf("route mode %q: the first look made %d status requests and %d signed reads, want one real pass", mode, statuses, reads)
		}
		_, _, writes := pub.Asked()
		for range 3 {
			look()
		}
		if s, r, w := pub.Asked(); s != 1 || r != 1 || w != writes {
			t.Fatalf("route mode %q: with nothing published and nothing wanted, three more looks asked the service %d status requests, %d reads and %d writes", mode, s-1, r-1, w-writes)
		}

		// An address to publish: the route is wanted now, and it is looked at.
		vpn := []string{"10.8.0.2"}
		box.set(func(s *Settings) { s.Certificate.RouteMode, s.Certificate.DirectAddresses = RouteDirect, &vpn })
		look()
		if _, r, _ := pub.Asked(); r != 2 {
			t.Fatalf("route mode %q: an address to publish appeared and the service was still not asked", mode)
		}
		// Taken away again, and nobody signals it: what was published and
		// is no longer wanted is withdrawn by a real pass, not left to
		// lapse because nothing is wanted now. Then nothing is asked.
		none := []string{}
		box.set(func(s *Settings) { s.Certificate.DirectAddresses = &none })
		_, readsBefore, writesBefore := pub.Asked()
		look()
		_, r, w := pub.Asked()
		if _, last := pub.LastRoute(); r != readsBefore+1 || w != writesBefore+1 || len(last) != 0 {
			t.Fatalf("route mode %q: an address that was published and is no longer wanted was not withdrawn by the next look (%d reads, %d writes, left %v)", mode, r-readsBefore, w-writesBefore, last)
		}
		look()
		look()
		if _, r2, w2 := pub.Asked(); r2 != r || w2 != w {
			t.Fatalf("route mode %q: withdrawn and settled, and two more looks asked %d reads and %d writes", mode, r2-r, w2-w)
		}
		// A signal is a new intent: looked at again, whatever stood.
		app.Signal()
		look()
		if _, r3, _ := pub.Asked(); r3 != r+1 {
			t.Fatalf("route mode %q: a signal did not make the next look a real pass", mode)
		}
	}
}

// A REAL PASS SETTLES WHAT THE QUIET RESTS ON, AND A SIGNAL UNSETTLES IT.
// After a pass that published the relay route under the current intent,
// the route is quiet once the relay carries the name. A signal is a new
// intent: the next look is a real pass, whatever the last one found.
func TestAPassSettlesTheRouteAndASignalUnsettlesIt(t *testing.T) {
	app, _, pub := carriedFixture(24 * time.Hour)
	if _, quiet := app.routeQuiet(); quiet {
		t.Fatal("a route no pass has looked at was taken as standing")
	}
	if _, err := app.reconcileRoute(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, quiet := app.routeQuiet(); quiet {
		t.Fatal("published, and not yet carried by the relay: the route was taken as standing")
	}
	app.CarryForTest("x.example.test", func() {}, nil).MarkConnectedForTest(true)
	if _, quiet := app.routeQuiet(); !quiet {
		t.Fatalf("published under this intent and carried, and the service would be asked again (reads so far: %d)", len(pub.ReadLeases))
	}
	app.Signal()
	if _, quiet := app.routeQuiet(); quiet {
		t.Fatal("the intent was signalled as moved and the route was still taken as standing")
	}
}

func TestTheDevicesReturnWakesTheRelayAndAsksTheServiceNothing(t *testing.T) {
	app, _, pub := carriedFixture(24 * time.Hour)
	app.Returned() // no relay yet: nothing to wake, and nothing breaks
	c := app.CarryForTest("x.example.test", func() {}, nil)
	before := app.RouteRevisionForTest()
	app.Returned()
	if !c.WokenForTest() {
		t.Fatal("the relay client was not told the device is back")
	}
	if app.RouteRevisionForTest() != before || len(pub.ReadLeases) != 0 {
		t.Fatalf("coming back moved the route's intent or asked the service: revision %d to %d, %d reads", before, app.RouteRevisionForTest(), len(pub.ReadLeases))
	}
}

// THE BUDGET HOLDS THE SHORTEST INTERVAL BETWEEN RENEWALS, NOT THE AVERAGE. A
// long lease is signed for as little as nine tenths of what is allowed and
// renewed with a third of the whole left, so two renewals can be as close
// as nine tenths less a third. A ceiling the unshortened lease would fit
// inside the hourly allowance, and the shortened one would not, is refused;
// a day's lease is one write an hour either way.
func TestTheBudgetHoldsTheShortestRenewalIntervalOfALongLease(t *testing.T) {
	tight := certs.ServiceStatus{MaxPublishesPerHour: 14}
	if n, ok := routeSustainable(tight, 10*time.Minute, false); !ok || n != 10 {
		t.Fatalf("a ten-minute lease signed whole: %d writes an hour, sustainable %v; want 10 and yes", n, ok)
	}
	if n, ok := routeSustainable(tight, 10*time.Minute, true); ok || n != 11 {
		t.Fatalf("the same lease signed for as little as nine tenths: %d writes an hour, sustainable %v; want 11 and no", n, ok)
	}
	if n, ok := routeSustainable(certs.ServiceStatus{MaxPublishesPerHour: 20}, 24*time.Hour-time.Minute, true); !ok || n != 1 {
		t.Fatalf("a day's lease: %d writes an hour, sustainable %v; want 1 and yes", n, ok)
	}
	if _, ok := routeSustainable(tight, 2*time.Second, true); ok {
		t.Fatal("a lease whose shortest renewal interval is nothing was called sustainable")
	}
}
