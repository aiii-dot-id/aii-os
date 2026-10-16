package publicnametest

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"fmt"
	"math/big"
	"net"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/certs"
	"github.com/aiii-dot-id/aii-os/internal/relay/relaytest"
)

type Publisher struct {
	Mu        sync.Mutex
	Claims    []string
	Zone      string
	Flat      bool
	Relays    []string
	Alias     string
	Records   map[string]certs.RecordSet
	Replaced  [][]certs.Record
	Fail      error
	ReadFail  error
	Published map[string][]string

	RelayLifetime time.Duration
	ReadLeases    []time.Duration
	WriteLeases   []time.Duration

	Statuses int
}

func (p *Publisher) Asked() (statuses, reads, writes int) {
	p.Mu.Lock()
	defer p.Mu.Unlock()
	return p.Statuses, len(p.ReadLeases), len(p.Replaced)
}

func (p *Publisher) Claim(_ context.Context, id string) (certs.ClaimResult, error) {
	p.Mu.Lock()
	defer p.Mu.Unlock()
	if p.Fail != nil {
		return certs.ClaimResult{}, p.Fail
	}
	p.Claims = append(p.Claims, id)
	if p.Flat {
		return certs.ClaimResult{Name: id + "." + p.Zone, Alias: p.Alias, Zone: p.Zone}, nil
	}
	return certs.ClaimResult{Name: "ui." + id + "." + p.Zone, Alias: p.Alias, Zone: p.Zone}, nil
}

func (p *Publisher) ReadRecords(_ context.Context, name string, lease time.Duration) (certs.RecordSet, error) {
	p.Mu.Lock()
	defer p.Mu.Unlock()
	p.ReadLeases = append(p.ReadLeases, lease)
	if p.ReadFail != nil {
		return certs.RecordSet{}, p.ReadFail
	}
	if p.Fail != nil {
		return certs.RecordSet{}, p.Fail
	}
	if set, ok := p.Records[name]; ok {

		set.Delivered = false
		return set, nil
	}
	return certs.RecordSet{HTTPStatus: 200}, nil
}

func (p *Publisher) ReplaceRecords(_ context.Context, name string, records []certs.Record, expected int64, lease time.Duration) (certs.RecordSet, error) {
	p.Mu.Lock()
	defer p.Mu.Unlock()
	if p.Fail != nil {
		return certs.RecordSet{}, p.Fail
	}
	current := p.Records[name]
	if current.Generation != expected {
		return certs.RecordSet{}, fmt.Errorf("%w: the name is at generation %d", certs.ErrRecordSetConflict, current.Generation)
	}
	p.Replaced = append(p.Replaced, records)
	p.WriteLeases = append(p.WriteLeases, lease)

	if lease <= 0 {
		lease = 14 * time.Minute
	}
	leased := make([]certs.LeasedRecord, 0, len(records))
	for _, r := range records {
		leased = append(leased, certs.LeasedRecord{Type: r.Type, Value: r.Value,
			ExpiresAt: time.Now().Add(lease).UTC().Truncate(time.Second).Format(time.RFC3339)})
	}
	set := certs.RecordSet{Generation: current.Generation + 1, Managed: true, Alias: p.Alias,
		Records: leased, Delivered: true, HTTPStatus: 202}
	if p.Records == nil {
		p.Records = map[string]certs.RecordSet{}
	}
	p.Records[name] = set
	return set, nil
}

func (p *Publisher) LastRoute() (writes int, last []certs.Record) {
	p.Mu.Lock()
	defer p.Mu.Unlock()
	if writes = len(p.Replaced); writes > 0 {
		last = p.Replaced[writes-1]
	}
	return writes, last
}

