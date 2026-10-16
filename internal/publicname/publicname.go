package publicname

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/certs"
	"github.com/aiii-dot-id/aii-os/internal/cognitive"
	"github.com/aiii-dot-id/aii-os/internal/crypto"
	"github.com/aiii-dot-id/aii-os/internal/dashboard"
	"github.com/aiii-dot-id/aii-os/internal/ledger"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
	"github.com/aiii-dot-id/aii-os/internal/relay"
	"github.com/aiii-dot-id/aii-os/internal/store"
	"github.com/aiii-dot-id/aii-os/internal/witness"
)

type CertificateManager interface {
	Certificate() *tls.Certificate
	State() certs.State
	DaysLeft() float64
	Ensure(ctx context.Context) (bool, error)
	Renew(ctx context.Context) (bool, error)
}

type NamePublisher interface {
	certs.Publisher
	Claim(ctx context.Context, nameID string) (certs.ClaimResult, error)
	ServiceStatus(ctx context.Context) (certs.ServiceStatus, error)

	ReadRecords(ctx context.Context, name string, lease time.Duration) (certs.RecordSet, error)
	ReplaceRecords(ctx context.Context, name string, records []certs.Record, expectedGeneration int64, lease time.Duration) (certs.RecordSet, error)
}

const (
	StatusNone = "none"

	StatusPrepared = "prepared"
	StatusClaiming = "claiming"
	StatusIssued   = "issued"
	StatusFailing  = "failing"

	certificateOwnerName = "certificate"
	certificateAlarmID   = "certificate.renewal"
	certificateHourLocal = 5
)

type Settings struct {
	TLS    bool
	Host   string
	Origin string

	Certificate CertificateConfig

	IdentityDir string
}

type AppendFunc func(eventType ledger.EventType, ring int, payload any, modelID string) (*ledger.Event, error)

type Wires struct {
	Lifetime func() context.Context

	Ctx func() context.Context

	Go func(func()) bool

	Door func() AppendFunc

	Store func() *store.Store

	Dashboard func() *dashboard.Server

	TIME func() *cognitive.TIME

	Key         func() *crypto.KeyPair
	IdentityDir func() string

	Settings func() Settings

	Alert func(kind, detail string)

	Stopping error
}

type Seams struct {
	NewPublisher   func(Settings) (NamePublisher, error)
	NewCertManager func(certs.Config) (CertificateManager, error)

	RelayTLS *tls.Config

	RetryIssueStarted func()
}

type Runtime struct {
	Seams
	w          Wires
	mu         sync.Mutex
	name       *store.PublicName
	origin     string
	bound      string
	status     string
	lastError  string
	manager    CertificateManager
	publisher  NamePublisher
	address    string
	resolvesTo string

	serviceZone string

	alias string

	service   *certs.ServiceStatus
	serviceAt time.Time
	lookMu    sync.Mutex

	claimMu sync.Mutex

	route routeState

	routeTold  string
	routeWake  chan struct{}
	routeOwner bool

	routeNotBefore time.Time

	routeRev uint64

	routeSettled      bool
	routeSettledRev   uint64
	routeHold         time.Duration
	routeSettledEmpty bool
	random            func() float64

	routePassCancel context.CancelFunc

	relay                *relay.Client
	relayCancel          context.CancelFunc
	relayDone            <-chan struct{}
	beforeRelayAdmission func()
}

func New(w Wires) *Runtime {
	return &Runtime{w: w}
}

func (r *Runtime) publisherFor(cfg Settings) (NamePublisher, error) {
	if cfg.Certificate.serverURL() == "" {
		return nil, errors.New("certificate service declined by configuration (certificate.server_url = none)")
	}
	if r.NewPublisher != nil {
		return r.NewPublisher(cfg)
	}
	kp, db := r.w.Key(), r.w.Store()
	if kp == nil || db == nil {
		return nil, errors.New("not live")
	}
	key := witness.AsIdentityKey(kp)
	canonical, env, err := witness.EnsureIdentityEnvelope(key, db, r.w.IdentityDir())
	if err != nil {
		return nil, fmt.Errorf("identity envelope: %w", err)
	}
	return certs.NewPublisherClient(cfg.Certificate.serverURL(), cfg.Certificate.TLSSPKISHA256, key, canonical, env)
}

