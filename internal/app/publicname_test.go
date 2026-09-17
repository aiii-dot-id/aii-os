package app

import (
	"bufio"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/dashboard"
	"github.com/aiii-dot-id/aii-os/internal/relay/relaytest"
	"github.com/aiii-dot-id/aii-os/internal/witness"

	"github.com/aiii-dot-id/aii-os/internal/certs"
	"github.com/aiii-dot-id/aii-os/internal/ledger"
)

// .
// .
// .
// .

type stubNamePublisher struct {
	mu        sync.Mutex
	claims    []string
	zone      string
	flat      bool
	relays    []string
	alias     string
	records   map[string]certs.RecordSet
	replaced  [][]certs.Record
	fail      error
	readFail  error
	published map[string][]string
}

func (p *stubNamePublisher) Claim(_ context.Context, id string) (certs.ClaimResult, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.fail != nil {
		return certs.ClaimResult{}, p.fail
	}
	p.claims = append(p.claims, id)
	if p.flat {
		return certs.ClaimResult{Name: id + "." + p.zone, Alias: p.alias, Zone: p.zone}, nil
	}
	return certs.ClaimResult{Name: "ui." + id + "." + p.zone, Alias: p.alias, Zone: p.zone}, nil
}

// .
// .
func (p *stubNamePublisher) ReadRecords(_ context.Context, name string, _ time.Duration) (certs.RecordSet, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.readFail != nil {
		return certs.RecordSet{}, p.readFail
	}
	if p.fail != nil {
		return certs.RecordSet{}, p.fail
	}
	if set, ok := p.records[name]; ok {
		return set, nil
	}
	return certs.RecordSet{HTTPStatus: 200}, nil
}

// .
// .
// .
func (p *stubNamePublisher) ReplaceRecords(_ context.Context, name string, records []certs.Record, expected int64, _ time.Duration) (certs.RecordSet, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.fail != nil {
		return certs.RecordSet{}, p.fail
	}
	current := p.records[name]
	if current.Generation != expected {
		return certs.RecordSet{}, fmt.Errorf("%w: the name is at generation %d", certs.ErrRecordSetConflict, current.Generation)
	}
	p.replaced = append(p.replaced, records)
	leased := make([]certs.LeasedRecord, 0, len(records))
	for _, r := range records {
		leased = append(leased, certs.LeasedRecord{Type: r.Type, Value: r.Value,
			ExpiresAt: time.Now().Add(14 * time.Minute).UTC().Truncate(time.Second).Format(time.RFC3339)})
	}
	set := certs.RecordSet{Generation: current.Generation + 1, Managed: true, Alias: p.alias,
		Records: leased, Delivered: true, HTTPStatus: 202}
	if p.records == nil {
		p.records = map[string]certs.RecordSet{}
	}
	p.records[name] = set
	return set, nil
}

// .
// .
// .
// .
func startRelayForTest(t *testing.T, app *App, name, addr string) {
	t.Helper()
	host, ps, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(ps)
	if err != nil {
		t.Fatal(err)
	}
	app.startRelay(app.configSnapshot(), name, certs.RelayEndpoint{Name: host, Port: port})
}

func (p *stubNamePublisher) relayEndpoints() []certs.RelayEndpoint {
	out := make([]certs.RelayEndpoint, 0, len(p.relays))
	for _, r := range p.relays {
		host, port := r, 443
		if h, ps, err := net.SplitHostPort(r); err == nil {
			host = h
			if n, err := strconv.Atoi(ps); err == nil {
				port = n
			}
		}
		out = append(out, certs.RelayEndpoint{Name: host, Port: port})
	}
	return out
}

func (p *stubNamePublisher) ServiceStatus(context.Context) (certs.ServiceStatus, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.fail != nil {
		return certs.ServiceStatus{}, p.fail
	}
	// .
	// .
	return certs.ServiceStatus{
		Zone:           p.zone,
		Capabilities:   []string{certs.CapabilityRecordSets, certs.CapabilityRelayV2},
		RecordSetTypes: []string{certs.RecordA, certs.RecordAAAA, certs.RecordCNAME},
		RelayEndpoints: p.relayEndpoints(),
	}, nil
}
func (p *stubNamePublisher) setZone(zone string, flat bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.zone, p.flat = zone, flat
}
func (p *stubNamePublisher) Publish(_ context.Context, name, value string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.published == nil {
		p.published = map[string][]string{}
	}
	p.published[name] = append(p.published[name], value)
	return nil
}
func (p *stubNamePublisher) Unpublish(context.Context, string, string) error { return nil }

