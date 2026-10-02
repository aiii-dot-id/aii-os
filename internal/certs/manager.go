package certs

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base32"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
	"io"
	"math/big"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/acme"
)

type Publisher interface {
	Publish(ctx context.Context, name, txt string) error
	Unpublish(ctx context.Context, name, txt string) error
}

type Config struct {
	Dir          string
	Name         string
	DirectoryURL string
	Contact      string
	Publisher    Publisher
	HTTPClient   *http.Client
	Now          func() time.Time
	Rand         io.Reader
}

type Window struct {
	Start time.Time `json:"start"`
	End   time.Time `json:"end"`
}

type State struct {
	Name         string    `json:"name"`
	DirectoryURL string    `json:"directory_url"`
	AccountURL   string    `json:"account_url,omitempty"`
	IssuedAt     time.Time `json:"issued_at,omitempty"`
	NotAfter     time.Time `json:"not_after,omitempty"`
	Window       *Window   `json:"ari_window,omitempty"`
	RenewAt      time.Time `json:"renew_at,omitempty"`
	LastAttempt  time.Time `json:"last_attempt,omitempty"`
	LastError    string    `json:"last_error,omitempty"`
	Orders       int       `json:"orders"`
}

type Manager struct {
	cfg      Config
	ordering chan struct{}
	mu       sync.Mutex
	state    State
	cert     *tls.Certificate
	leaf     *x509.Certificate
	client   *acme.Client
}

const (
	accountKeyFile = "account.key"
	stateFile      = "state.json"
)

var (
	ErrNoPublisher = errors.New("certs: no publisher configured")

	ErrNoName = errors.New("certs: no public name")
)

func New(cfg Config) (*Manager, error) {
	if cfg.Dir == "" {
		return nil, errors.New("certs: no directory")
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Rand == nil {
		cfg.Rand = rand.Reader
	}
	if err := os.MkdirAll(cfg.Dir, 0o700); err != nil {
		return nil, fmt.Errorf("certs: %w", err)
	}
	m := &Manager{cfg: cfg, ordering: make(chan struct{}, 1)}
	if raw, err := os.ReadFile(filepath.Join(cfg.Dir, stateFile)); err == nil {
		if err := json.Unmarshal(raw, &m.state); err != nil {
			return nil, fmt.Errorf("certs: state.json: %w", err)
		}
	}
	if cfg.Name != "" && m.state.Name != "" && m.state.Name != cfg.Name {

		logsink.Info("certs.decision", "the name changed from %s to %s — the stored certificate is set aside", m.state.Name, cfg.Name)
		for _, p := range []string{m.certPath(), m.keyPath()} {
			if _, err := os.Stat(p); err == nil {
				_ = os.Rename(p, p+".prev")
			}
		}
		m.state = State{}
	}
	if err := m.loadCertificate(); err != nil {
		logsink.Warn("certs.refusal", "stored certificate not used: %v", err)
	}
	return m, nil
}

func (m *Manager) certPath() string { return filepath.Join(m.cfg.Dir, "public.crt") }
func (m *Manager) keyPath() string  { return filepath.Join(m.cfg.Dir, "public.key") }

func (m *Manager) loadCertificate() error {
	certPEM, err := os.ReadFile(m.certPath())
	if err != nil {
		return nil
	}
	keyPEM, err := os.ReadFile(m.keyPath())
	if err != nil {
		return fmt.Errorf("certificate without its key")
	}
	pair, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return fmt.Errorf("certificate and key do not belong together: %w", err)
	}
	leaf, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		return err
	}
	if m.cfg.Name != "" && leaf.VerifyHostname(m.cfg.Name) != nil {
		return fmt.Errorf("certificate names %v, not %s", leaf.DNSNames, m.cfg.Name)
	}
	if !m.cfg.Now().Before(leaf.NotAfter) {
		return fmt.Errorf("certificate expired %s", leaf.NotAfter.Format(time.RFC3339))
	}
	pair.Leaf = leaf
	m.cert = &pair
	m.leaf = leaf
	return nil
}

func (m *Manager) Certificate() *tls.Certificate {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.cert
}

func (m *Manager) State() State {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.state
}