func (r *Runtime) managerFor(cfg Settings, name string, pub certs.Publisher) (CertificateManager, error) {
	c := certs.Config{
		Dir:          filepath.Join(cfg.IdentityDir, "tls", "public"),
		Name:         name,
		DirectoryURL: cfg.Certificate.acmeDirectory(),
		Contact:      cfg.Certificate.Contact,
		Publisher:    pub,
	}
	if r.NewCertManager != nil {
		return r.NewCertManager(c)
	}
	return certs.New(c)
}

func publicOrigin(name, port string) string {
	if port == "" || port == "443" {
		return "https://" + name
	}
	return "https://" + name + ":" + port
}

func (r *Runtime) Wire(cfg Settings) error {
	db, dash := r.w.Store(), r.w.Dashboard()
	if db == nil || dash == nil {
		return nil
	}
	pn, ok, err := db.PublicName()
	if err != nil {
		return fmt.Errorf("public name: %w", err)
	}
	if !ok {
		return nil
	}
	pub, err := r.publisherFor(cfg)
	if err != nil {
		return fmt.Errorf("public name %s: certificate service: %w", pn.Name, err)
	}
	mgr, err := r.managerFor(cfg, pn.Name, pub)
	if err != nil {
		return fmt.Errorf("public name %s: certificate manager: %w", pn.Name, err)
	}
	port := dash.BoundPort()
	dash.SetPublicCertificate(pn.Name, mgr.Certificate)

	order := mgr.Certificate() == nil && certificateWanted(cfg)
	r.mu.Lock()
	name := pn
	r.name = &name
	r.bound = port
	r.manager = mgr
	r.publisher = pub
	switch {
	case mgr.Certificate() != nil:
		r.status = StatusIssued
	case order:
		r.status = StatusClaiming
	default:
		r.status = StatusPrepared
	}
	r.mu.Unlock()

	origin := r.applyNameURLFor(cfg)
	logsink.Info("route.start", "%s — origin %s", pn.Name, origin)

	if !r.startRouteOwner() {
		r.Signal()
	}
	if order {
		r.w.Go(func() { r.issue(mgr) })
	}
	if tf := r.w.TIME(); tf != nil {
		if err := armCertificateAlarm(tf, time.Now()); err != nil {
			logsink.Warn("route.error", "renewal alarm could not be armed: %v", err)
		}
	}
	return nil
}

func (r *Runtime) sessionServers() []dashboard.ICEServer {
	r.mu.Lock()
	c := r.relay
	r.mu.Unlock()
	if c == nil {
		return nil
	}
	named, _ := c.SessionServers()
	out := make([]dashboard.ICEServer, 0, len(named))
	for _, sv := range named {
		out = append(out, dashboard.ICEServer{URLs: sv.URLs, Username: sv.Username, Credential: sv.Credential})
	}
	return out
}

func (r *Runtime) Returned() {
	r.mu.Lock()
	c := r.relay
	r.mu.Unlock()
	if c != nil {
		c.Wake()
	}
}

func certificateWanted(cfg Settings) bool {
	if cfg.Certificate.PrepareCertificate || cfg.TLS {
		return true
	}
	switch mode, err := RouteMode(cfg.Certificate); {
	case err != nil:
		return false
	case mode == RouteRelay:
		return true
	case mode == RouteDirect:
		records, err := directRoute(cfg)
		return err == nil && len(records) > 0
	}
	return false
}

