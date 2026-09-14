// .
// .
// .
// .
// .
// .
package relaytest

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/json"
	"math/big"
	"net"
	"sync"
	"testing"
	"time"

	aiicrypto "github.com/aiii-dot-id/aii-os/internal/crypto"
	"github.com/aiii-dot-id/aii-os/internal/relay"
	"github.com/aiii-dot-id/aii-os/internal/witness"
	"github.com/aiii-dot-id/aii-os/internal/witness/witnesstest"
)

// .
func SelfSigned(t *testing.T, name string) tls.Certificate {
	t.Helper()
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()), Subject: pkix.Name{CommonName: name}, DNSNames: []string{name},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, _ := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	leaf, _ := x509.ParseCertificate(der)
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key, Leaf: leaf}
}

// .
// .
// .
// .
type Relay struct {
	t        *testing.T
	name     string
	ln       net.Listener
	cert     tls.Certificate
	mu       sync.Mutex
	known    map[string]knownName
	tunnels  map[string]net.Conn
	pending  map[string]chan net.Conn
	refusals int
	opens    int
	dropAll  chan struct{}
}

// .
func New(t *testing.T) *Relay {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	f := &Relay{t: t, name: "relay.test", ln: ln, cert: SelfSigned(t, "relay.test"), known: map[string]knownName{}, tunnels: map[string]net.Conn{}, pending: map[string]chan net.Conn{}, dropAll: make(chan struct{})}
	go f.serve()
	t.Cleanup(func() { ln.Close() })
	return f
}

// .
// .
func (f *Relay) Addr() string { return f.ln.Addr().String() }
func (f *Relay) Name() string { return f.name }
func (f *Relay) TLSConfig() *tls.Config {
	roots := x509.NewCertPool()
	roots.AddCert(f.cert.Leaf)
	return &tls.Config{ServerName: f.name, RootCAs: roots, MinVersion: tls.VersionTLS12}
}

// .
// .
type knownName struct {
	id  string
	env witness.PublicKeyEnvelope
}

// .
// .
func (f *Relay) Know(name string, canonical []byte, env *witness.PublicKeyEnvelope) {
	id, err := witness.DeriveIdentityID(canonical, env)
	if err != nil {
		f.t.Fatal(err)
	}
	f.mu.Lock()
	f.known[name] = knownName{id: id, env: *env}
	f.mu.Unlock()
}

// .
func (f *Relay) Opens() int    { f.mu.Lock(); defer f.mu.Unlock(); return f.opens }
func (f *Relay) Refusals() int { f.mu.Lock(); defer f.mu.Unlock(); return f.refusals }

// .
func (f *Relay) Drop() {
	f.mu.Lock()
	close(f.dropAll)
	f.dropAll = make(chan struct{})
	f.mu.Unlock()
}

func (f *Relay) dropCh() chan struct{} { f.mu.Lock(); defer f.mu.Unlock(); return f.dropAll }

func (f *Relay) serve() {
	for {
		c, err := f.ln.Accept()
		if err != nil {
			return
		}
		go f.handle(c)
	}
}

func (f *Relay) handle(c net.Conn) {
	sni, replay, err := relay.PeekServerName(c, 5*time.Second)
	if err != nil {
		c.Close()
		return
	}
	if sni == f.name {
		f.control(tls.Server(replay, &tls.Config{Certificates: []tls.Certificate{f.cert}}))
		return
	}
	f.route(sni, replay)
}

// .
// .
func (f *Relay) control(c *tls.Conn) {
	if err := c.Handshake(); err != nil {
		c.Close()
		return
	}
	r := relay.NewReader(c)
	m, err := relay.ReadMessage(r)
	if err != nil {
		c.Close()
		return
	}
	switch m.Type {
	case "register":
		if reason := f.verify(m); reason != "" {
			f.mu.Lock()
			f.refusals++
			f.mu.Unlock()
			_ = relay.WriteMessage(c, relay.Message{Type: "refused", Reason: reason})
			c.Close()
			return
		}
		f.mu.Lock()
		f.tunnels[m.Name] = c
		f.mu.Unlock()
		drop := f.dropCh()
		_ = relay.WriteMessage(c, relay.Message{Type: "registered", Name: m.Name})
		for {
			select {
			case <-drop:
				c.Close()
				return
			default:
			}
			_ = c.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
			m, err := relay.ReadMessage(r)
			if err != nil {
				if ne, ok := err.(net.Error); ok && ne.Timeout() {
					continue
				}
				f.mu.Lock()
				delete(f.tunnels, m.Name)
				f.mu.Unlock()
				c.Close()
				return
			}
			if m.Type == "ping" {
				_ = relay.WriteMessage(c, relay.Message{Type: "pong"})
			}
		}
	case "attach":
		f.mu.Lock()
		ch := f.pending[m.Conn]
		f.mu.Unlock()
		if ch == nil {
			_ = relay.WriteMessage(c, relay.Message{Type: "refused", Reason: "unknown connection"})
			c.Close()
			return
		}
		_ = relay.WriteMessage(c, relay.Message{Type: "attached"})
		ch <- relay.Carrier(c, r)
	default:
		c.Close()
	}
}

// .
// .
// .
func (f *Relay) verify(m relay.Message) string {
	f.mu.Lock()
	kn, ok := f.known[m.Name]
	f.mu.Unlock()
	if !ok {
		return "unknown name"
	}
	env := kn.env
	var sent witness.PublicKeyEnvelope
	if err := json.Unmarshal(m.IdentityPublicKey, &sent); err != nil || sent.KeyID != env.KeyID || m.IdentityID != kn.id {
		return "envelope is not the name's"
	}
	exp, err := time.Parse(time.RFC3339, m.ExpiresAt)
	if err != nil || exp.After(time.Now().Add(relay.RegisterLifetime+time.Minute)) || exp.Before(time.Now()) {
		return "expiry out of window"
	}
	if m.IdentitySignature == nil {
		return "unsigned"
	}
	ml, ok := env.FindPublicKey(witness.AlgMLDSA87)
	if !ok || m.IdentitySignature.KeyID != env.KeyID || m.IdentitySignature.PublicKeyFingerprint != ml.PublicKeyFingerprint {
		return "signature names another key"
	}
	pub, err := witnesstest.DecodeB64(ml.PublicKeyB64)
	if err != nil {
		return "bad key"
	}
	raw, err := witnesstest.DecodeB64(m.IdentitySignature.SigB64)
	if err != nil {
		return "bad signature"
	}
	if aiicrypto.Verify(pub, relay.RegisterInput(f.name, m.Name, m.IdentityID, m.Nonce, m.ExpiresAt), raw) != nil {
		return "signature does not verify"
	}
	return ""
}

// .
// .
func (f *Relay) route(name string, browser net.Conn) {
	f.mu.Lock()
	ctl := f.tunnels[name]
	f.mu.Unlock()
	if ctl == nil {
		browser.Close()
		return
	}
	var idb [16]byte
	rand.Read(idb[:])
	id := hex.EncodeToString(idb[:])
	ch := make(chan net.Conn, 1)
	f.mu.Lock()
	f.pending[id] = ch
	f.opens++
	f.mu.Unlock()
	if err := relay.WriteMessage(ctl, relay.Message{Type: "open", Conn: id}); err != nil {
		browser.Close()
		return
	}
	select {
	case carrier := <-ch:
		relay.Splice(context.Background(), browser, carrier)
	case <-time.After(relay.AttachWithin):
		browser.Close()
	}
	f.mu.Lock()
	delete(f.pending, id)
	f.mu.Unlock()
}