func (m *Manager) takeOrder(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case m.ordering <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (m *Manager) orderDone() { <-m.ordering }

func (m *Manager) Ensure(ctx context.Context) (issued bool, err error) {
	if m.cfg.Name == "" {
		return false, ErrNoName
	}
	if err := m.takeOrder(ctx); err != nil {
		return false, err
	}
	defer m.orderDone()
	if m.Certificate() != nil {
		return false, nil
	}
	if err := m.obtain(ctx); err != nil {
		return false, err
	}
	return true, nil
}

func (m *Manager) Renew(ctx context.Context) (renewed bool, err error) {
	if m.cfg.Name == "" {
		return false, ErrNoName
	}
	if err := m.takeOrder(ctx); err != nil {
		return false, err
	}
	defer m.orderDone()
	m.mu.Lock()
	leaf := m.leaf
	m.mu.Unlock()
	if leaf == nil {
		if err := m.obtain(ctx); err != nil {
			return false, err
		}
		return true, nil
	}

	w, werr := m.renewalWindow(ctx, leaf)
	m.mu.Lock()
	if werr == nil && w != nil && (m.state.Window == nil || *m.state.Window != *w) {
		m.state.Window = w
		m.state.RenewAt = m.pointIn(*w)
		m.keep("the renewal window")
	}
	if m.state.RenewAt.IsZero() {
		m.state.RenewAt = m.pointIn(m.fallbackWindow())
		m.keep("the renewal point")
	}
	due := !m.cfg.Now().Before(m.state.RenewAt)
	m.mu.Unlock()
	if !due {
		return false, nil
	}
	if err := m.obtain(ctx); err != nil {
		return false, err
	}
	return true, nil
}

func (m *Manager) DaysLeft() float64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.leaf == nil {
		return 0
	}
	return m.leaf.NotAfter.Sub(m.cfg.Now()).Hours() / 24
}

func (m *Manager) obtain(ctx context.Context) (retErr error) {
	if m.cfg.Publisher == nil {
		return ErrNoPublisher
	}
	m.mu.Lock()
	m.state.LastAttempt = m.cfg.Now()
	m.mu.Unlock()
	defer func() {
		m.mu.Lock()
		defer m.mu.Unlock()
		if retErr == nil {
			m.state.LastError = ""
			m.keep("the new certificate's state")
			return
		}
		m.state.LastError = retErr.Error()
		if err := m.saveState(); err != nil {
			retErr = errors.Join(retErr, fmt.Errorf("certs: the attempt was not saved either: %w", err))
		}
	}()
	client, err := m.acmeClient(ctx)
	if err != nil {
		return err
	}
	m.mu.Lock()
	m.state.Orders++
	m.mu.Unlock()
	order, err := client.AuthorizeOrder(ctx, acme.DomainIDs(m.cfg.Name))
	if err != nil {
		return fmt.Errorf("order: %w", err)
	}
	for _, u := range order.AuthzURLs {
		z, err := client.GetAuthorization(ctx, u)
		if err != nil {
			return fmt.Errorf("authorization: %w", err)
		}
		if z.Status == acme.StatusValid {
			continue
		}
		var chal *acme.Challenge
		for _, c := range z.Challenges {
			if c.Type == "dns-01" {
				chal = c
				break
			}
		}
		if chal == nil {
			return fmt.Errorf("authorization for %s offers no dns-01 challenge", z.Identifier.Value)
		}
		txt, err := client.DNS01ChallengeRecord(chal.Token)
		if err != nil {
			return err
		}
		fqdn := "_acme-challenge." + z.Identifier.Value
		if err := m.cfg.Publisher.Publish(ctx, fqdn, txt); err != nil {
			return fmt.Errorf("publish challenge: %w", err)
		}
		if _, err := client.Accept(ctx, chal); err != nil {
			_ = m.cfg.Publisher.Unpublish(ctx, fqdn, txt)
			return fmt.Errorf("accept challenge: %w", err)
		}
		_, werr := client.WaitAuthorization(ctx, z.URI)
		_ = m.cfg.Publisher.Unpublish(ctx, fqdn, txt)
		if werr != nil {
			return fmt.Errorf("validation: %w", werr)
		}
	}
	if _, err := client.WaitOrder(ctx, order.URI); err != nil {
		return fmt.Errorf("order: %w", err)
	}

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}
	csr, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{
		Subject:  pkix.Name{CommonName: m.cfg.Name},
		DNSNames: []string{m.cfg.Name},
	}, key)
	if err != nil {
		return err
	}
	der, _, err := client.CreateOrderCert(ctx, order.FinalizeURL, csr, true)
	if err != nil {
		return fmt.Errorf("finalize: %w", err)
	}
	if len(der) == 0 {
		return errors.New("finalize: empty certificate")
	}
	leaf, err := x509.ParseCertificate(der[0])
	if err != nil {
		return err
	}
	if err := m.install(der, key, leaf); err != nil {
		return err
	}

	w, werr := m.renewalWindow(ctx, leaf)
	m.mu.Lock()
	defer m.mu.Unlock()
	if werr == nil && w != nil {
		m.state.Window = w
		m.state.RenewAt = m.pointIn(*w)
	} else {
		m.state.RenewAt = m.pointIn(m.fallbackWindow())
	}
	return nil
}