func (r *Runtime) orderCertificateIfWanted() {
	wanted := certificateWanted(r.w.Settings())
	r.mu.Lock()
	mgr := r.manager
	start := wanted && mgr != nil && r.status == StatusPrepared
	if start {
		r.status = StatusClaiming
		r.lastError = ""
	}
	r.mu.Unlock()
	if start && !r.w.Go(func() { r.issue(mgr) }) {
		r.mu.Lock()
		r.status = StatusPrepared
		r.mu.Unlock()
	}
}

func (r *Runtime) startRelay(cfg Settings, name string, endpoint certs.RelayEndpoint) {
	addr := net.JoinHostPort(endpoint.Name, strconv.Itoa(endpoint.Port))
	r.mu.Lock()

	if cur := r.relay; cur != nil && cur.Name == name && cur.Relay == addr {
		r.mu.Unlock()
		return
	}
	revision := r.routeRev
	r.mu.Unlock()
	if done := r.stopRelay(); done != nil {
		<-done
	}
	kp, db := r.w.Key(), r.w.Store()
	if endpoint.Name == "" || endpoint.Port <= 0 || kp == nil || db == nil {
		return
	}

	dash := r.w.Dashboard()
	if dash == nil || !dash.AccessTokenRequired() {
		logsink.Warn("route.refusal", "REFUSED for %s — the running dashboard is not requiring an access token, and a relay would carry the public internet to it. Set dashboard.require_token true and RESTART (a saved setting does not take effect until then); read the token with `aii dashboard-token`.", name)
		return
	}
	key := witness.AsIdentityKey(kp)
	canonical, env, err := witness.EnsureIdentityEnvelope(key, db, r.w.IdentityDir())
	if err != nil {
		logsink.Warn("route.error", "identity envelope: %v", err)
		return
	}

	id, err := witness.DeriveIdentityID(canonical, env)
	if err != nil {
		logsink.Warn("route.error", "identity id: %v", err)
		return
	}

	if _, held := cfg.Certificate.RelayTuning(); len(held) > 0 {
		logsink.Warn("route.refusal", "the relay connection's settings: %s", strings.Join(held, "; "))
	}
	tune := func() relay.Tuning { t, _ := r.w.Settings().Certificate.RelayTuning(); return t }
	c := &relay.Client{Relay: addr, Name: name, IdentityID: id, Key: key, Env: env, Envelope: canonical, Deliver: dash.Carry, TLS: r.RelayTLS, Tune: tune}
	parent := r.w.Ctx()
	if parent == nil {
		logsink.Warn("route.refusal", "relay not started: application lifecycle is required")
		return
	}
	ctx, cancel := context.WithCancel(parent)
	if r.beforeRelayAdmission != nil {
		r.beforeRelayAdmission()
	}
	r.mu.Lock()

	if !dash.AccessTokenRequired() || r.relay != nil || r.routeRev != revision {
		r.mu.Unlock()
		cancel()
		logsink.Warn("route.refusal", "relay not started: authenticated dashboard, unchanged route intent and an unoccupied relay owner are required")
		return
	}
	done := make(chan struct{})
	if !r.w.Go(func() {
		defer close(done)
		c.Run(ctx)
		r.mu.Lock()
		if r.relay == c {
			r.relay, r.relayCancel, r.relayDone = nil, nil, nil
		}
		r.mu.Unlock()
	}) {
		r.mu.Unlock()
		cancel()
		return
	}
	r.relay, r.relayCancel, r.relayDone = c, cancel, done
	r.mu.Unlock()
}

func (r *Runtime) LearnServiceZone() {
	r.mu.Lock()
	pub, prepared := r.publisher, r.status == StatusPrepared
	r.mu.Unlock()
	if pub == nil || prepared {
		return
	}
	_ = r.learnService(pub)
}