type stubManager struct {
	mu      sync.Mutex
	cfg     certs.Config
	cert    *tls.Certificate
	ensures int
	renews  int
	fail    error
}

func (m *stubManager) Certificate() *tls.Certificate {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.cert
}
func (m *stubManager) State() certs.State {
	return certs.State{Name: m.cfg.Name, NotAfter: time.Now().Add(60 * 24 * time.Hour)}
}
func (m *stubManager) DaysLeft() float64 { return 60 }
func (m *stubManager) Ensure(context.Context) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ensures++
	if m.fail != nil {
		return false, m.fail
	}
	if m.cert == nil {
		m.cert = selfSignedFor(m.cfg.Name)
		return true, nil
	}
	return false, nil
}
func (m *stubManager) Renew(context.Context) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.renews++
	return false, nil
}

func selfSignedFor(name string) *tls.Certificate {
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()), Subject: pkix.Name{CommonName: name}, DNSNames: []string{name},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, _ := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	leaf, _ := x509.ParseCertificate(der)
	return &tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key, Leaf: leaf}
}

// .
// .
func birthNamed(t *testing.T, dir string) *Config {
	t.Helper()
	keyPath, ledgerPath, dbPath := birthFixture(t, dir, "Named")
	buildPriorProjection(t, ledgerPath, dbPath)
	cfg := safebootConfig(t, dir, "Named", keyPath, ledgerPath, dbPath)
	cfg.Dashboard.TLS = true
	return cfg
}

func publicNameApp(t *testing.T, dir string, pub *stubNamePublisher, managers *[]*stubManager) *App {
	t.Helper()
	return bootNamed(t, birthNamed(t, dir), pub, managers)
}

func bootNamed(t *testing.T, cfg *Config, pub *stubNamePublisher, managers *[]*stubManager) *App {
	t.Helper()
	app := New(cfg)
	app.newPublisher = func(Config) (namePublisher, error) { return pub, nil }
	app.newCertManager = func(c certs.Config) (certificateManager, error) {
		m := &stubManager{cfg: c}
		*managers = append(*managers, m)
		return m, nil
	}
	if err := startLiveForTest(app); err != nil {
		t.Fatalf("boot: %v", err)
	}
	if reason, safe := app.SafeMode(); safe {
		app.Stop()
		t.Fatalf("SAFE: %s", reason)
	}
	return app
}

