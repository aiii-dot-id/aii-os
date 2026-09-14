package app

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log"
	"net"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/certs"
	"github.com/aiii-dot-id/aii-os/internal/cognitive"
	"github.com/aiii-dot-id/aii-os/internal/dashboard"
	"github.com/aiii-dot-id/aii-os/internal/ledger"
	"github.com/aiii-dot-id/aii-os/internal/relay"
	"github.com/aiii-dot-id/aii-os/internal/store"
	"github.com/aiii-dot-id/aii-os/internal/witness"
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
type certificateManager interface {
	Certificate() *tls.Certificate
	State() certs.State
	DaysLeft() float64
	Ensure(ctx context.Context) (bool, error)
	Renew(ctx context.Context) (bool, error)
}

// .
// .
type namePublisher interface {
	certs.Publisher
	Claim(ctx context.Context, nameID string) (certs.ClaimResult, error)
	ServiceStatus(ctx context.Context) (certs.ServiceStatus, error)
	// .
	// .
	// .
	ReadRecords(ctx context.Context, name string, lease time.Duration) (certs.RecordSet, error)
	ReplaceRecords(ctx context.Context, name string, records []certs.Record, expectedGeneration int64, lease time.Duration) (certs.RecordSet, error)
}

const (
	publicNameNone     = "none"
	publicNameClaiming = "claiming"
	publicNameIssued   = "issued"
	publicNameFailing  = "failing"

	certificateOwnerName = "certificate"
	certificateAlarmID   = "certificate.renewal"
	certificateHourLocal = 5
)

// .
type publicNameRuntime struct {
	mu         sync.Mutex
	name       *store.PublicName
	origin     string
	status     string
	lastError  string
	manager    certificateManager
	publisher  namePublisher
	address    string
	resolvesTo string
	// .
	// .
	// .
	serviceZone string
	// .
	// .
	// .
	alias string
	// .
	// .
	route      routeState
	routeWake  chan struct{}
	routeOwner bool
	// .
	// .
	routeNotBefore time.Time
	// .
	// .
	routeRev uint64
	// .
	// .
	routePassCancel context.CancelFunc
	// .
	// .
	relay       *relay.Client
	relayCancel context.CancelFunc
}

// .
// .
// .
func (a *App) publisherFor(cfg Config) (namePublisher, error) {
	if cfg.Certificate.serverURL() == "" {
		return nil, errors.New("certificate service declined by configuration (certificate.server_url = none)")
	}
	if a.newPublisher != nil {
		return a.newPublisher(cfg)
	}
	if a.keyPair == nil || a.store == nil {
		return nil, errors.New("not live")
	}
	key := witness.AsIdentityKey(a.keyPair)
	canonical, env, err := witness.EnsureIdentityEnvelope(key, a.store)
	if err != nil {
		return nil, fmt.Errorf("identity envelope: %w", err)
	}
	return certs.NewPublisherClient(cfg.Certificate.serverURL(), cfg.Certificate.TLSSPKISHA256, key, canonical, env)
}

// .
func (a *App) managerFor(cfg Config, name string, pub certs.Publisher) (certificateManager, error) {
	c := certs.Config{
		Dir:          filepath.Join(filepath.Dir(cfg.Identity.LedgerPath), "tls", "public"),
		Name:         name,
		DirectoryURL: cfg.Certificate.acmeDirectory(),
		Contact:      cfg.Certificate.Contact,
		Publisher:    pub,
	}
	if a.newCertManager != nil {
		return a.newCertManager(c)
	}
	return certs.New(c)
}

// .
// .
func publicOrigin(name, port string) string {
	if port == "" || port == "443" {
		return "https://" + name
	}
	return "https://" + name + ":" + port
}