func (r *Runtime) Look() (dashboard.PublicNameState, error) {
	r.lookMu.Lock()
	defer r.lookMu.Unlock()
	r.mu.Lock()
	pub := r.publisher
	fresh := r.service != nil && time.Since(r.serviceAt) < routeIdle
	r.mu.Unlock()
	if pub == nil {
		return r.State(), errors.New("no public name is claimed")
	}
	if !fresh {
		if err := r.learnService(pub); err != nil {
			return r.State(), fmt.Errorf("the certificate service did not answer: %w", err)
		}
	}
	return r.State(), nil
}

func (r *Runtime) learnService(pub NamePublisher) error {
	ctx, cancel := context.WithTimeout(r.w.Lifetime(), 10*time.Second)
	defer cancel()
	st, err := pub.ServiceStatus(ctx)
	if err != nil {
		return err
	}
	zone := st.Zone
	r.mu.Lock()
	r.serviceZone = zone
	r.service, r.serviceAt = &st, time.Now()
	moved := r.name != nil && r.name.Zone != zone
	name := ""
	if r.name != nil {
		name = r.name.Name
	}
	r.mu.Unlock()
	if moved {
		logsink.Warn("route.refusal", "the certificate service now serves %s and %s is under another zone — move the name under Settings → Dashboard → Public name", zone, name)
	}
	return nil
}

func (r *Runtime) Move() (dashboard.PublicNameState, error) {
	cfg := r.w.Settings()
	db, door := r.w.Store(), r.w.Door()
	if db == nil || door == nil || r.w.Dashboard() == nil {
		return r.State(), errors.New("the identity is not live")
	}
	current, ok, err := db.PublicName()
	if err != nil {
		return r.State(), err
	}
	if !ok {
		return r.State(), errors.New("no public name is claimed — claim one instead of moving")
	}
	pub, err := r.publisherFor(cfg)
	if err != nil {
		return r.State(), err
	}
	ctx, cancel := context.WithTimeout(r.w.Lifetime(), 60*time.Second)
	defer cancel()
	st, err := pub.ServiceStatus(ctx)
	if err != nil {
		return r.State(), fmt.Errorf("the certificate service's zone: %w", err)
	}
	zone := st.Zone
	if zone == current.Zone {
		return r.State(), fmt.Errorf("the certificate service still serves %s — there is nothing to move", zone)
	}
	id, err := certs.NewNameID()
	if err != nil {
		return r.State(), err
	}
	claimed, err := pub.Claim(ctx, id)
	name, claimedZone := claimed.Name, claimed.Zone
	if err != nil {
		return r.State(), fmt.Errorf("claim under %s: %w", zone, err)
	}
	if _, err := door(ledger.EventNetworkNameClaimed, 0, store.PublicNamePayload{NameID: id, Name: name, Zone: claimedZone}, ""); err != nil {
		return r.State(), fmt.Errorf("record the move: %w", err)
	}
	logsink.Info("route.decision", "moved from %s to %s — the previous name is retired", current.Name, name)
	r.mu.Lock()
	r.serviceZone, r.service, r.serviceAt = zone, &st, time.Now()
	r.mu.Unlock()
	if err := r.Wire(cfg); err != nil {
		return r.State(), err
	}
	return r.State(), nil
}

func (r *Runtime) issue(mgr CertificateManager) {
	ctx, cancel := context.WithTimeout(r.w.Lifetime(), 10*time.Minute)
	defer cancel()
	r.mu.Lock()
	r.status = StatusClaiming
	r.lastError = ""
	r.mu.Unlock()
	_, err := mgr.Ensure(ctx)
	r.mu.Lock()
	if err != nil {
		r.status = StatusFailing
		r.lastError = err.Error()
	} else {
		r.status = StatusIssued
		r.lastError = ""
	}
	name := ""
	if r.name != nil {
		name = r.name.Name
	}
	r.mu.Unlock()
	if err != nil {
		logsink.Warn("route.error", "%s: certificate not obtained: %v", name, err)
		r.w.Alert("certificate", fmt.Sprintf("the certificate for %s was not obtained: %v — Settings → Dashboard → Public name → Retry", name, err))
		return
	}
	logsink.Info("route.start", "%s: certificate obtained, served now", name)
	if dash := r.w.Dashboard(); dash != nil {
		dash.BroadcastStatus()
	}
}

