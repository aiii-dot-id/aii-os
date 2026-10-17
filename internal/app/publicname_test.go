package app

import (
	"bufio"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/dashboard"
	"github.com/aiii-dot-id/aii-os/internal/genesis/genesistest"
	"github.com/aiii-dot-id/aii-os/internal/publicname"
	"github.com/aiii-dot-id/aii-os/internal/publicname/publicnametest"
	"github.com/aiii-dot-id/aii-os/internal/witness"

	"github.com/aiii-dot-id/aii-os/internal/ledger"
)

// publicname_test.go — the operator's act, end to end through the app:
// the claim is recorded once, the certificate is served hot for the
// name, the origin and the host gate follow the name, a restart finds
// it all again from the record, and the daily pass renews.

// startRelayForTest asks for the tunnel the way the route owner does,
// with an endpoint already chosen. A loopback relay has no DNS name, so
// it cannot be reached through a published CNAME; the guard inside
// startRelay is what these tests are about and it is unchanged.
func startRelayForTest(t *testing.T, app *App, name, addr string) {
	t.Helper()
	if err := app.pn.StartRelayForTest(name, addr); err != nil {
		t.Fatal(err)
	}
}

// birthNamed births an identity with HTTPS on; bootNamed boots it with
// the seams stubbed; publicNameApp does both.
func birthNamed(t *testing.T, dir string) *Config {
	t.Helper()
	keyPath, ledgerPath, dbPath := birthFixture(t, dir, "Named")
	buildPriorProjection(t, ledgerPath, dbPath)
	cfg := safebootConfig(t, dir, "Named", keyPath, ledgerPath, dbPath)
	cfg.Dashboard.TLS = true
	return cfg
}

func publicNameApp(t *testing.T, dir string, pub *publicnametest.Publisher, managers *[]*publicnametest.Manager) *App {
	t.Helper()
	return bootNamed(t, birthNamed(t, dir), pub, managers)
}

func bootNamed(t *testing.T, cfg *Config, pub *publicnametest.Publisher, managers *[]*publicnametest.Manager) *App {
	t.Helper()
	app := New(cfg)
	app.pn.NewPublisher = func(publicname.Settings) (publicname.NamePublisher, error) { return pub, nil }
	app.pn.NewCertManager = publicnametest.Managers[publicname.CertificateManager](managers)
	if err := startLiveForTest(app); err != nil {
		t.Fatalf("boot: %v", err)
	}
	if reason, safe := app.SafeMode(); safe {
		stopApp(t, app)
		t.Fatalf("SAFE: %s", reason)
	}
	return app
}