// .
// .
// .
// .
// .
func (a *App) wirePublicName(cfg Config) error {
	if a.store == nil || a.dashboard == nil {
		return nil
	}
	pn, ok, err := a.store.PublicName()
	if err != nil {
		return fmt.Errorf("public name: %w", err)
	}
	if !ok {
		return nil
	}
	pub, err := a.publisherFor(cfg)
	if err != nil {
		return fmt.Errorf("public name %s: certificate service: %w", pn.Name, err)
	}
	mgr, err := a.managerFor(cfg, pn.Name, pub)
	if err != nil {
		return fmt.Errorf("public name %s: certificate manager: %w", pn.Name, err)
	}
	port := a.dashboard.BoundPort()
	origin := publicOrigin(pn.Name, port)
	a.dashboard.SetPublicCertificate(pn.Name, mgr.Certificate)
	a.dashboard.AllowHost(net.JoinHostPort(pn.Name, port))
	a.learnServiceZone(pub)
	// .
	// .
	// .
	// .
	// .
	// .
	if port == "443" {
		// .
		a.dashboard.AllowHost(pn.Name)
	}
	if cfg.Dashboard.Origin == "" {
		a.dashboard.SetOrigin(origin)
	}
	a.pn.mu.Lock()
	name := pn
	a.pn.name = &name
	a.pn.origin = origin
	a.pn.manager = mgr
	a.pn.publisher = pub
	if mgr.Certificate() != nil {
		a.pn.status = publicNameIssued
	} else {
		a.pn.status = publicNameClaiming
	}
	a.pn.mu.Unlock()
	log.Printf("public name: %s — origin %s", pn.Name, origin)
	// .
	// .
	// .
	// .
	a.startRouteOwner()
	a.signalRoute()
	if mgr.Certificate() == nil {
		go a.issuePublicCertificate(mgr)
	}
	if a.timeFac != nil {
		if err := armCertificateAlarm(a.timeFac, time.Now()); err != nil {
			log.Printf("public name: renewal alarm could not be armed: %v", err)
		}
	}
	return nil
}

// .
// .
// .
// .
// .
func (a *App) startRelay(cfg Config, name string, endpoint certs.RelayEndpoint) {
	addr := net.JoinHostPort(endpoint.Name, strconv.Itoa(endpoint.Port))
	a.pn.mu.Lock()
	// .
	// .
	// .
	// .
	if cur := a.pn.relay; cur != nil && cur.Name == name && cur.Relay == addr {
		a.pn.mu.Unlock()
		return
	}
	if a.pn.relayCancel != nil {
		a.pn.relayCancel()
		a.pn.relayCancel, a.pn.relay = nil, nil
	}
	a.pn.mu.Unlock()
	if endpoint.Name == "" || endpoint.Port <= 0 || a.keyPair == nil || a.store == nil {
		return
	}
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
	// .
	// .
	// .
	if a.dashboard == nil || !a.dashboard.AccessTokenRequired() {
		log.Printf("relay: REFUSED for %s — the running dashboard is not requiring an access token, and a relay would carry the public internet to it. Set dashboard.require_token true and RESTART (a saved setting does not take effect until then); the token is minted and shown once on the boot console.", name)
		return
	}
	key := witness.AsIdentityKey(a.keyPair)
	canonical, env, err := witness.EnsureIdentityEnvelope(key, a.store)
	if err != nil {
		log.Printf("relay: identity envelope: %v", err)
		return
	}
	// .
	// .
	id, err := witness.DeriveIdentityID(canonical, env)
	if err != nil {
		log.Printf("relay: identity id: %v", err)
		return
	}
	c := &relay.Client{Relay: addr, Name: name, IdentityID: id, Key: key, Env: env, Envelope: canonical, Local: a.dashboard.BoundAddr(), TLS: a.relayTLS}
	parent := a.bgCtx
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithCancel(parent)
	a.pn.mu.Lock()
	a.pn.relay, a.pn.relayCancel = c, cancel
	a.pn.mu.Unlock()
	a.runBackground(func() { c.Run(ctx) })
}

// .
// .
// .
// .
func (a *App) learnServiceZone(pub namePublisher) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	st, err := pub.ServiceStatus(ctx)
	if err != nil {
		return
	}
	zone := st.Zone
	a.pn.mu.Lock()
	a.pn.serviceZone = zone
	moved := a.pn.name != nil && a.pn.name.Zone != zone
	name := ""
	if a.pn.name != nil {
		name = a.pn.name.Name
	}
	a.pn.mu.Unlock()
	if moved {
		log.Printf("public name: the certificate service now serves %s and %s is under another zone — move the name under Settings → Dashboard → Public name", zone, name)
	}
}