func (r *Runtime) Claim() (dashboard.PublicNameState, error) {
	st, err := r.claim(r.w.Settings())
	if err != nil {
		return st, err
	}

	r.mu.Lock()
	prepared := r.status == StatusPrepared
	r.mu.Unlock()
	if prepared {
		return r.Retry()
	}
	return st, nil
}

func (r *Runtime) claim(cfg Settings) (dashboard.PublicNameState, error) {
	r.claimMu.Lock()
	defer r.claimMu.Unlock()
	db, door := r.w.Store(), r.w.Door()
	if db == nil || door == nil || r.w.Dashboard() == nil {
		return r.State(), errors.New("the identity is not live")
	}
	if _, ok, err := db.PublicName(); err != nil {
		return r.State(), err
	} else if ok {
		return r.State(), nil
	}
	pub, err := r.publisherFor(cfg)
	if err != nil {
		return r.State(), err
	}
	id, err := certs.NewNameID()
	if err != nil {
		return r.State(), err
	}
	ctx, cancel := context.WithTimeout(r.w.Lifetime(), 60*time.Second)
	defer cancel()
	claimed, err := pub.Claim(ctx, id)
	name, zone := claimed.Name, claimed.Zone
	if err != nil {
		return r.State(), fmt.Errorf("claim: %w", err)
	}

	if _, err := door(ledger.EventNetworkNameClaimed, 0, store.PublicNamePayload{NameID: id, Name: name, Zone: zone}, ""); err != nil {
		return r.State(), fmt.Errorf("record the claim: %w", err)
	}
	r.mu.Lock()
	r.serviceZone = zone
	r.mu.Unlock()
	if err := r.Wire(cfg); err != nil {
		return r.State(), err
	}
	return r.State(), nil
}

func (r *Runtime) Retry() (dashboard.PublicNameState, error) {
	r.mu.Lock()
	mgr := r.manager
	if mgr != nil {
		r.status = StatusClaiming
		r.lastError = ""
	}
	r.mu.Unlock()
	if mgr == nil {
		return r.State(), errors.New("no public name is claimed")
	}
	if !r.w.Go(func() { r.issue(mgr) }) {
		return r.State(), fmt.Errorf("public-name issuance is unavailable: %w", r.w.Stopping)
	}
	if r.RetryIssueStarted != nil {
		r.RetryIssueStarted()
	}
	return r.State(), nil
}