func waitStatus(t *testing.T, app *App, want string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if app.publicNameState().Status == want {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("status %q never reached; state %+v", want, app.publicNameState())
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
	pub := &stubNamePublisher{zone: "example.test"}
	var managers []*stubManager
	app := publicNameApp(t, dir, pub, &managers)
	defer app.Stop()

	if st := app.publicNameState(); st.Status != publicNameNone || !st.CanClaim {
		t.Fatalf("before the claim: %+v", st)
	}
	before := app.ledger.LastSeq()
	st, err := app.claimPublicName()
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if len(pub.claims) != 1 || !strings.HasPrefix(st.Name, "ui."+pub.claims[0]+".example.test") {
		t.Fatalf("the claim did not reach the service with the minted id: claims=%v state=%+v", pub.claims, st)
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
	waitStatus(t, app, publicNameIssued)
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
	// .
	again, err := app.claimPublicName()
	if err != nil || again.Name != st.Name || len(pub.claims) != 1 || app.ledger.LastSeq() != before+1 {
		t.Fatalf("a second claim changed something: %+v err=%v claims=%d seq=%d", again, err, len(pub.claims), app.ledger.LastSeq())
	}
	if len(managers) != 1 || managers[0].ensures != 1 {
		t.Fatalf("managers=%d ensures=%d, want one manager asked once", len(managers), managers[0].ensures)
	}
}

func TestAClaimNeedsHTTPS(t *testing.T) {
	dir := t.TempDir()
	keyPath, ledgerPath, dbPath := birthFixture(t, dir, "Plain")
	buildPriorProjection(t, ledgerPath, dbPath)
	cfg := safebootConfig(t, dir, "Plain", keyPath, ledgerPath, dbPath)
	app := New(cfg)
	pub := &stubNamePublisher{zone: "example.test"}
	app.newPublisher = func(Config) (namePublisher, error) { return pub, nil }
	if err := startLiveForTest(app); err != nil {
		t.Fatal(err)
	}
	defer app.Stop()
	before := app.ledger.LastSeq()
	if _, err := app.claimPublicName(); err == nil || !strings.Contains(err.Error(), "HTTPS") {
		t.Fatalf("a claim without HTTPS must be refused naming HTTPS, got %v", err)
	}
	if len(pub.claims) != 0 || app.ledger.LastSeq() != before {
		t.Fatal("a refused claim reached the service or the record")
	}
}

func TestARestartFindsTheNameInTheRecordAndServesItAgain(t *testing.T) {
	dir := t.TempDir()
	pub := &stubNamePublisher{zone: "example.test"}
	var managers []*stubManager
	cfg := birthNamed(t, dir)
	app := bootNamed(t, cfg, pub, &managers)
	st, err := app.claimPublicName()
	if err != nil {
		t.Fatal(err)
	}
	waitStatus(t, app, publicNameIssued)
	app.Stop()

	// .
	// .
	// .
	cfg2 := *cfg
	app2 := bootNamed(t, &cfg2, pub, &managers)
	defer app2.Stop()
	waitStatus(t, app2, publicNameIssued)
	st2 := app2.publicNameState()
	if st2.Name != st.Name {
		t.Fatalf("the restart named %q, want %q", st2.Name, st.Name)
	}
	if len(pub.claims) != 1 {
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
	pub := &stubNamePublisher{zone: "example.test"}
	var managers []*stubManager
	app := publicNameApp(t, dir, pub, &managers)
	defer app.Stop()
	if _, err := app.claimPublicName(); err != nil {
		t.Fatal(err)
	}
	waitStatus(t, app, publicNameIssued)
	res := certificateOwner{app}.OnAlarm(context.Background(), certificateAlarmID, "wall", 0, "")
	if !res.Accepted || res.NextDeadline == nil {
		t.Fatalf("the owner must accept and name its next hour: %+v", res)
	}
	if managers[0].renews != 1 {
		t.Fatalf("renews=%d, want 1", managers[0].renews)
	}
	// .
	managers[0].mu.Lock()
	managers[0].fail = context.DeadlineExceeded
	managers[0].mu.Unlock()
	if _, err := app.retryPublicCertificate(); err != nil {
		t.Fatal(err)
	}
	waitStatus(t, app, publicNameFailing)
	if st := app.publicNameState(); st.LastError == "" {
		t.Fatal("the failure was not shown")
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

// .
// .
// .
func TestANetworkBindClaimsItsNameWithoutAButton(t *testing.T) {
	dir := t.TempDir()
	pub := &stubNamePublisher{zone: "example.test"}
	var managers []*stubManager
	app := publicNameApp(t, dir, pub, &managers)
	defer app.Stop()
	loop := app.configSnapshot()
	app.autoClaimPublicName(loop)
	if len(pub.claims) != 0 {
		t.Fatal("loopback claimed a name")
	}
	declined := loop
	declined.Dashboard.Host = "10.0.0.5"
	declined.Certificate.ServerURL = "none"
	app.autoClaimPublicName(declined)
	if len(pub.claims) != 0 {
		t.Fatal("a declined service claimed a name")
	}
	if _, err := app.publisherFor(declined); err == nil || !strings.Contains(err.Error(), "server_url = none") {
		t.Fatalf("a declined service must say so: %v", err)
	}
	network := loop
	network.Dashboard.Host = "10.0.0.5"
	app.autoClaimPublicName(network)
	if len(pub.claims) != 1 {
		t.Fatalf("a network bind claims once, got %d", len(pub.claims))
	}
	waitStatus(t, app, publicNameIssued)
	st := app.publicNameState()
	if !strings.HasPrefix(st.Origin, "https://ui.") || !strings.HasPrefix(app.dashboard.Origin(), "https://ui.") {
		t.Fatalf("the claimed name is the advertised origin: state %q, dashboard %q", st.Origin, app.dashboard.Origin())
	}
	app.autoClaimPublicName(network)
	if len(pub.claims) != 1 {
		t.Fatal("a second boot claimed again")
	}
}

// .
// .
func TestAFailedClaimKeepsTheLocalCertificateAndTheDailyPassRetries(t *testing.T) {
	dir := t.TempDir()
	pub := &stubNamePublisher{zone: "example.test", fail: errors.New("service down")}
	var managers []*stubManager
	app := publicNameApp(t, dir, pub, &managers)
	defer app.Stop()
	network := app.configSnapshot()
	network.Dashboard.Host = "10.0.0.5"
	app.autoClaimPublicName(network)
	if _, ok, _ := app.store.PublicName(); ok {
		t.Fatal("a failed claim recorded a name")
	}
	if app.publicNameState().Status != publicNameNone {
		t.Fatalf("state after a failed claim: %+v", app.publicNameState())
	}
	if got := servedCert(t, app.dashboard.BoundAddr(), "127.0.0.1"); got.VerifyHostname("127.0.0.1") != nil {
		t.Fatal("the local certificate must keep serving after a failed claim")
	}
	pub.mu.Lock()
	pub.fail = nil
	pub.mu.Unlock()
	app.cfgMu.Lock()
	app.cfg.Dashboard.Host = "10.0.0.5"
	app.cfgMu.Unlock()
	app.renewPublicCertificate()
	if len(pub.claims) != 1 {
		t.Fatalf("the daily pass must retry the claim, got %d", len(pub.claims))
	}
	waitStatus(t, app, publicNameIssued)
}

// .
// .
// .
func TestBootAdviceNamesTheTrustedURLAndNeverTheRootWhenANameIsHeld(t *testing.T) {
	mat := &dashboard.TLSMaterial{CACertPath: "/x/dashboard-ca.crt", Regenerated: true}
	issued := dashboard.PublicNameState{Status: publicNameIssued, Origin: "https://ui.abc.example.test:8180"}
	for _, c := range []struct {
		name      string
		tlsOn     bool
		host      string
		st        dashboard.PublicNameState
		mat       *dashboard.TLSMaterial
		declined  bool
		wantLines int
		want      string
		never     string
	}{
		{"plain loopback", false, "127.0.0.1", dashboard.PublicNameState{Status: publicNameNone}, nil, false, 0, "", "certutil"},
		{"name held", true, "10.0.0.5", issued, mat, false, 1, "https://ui.abc.example.test:8180", "certutil"},
		{"name held and resolving", true, "10.0.0.5", dashboard.PublicNameState{Status: publicNameIssued, Origin: "https://ui.abc.example.test:8180", Address: "10.0.0.5", ResolvesTo: "10.0.0.5"}, mat, false, 1, "resolves to 10.0.0.5", "certutil"},
		{"name claiming", true, "10.0.0.5", dashboard.PublicNameState{Status: publicNameClaiming, Origin: "https://ui.abc.example.test:8180"}, mat, false, 1, "being issued", "certutil"},
		{"no name, root just minted", true, "10.0.0.5", dashboard.PublicNameState{Status: publicNameNone}, mat, false, 5, "certutil", ""},
		{"no name, root reused", true, "10.0.0.5", dashboard.PublicNameState{Status: publicNameNone}, &dashboard.TLSMaterial{CACertPath: "/x/dashboard-ca.crt"}, false, 1, "not claimed yet", "certutil"},
		{"declined", true, "10.0.0.5", dashboard.PublicNameState{Status: publicNameNone}, nil, true, 1, "server_url = none", "certutil"},
		{"tls on loopback", true, "127.0.0.1", dashboard.PublicNameState{Status: publicNameNone}, nil, false, 1, "secure context", "certutil"},
	} {
		got := dashboardAdvice(c.tlsOn, c.host, c.st, c.mat, c.declined)
		joined := strings.Join(got, "\n")
		if len(got) != c.wantLines {
			t.Errorf("%s: %d line(s), want %d: %q", c.name, len(got), c.wantLines, joined)
		}
		if c.want != "" && !strings.Contains(joined, c.want) {
			t.Errorf("%s: missing %q in %q", c.name, c.want, joined)
		}
		if c.never != "" && strings.Contains(joined, c.never) {
			t.Errorf("%s: %q must not appear in %q", c.name, c.never, joined)
		}
	}
}

// .
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

// .
// .
// .
func TestTheRouteOwnerPublishesTheWholeAddressSet(t *testing.T) {
	dir := t.TempDir()
	pub := &stubNamePublisher{zone: "example.test"}
	var managers []*stubManager
	app := publicNameApp(t, dir, pub, &managers)
	defer app.Stop()
	if _, err := app.claimPublicName(); err != nil {
		t.Fatal(err)
	}
	waitStatus(t, app, publicNameIssued)
	name := app.publicNameState().Name

	// .
	// .
	// .
	if _, err := app.reconcileRoute(context.Background()); err != nil {
		t.Fatalf("loopback pass: %v", err)
	}
	pub.mu.Lock()
	writes := len(pub.replaced)
	last := []certs.Record(nil)
	if writes > 0 {
		last = pub.replaced[writes-1]
	}
	pub.mu.Unlock()
	if writes != 1 || len(last) != 0 {
		t.Fatalf("a loopback bind published %v (%d writes)", last, writes)
	}

	// .
	app.cfgMu.Lock()
	app.cfg.Dashboard.Host = "10.0.0.5"
	app.cfgMu.Unlock()
	if _, err := app.reconcileRoute(context.Background()); err != nil {
		t.Fatalf("network pass: %v", err)
	}
	pub.mu.Lock()
	last = pub.replaced[len(pub.replaced)-1]
	pub.mu.Unlock()
	if len(last) != 1 || last[0].Type != certs.RecordA || last[0].Value != "10.0.0.5" {
		t.Fatalf("the route must carry the address: %v", last)
	}
	if st := app.publicNameState(); st.Address != "10.0.0.5" {
		t.Fatalf("Settings must carry the published route: %+v", st)
	}

	// .
	// .
	// .
	// .
	pub.mu.Lock()
	before := len(pub.replaced)
	pub.mu.Unlock()
	if _, err := app.reconcileRoute(context.Background()); err != nil {
		t.Fatalf("idempotent pass: %v", err)
	}
	pub.mu.Lock()
	after := len(pub.replaced)
	pub.mu.Unlock()
	if after != before {
		t.Fatalf("an unchanged route was published again (%d -> %d)", before, after)
	}
	_ = name
}

// .
// .
// .
// .
// .
// .
func TestTheZoneMoveIsTheOperatorsActAndReissuesTheCertificate(t *testing.T) {
	dir := t.TempDir()
	pub := &stubNamePublisher{zone: "id.example.test"}
	var managers []*stubManager
	app := publicNameApp(t, dir, pub, &managers)
	defer app.Stop()
	first, err := app.claimPublicName()
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	waitStatus(t, app, publicNameIssued)
	if st := app.publicNameState(); st.CanMove || st.ServiceZone != "id.example.test" || st.Zone != "id.example.test" {
		t.Fatalf("nothing to move while the service serves the name's zone: %+v", st)
	}
	if _, err := app.movePublicName(); err == nil {
		t.Fatal("a move under the zone already held must be refused")
	}
	// .
	pub.setZone("aiios.id", true)
	app.renewPublicCertificate()
	st := app.publicNameState()
	if !st.CanMove || st.ServiceZone != "aiios.id" || st.Name != first.Name {
		t.Fatalf("the move must be offered once the service's zone differs: %+v", st)
	}
	before := app.ledger.LastSeq()
	moved, err := app.movePublicName()
	if err != nil {
		t.Fatalf("move: %v", err)
	}
	if len(pub.claims) != 2 || moved.Name != pub.claims[1]+".aiios.id" || moved.Zone != "aiios.id" || moved.CanMove {
		t.Fatalf("the move claims a flat name under the new zone: %+v claims=%v", moved, pub.claims)
	}
	if app.ledger.LastSeq() != before+1 {
		t.Fatalf("the move minted %d records, want 1", app.ledger.LastSeq()-before)
	}
	pn, ok, _ := app.store.PublicName()
	if !ok || pn.Name != moved.Name || pn.Zone != "aiios.id" {
		t.Fatalf("the projection carries the new name: %+v", pn)
	}
	waitStatus(t, app, publicNameIssued)
	if len(managers) != 2 || managers[1].cfg.Name != moved.Name || managers[1].ensures != 1 {
		t.Fatalf("a new manager for the new name, asked once: %d managers, last %q ensures=%d", len(managers), managers[len(managers)-1].cfg.Name, managers[len(managers)-1].ensures)
	}
	addr := app.dashboard.BoundAddr()
	_, port, _ := net.SplitHostPort(addr)
	if got := servedCert(t, addr, moved.Name); got.VerifyHostname(moved.Name) != nil {
		t.Fatal("the dashboard does not serve the certificate for the new name")
	}
	if app.dashboard.Origin() != "https://"+moved.Name+":"+port {
		t.Fatalf("the advertised origin did not move: %q", app.dashboard.Origin())
	}
	// .
	if _, err := app.movePublicName(); err == nil {
		t.Fatal("a move under the zone already held must be refused")
	}
}

// .
// .
// .
// .
// .
// .
func TestTheNameIsCarriedByTheRelayWhenOneIsConfigured(t *testing.T) {
	dir := t.TempDir()
	rly := relaytest.New(t)
	cfg := birthNamed(t, dir)
	// .
	// .
	// .
	cfg.Dashboard.RequireToken = true
	cfg.Certificate.RouteMode = routeRelay
	cfg.Certificate.RelayEndpoint = rly.Addr()
	pub := &stubNamePublisher{zone: "id.example.test"}
	var managers []*stubManager
	app := New(cfg)
	app.newPublisher = func(Config) (namePublisher, error) { return pub, nil }
	app.newCertManager = func(c certs.Config) (certificateManager, error) {
		m := &stubManager{cfg: c}
		managers = append(managers, m)
		return m, nil
	}
	app.relayTLS = rly.TLSConfig()
	if err := startLiveForTest(app); err != nil {
		t.Fatalf("boot: %v", err)
	}
	defer app.Stop()
	canonical, env, err := witness.EnsureIdentityEnvelope(witness.AsIdentityKey(app.keyPair), app.store)
	if err != nil {
		t.Fatal(err)
	}
	// .
	first, err := app.claimPublicName()
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	rly.Know(first.Name, canonical, env)
	waitStatus(t, app, publicNameIssued)
	originBefore := app.publicNameState().Origin
	startRelayForTest(t, app, first.Name, rly.Addr())
	waitFor(t, "the registration at the relay", func() bool { return app.publicNameState().RelayConnected })
	// .
	// .
	// .
	// .
	if st := app.publicNameState(); st.Relay != rly.Addr() || st.Origin != originBefore || app.dashboard.Origin() != originBefore {
		t.Fatalf("a relay changed the origin (%q -> %q, dashboard %q)", originBefore, st.Origin, app.dashboard.Origin())
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
	// .
	pub.setZone("aiios.id", true)
	app.renewPublicCertificate()
	moved, err := app.movePublicName()
	if err != nil {
		t.Fatalf("move: %v", err)
	}
	rly.Know(moved.Name, canonical, env)
	waitStatus(t, app, publicNameIssued)
	startRelayForTest(t, app, moved.Name, rly.Addr())
	waitFor(t, "the new name at the relay", func() bool {
		st := app.publicNameState()
		return st.RelayConnected && st.Name == moved.Name
	})
	if code := browse(moved.Name); code != http.StatusOK {
		t.Fatalf("the moved name through the relay answered %d", code)
	}
}

// .
// .
// .
// .
// .
// .
func TestDiscoveryNeverEnablesARelay(t *testing.T) {
	dir := t.TempDir()
	rly := relaytest.New(t)
	cfg := birthNamed(t, dir)
	cfg.Dashboard.RequireToken = true
	// .
	pub := &stubNamePublisher{zone: "aiios.id", flat: true, relays: []string{rly.Addr()}}
	var managers []*stubManager
	app := New(cfg)
	app.newPublisher = func(Config) (namePublisher, error) { return pub, nil }
	app.newCertManager = func(c certs.Config) (certificateManager, error) {
		m := &stubManager{cfg: c}
		managers = append(managers, m)
		return m, nil
	}
	app.relayTLS = rly.TLSConfig()
	if err := startLiveForTest(app); err != nil {
		t.Fatalf("boot: %v", err)
	}
	defer app.Stop()
	canonical, env, err := witness.EnsureIdentityEnvelope(witness.AsIdentityKey(app.keyPair), app.store)
	if err != nil {
		t.Fatal(err)
	}
	st, err := app.claimPublicName()
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	rly.Know(st.Name, canonical, env)
	if _, err := app.reconcileRoute(context.Background()); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	time.Sleep(300 * time.Millisecond)
	if app.publicNameState().RelayConnected {
		t.Fatal("an advertised relay enabled itself without the operator choosing it")
	}
	// .
	pub.mu.Lock()
	last := []certs.Record(nil)
	if len(pub.replaced) > 0 {
		last = pub.replaced[len(pub.replaced)-1]
	}
	pub.mu.Unlock()
	for _, r := range last {
		if r.Type == certs.RecordCNAME {
			t.Fatalf("discovery published a relay CNAME: %v", last)
		}
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
func TestARelayRefusesAnUnauthenticatedDashboard(t *testing.T) {
	dir := t.TempDir()
	rly := relaytest.New(t)
	cfg := birthNamed(t, dir)
	cfg.Dashboard.Host = "127.0.0.1"
	cfg.Dashboard.RequireToken = false
	cfg.Certificate.RouteMode = routeRelay
	cfg.Certificate.RelayEndpoint = rly.Addr()
	pub := &stubNamePublisher{zone: "id.example.test", relays: []string{rly.Addr()}}
	app := New(cfg)
	app.newPublisher = func(Config) (namePublisher, error) { return pub, nil }
	app.newCertManager = func(c certs.Config) (certificateManager, error) { return &stubManager{cfg: c}, nil }
	app.relayTLS = rly.TLSConfig()
	if err := startLiveForTest(app); err != nil {
		t.Fatalf("boot: %v", err)
	}
	defer app.Stop()
	canonical, env, err := witness.EnsureIdentityEnvelope(witness.AsIdentityKey(app.keyPair), app.store)
	if err != nil {
		t.Fatal(err)
	}
	first, err := app.claimPublicName()
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	rly.Know(first.Name, canonical, env)
	waitStatus(t, app, publicNameIssued)
	startRelayForTest(t, app, first.Name, rly.Addr())

	// .
	// .
	time.Sleep(300 * time.Millisecond)
	if app.publicNameState().RelayConnected {
		t.Fatal("a relay must not carry the public internet to a dashboard that asks for nothing")
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
func TestTheRelayGuardAsksTheRunningDashboardNotTheFile(t *testing.T) {
	dir := t.TempDir()
	rly := relaytest.New(t)
	cfg := birthNamed(t, dir)
	cfg.Dashboard.Host = "127.0.0.1"
	cfg.Dashboard.RequireToken = false
	cfg.Certificate.RouteMode = routeRelay
	cfg.Certificate.RelayEndpoint = rly.Addr()
	pub := &stubNamePublisher{zone: "id.example.test", relays: []string{rly.Addr()}}
	app := New(cfg)
	app.newPublisher = func(Config) (namePublisher, error) { return pub, nil }
	app.newCertManager = func(c certs.Config) (certificateManager, error) { return &stubManager{cfg: c}, nil }
	app.relayTLS = rly.TLSConfig()
	if err := startLiveForTest(app); err != nil {
		t.Fatalf("boot: %v", err)
	}
	defer app.Stop()

	// .
	// .
	// .
	app.cfgMu.Lock()
	app.cfg.Dashboard.RequireToken = true
	app.cfg.Dashboard.AccessToken = strings.Repeat("a", 64)
	app.cfgMu.Unlock()
	if app.dashboard.AccessTokenRequired() {
		t.Fatal("the running server has not been told to require anything")
	}

	canonical, env, err := witness.EnsureIdentityEnvelope(witness.AsIdentityKey(app.keyPair), app.store)
	if err != nil {
		t.Fatal(err)
	}
	first, err := app.claimPublicName()
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	rly.Know(first.Name, canonical, env)
	waitStatus(t, app, publicNameIssued)
	time.Sleep(300 * time.Millisecond)
	if app.publicNameState().RelayConnected {
		t.Fatal("a saved-but-not-installed token must not open the tunnel")
	}

	// .
	app.dashboard.SetAccessToken(true, strings.Repeat("a", 64))
	if !app.dashboard.AccessTokenRequired() {
		t.Fatal("the running server now requires a token")
	}
}