// .
// .
// .
// .
// .
func (a *App) movePublicName() (dashboard.PublicNameState, error) {
	cfg := a.configSnapshot()
	if !cfg.Dashboard.TLS {
		return a.publicNameState(), errors.New("turn on HTTPS under Dashboard first — a public name is served over TLS only")
	}
	if a.store == nil || a.door == nil || a.dashboard == nil {
		return a.publicNameState(), errors.New("the identity is not live")
	}
	current, ok, err := a.store.PublicName()
	if err != nil {
		return a.publicNameState(), err
	}
	if !ok {
		return a.publicNameState(), errors.New("no public name is claimed — claim one instead of moving")
	}
	pub, err := a.publisherFor(cfg)
	if err != nil {
		return a.publicNameState(), err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	st, err := pub.ServiceStatus(ctx)
	if err != nil {
		return a.publicNameState(), fmt.Errorf("the certificate service's zone: %w", err)
	}
	zone := st.Zone
	if zone == current.Zone {
		return a.publicNameState(), fmt.Errorf("the certificate service still serves %s — there is nothing to move", zone)
	}
	id, err := certs.NewNameID()
	if err != nil {
		return a.publicNameState(), err
	}
	claimed, err := pub.Claim(ctx, id)
	name, claimedZone := claimed.Name, claimed.Zone
	if err != nil {
		return a.publicNameState(), fmt.Errorf("claim under %s: %w", zone, err)
	}
	if _, err := a.door.Append(ledger.EventNetworkNameClaimed, 0, store.PublicNamePayload{NameID: id, Name: name, Zone: claimedZone}, ""); err != nil {
		return a.publicNameState(), fmt.Errorf("record the move: %w", err)
	}
	log.Printf("public name: moved from %s to %s — the previous name is retired", current.Name, name)
	if err := a.wirePublicName(cfg); err != nil {
		return a.publicNameState(), err
	}
	return a.publicNameState(), nil
}

// .
// .
func (a *App) issuePublicCertificate(mgr certificateManager) {
	ctx := a.bgCtx
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	a.pn.mu.Lock()
	a.pn.status = publicNameClaiming
	a.pn.lastError = ""
	a.pn.mu.Unlock()
	_, err := mgr.Ensure(ctx)
	a.pn.mu.Lock()
	if err != nil {
		a.pn.status = publicNameFailing
		a.pn.lastError = err.Error()
	} else {
		a.pn.status = publicNameIssued
		a.pn.lastError = ""
	}
	name := ""
	if a.pn.name != nil {
		name = a.pn.name.Name
	}
	a.pn.mu.Unlock()
	if err != nil {
		log.Printf("public name %s: certificate not obtained: %v", name, err)
		a.maintenanceAlert("certificate", fmt.Sprintf("the certificate for %s was not obtained: %v — Settings → Dashboard → Public name → Retry", name, err))
		return
	}
	log.Printf("public name %s: certificate obtained, served now", name)
	if a.dashboard != nil {
		a.dashboard.BroadcastStatus()
	}
}

// .
// .
// .
func (a *App) claimPublicName() (dashboard.PublicNameState, error) {
	cfg := a.configSnapshot()
	if !cfg.Dashboard.TLS {
		return a.publicNameState(), errors.New("turn on HTTPS under Dashboard first — a public name is served over TLS only")
	}
	if a.store == nil || a.door == nil || a.dashboard == nil {
		return a.publicNameState(), errors.New("the identity is not live")
	}
	if _, ok, err := a.store.PublicName(); err != nil {
		return a.publicNameState(), err
	} else if ok {
		return a.publicNameState(), nil
	}
	pub, err := a.publisherFor(cfg)
	if err != nil {
		return a.publicNameState(), err
	}
	id, err := certs.NewNameID()
	if err != nil {
		return a.publicNameState(), err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	claimed, err := pub.Claim(ctx, id)
	name, zone := claimed.Name, claimed.Zone
	if err != nil {
		return a.publicNameState(), fmt.Errorf("claim: %w", err)
	}
	// .
	// .
	if _, err := a.door.Append(ledger.EventNetworkNameClaimed, 0, store.PublicNamePayload{NameID: id, Name: name, Zone: zone}, ""); err != nil {
		return a.publicNameState(), fmt.Errorf("record the claim: %w", err)
	}
	if err := a.wirePublicName(cfg); err != nil {
		return a.publicNameState(), err
	}
	return a.publicNameState(), nil
}

// .
func (a *App) retryPublicCertificate() (dashboard.PublicNameState, error) {
	a.pn.mu.Lock()
	mgr := a.pn.manager
	a.pn.mu.Unlock()
	if mgr == nil {
		return a.publicNameState(), errors.New("no public name is claimed")
	}
	go a.issuePublicCertificate(mgr)
	a.pn.mu.Lock()
	a.pn.status = publicNameClaiming
	a.pn.mu.Unlock()
	return a.publicNameState(), nil
}

// .
func (a *App) publicNameState() dashboard.PublicNameState {
	cfg := a.configSnapshot()
	st := dashboard.PublicNameState{Status: publicNameNone, TLS: cfg.Dashboard.TLS, CanClaim: cfg.Dashboard.TLS}
	a.pn.mu.Lock()
	defer a.pn.mu.Unlock()
	if a.pn.name == nil {
		return st
	}
	st.CanClaim = false
	st.Name = a.pn.name.Name
	if a.pn.relay != nil {
		rs := a.pn.relay.State()
		st.Relay, st.RelayConnected = rs.Relay, rs.Connected
		if !rs.Connected && rs.LastError != "" {
			st.RelayError = rs.LastError
		}
	}
	st.Zone = a.pn.name.Zone
	st.ServiceZone = a.pn.serviceZone
	st.CanMove = cfg.Dashboard.TLS && a.pn.serviceZone != "" && a.pn.serviceZone != a.pn.name.Zone
	st.Origin = a.pn.origin
	st.Address = a.pn.address
	st.ResolvesTo = a.pn.resolvesTo
	st.Status = a.pn.status
	st.LastError = a.pn.lastError
	if a.pn.manager != nil {
		s := a.pn.manager.State()
		if !s.NotAfter.IsZero() {
			st.NotAfter = s.NotAfter.UTC().Format(time.RFC3339)
		}
		if !s.RenewAt.IsZero() {
			st.RenewAt = s.RenewAt.UTC().Format(time.RFC3339)
		}
		st.DaysLeft = a.pn.manager.DaysLeft()
	}
	return st
}

// .
// .
func (a *App) renewPublicCertificate() {
	a.pn.mu.Lock()
	mgr, name := a.pn.manager, ""
	if a.pn.name != nil {
		name = a.pn.name.Name
	}
	a.pn.mu.Unlock()
	if mgr == nil {
		// .
		// .
		a.autoClaimPublicName(a.configSnapshot())
		return
	}
	ctx := a.bgCtx
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	a.pn.mu.Lock()
	pub := a.pn.publisher
	a.pn.mu.Unlock()
	if pub != nil {
		a.learnServiceZone(pub)
	}
	renewed, err := mgr.Renew(ctx)
	days := mgr.DaysLeft()
	a.pn.mu.Lock()
	switch {
	case err != nil:
		a.pn.status = publicNameFailing
		a.pn.lastError = err.Error()
	default:
		a.pn.status = publicNameIssued
		a.pn.lastError = ""
	}
	a.pn.mu.Unlock()
	switch {
	case err != nil && days <= 0:
		a.maintenanceAlert("certificate", fmt.Sprintf("the certificate for %s HAS EXPIRED and renewal fails: %v — the name serves no trusted certificate until this is fixed", name, err))
	case err != nil:
		a.maintenanceAlert("certificate", fmt.Sprintf("renewal for %s failed with %.0f days left: %v", name, days, err))
	case renewed:
		log.Printf("public name %s: certificate renewed, %.0f days", name, days)
		if a.dashboard != nil {
			a.dashboard.BroadcastStatus()
		}
	case days <= 7:
		a.maintenanceAlert("certificate", fmt.Sprintf("the certificate for %s has %.0f days left and is not yet due by the CA's window", name, days))
	}
}

// .
// .
type certificateOwner struct{ a *App }

func (o certificateOwner) Name() string { return certificateOwnerName }

func (o certificateOwner) OnAlarm(_ context.Context, _ string, _ string, _ int64, _ string) cognitive.AlarmResult {
	o.a.renewPublicCertificate()
	next := cognitive.NextLocalDaily(time.Now(), certificateHourLocal, 0)
	return cognitive.AlarmResult{Accepted: true, NextDeadline: &next}
}

func armCertificateAlarm(t *cognitive.TIME, now time.Time) error {
	next := cognitive.NextLocalDaily(now, certificateHourLocal, 0)
	return t.SetAlarm(certificateAlarmID, certificateOwnerName, "wall", next, nil, "")
}

// .
// .
// .
// .
// .
// .
func autoClaimApplies(cfg Config) bool {
	return cfg.Dashboard.TLS && !dashboard.IsLoopback(cfg.Dashboard.Host) && cfg.Certificate.serverURL() != ""
}

// .
// .
// .
// .
// .
func (a *App) autoClaimPublicName(cfg Config) {
	if !autoClaimApplies(cfg) || a.store == nil {
		return
	}
	if _, ok, err := a.store.PublicName(); err != nil || ok {
		return
	}
	if _, err := a.claimPublicName(); err != nil {
		a.pn.mu.Lock()
		a.pn.lastError = err.Error()
		a.pn.mu.Unlock()
		log.Printf("public name: not claimed (%v) — the local certificate serves meanwhile; the claim is retried at the next boot and at the daily certificate pass", err)
		if a.timeFac != nil {
			if aerr := armCertificateAlarm(a.timeFac, time.Now()); aerr != nil {
				log.Printf("public name: retry alarm could not be armed: %v", aerr)
			}
		}
	}
}

// .
// .
// .
// .
// .
// .
func dashboardAdvice(tlsOn bool, host string, st dashboard.PublicNameState, mat *dashboard.TLSMaterial, serviceDeclined bool) []string {
	if !tlsOn {
		return nil
	}
	switch st.Status {
	case publicNameIssued:
		line := "Public name: " + st.Origin + " — trusted certificate, nothing to install"
		switch {
		case st.ResolvesTo != "":
			line += "; resolves to " + st.ResolvesTo
		case st.Address != "":
			line += "; address " + st.Address + " published, not resolving from here yet (DNS TTL 60 s)"
		default:
			line += "; address not published yet (see the log)"
		}
		return []string{line}
	case publicNameClaiming, publicNameFailing:
		return []string{"Public name: " + st.Origin + " — certificate being issued; the URL is trusted once it is (Settings → Dashboard → Public name)"}
	}
	var lines []string
	switch {
	case serviceDeclined:
		lines = append(lines, "Public name: declined by configuration (certificate.server_url = none) — the dashboard serves a local certificate")
	case dashboard.IsLoopback(host):
		lines = append(lines, "TLS on loopback serves a local certificate; plain HTTP on loopback is already a secure context and needs nothing installed (dashboard.tls false)")
	default:
		lines = append(lines, "Public name: not claimed yet (see the log) — the dashboard serves a local certificate until it is")
	}
	if mat != nil && mat.Regenerated {
		lines = append(lines,
			"Install this root ONCE for a browser with no warnings: "+mat.CACertPath,
			"  Linux (Chrome/Firefox keep their own store; needs libnss3-tools): certutil -d sql:$HOME/.pki/nssdb -A -t \"C,,\" -n \"AII OS "+host+"\" -i "+mat.CACertPath,
			"  macOS: sudo security add-trusted-cert -d -k /Library/Keychains/System.keychain "+mat.CACertPath,
			"  Windows: certutil -addstore -f Root "+mat.CACertPath)
	}
	return lines
}