func (r *Runtime) State() dashboard.PublicNameState {
	cfg := r.w.Settings()

	st := dashboard.PublicNameState{Status: StatusNone, TLS: cfg.TLS, CanClaim: true}

	if cfg.Certificate.serverURL() == "" {
		st.CanClaim, st.ServiceDeclined = false, true
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.name == nil {
		return st
	}
	st.CanClaim = false
	st.Name = r.name.Name
	if r.relay != nil {
		rs := r.relay.State()
		st.Relay, st.RelayConnected = rs.Relay, rs.Connected
		if !rs.Connected && rs.LastError != "" {
			st.RelayError = rs.LastError
		}
	}

	st.RouteMode, st.RelayEndpoint = cfg.Certificate.RouteMode, cfg.Certificate.RelayEndpoint

	if set := cfg.Certificate.DirectAddresses; set != nil {
		list := append([]string{}, *set...)
		st.DirectAddresses = &list
	}
	st.MachineAddresses = machineAddresses()
	if mode, err := RouteMode(cfg.Certificate); err == nil {
		st.RouteMode = mode
	}
	if r.service != nil {
		relays := []dashboard.RelayChoice{}
		if serviceOffersRelays(*r.service) {

			held, _ := relayChoice(cfg.Certificate)
			pick, _ := suggestedRelay(r.name.Name, held, r.service.RelayEndpoints)
			for _, e := range r.service.RelayEndpoints {
				relays = append(relays, dashboard.RelayChoice{Name: e.Name, Port: e.Port, Origin: publicOrigin(r.name.Name, strconv.Itoa(e.Port)), Suggested: e.Name == pick.Name})
			}
		}
		st.Relays = &relays
	}
	st.Zone = r.name.Zone
	st.ServiceZone = r.serviceZone
	st.CanMove = cfg.TLS && r.serviceZone != "" && r.serviceZone != r.name.Zone
	st.Origin = r.origin
	st.DirectOrigin = publicOrigin(r.name.Name, r.bound)
	st.Address = r.address
	st.ResolvesTo = r.resolvesTo
	st.Status = r.status
	st.LastError = r.lastError
	if r.manager != nil {
		s := r.manager.State()
		if !s.NotAfter.IsZero() {
			st.NotAfter = s.NotAfter.UTC().Format(time.RFC3339)
		}
		if !s.RenewAt.IsZero() {
			st.RenewAt = s.RenewAt.UTC().Format(time.RFC3339)
		}
		st.DaysLeft = r.manager.DaysLeft()
	}
	return st
}

func (r *Runtime) Renew() {
	r.mu.Lock()
	mgr, name := r.manager, ""
	if r.name != nil {
		name = r.name.Name
	}
	r.mu.Unlock()
	if mgr == nil {

		r.AutoClaim(r.w.Settings())
		return
	}

	r.mu.Lock()
	prepared := r.status == StatusPrepared
	r.mu.Unlock()
	if prepared {
		return
	}
	ctx, cancel := context.WithTimeout(r.w.Lifetime(), 10*time.Minute)
	defer cancel()
	r.LearnServiceZone()
	renewed, err := mgr.Renew(ctx)
	days := mgr.DaysLeft()
	r.mu.Lock()
	switch {
	case err != nil:
		r.status = StatusFailing
		r.lastError = err.Error()
	default:
		r.status = StatusIssued
		r.lastError = ""
	}
	r.mu.Unlock()
	switch {
	case err != nil && days <= 0:
		r.w.Alert("certificate", fmt.Sprintf("the certificate for %s HAS EXPIRED and renewal fails: %v — the name serves no trusted certificate until this is fixed", name, err))
	case err != nil:
		r.w.Alert("certificate", fmt.Sprintf("renewal for %s failed with %.0f days left: %v", name, days, err))
	case renewed:
		logsink.Info("route.renewal", "%s: certificate renewed, %.0f days", name, days)
		if dash := r.w.Dashboard(); dash != nil {
			dash.BroadcastStatus()
		}
	case days <= 7:
		r.w.Alert("certificate", fmt.Sprintf("the certificate for %s has %.0f days left and is not yet due by the CA's window", name, days))
	}
}

type certificateOwner struct{ r *Runtime }

func (r *Runtime) AlarmOwner() cognitive.AlarmOwner { return certificateOwner{r} }

func (o certificateOwner) Name() string { return certificateOwnerName }

func (o certificateOwner) OnAlarm(_ context.Context, _ string, _ string, _ int64, _ string) cognitive.AlarmResult {
	o.r.Renew()
	next := cognitive.NextLocalDaily(time.Now(), certificateHourLocal, 0)
	return cognitive.AlarmResult{Accepted: true, NextDeadline: &next}
}

func armCertificateAlarm(t *cognitive.TIME, now time.Time) error {
	next := cognitive.NextLocalDaily(now, certificateHourLocal, 0)
	return t.SetAlarm(certificateAlarmID, certificateOwnerName, "wall", next, nil, "")
}

func autoClaimApplies(cfg Settings) bool {
	return cfg.TLS && !dashboard.IsLoopback(cfg.Host) && cfg.Certificate.serverURL() != ""
}

func prepareApplies(cfg Settings) bool {
	return cfg.Certificate.serverURL() != "" && cfg.Certificate.prepare()
}

func (r *Runtime) PrepareAtBirth(cfg Settings) {
	db := r.w.Store()
	if !prepareApplies(cfg) || db == nil {
		return
	}
	if _, ok, err := db.PublicName(); err != nil || ok {
		return
	}
	if _, err := r.claim(cfg); err != nil {
		r.mu.Lock()
		r.lastError = err.Error()
		r.mu.Unlock()
		logsink.Warn("route.refusal", "the new identity's name was not claimed (%v) — the identity runs as it is, and the name is claimed when its dashboard is opened on another device (Settings → Dashboard)", err)
	}
}

func (r *Runtime) AutoClaim(cfg Settings) {
	db := r.w.Store()
	if !autoClaimApplies(cfg) || db == nil {
		return
	}
	if _, ok, err := db.PublicName(); err != nil || ok {
		return
	}
	if _, err := r.claim(cfg); err != nil {
		r.mu.Lock()
		r.lastError = err.Error()
		r.mu.Unlock()
		logsink.Warn("route.refusal", "not claimed (%v) — the identity runs as it is meanwhile; the claim is retried at the next boot and at the daily certificate pass", err)
		if tf := r.w.TIME(); tf != nil {
			if aerr := armCertificateAlarm(tf, time.Now()); aerr != nil {
				logsink.Warn("route.error", "retry alarm could not be armed: %v", aerr)
			}
		}
	}
}

func dashboardAdvice(tlsOn bool, host string, st dashboard.PublicNameState, mat *dashboard.TLSMaterial, serviceDeclined bool) []string {

	if !tlsOn && st.RouteMode != RouteRelay {
		return nil
	}
	switch st.Status {
	case StatusIssued:
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
	case StatusClaiming, StatusFailing:
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

func (r *Runtime) Advice(cfg Settings, mat *dashboard.TLSMaterial) []string {
	return dashboardAdvice(cfg.TLS, cfg.Host, r.State(), mat, cfg.Certificate.serverURL() == "")
}

func (r *Runtime) WireHooks(h *dashboard.WSHandler) {
	h.PublicNameClaim = r.Claim
	h.PublicNameRetry = r.Retry
	h.PublicNameMove = r.Move
	h.PublicNameLook = r.Look
	h.PublicNameState = r.State
	h.AccessCodeOrigin = func() string { return r.State().Origin }
	h.SessionServers = r.sessionServers
}

func (r *Runtime) WithoutRelay(fn func()) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.relay != nil {
		return false
	}
	fn()
	return true
}

func (r *Runtime) RelayChoiceRefusal(was, now Settings) error {
	r.mu.Lock()
	var service *certs.ServiceStatus
	if r.service != nil {
		st := *r.service
		service = &st
	}
	bound, name := r.bound, ""
	if r.name != nil {
		name = r.name.Name
	}
	r.mu.Unlock()
	if service == nil {
		return errors.New("certificate.route_mode: the certificate service has not been asked which relays it offers, and a relay is chosen from those — Open on another device asks it, and so does Ask which relays are offered on the route control (the page's public_name_look); then choose again")
	}
	if _, err := r.desiredRoute(now, RouteRelay, *service); err != nil {
		return fmt.Errorf("certificate.route_mode: %w", err)
	}

	host, named := relayChoice(now.Certificate)
	if from, to := namePort(was, bound), namePort(now, bound); named == "" && from != to {
		port := 0
		if endpoint, ok := service.Relay(host); ok {
			port = endpoint.Port
		}
		return fmt.Errorf("certificate.route_mode: %w", &RelayPortUnnamed{Relay: host, Port: port, From: publicOrigin(name, from), To: publicOrigin(name, to), FromPort: from})
	}
	return nil
}