func (m *Manager) install(der [][]byte, key *ecdsa.PrivateKey, leaf *x509.Certificate) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := writePair(m.certPath(), m.keyPath(), der, key); err != nil {
		return err
	}
	if err := m.loadCertificate(); err != nil {
		return fmt.Errorf("stored certificate does not load: %w", err)
	}
	m.state.Name = m.cfg.Name
	m.state.DirectoryURL = m.cfg.DirectoryURL
	m.state.IssuedAt = leaf.NotBefore
	m.state.NotAfter = leaf.NotAfter
	m.state.Window = nil
	return nil
}

func (m *Manager) acmeClient(ctx context.Context) (*acme.Client, error) {
	m.mu.Lock()
	c, accountURL := m.client, m.state.AccountURL
	m.mu.Unlock()
	if c != nil {
		return c, nil
	}
	key, err := loadOrMintAccountKey(filepath.Join(m.cfg.Dir, accountKeyFile))
	if err != nil {
		return nil, err
	}
	c = &acme.Client{Key: key, DirectoryURL: m.cfg.DirectoryURL, HTTPClient: m.cfg.HTTPClient}
	if accountURL != "" {
		c.KID = acme.KeyID(accountURL)
		m.mu.Lock()
		m.client = c
		m.mu.Unlock()
		return c, nil
	}
	acct := &acme.Account{}
	if m.cfg.Contact != "" {
		acct.Contact = []string{m.cfg.Contact}
	}
	a, err := c.Register(ctx, acct, acme.AcceptTOS)
	if errors.Is(err, acme.ErrAccountAlreadyExists) {
		a, err = c.GetReg(ctx, "")
	}
	if err != nil {
		return nil, fmt.Errorf("account: %w", err)
	}
	c.KID = acme.KeyID(a.URI)
	m.mu.Lock()
	defer m.mu.Unlock()
	m.state.AccountURL = a.URI
	m.state.DirectoryURL = m.cfg.DirectoryURL
	m.keep("the ACME account")
	m.client = c
	return c, nil
}

func (m *Manager) fallbackWindow() Window {
	life := m.leaf.NotAfter.Sub(m.leaf.NotBefore)
	return Window{Start: m.leaf.NotBefore.Add(life * 2 / 3), End: m.leaf.NotAfter.Add(-life / 12)}
}

func (m *Manager) pointIn(w Window) time.Time {
	if !w.End.After(w.Start) {
		return w.Start
	}
	span := w.End.Sub(w.Start)
	n, err := rand.Int(m.cfg.Rand, big.NewInt(int64(span)))
	if err != nil {
		return w.Start
	}
	at := w.Start.Add(time.Duration(n.Int64()))
	if now := m.cfg.Now(); at.Before(now) && now.Before(w.End) {
		return now
	}
	return at
}

func (m *Manager) keep(what string) {
	if err := m.saveState(); err != nil {
		logsink.Warn("certs.error", "%s for %s was not saved — the certificate is unaffected, but a restart would read an older state: %v", what, m.cfg.Name, err)
	}
}

func (m *Manager) saveState() error {
	raw, err := json.MarshalIndent(m.state, "", "  ")
	if err != nil {
		return err
	}
	return writeFile(filepath.Join(m.cfg.Dir, stateFile), raw, 0o600)
}

func NewNameID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(b)), nil
}
