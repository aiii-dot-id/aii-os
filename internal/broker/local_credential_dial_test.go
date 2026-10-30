package broker

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"sync/atomic"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/firewall"
	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
)

// credentialDevice is a device that counts the requests that reached it
// carrying the bearer token.
func credentialDevice(t *testing.T, tlsServer bool, token string) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var withToken atomic.Int32
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "Bearer "+token {
			withToken.Add(1)
		}
		fmt.Fprint(w, `{}`)
	})
	var ts *httptest.Server
	if tlsServer {
		ts = httptest.NewTLSServer(h)
	} else {
		ts = httptest.NewServer(h)
	}
	t.Cleanup(ts.Close)
	return ts, &withToken
}

func TestACredentialReachesAGrantedLocalNameThroughTheRealDialer(t *testing.T) {
	const token = "fixture-local-name-token"
	ts, withToken := credentialDevice(t, false, token)
	_, port := tsHostPort(t, ts)
	t.Setenv("AII_TEST_LOCAL_NAME_TOKEN", token)
	name := fmt.Sprintf("localhost:%d", port)
	h := newHost(t, newStore(t), Config{
		Grants: map[string]Grant{"p": {Local: []string{name}, CredentialHandles: []string{"ha"}, PlaintextCredentials: true}},
		AuthProfiles: map[string]AuthProfile{"ha": {
			Scheme: "bearer", SecretEnv: "AII_TEST_LOCAL_NAME_TOKEN", Host: "localhost", Port: port,
		}},
	})
	b := h.Bind("p", packagefmt.TierT1, []string{"net.local"})
	m := dispatch(t, b, netParams("http://"+name+"/api/states", `{"auth_profile":"ha"}`))
	if n := withToken.Load(); n != 1 {
		t.Fatalf("a credentialed call to a granted local name reached the device %d time(s) with its token; the connect refused what the first hop admitted: %s", n, replyText(m))
	}
	wantResult(t, m, statusSucceeded, "")
	noCredential(t, m, token)
}

// trustForTheDialer makes the production transport trust the fixture's
// certificate for this test: guardedTransport clones http.DefaultTransport
// when the client is built, so its TLS roots are the one thing changed;
// the dialer stays the production one. Broker tests do not run in
// parallel, so no other test sees the change before it is put back.
func trustForTheDialer(t *testing.T, ts *httptest.Server) {
	t.Helper()
	dt := http.DefaultTransport.(*http.Transport)
	was := dt.TLSClientConfig
	pool := x509.NewCertPool()
	pool.AddCert(ts.Certificate())
	dt.TLSClientConfig = &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}
	t.Cleanup(func() { dt.TLSClientConfig = was })
}

func TestACredentialOverHTTPSToALocalAddressNeedsNoPlaintextConsent(t *testing.T) {
	const token = "fixture-local-tls-token"
	ts, withToken := credentialDevice(t, true, token)
	trustForTheDialer(t, ts)
	host, port := tsHostPort(t, ts)
	t.Setenv("AII_TEST_LOCAL_TLS_TOKEN", token)
	h := newHost(t, newStore(t), Config{
		// No plaintext_credentials: https needs none.
		Grants: map[string]Grant{"p": {Local: []string{fmt.Sprintf("%s:%d", host, port)}, CredentialHandles: []string{"ha"}}},
		AuthProfiles: map[string]AuthProfile{"ha": {
			Scheme: "bearer", SecretEnv: "AII_TEST_LOCAL_TLS_TOKEN", Host: host, Port: port,
		}},
	})
	b := h.Bind("p", packagefmt.TierT1, []string{"net.local"})
	m := dispatch(t, b, netParams(ts.URL+"/api", `{"auth_profile":"ha"}`))
	if n := withToken.Load(); n != 1 {
		t.Fatalf("a credentialed https call to a granted local address reached the device %d time(s) with its token; the connect asked for plaintext consent it does not need: %s", n, replyText(m))
	}
	wantResult(t, m, statusSucceeded, "")
	noCredential(t, m, token)
}

// THE CONNECT DOES NOT WIDEN WHERE A CREDENTIAL RIDES. The scheme rule and
// the pin's host stay the URL's (admit, on the first hop and every
// redirect); at the connect a credential goes only to the pinned host, at
// an address that host is: the literal itself, or one the guard pinned the
// name to. Without a credential the connect's question is the one it
// always asked.
func TestTheConnectKeepsACredentialOnItsPinnedHostsOwnAddresses(t *testing.T) {
	scopes, _ := firewall.LocalScopes([]string{"192.168.1.0/24:*", "device.local:8123"})
	pins := map[string][]net.IP{"device.local": {net.ParseIP("192.168.1.10")}}
	policy := func(profile, host string, port int) hopPolicy {
		return hopPolicy{
			guard:        func(context.Context, string) error { return nil },
			pinned:       func(n string) ([]net.IP, bool) { ips, ok := pins[n]; return ips, ok },
			localCapable: true, localScopes: scopes,
			profile: profile, host: host, port: port, local: true,
		}
	}
	at := func(s string) netip.AddrPort { return netip.MustParseAddrPort(s) }
	for _, tc := range []struct {
		why     string
		h       hopPolicy
		dialled string
		addr    netip.AddrPort
		admit   bool
	}{
		{"a granted name at its pinned address", policy("ha", "device.local", 8123), "device.local", at("192.168.1.10:8123"), true},
		{"a local literal at itself", policy("ha", "192.168.1.20", 8443), "192.168.1.20", at("192.168.1.20:8443"), true},
		{"a public name answering local at the connect", policy("api", "api.example.test", 443), "api.example.test", at("192.168.1.5:443"), false},
		{"the pinned name at an address it was not pinned to", policy("ha", "device.local", 8123), "device.local", at("192.168.1.11:8123"), false},
		{"another host than the profile's (a proxy)", policy("ha", "device.local", 8123), "proxy.local", at("192.168.1.10:8123"), false},
		{"the profile's host on another port", policy("ha", "192.168.1.20", 8443), "192.168.1.20", at("192.168.1.20:8444"), false},
		{"no credential: the scopes decide", policy("", "api.example.test", 443), "api.example.test", at("192.168.1.5:443"), true},
	} {
		err := tc.h.admitDial(context.Background(), tc.dialled, tc.addr)
		if tc.admit && err != nil {
			t.Errorf("%s: refused at the connect: %v", tc.why, err)
		}
		if !tc.admit && !errors.Is(err, firewall.ErrEgressBlocked) {
			t.Errorf("%s: a credential would ride to %s for %s (err %v)", tc.why, tc.addr, tc.dialled, err)
		}
	}
	// The URL's own rules are unchanged: an http hop with a credential and
	// no plaintext consent is refused by admit, the question every URL meets.
	h := policy("ha", "192.168.1.20", 8443)
	if err := h.admit(context.Background(), "http://192.168.1.20:8443/"); err == nil {
		t.Error("an http URL carried a credential without the operator's plaintext consent")
	}
}

// replyText is a reply whole, for a failure message.
func replyText(m map[string]json.RawMessage) string {
	b, _ := json.Marshal(m)
	return string(b)
}