func relayEndpoints(relays []string) []certs.RelayEndpoint {
	out := make([]certs.RelayEndpoint, 0, len(relays))
	for _, r := range relays {
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

func (p *Publisher) ServiceStatus(context.Context) (certs.ServiceStatus, error) {
	p.Mu.Lock()
	defer p.Mu.Unlock()
	p.Statuses++
	if p.Fail != nil {
		return certs.ServiceStatus{}, p.Fail
	}

	st := *Service(p.Zone, p.Relays...)
	if p.RelayLifetime > 0 {

		st.MaxPublishLifetime, st.MaxRelayPublishLifetime = 15*time.Minute, p.RelayLifetime
	}
	return st, nil
}

func (p *Publisher) SetZone(zone string, flat bool) {
	p.Mu.Lock()
	defer p.Mu.Unlock()
	p.Zone, p.Flat = zone, flat
}

func (p *Publisher) Publish(_ context.Context, name, value string) error {
	p.Mu.Lock()
	defer p.Mu.Unlock()
	if p.Published == nil {
		p.Published = map[string][]string{}
	}
	p.Published[name] = append(p.Published[name], value)
	return nil
}

func (p *Publisher) Unpublish(context.Context, string, string) error { return nil }

func Service(zone string, relays ...string) *certs.ServiceStatus {
	return &certs.ServiceStatus{
		Zone:           zone,
		Capabilities:   []string{certs.CapabilityRecordSets, certs.CapabilityRelayV2},
		RecordSetTypes: []string{certs.RecordA, certs.RecordAAAA, certs.RecordCNAME},
		RelayEndpoints: relayEndpoints(relays),
	}
}

func ServiceWithoutRelays(zone string, relays ...string) *certs.ServiceStatus {
	st := Service(zone, relays...)
	st.Capabilities = []string{certs.CapabilityRecordSets}
	return st
}

type BlackholedStatus struct{ *Publisher }

func (BlackholedStatus) ServiceStatus(ctx context.Context) (certs.ServiceStatus, error) {
	<-ctx.Done()
	return certs.ServiceStatus{}, ctx.Err()
}

type UnansweredClaim struct{ *Publisher }

func (UnansweredClaim) Claim(ctx context.Context, _ string) (certs.ClaimResult, error) {
	<-ctx.Done()
	return certs.ClaimResult{}, ctx.Err()
}

type BlockingStatus struct {
	*Publisher
	Entered chan struct{}
}

func (p *BlockingStatus) ServiceStatus(ctx context.Context) (certs.ServiceStatus, error) {
	close(p.Entered)
	<-ctx.Done()
	return certs.ServiceStatus{}, ctx.Err()
}

type Manager struct {
	Mu      sync.Mutex
	Cfg     certs.Config
	Cert    *tls.Certificate
	Ensures int
	Renews  int
	Fail    error
}

func (m *Manager) Certificate() *tls.Certificate {
	m.Mu.Lock()
	defer m.Mu.Unlock()
	return m.Cert
}
func (m *Manager) State() certs.State {
	return certs.State{Name: m.Cfg.Name, NotAfter: time.Now().Add(60 * 24 * time.Hour)}
}
func (m *Manager) DaysLeft() float64 { return 60 }
func (m *Manager) Ensure(context.Context) (bool, error) {
	m.Mu.Lock()
	defer m.Mu.Unlock()
	m.Ensures++
	if m.Fail != nil {
		return false, m.Fail
	}
	if m.Cert == nil {
		m.Cert = selfSignedFor(m.Cfg.Name)
		return true, nil
	}
	return false, nil
}
func (m *Manager) Renew(context.Context) (bool, error) {
	m.Mu.Lock()
	defer m.Mu.Unlock()
	m.Renews++
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

func Managers[M any](into *[]*Manager) func(certs.Config) (M, error) {
	return func(c certs.Config) (M, error) {
		m := &Manager{Cfg: c}
		if into != nil {
			*into = append(*into, m)
		}
		return any(m).(M), nil
	}
}

type HeldIssueManager struct {
	Base      *Manager
	Started   chan struct{}
	Cancelled chan struct{}
	Release   chan struct{}
}

func NewHeldIssueManager() *HeldIssueManager {
	return &HeldIssueManager{
		Base:      &Manager{},
		Started:   make(chan struct{}),
		Cancelled: make(chan struct{}),
		Release:   make(chan struct{}),
	}
}

func (m *HeldIssueManager) Certificate() *tls.Certificate { return m.Base.Certificate() }
func (m *HeldIssueManager) State() certs.State            { return m.Base.State() }
func (m *HeldIssueManager) DaysLeft() float64             { return m.Base.DaysLeft() }
func (m *HeldIssueManager) Renew(ctx context.Context) (bool, error) {
	return m.Base.Renew(ctx)
}
func (m *HeldIssueManager) Ensure(ctx context.Context) (bool, error) {
	close(m.Started)
	<-ctx.Done()
	close(m.Cancelled)
	<-m.Release
	return false, ctx.Err()
}

func Held[M any](h *HeldIssueManager) func(certs.Config) (M, error) {
	return func(c certs.Config) (M, error) {
		h.Base.Cfg = c
		return any(h).(M), nil
	}
}

func NewRelay(t *testing.T) *relaytest.Relay { return relaytest.New(t) }
