package relay_test

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	aiicrypto "github.com/aiii-dot-id/aii-os/internal/crypto"
	"github.com/aiii-dot-id/aii-os/internal/relay"
	"github.com/aiii-dot-id/aii-os/internal/relay/relaytest"
	"github.com/aiii-dot-id/aii-os/internal/witness"
)

type memEnvelopes struct{ raw []byte }

func (m *memEnvelopes) SaveWitnessEnvelope(c []byte) error      { m.raw = c; return nil }
func (m *memEnvelopes) LoadWitnessEnvelope() ([]byte, error)    { return m.raw, nil }
func (m *memEnvelopes) ReplaceWitnessEnvelope(raw []byte) error { m.raw = raw; return nil }

// No system.witnessed in this fake's record: the envelope is minted.
func (m *memEnvelopes) WitnessEnrollment() ([]string, time.Time, time.Time, error) {
	return nil, time.Time{}, time.Time{}, nil
}

func identityForTest(t *testing.T) (witness.IdentityKey, []byte, *witness.PublicKeyEnvelope) {
	t.Helper()
	kp, err := aiicrypto.GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	key := witness.AsIdentityKey(kp)
	canonical, env, err := witness.EnsureIdentityEnvelope(key, &memEnvelopes{}, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return key, canonical, env
}

// The whole path: a registered identity, a browser at the relay asking
// for the identity's name, the session carried through the tunnel and
// handed to the dashboard, which serves it in process — the dashboard's
// certificate is the one the browser sees, the relay having read only
// the server name — and an HTTP exchange end to end. A registration with the wrong key is
// refused; when the relay drops the control connection the client
// registers again.
func TestABrowserAtTheRelayReachesTheDashboardsOwnTLSBind(t *testing.T) {
	const name = "abcdefghijklmnopqrstuvwxyz.aiios.id"
	dashCert := relaytest.SelfSigned(t, name)
	deliver := relaytest.Dashboard(t, dashCert, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "hello from "+r.Host)
	}))

	rly := relaytest.New(t)
	key, canonical, env := identityForTest(t)
	rly.Know(name, canonical, env)
	id, err := witness.DeriveIdentityID(canonical, env)
	if err != nil {
		t.Fatal(err)
	}
	client := &relay.Client{Relay: rly.Addr(), Name: name, IdentityID: id, Key: key, Env: env, Envelope: canonical, Deliver: deliver, TLS: rly.TLSConfig()}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go client.Run(ctx)
	waitFor(t, "the registration", func() bool { return client.State().Connected })

	// The browser: TLS to the relay's address, asking for the identity's
	// name, trusting the dashboard's own certificate and nothing else.
	dashRoots := x509.NewCertPool()
	dashRoots.AddCert(dashCert.Leaf)
	browse := func() string {
		t.Helper()
		bc, err := tls.Dial("tcp", rly.Addr(), &tls.Config{ServerName: name, RootCAs: dashRoots, MinVersion: tls.VersionTLS12})
		if err != nil {
			t.Fatalf("the browser's session through the relay: %v", err)
		}
		defer bc.Close()
		if got := bc.ConnectionState().PeerCertificates[0]; got.VerifyHostname(name) != nil || !got.Equal(dashCert.Leaf) {
			t.Fatal("the certificate the browser saw is not the dashboard's own")
		}
		req, _ := http.NewRequest("GET", "https://"+name+"/", nil)
		if err := req.Write(bc); err != nil {
			t.Fatal(err)
		}
		res, err := http.ReadResponse(relay.NewReader(bc), req)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(res.Body)
		res.Body.Close()
		return string(body)
	}
	if got := browse(); got != "hello from "+name {
		t.Fatalf("through the relay: %q", got)
	}
	if got := browse(); got != "hello from "+name {
		t.Fatalf("a second session: %q", got)
	}
	if opens := rly.Opens(); opens != 2 {
		t.Fatalf("two browser sessions, %d opens", opens)
	}

	// The relay drops every control connection; the client comes back.
	rly.Drop()
	waitFor(t, "the drop", func() bool { return !client.State().Connected })
	waitFor(t, "the re-registration", func() bool { return client.State().Connected })
	if got := browse(); got != "hello from "+name {
		t.Fatalf("after re-registration: %q", got)
	}

	// Another identity claiming this name is refused: the envelope the
	// publisher pushed is not its.
	okey, ocanonical, oenv := identityForTest(t)
	oid, _ := witness.DeriveIdentityID(ocanonical, oenv)
	other := &relay.Client{Relay: rly.Addr(), Name: name, IdentityID: oid, Key: okey, Env: oenv, Envelope: ocanonical, Deliver: deliver, TLS: client.TLS}
	octx, ocancel := context.WithCancel(context.Background())
	defer ocancel()
	go other.Run(octx)
	waitFor(t, "the refusal", func() bool { return strings.Contains(other.State().LastError, "refused") })
	if refusals := rly.Refusals(); refusals == 0 || other.State().Connected {
		t.Fatalf("another identity must be refused: refusals=%d connected=%v", refusals, other.State().Connected)
	}
}

func waitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if ok() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("waiting for %s", what)
}