func waitStatus(t *testing.T, app *App, want string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if app.pn.State().Status == want {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("status %q never reached; state %+v", want, app.pn.State())
}

func servedCert(t *testing.T, addr, serverName string) *x509.Certificate {
	t.Helper()
	conn, err := tls.Dial("tcp", addr, &tls.Config{ServerName: serverName, InsecureSkipVerify: true})
	if err != nil {
		t.Fatalf("dial as %q: %v", serverName, err)
	}
	defer conn.Close()
	return conn.ConnectionState().PeerCertificates[0]
}

func TestClaimingAPublicNameRecordsItOnceAndServesItsCertificateLive(t *testing.T) {
	dir := t.TempDir()
	pub := &publicnametest.Publisher{Zone: "example.test"}
	var managers []*publicnametest.Manager
	app := publicNameApp(t, dir, pub, &managers)
	defer stopApp(t, app)

	if st := app.pn.State(); st.Status != publicname.StatusNone || !st.CanClaim {
		t.Fatalf("before the claim: %+v", st)
	}
	before := app.ledger.LastSeq()
	st, err := app.pn.Claim()
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if len(pub.Claims) != 1 || !strings.HasPrefix(st.Name, "ui."+pub.Claims[0]+".example.test") {
		t.Fatalf("the claim did not reach the service with the minted id: claims=%v state=%+v", pub.Claims, st)
	}
	if app.ledger.LastSeq() != before+1 {
		t.Fatalf("the claim minted %d records, want 1", app.ledger.LastSeq()-before)
	}
	pn, ok, err := app.store.PublicName()
	if err != nil || !ok || pn.Name != st.Name {
		t.Fatalf("the projection does not carry the claim: %+v ok=%v err=%v", pn, ok, err)
	}
	events, _ := ledger.ReadAll(app.ledger.Path())
	if last := events[len(events)-1]; last.Type != ledger.EventNetworkNameClaimed {
		t.Fatalf("the last record is %s, want network.name_claimed", last.Type)
	}
	waitStatus(t, app, publicname.StatusIssued)
	addr := app.dashboard.BoundAddr()
	_, port, _ := net.SplitHostPort(addr)
	if got := servedCert(t, addr, st.Name); got.VerifyHostname(st.Name) != nil {
		t.Fatal("the dashboard does not serve the public certificate for the public name")
	}
	if !app.dashboard.HostAllowedForTest(net.JoinHostPort(st.Name, port)) {
		t.Fatal("the public name is not an allowed host")
	}
	if app.dashboard.Origin() != "https://"+st.Name+":"+port || st.Origin != app.dashboard.Origin() {
		t.Fatalf("origin: dashboard %q state %q", app.dashboard.Origin(), st.Origin)
	}
	// A second claim is the same claim.
	again, err := app.pn.Claim()
	if err != nil || again.Name != st.Name || len(pub.Claims) != 1 || app.ledger.LastSeq() != before+1 {
		t.Fatalf("a second claim changed something: %+v err=%v claims=%d seq=%d", again, err, len(pub.Claims), app.ledger.LastSeq())
	}
	if len(managers) != 1 || managers[0].Ensures != 1 {
		t.Fatalf("managers=%d ensures=%d, want one manager asked once", len(managers), managers[0].Ensures)
	}
}

func TestAClaimNeedsNoHTTPSSetting(t *testing.T) {
	dir := t.TempDir()
	keyPath, ledgerPath, dbPath := birthFixture(t, dir, "Plain")
	buildPriorProjection(t, ledgerPath, dbPath)
	cfg := safebootConfig(t, dir, "Plain", keyPath, ledgerPath, dbPath)
	app := New(cfg)
	pub := &publicnametest.Publisher{Zone: "example.test"}
	var managers []*publicnametest.Manager
	app.pn.NewPublisher = func(publicname.Settings) (publicname.NamePublisher, error) { return pub, nil }
	app.pn.NewCertManager = publicnametest.Managers[publicname.CertificateManager](&managers)
	if err := startLiveForTest(app); err != nil {
		t.Fatal(err)
	}
	defer stopApp(t, app)
	if app.configSnapshot().Dashboard.TLS {
		t.Fatal("the rig is not the ordinary install: HTTPS is on")
	}
	before := app.ledger.LastSeq()
	if _, err := app.pn.Claim(); err != nil {
		t.Fatalf("a claim on a plain loopback dashboard was refused: %v", err)
	}
	if len(pub.Claims) != 1 || app.ledger.LastSeq() == before {
		t.Fatalf("the claim did not reach the service and the record once: %d claims", len(pub.Claims))
	}
	waitStatus(t, app, publicname.StatusIssued)
	if _, err := app.pn.Claim(); err != nil || len(pub.Claims) != 1 {
		t.Fatalf("a second claim is not the first one found held: %v, %d claims", err, len(pub.Claims))
	}
}

func TestARestartFindsTheNameInTheRecordAndServesItAgain(t *testing.T) {
	dir := t.TempDir()
	pub := &publicnametest.Publisher{Zone: "example.test"}
	var managers []*publicnametest.Manager
	cfg := birthNamed(t, dir)
	app := bootNamed(t, cfg, pub, &managers)
	st, err := app.pn.Claim()
	if err != nil {
		t.Fatal(err)
	}
	waitStatus(t, app, publicname.StatusIssued)
	stopApp(t, app)

	// The next boot: the name comes from the projection (rebuilt from
	// the record), the manager is asked once more and holds a
	// certificate, and the dashboard is wired without any operator act.
	cfg2 := *cfg
	app2 := bootNamed(t, &cfg2, pub, &managers)
	defer stopApp(t, app2)
	waitStatus(t, app2, publicname.StatusIssued)
	st2 := app2.pn.State()
	if st2.Name != st.Name {
		t.Fatalf("the restart named %q, want %q", st2.Name, st.Name)
	}
	if len(pub.Claims) != 1 {
		t.Fatal("the restart claimed again")
	}
	addr := app2.dashboard.BoundAddr()
	if got := servedCert(t, addr, st.Name); got.VerifyHostname(st.Name) != nil {
		t.Fatal("after the restart the public certificate is not served")
	}
	if !strings.HasPrefix(app2.dashboard.Origin(), "https://"+st.Name) {
		t.Fatalf("after the restart the origin is %q", app2.dashboard.Origin())
	}
}

func TestTheDailyPassRenewsAndAFailureIsToldWithDaysLeft(t *testing.T) {
	dir := t.TempDir()
	pub := &publicnametest.Publisher{Zone: "example.test"}
	var managers []*publicnametest.Manager
	app := publicNameApp(t, dir, pub, &managers)
	defer stopApp(t, app)
	if _, err := app.pn.Claim(); err != nil {
		t.Fatal(err)
	}
	waitStatus(t, app, publicname.StatusIssued)
	res := app.pn.AlarmOwner().OnAlarm(context.Background(), "certificate.renewal", "wall", 0, "")
	if !res.Accepted || res.NextDeadline == nil {
		t.Fatalf("the owner must accept and name its next hour: %+v", res)
	}
	if managers[0].Renews != 1 {
		t.Fatalf("renews=%d, want 1", managers[0].Renews)
	}
	// A failing issuance is reported, not hidden.
	managers[0].Mu.Lock()
	managers[0].Fail = context.DeadlineExceeded
	managers[0].Mu.Unlock()
	notification := make(chan struct{}, 1)
	app.store.OnOutboxWrite(func() {
		messages, err := app.store.UndeliveredFor("operator")
		if err != nil {
			t.Error(err)
			return
		}
		certificate := false
		for _, m := range messages {
			certificate = certificate || strings.Contains(m.Content, "certificate")
		}
		if !certificate {
			return
		}
		select {
		case notification <- struct{}{}:
		default:
		}
	})
	if _, err := app.pn.Retry(); err != nil {
		t.Fatal(err)
	}
	waitStatus(t, app, publicname.StatusFailing)
	if st := app.pn.State(); st.LastError == "" {
		t.Fatal("the failure was not shown")
	}
	// Status publication precedes notification persistence. Wait on the durable
	// outbox owner, not a scheduling assumption about the issuing goroutine.
	select {
	case <-notification:
	case <-time.After(5 * time.Second):
		t.Fatal("certificate notification did not commit")
	}
	msgs, err := app.store.UndeliveredFor("operator")
	if err != nil {
		t.Fatal(err)
	}
	told := false
	for _, m := range msgs {
		if strings.Contains(m.Content, "certificate") {
			told = true
		}
	}
	if !told {
		t.Fatal("the operator was not told in the outbox")
	}
}

func TestANetworkBindClaimsItsNameWithoutAButton(t *testing.T) {
	dir := t.TempDir()
	pub := &publicnametest.Publisher{Zone: "example.test"}
	var managers []*publicnametest.Manager
	app := publicNameApp(t, dir, pub, &managers)
	defer stopApp(t, app)
	loop := app.configSnapshot()

	on := true
	loop.Certificate.Prepare = &on
	app.pn.AutoClaim(publicNameSettingsOf(loop))
	if len(pub.Claims) != 0 {
		t.Fatal("a start claimed a name for a loopback identity")
	}
	declined := loop
	declined.Dashboard.Host = "10.0.0.5"
	declined.Certificate.ServerURL = "none"
	app.pn.AutoClaim(publicNameSettingsOf(declined))
	if len(pub.Claims) != 0 {
		t.Fatal("a declined service claimed a name")
	}
	// The claim itself says so, from the live configuration it reads.
	app.cfgMu.Lock()
	app.cfg.Certificate.ServerURL = "none"
	app.cfgMu.Unlock()
	if _, err := app.pn.Claim(); err == nil || !strings.Contains(err.Error(), "server_url = none") {
		t.Fatalf("a declined service must say so: %v", err)
	}
	// And the card is told so before anyone presses: no claim is offered.
	if st := app.pn.State(); st.CanClaim || !st.ServiceDeclined {
		t.Fatalf("a declined service is offered a claim on the card: can_claim %v, service_declined %v", st.CanClaim, st.ServiceDeclined)
	}
	app.cfgMu.Lock()
	app.cfg.Certificate.ServerURL = loop.Certificate.ServerURL
	app.cfgMu.Unlock()
	if st := app.pn.State(); !st.CanClaim || st.ServiceDeclined {
		t.Fatalf("a service in force is not offered a claim on the card: can_claim %v, service_declined %v", st.CanClaim, st.ServiceDeclined)
	}
	network := loop
	network.Dashboard.Host = "10.0.0.5"
	app.pn.AutoClaim(publicNameSettingsOf(network))
	if len(pub.Claims) != 1 {
		t.Fatalf("a network bind claims once, got %d", len(pub.Claims))
	}
	waitStatus(t, app, publicname.StatusIssued)
	st := app.pn.State()
	if !strings.HasPrefix(st.Origin, "https://ui.") || !strings.HasPrefix(app.dashboard.Origin(), "https://ui.") {
		t.Fatalf("the claimed name is the advertised origin: state %q, dashboard %q", st.Origin, app.dashboard.Origin())
	}
	app.pn.AutoClaim(publicNameSettingsOf(network))
	if len(pub.Claims) != 1 {
		t.Fatal("a second boot claimed again")
	}
}

func TestANewIdentitysNameIsPreparedAtItsBirthAndThenAsksTheServiceNothing(t *testing.T) {
	dir := t.TempDir()
	pub := &publicnametest.Publisher{Zone: "example.test", Relays: []string{"relay.example.test:443"}}
	var managers []*publicnametest.Manager
	app := publicNameApp(t, dir, pub, &managers)
	defer stopApp(t, app)
	if len(pub.Claims) != 0 {
		t.Fatal("this test binary's identities claim nothing unless a test asks: the rig claimed at boot")
	}

	on := true
	app.cfgMu.Lock()
	app.cfg.Certificate.Prepare, app.cfg.Dashboard.TLS = &on, false
	app.cfgMu.Unlock()
	prepared := publicNameSettingsOf(app.configSnapshot())

	app.pn.PrepareAtBirth(prepared)
	if len(pub.Claims) != 1 {
		t.Fatalf("a birth with preparation on claims the name once, got %d", len(pub.Claims))
	}
	if _, ok, err := app.store.PublicName(); err != nil || !ok {
		t.Fatalf("the claim was not recorded: %v", err)
	}
	st := app.pn.State()
	if st.Status != publicname.StatusPrepared || st.Name == "" {
		t.Fatalf("a prepared name is held and is not being issued a certificate: %+v", st)
	}
	if st.Relays != nil {
		t.Fatalf("the card lists relays nobody has asked the service for: %+v", *st.Relays)
	}
	ordered := func() (n int) {
		for _, m := range managers {
			m.Mu.Lock()
			n += m.Ensures + m.Renews
			m.Mu.Unlock()
		}
		return n
	}
	app.pn.PrepareAtBirth(prepared) // asked twice: one name
	app.pn.AutoClaim(prepared)      // a later start
	app.pn.LearnServiceZone()       // the start's own asking
	app.pn.Renew()                  // the daily pass
	app.pn.Signal()                 // the route looked at again, with nothing changed
	for look := 1; look <= 3; look++ {
		if err := app.pn.LookAtRouteForTest(t.Context()); err != nil {
			t.Fatalf("look %d of the route owner: %v", look, err)
		}
	}
	if len(pub.Claims) != 1 || ordered() != 0 || app.pn.State().Status != publicname.StatusPrepared {
		t.Fatalf("a held name was claimed again or had a certificate ordered: %d claims, %d orders, %+v", len(pub.Claims), ordered(), app.pn.State())
	}
	if statuses, reads, writes := pub.Asked(); statuses+reads+writes != 0 {
		t.Fatalf("a prepared identity asked the certificate service %d status requests, %d signed reads and %d route writes: nobody has asked it for anything, and it asks nothing", statuses, reads, writes)
	}

	// A person at the card asks which relays are offered: one request, and
	// the card has them. Asked again at once, the answer in hand is given.
	for ask := 1; ask <= 2; ask++ {
		st, err := app.pn.Look()
		if err != nil || st.Relays == nil || len(*st.Relays) != 1 || !(*st.Relays)[0].Suggested {
			t.Fatalf("asking %d: the card was not given the service's relay, with the one to choose marked: %+v, %v", ask, st.Relays, err)
		}
	}
	if statuses, reads, writes := pub.Asked(); statuses != 1 || reads+writes != 0 {
		t.Fatalf("two askings at the card cost %d status requests, %d reads and %d writes, want one status request", statuses, reads, writes)
	}

	// A relay is chosen: the name leads somewhere now, and the same signal
	// that tells the route owner orders the certificate.
	app.cfgMu.Lock()
	app.cfg.Certificate.RouteMode, app.cfg.Certificate.RelayEndpoint = publicname.RouteRelay, "relay.example.test:443"
	app.cfgMu.Unlock()
	app.pn.Signal()
	app.pn.Signal() // twice, as a burst of changes does: one order
	waitStatus(t, app, publicname.StatusIssued)
	if n := ordered(); n != 1 {
		t.Fatalf("choosing a relay ordered the certificate %d times, want once", n)
	}
	// And now the route is the owner's to publish: its look asks.
	if err := app.pn.LookAtRouteForTest(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, reads, _ := pub.Asked(); reads == 0 {
		t.Fatal("with a relay chosen the route owner's look still asked the service nothing")
	}
}

func TestABirthPreparesTheNewIdentitysPublicName(t *testing.T) {
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			fmt.Fprint(w, `{"data":[{"id":"birth-model"}]}`)
		case "/v1/chat/completions":
			fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","content":"Hello, I am Mira."},"finish_reason":"stop"}]}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer provider.Close()
	a := birthApp(t, providerEntry{
		Name: "Local", APIType: "openai", URL: provider.URL + "/v1",
		APIKey: "stored-birth-key", DefaultModel: "birth-model", Default: true,
	})
	defer stopApp(t, a)
	pub := &publicnametest.Publisher{Zone: "example.test"}
	var managers []*publicnametest.Manager
	a.pn.NewPublisher = func(publicname.Settings) (publicname.NamePublisher, error) { return pub, nil }
	a.pn.NewCertManager = publicnametest.Managers[publicname.CertificateManager](&managers)
	on := true
	a.cfgMu.Lock()
	a.cfg.Certificate.Prepare = &on // this test binary turns the product's default off for every other test
	a.cfgMu.Unlock()

	if _, err := a.handleGenesis(t.Context(), &dashboard.GenesisRequest{Provider: "Local", Endpoint: provider.URL + "/v1", Model: "birth-model"}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, ok, err := a.store.PublicName(); err != nil {
			t.Fatal(err)
		} else if ok {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the birth ended and the new identity holds no name: nothing claimed one for it")
		}
		time.Sleep(20 * time.Millisecond)
	}
	waitStatus(t, a, publicname.StatusPrepared)
	pub.Mu.Lock()
	claims := len(pub.Claims)
	pub.Mu.Unlock()
	if statuses, reads, writes := pub.Asked(); claims != 1 || statuses+reads+writes != 0 {
		t.Fatalf("the birth made %d claims and %d other requests of the certificate service, want one claim and nothing else", claims, statuses+reads+writes)
	}
	for _, m := range managers {
		m.Mu.Lock()
		ordered := m.Ensures + m.Renews
		m.Mu.Unlock()
		if ordered != 0 {
			t.Fatalf("the birth ordered a certificate for a name nothing leads to (%d)", ordered)
		}
	}
}

// upgradedIdentity is an identity that was alive before this program
// reached it, on the ordinary install of the released program before this
// one, in a directory of the test's own. Its configuration is the file that
// program wrote for a first install (testdata/identity-by-v0.1.17/config.json;
// the README there says how it was made), read by this program's loader.
// The identity is given birth here, as this package's other tests give
// birth, under the Ring 0 the platform serves now (genesistest fetches it
// live): no key and no Ring 0 are kept in the tree. Its record is what that
// birth leaves, the birth and the founding relationship, with no name in
// it. Nothing of the file is changed but what a test must have of its own:
// where the identity's files are, a model provider that is never called, a
// free port, and the Ring 5 the test binary serves.
func upgradedIdentity(t *testing.T) *Config {
	t.Helper()
	dir := t.TempDir()
	written, err := os.ReadFile(filepath.Join("testdata", "identity-by-v0.1.17", "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.json"), written, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(filepath.Join(dir, "config.json"))
	if err != nil {
		t.Fatalf("this program reading the configuration the earlier program wrote: %v", err)
	}
	// The file is what it says: the ordinary install of an earlier
	// program, which states nothing about a public name.
	if cfg.Certificate.Prepare != nil || cfg.Certificate.ServerURL != "" || cfg.Certificate.RouteMode != "" || cfg.Dashboard.TLS || !dashboard.IsLoopback(cfg.Dashboard.Host) {
		t.Fatalf("the fixture's configuration is not the ordinary install's: certificate %+v, dashboard host %q tls %v", cfg.Certificate, cfg.Dashboard.Host, cfg.Dashboard.TLS)
	}
	// The birth is the genesis package's own, not the app's birth door, so
	// nothing at it claims a name (PrepareAtBirth is the door's): the
	// identity holds none when this program first starts it.
	cfg.Identity.KeyPath, cfg.Identity.LedgerPath, cfg.Identity.DBPath = birthFixture(t, dir, "Upgraded")
	buildPriorProjection(t, cfg.Identity.LedgerPath, cfg.Identity.DBPath)
	cfg.LLM = withTestProvider(t, dir, "test", "https://127.0.0.1:1", "m", "sk-x")
	cfg.Dashboard.Port = 0
	cfg.Tools.CWD = dir
	cfg.Genesis.FirewallURL = genesistest.Ring5Server(t)
	return cfg
}

func TestAnIdentityThatWasAlreadyAliveClaimsNoNameByItself(t *testing.T) {
	pub := &publicnametest.Publisher{Zone: "example.test"}
	var managers []*publicnametest.Manager
	cfg := upgradedIdentity(t)
	// Unset is on in the product. This test binary reads unset as off, so
	// that no other test's identity prepares (lane_harness_test.go); the
	// product's reading of this file is put back for this identity.
	on := true
	cfg.Certificate.Prepare = &on
	ended := make(chan struct{}, 8)
	was := startNameStep
	startNameStep = func() { ended <- struct{}{} }
	t.Cleanup(func() { startNameStep = was })

	app := bootNamed(t, cfg, pub, &managers)
	stopped := false
	defer func() {
		if !stopped {
			stopApp(t, app)
		}
	}()
	select {
	case <-ended:
	case <-time.After(60 * time.Second):
		t.Fatal("the start's public-name job did not end: nothing it asked can be counted")
	}
	app.pn.Renew() // the daily pass, were its alarm armed from an earlier life
	named := func() int {
		t.Helper()
		raw, err := os.ReadFile(cfg.Identity.LedgerPath)
		if err != nil {
			t.Fatal(err)
		}
		return strings.Count(string(raw), `"`+string(ledger.EventNetworkNameClaimed)+`"`)
	}
	if _, ok, err := app.store.PublicName(); err != nil || ok {
		t.Fatalf("an identity that was already alive holds a name it was never asked about: %v", err)
	}
	statuses, reads, writes := pub.Asked()
	if len(pub.Claims) != 0 || statuses+reads+writes != 0 {
		t.Fatalf("an upgraded identity's start sent the name service %d claims and %d other requests, with no act of its operator", len(pub.Claims), statuses+reads+writes)
	}
	if n := named(); n != 0 {
		t.Fatalf("a start wrote %d name-claimed event(s) into the record of an identity that was already alive", n)
	}
	// Its operator's act claims one.
	if _, err := app.pn.Claim(); err != nil || len(pub.Claims) != 1 || named() != 1 {
		t.Fatalf("the operator's own claim: %d claims, %d in the record, %v", len(pub.Claims), named(), err)
	}
	stopped = true
	stopApp(t, app)
	if len(pub.Claims) != 1 {
		t.Fatalf("by the time the identity stopped, %d claims had been sent for one act of its operator", len(pub.Claims))
	}
}

func TestAPreparedCertificateIsTheOperatorsToAskFor(t *testing.T) {
	on := true
	for _, c := range []struct {
		what  string
		ahead bool
		claim func(*App)
	}{
		{"certificate.prepare_certificate", true, func(a *App) { a.pn.PrepareAtBirth(publicNameSettingsOf(a.configSnapshot())) }},
		{"the operator's own claim", false, func(a *App) {
			if _, err := a.pn.Claim(); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		pub := &publicnametest.Publisher{Zone: "example.test"}
		var managers []*publicnametest.Manager
		app := publicNameApp(t, t.TempDir(), pub, &managers)
		app.cfgMu.Lock()
		app.cfg.Certificate.Prepare, app.cfg.Certificate.PrepareCertificate = &on, c.ahead
		app.cfgMu.Unlock()
		c.claim(app)
		if len(pub.Claims) != 1 {
			stopApp(t, app)
			t.Fatalf("%s: %d claims, want one", c.what, len(pub.Claims))
		}
		waitStatus(t, app, publicname.StatusIssued)
		stopApp(t, app)
	}
}

func TestAFailedClaimKeepsTheLocalCertificateAndTheDailyPassRetries(t *testing.T) {
	dir := t.TempDir()
	pub := &publicnametest.Publisher{Zone: "example.test", Fail: errors.New("service down")}
	var managers []*publicnametest.Manager
	app := publicNameApp(t, dir, pub, &managers)
	defer stopApp(t, app)
	network := app.configSnapshot()
	network.Dashboard.Host = "10.0.0.5"
	app.pn.AutoClaim(publicNameSettingsOf(network))
	if _, ok, _ := app.store.PublicName(); ok {
		t.Fatal("a failed claim recorded a name")
	}
	if app.pn.State().Status != publicname.StatusNone {
		t.Fatalf("state after a failed claim: %+v", app.pn.State())
	}
	if got := servedCert(t, app.dashboard.BoundAddr(), "127.0.0.1"); got.VerifyHostname("127.0.0.1") != nil {
		t.Fatal("the local certificate must keep serving after a failed claim")
	}
	pub.Mu.Lock()
	pub.Fail = nil
	pub.Mu.Unlock()
	app.cfgMu.Lock()
	app.cfg.Dashboard.Host = "10.0.0.5"
	app.cfgMu.Unlock()
	app.pn.Renew() // the daily pass with no name is the retry
	if len(pub.Claims) != 1 {
		t.Fatalf("the daily pass must retry the claim, got %d", len(pub.Claims))
	}
	waitStatus(t, app, publicname.StatusIssued)
}

func TestANetworkBindImpliesTLS(t *testing.T) {
	for _, c := range []struct {
		host string
		tls  bool
		want bool
	}{
		{"", false, false}, {"127.0.0.1", false, false}, {"localhost", false, false}, {"::1", false, false},
		{"10.0.0.5", false, true}, {"0.0.0.0", false, true}, {"::", false, true}, {"10.0.0.5", true, true}, {"127.0.0.1", true, true},
	} {
		cfg := &Config{}
		cfg.Dashboard.Host = c.host
		cfg.Dashboard.TLS = c.tls
		applyDefaults(cfg)
		if cfg.Dashboard.TLS != c.want {
			t.Errorf("host %q tls %v -> %v, want %v", c.host, c.tls, cfg.Dashboard.TLS, c.want)
		}
	}
}

func TestTheRouteOwnerPublishesTheWholeAddressSet(t *testing.T) {
	dir := t.TempDir()
	pub := &publicnametest.Publisher{Zone: "example.test"}
	var managers []*publicnametest.Manager
	app := publicNameApp(t, dir, pub, &managers)
	defer stopApp(t, app)
	if _, err := app.pn.Claim(); err != nil {
		t.Fatal(err)
	}
	waitStatus(t, app, publicname.StatusIssued)
	name := app.pn.State().Name

	// A loopback bind is reachable from nowhere else, so the route is
	// empty — and empty is published, not skipped: the service holds a
	// withdrawal as a tombstone rather than as an absence.
	if _, err := app.pn.ReconcileForTest(context.Background()); err != nil {
		t.Fatalf("loopback pass: %v", err)
	}
	writes, last := pub.LastRoute()
	if writes != 1 || len(last) != 0 {
		t.Fatalf("a loopback bind published %v (%d writes)", last, writes)
	}

	// A network bind publishes that address, and the state says so.
	app.cfgMu.Lock()
	app.cfg.Dashboard.Host = "10.0.0.5"
	app.cfgMu.Unlock()
	if _, err := app.pn.ReconcileForTest(context.Background()); err != nil {
		t.Fatalf("network pass: %v", err)
	}
	pub.Mu.Lock()
	last = pub.Replaced[len(pub.Replaced)-1]
	pub.Mu.Unlock()
	if len(last) != 1 || last[0].Type != "A" || last[0].Value != "10.0.0.5" {
		t.Fatalf("the route must carry the address: %v", last)
	}
	if st := app.pn.State(); st.Address != "10.0.0.5" {
		t.Fatalf("Settings must carry the published route: %+v", st)
	}

	// AND IT DOES NOT SPEND A MUTATION ON NO CHANGE. The identity has
	// twenty an hour for every name it holds and its ACME challenges
	// too; republishing an unchanged route inside its lease is how that
	// budget disappears.
	pub.Mu.Lock()
	before := len(pub.Replaced)
	pub.Mu.Unlock()
	if _, err := app.pn.ReconcileForTest(context.Background()); err != nil {
		t.Fatalf("idempotent pass: %v", err)
	}
	pub.Mu.Lock()
	after := len(pub.Replaced)
	pub.Mu.Unlock()
	if after != before {
		t.Fatalf("an unchanged route was published again (%d -> %d)", before, after)
	}
	_ = name
}

// A zone move is the platform's act and a new claim on the operator's:
// the runtime learns the service's zone, offers the move when it is not
// the name's, claims a new id under the new zone as a second record,
// issues the certificate afresh through a new manager, rewires the
// dashboard to the new name, and refuses to move again under the zone
// it already holds.
func TestTheZoneMoveIsTheOperatorsActAndReissuesTheCertificate(t *testing.T) {
	dir := t.TempDir()
	pub := &publicnametest.Publisher{Zone: "id.example.test"}
	var managers []*publicnametest.Manager
	app := publicNameApp(t, dir, pub, &managers)
	defer stopApp(t, app)
	first, err := app.pn.Claim()
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	waitStatus(t, app, publicname.StatusIssued)
	if st := app.pn.State(); st.CanMove || st.ServiceZone != "id.example.test" || st.Zone != "id.example.test" {
		t.Fatalf("nothing to move while the service serves the name's zone: %+v", st)
	}
	if _, err := app.pn.Move(); err == nil {
		t.Fatal("a move under the zone already held must be refused")
	}
	// The platform moves the zone; the daily pass notices.
	pub.SetZone("aiios.id", true)
	app.pn.Renew()
	st := app.pn.State()
	if !st.CanMove || st.ServiceZone != "aiios.id" || st.Name != first.Name {
		t.Fatalf("the move must be offered once the service's zone differs: %+v", st)
	}
	before := app.ledger.LastSeq()
	moved, err := app.pn.Move()
	if err != nil {
		t.Fatalf("move: %v", err)
	}
	if len(pub.Claims) != 2 || moved.Name != pub.Claims[1]+".aiios.id" || moved.Zone != "aiios.id" || moved.CanMove {
		t.Fatalf("the move claims a flat name under the new zone: %+v claims=%v", moved, pub.Claims)
	}
	if app.ledger.LastSeq() != before+1 {
		t.Fatalf("the move minted %d records, want 1", app.ledger.LastSeq()-before)
	}
	pn, ok, _ := app.store.PublicName()
	if !ok || pn.Name != moved.Name || pn.Zone != "aiios.id" {
		t.Fatalf("the projection carries the new name: %+v", pn)
	}
	waitStatus(t, app, publicname.StatusIssued)
	if len(managers) != 2 || managers[1].Cfg.Name != moved.Name || managers[1].Ensures != 1 {
		t.Fatalf("a new manager for the new name, asked once: %d managers, last %q ensures=%d", len(managers), managers[len(managers)-1].Cfg.Name, managers[len(managers)-1].Ensures)
	}
	addr := app.dashboard.BoundAddr()
	_, port, _ := net.SplitHostPort(addr)
	if got := servedCert(t, addr, moved.Name); got.VerifyHostname(moved.Name) != nil {
		t.Fatal("the dashboard does not serve the certificate for the new name")
	}
	if app.dashboard.Origin() != "https://"+moved.Name+":"+port {
		t.Fatalf("the advertised origin did not move: %q", app.dashboard.Origin())
	}
	// Under the new zone, a second move is refused; the old zone is history.
	if _, err := app.pn.Move(); err == nil {
		t.Fatal("a move under the zone already held must be refused")
	}
}

// With a relay configured, the name is carried from anywhere: the
// runtime registers it at the relay with its own key, a browser at the
// relay asking for the name reaches this dashboard's own TLS bind under
// this identity's certificate, the host gate admits the bare name and
// the advertised origin carries no port; after a zone move the new name
// is registered in the old one's place.
func TestTheNameIsCarriedByTheRelayWhenOneIsConfigured(t *testing.T) {
	dir := t.TempDir()
	rly := publicnametest.NewRelay(t)
	cfg := birthNamed(t, dir)

	cfg.Dashboard.RequireToken = true
	cfg.Certificate.RouteMode = publicname.RouteRelay
	cfg.Certificate.RelayEndpoint = rly.Addr()
	pub := &publicnametest.Publisher{Zone: "id.example.test", Relays: []string{rly.Addr()}}
	var managers []*publicnametest.Manager
	app := New(cfg)
	app.pn.NewPublisher = func(publicname.Settings) (publicname.NamePublisher, error) { return pub, nil }
	app.pn.NewCertManager = publicnametest.Managers[publicname.CertificateManager](&managers)
	app.pn.RelayTLS = rly.TLSConfig()
	if err := startLiveForTest(app); err != nil {
		t.Fatalf("boot: %v", err)
	}
	defer stopApp(t, app)
	canonical, env, err := witness.EnsureIdentityEnvelope(witness.AsIdentityKey(app.keyPair), app.store, filepath.Dir(app.ledger.Path()))
	if err != nil {
		t.Fatal(err)
	}
	// The publisher's push reaches the relay before the identity registers.
	first, err := app.pn.Claim()
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	rly.Know(first.Name, canonical, env)
	waitStatus(t, app, publicname.StatusIssued)
	// Reconcile explicitly so an invalid fixture cannot race a manually started tunnel.
	if _, err := app.pn.ReconcileForTest(t.Context()); err != nil {
		t.Fatalf("relay route reconciliation: %v", err)
	}
	originBefore := app.pn.State().Origin
	startRelayForTest(t, app, first.Name, rly.Addr())
	waitFor(t, "the registration at the relay", func() bool { return app.pn.State().RelayConnected })
	// A RELAY MOVES THE URL ONLY BY A CHOICE THAT NAMES ITS PORT. This used
	// to rewrite the origin to the bare name on 443 whenever a relay was
	// configured, silently changing the address people had bookmarked. DNS
	// carries no port, so behind a relay the name is reached on the relay's
	// port: the configuration here names it, the name's URL carried it from
	// the claim on, and starting the relay moves nothing.
	if st := app.pn.State(); st.Relay != rly.Addr() || st.Origin != originBefore || app.dashboard.Origin() != originBefore {
		t.Fatalf("starting the relay changed the origin (%q -> %q, dashboard %q)", originBefore, st.Origin, app.dashboard.Origin())
	}
	_, relayPort, _ := net.SplitHostPort(rly.Addr())
	if want := "https://" + net.JoinHostPort(first.Name, relayPort); originBefore != want {
		t.Fatalf("the name's URL is %q and the route names a relay on %s: want %q", originBefore, relayPort, want)
	}
	if !app.dashboard.HostAllowedForTest(net.JoinHostPort(first.Name, relayPort)) {
		t.Fatal("the dashboard refuses the Host a browser sends at the relay's port")
	}
	browse := func(name string) int {
		t.Helper()
		roots := x509.NewCertPool()
		roots.AddCert(managers[len(managers)-1].Certificate().Leaf)
		bc, err := tls.Dial("tcp", rly.Addr(), &tls.Config{ServerName: name, RootCAs: roots, MinVersion: tls.VersionTLS12})
		if err != nil {
			t.Fatalf("the browser's session through the relay: %v", err)
		}
		defer bc.Close()
		req, _ := http.NewRequest("GET", "https://"+net.JoinHostPort(name, app.dashboard.BoundPort())+"/", nil)
		req.Header.Set("Accept", "text/html")
		if err := req.Write(bc); err != nil {
			t.Fatal(err)
		}
		res, err := http.ReadResponse(bufio.NewReader(bc), req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		return res.StatusCode
	}
	if code := browse(first.Name); code != http.StatusOK {
		t.Fatalf("the dashboard through the relay answered %d", code)
	}
	// The zone moves; the new name takes the old one's place at the relay.
	pub.SetZone("aiios.id", true)
	app.pn.Renew()
	moved, err := app.pn.Move()
	if err != nil {
		t.Fatalf("move: %v", err)
	}
	rly.Know(moved.Name, canonical, env)
	waitStatus(t, app, publicname.StatusIssued)
	startRelayForTest(t, app, moved.Name, rly.Addr())
	waitFor(t, "the new name at the relay", func() bool {
		st := app.pn.State()
		return st.RelayConnected && st.Name == moved.Name
	})
	if code := browse(moved.Name); code != http.StatusOK {
		t.Fatalf("the moved name through the relay answered %d", code)
	}
}

func TestALoopbackIdentityWithNoHTTPSIsCarriedByARelay(t *testing.T) {
	dir := t.TempDir()
	rly := publicnametest.NewRelay(t)
	cfg := birthNamed(t, dir)
	cfg.Dashboard.TLS = false
	cfg.Dashboard.RequireToken = true
	cfg.Certificate.RouteMode = publicname.RouteRelay
	cfg.Certificate.RelayEndpoint = rly.Addr()
	pub := &publicnametest.Publisher{Zone: "id.example.test", Relays: []string{rly.Addr()}}
	var managers []*publicnametest.Manager
	app := New(cfg)
	app.pn.NewPublisher = func(publicname.Settings) (publicname.NamePublisher, error) { return pub, nil }
	app.pn.NewCertManager = publicnametest.Managers[publicname.CertificateManager](&managers)
	app.pn.RelayTLS = rly.TLSConfig()
	if err := startLiveForTest(app); err != nil {
		t.Fatalf("boot: %v", err)
	}
	defer stopApp(t, app)
	if local := app.dashboard.LocalURL(); !strings.HasPrefix(local, "http://127.0.0.1:") {
		t.Fatalf("the fixture is not a plain loopback dashboard: %q", local)
	}
	if st := app.pn.State(); !st.CanClaim || st.TLS {
		t.Fatalf("a plain loopback dashboard is not offered a name: %+v", st)
	}
	canonical, env, err := witness.EnsureIdentityEnvelope(witness.AsIdentityKey(app.keyPair), app.store, filepath.Dir(app.ledger.Path()))
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := app.pn.Claim()
	if err != nil {
		t.Fatalf("a name claimed with HTTPS off: %v", err)
	}
	rly.Know(claimed.Name, canonical, env)
	waitStatus(t, app, publicname.StatusIssued)
	// Reconcile explicitly so an invalid fixture cannot race a manually started tunnel.
	if _, err := app.pn.ReconcileForTest(t.Context()); err != nil {
		t.Fatalf("relay route reconciliation: %v", err)
	}
	startRelayForTest(t, app, claimed.Name, rly.Addr())
	waitFor(t, "the registration at the relay", func() bool { return app.pn.State().RelayConnected })

	_, relayPort, _ := net.SplitHostPort(rly.Addr())
	roots := x509.NewCertPool()
	roots.AddCert(managers[len(managers)-1].Certificate().Leaf)
	bc, err := tls.Dial("tcp", rly.Addr(), &tls.Config{ServerName: claimed.Name, RootCAs: roots, MinVersion: tls.VersionTLS12})
	if err != nil {
		t.Fatalf("the browser's session through the relay, to a dashboard with HTTPS off: %v", err)
	}
	defer bc.Close()
	req, _ := http.NewRequest("GET", "https://"+net.JoinHostPort(claimed.Name, relayPort)+"/", nil)
	req.Header.Set("Accept", "text/html")
	if err := req.Write(bc); err != nil {
		t.Fatal(err)
	}
	res, err := http.ReadResponse(bufio.NewReader(bc), req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("the dashboard through the relay answered %d", res.StatusCode)
	}
	// The machine's own address is still plain and still served.
	local, err := http.Get(app.dashboard.LocalURL() + "/")
	if err != nil || local.StatusCode != http.StatusOK {
		t.Fatalf("plain loopback beside the relay: %v %v", local, err)
	}
	local.Body.Close()
	// Boot's advice names the trusted URL for a name carried by a relay,
	// HTTPS setting or not.
	advice := strings.Join(app.pn.Advice(publicNameSettingsOf(app.configSnapshot()), nil), "\n")
	if !strings.Contains(advice, claimed.Name) {
		t.Fatalf("boot's advice does not name the relayed URL of a dashboard with HTTPS off: %q", advice)
	}
}

// DISCOVERY OFFERS CHOICES AND NEVER ENABLES ONE. The old behaviour was
// the opposite: with no relay in configuration, the relays the service
// fed were the ones the name registered at, so a service could put this
// identity behind a tunnel it never chose. Relay mode is now the
// operator's stored word, and a service advertising relays changes
// nothing on its own.
func TestDiscoveryNeverEnablesARelay(t *testing.T) {
	dir := t.TempDir()
	rly := publicnametest.NewRelay(t)
	cfg := birthNamed(t, dir)
	cfg.Dashboard.RequireToken = true
	// The service advertises a relay. Configuration names no mode.
	pub := &publicnametest.Publisher{Zone: "aiios.id", Flat: true, Relays: []string{rly.Addr()}}
	var managers []*publicnametest.Manager
	app := New(cfg)
	app.pn.NewPublisher = func(publicname.Settings) (publicname.NamePublisher, error) { return pub, nil }
	app.pn.NewCertManager = publicnametest.Managers[publicname.CertificateManager](&managers)
	app.pn.RelayTLS = rly.TLSConfig()
	if err := startLiveForTest(app); err != nil {
		t.Fatalf("boot: %v", err)
	}
	defer stopApp(t, app)
	canonical, env, err := witness.EnsureIdentityEnvelope(witness.AsIdentityKey(app.keyPair), app.store, filepath.Dir(app.ledger.Path()))
	if err != nil {
		t.Fatal(err)
	}
	st, err := app.pn.Claim()
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	rly.Know(st.Name, canonical, env)
	if _, err := app.pn.ReconcileForTest(context.Background()); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	time.Sleep(300 * time.Millisecond)
	if app.pn.State().RelayConnected {
		t.Fatal("an advertised relay enabled itself without the operator choosing it")
	}
	// What it published is the direct route, not a CNAME.
	_, last := pub.LastRoute()
	for _, r := range last {
		if r.Type == "CNAME" {
			t.Fatalf("discovery published a relay CNAME: %v", last)
		}
	}
}

func TestARelayRefusesAnUnauthenticatedDashboard(t *testing.T) {
	dir := t.TempDir()
	rly := publicnametest.NewRelay(t)
	cfg := birthNamed(t, dir)
	cfg.Dashboard.Host = "127.0.0.1" // loopback, so nothing escalates for us
	cfg.Dashboard.RequireToken = false
	cfg.Certificate.RouteMode = publicname.RouteRelay
	cfg.Certificate.RelayEndpoint = rly.Addr()
	pub := &publicnametest.Publisher{Zone: "id.example.test", Relays: []string{rly.Addr()}}
	app := New(cfg)
	app.pn.NewPublisher = func(publicname.Settings) (publicname.NamePublisher, error) { return pub, nil }
	app.pn.NewCertManager = publicnametest.Managers[publicname.CertificateManager](nil)
	app.pn.RelayTLS = rly.TLSConfig()
	if err := startLiveForTest(app); err != nil {
		t.Fatalf("boot: %v", err)
	}
	defer stopApp(t, app)
	canonical, env, err := witness.EnsureIdentityEnvelope(witness.AsIdentityKey(app.keyPair), app.store, filepath.Dir(app.ledger.Path()))
	if err != nil {
		t.Fatal(err)
	}
	first, err := app.pn.Claim()
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	rly.Know(first.Name, canonical, env)
	waitStatus(t, app, publicname.StatusIssued)
	startRelayForTest(t, app, first.Name, rly.Addr())

	// The certificate is the identity's and is unaffected; only the
	// tunnel is refused, and it stays refused.
	time.Sleep(300 * time.Millisecond)
	if app.pn.State().RelayConnected {
		t.Fatal("a relay must not carry the public internet to a dashboard that asks for nothing")
	}
}

func TestTheRelayGuardAsksTheRunningDashboardNotTheFile(t *testing.T) {
	dir := t.TempDir()
	rly := publicnametest.NewRelay(t)
	cfg := birthNamed(t, dir)
	cfg.Dashboard.Host = "127.0.0.1" // loopback: nothing escalates for us
	cfg.Dashboard.RequireToken = false
	cfg.Certificate.RouteMode = publicname.RouteRelay
	cfg.Certificate.RelayEndpoint = rly.Addr()
	pub := &publicnametest.Publisher{Zone: "id.example.test", Relays: []string{rly.Addr()}}
	app := New(cfg)
	app.pn.NewPublisher = func(publicname.Settings) (publicname.NamePublisher, error) { return pub, nil }
	app.pn.NewCertManager = publicnametest.Managers[publicname.CertificateManager](nil)
	app.pn.RelayTLS = rly.TLSConfig()
	if err := startLiveForTest(app); err != nil {
		t.Fatalf("boot: %v", err)
	}
	defer stopApp(t, app)

	// THE FILE NOW CLAIMS A TOKEN IS REQUIRED. The server was built
	// without one and is still serving without one, so the relay must
	// still refuse.
	app.cfgMu.Lock()
	app.cfg.Dashboard.RequireToken = true
	app.cfg.Dashboard.AccessToken = strings.Repeat("a", 64)
	app.cfgMu.Unlock()
	if app.dashboard.AccessTokenRequired() {
		t.Fatal("the running server has not been told to require anything")
	}

	canonical, env, err := witness.EnsureIdentityEnvelope(witness.AsIdentityKey(app.keyPair), app.store, filepath.Dir(app.ledger.Path()))
	if err != nil {
		t.Fatal(err)
	}
	first, err := app.pn.Claim()
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	rly.Know(first.Name, canonical, env)
	waitStatus(t, app, publicname.StatusIssued)
	time.Sleep(300 * time.Millisecond)
	if app.pn.State().RelayConnected {
		t.Fatal("a saved-but-not-installed token must not open the tunnel")
	}

	// And once the SERVER is actually serving with one, it may.
	app.dashboard.SetAccessToken(true, strings.Repeat("a", 64))
	if !app.dashboard.AccessTokenRequired() {
		t.Fatal("the running server now requires a token")
	}
}

// THE BOOT DOES NOT WAIT ON THE CERTIFICATE SERVICE. Wiring a claimed name
// asked the service which zone it serves, inline, for up to ten seconds,
// a few lines above the boot's promise that an unreachable service would
// not hold it. The boot's background job asks now.
func TestABootDoesNotWaitOnTheCertificateService(t *testing.T) {
	dir := t.TempDir()
	cfg := birthNamed(t, dir)
	pub := &publicnametest.Publisher{Zone: "id.example.test"}
	var managers []*publicnametest.Manager
	first := bootNamed(t, cfg, pub, &managers)
	if _, err := first.pn.Claim(); err != nil {
		t.Fatalf("claim: %v", err)
	}
	waitStatus(t, first, publicname.StatusIssued)
	stopApp(t, first)

	app := New(cfg)
	app.pn.NewPublisher = func(publicname.Settings) (publicname.NamePublisher, error) {
		return publicnametest.BlackholedStatus{Publisher: pub}, nil
	}
	app.pn.NewCertManager = publicnametest.Managers[publicname.CertificateManager](nil)
	began := time.Now()
	if err := startLiveForTest(app); err != nil {
		t.Fatalf("boot: %v", err)
	}
	took := time.Since(began)
	defer stopApp(t, app)
	if took > 5*time.Second {
		t.Fatalf("the boot waited %s on a certificate service that never answers", took.Round(time.Millisecond))
	}
	if st := app.pn.State(); st.Name == "" {
		t.Fatalf("the claimed name was not wired: %+v", st)
	}
}

// A CLAIM DOES NOT HOLD THE PAGE'S CONNECTION. The page's claim ran on its
// connection's read loop, so a slow certificate service left every other
// message from that page unanswered for up to a minute, in work no owner
// tracked. It runs in the app's job owner, as an install does.
func TestAPublicNameClaimDoesNotHoldTheConnection(t *testing.T) {
	dir := t.TempDir()
	cfg := birthNamed(t, dir)
	app := New(cfg)
	app.pn.NewPublisher = func(publicname.Settings) (publicname.NamePublisher, error) {
		return publicnametest.UnansweredClaim{Publisher: &publicnametest.Publisher{Zone: "id.example.test"}}, nil
	}
	app.pn.NewCertManager = publicnametest.Managers[publicname.CertificateManager](nil)
	if err := startLiveForTest(app); err != nil {
		t.Fatalf("boot: %v", err)
	}
	defer stopApp(t, app)
	conn := dialWSS(t, app.dashboard.Origin())
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := wsjsonWrite(ctx, conn, map[string]any{"type": "public_name_claim", "request_id": "claim-1"}); err != nil {
		t.Fatal(err)
	}
	if err := wsjsonWrite(ctx, conn, map[string]any{"type": "public_name_state", "request_id": "state-1"}); err != nil {
		t.Fatal(err)
	}
	readUntil(t, conn, func(m *dashboard.ServerMessage) bool { return m.RequestID == "state-1" && m.Type == "public_name" })
}
