package app

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/dashboard"
	"github.com/aiii-dot-id/aii-os/internal/publicname"
	"github.com/aiii-dot-id/aii-os/internal/publicname/publicnametest"
	"github.com/aiii-dot-id/aii-os/internal/witness"
)

func TestRelayAuthenticationSurvivesRefusedReloadAndValidRotation(t *testing.T) {
	const initialToken = "fixture-running-token"
	rly := publicnametest.NewRelay(t)
	cfg := birthNamed(t, t.TempDir())
	cfg.Dashboard.Host = "127.0.0.1"
	cfg.Dashboard.RequireToken = true
	cfg.Dashboard.AccessToken = initialToken
	cfg.Certificate.RouteMode = publicname.RouteRelay
	cfg.Certificate.RelayEndpoint = rly.Addr()
	if _, err := saveConfig(cfg); err != nil {
		t.Fatal(err)
	}
	pub := &publicnametest.Publisher{Zone: "id.example.test"}
	var managers []*publicnametest.Manager
	a := New(cfg)
	a.pn.NewPublisher = func(publicname.Settings) (publicname.NamePublisher, error) { return pub, nil }
	a.pn.NewCertManager = publicnametest.Managers[publicname.CertificateManager](&managers)
	a.pn.RelayTLS = rly.TLSConfig()
	if err := startLiveForTest(a); err != nil {
		t.Fatal(err)
	}
	defer stopApp(t, a)
	raw, env, err := witness.EnsureIdentityEnvelope(witness.AsIdentityKey(a.keyPair), a.store, filepath.Dir(a.ledger.Path()))
	if err != nil {
		t.Fatal(err)
	}
	name, err := a.pn.Claim()
	if err != nil {
		t.Fatal(err)
	}
	rly.Know(name.Name, raw, env)
	waitStatus(t, a, publicname.StatusIssued)
	startRelayForTest(t, a, name.Name, rly.Addr())
	waitFor(t, "authenticated relay", func() bool { return a.pn.State().RelayConnected })
	root := x509.NewCertPool()
	root.AddCert(managers[len(managers)-1].Certificate().Leaf)
	transport := &http.Transport{DialTLSContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&tls.Dialer{Config: &tls.Config{RootCAs: root, ServerName: name.Name, MinVersion: tls.VersionTLS12}}).DialContext(ctx, "tcp", rly.Addr())
	}}
	defer transport.CloseIdleConnections()
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Transport: transport, Jar: jar, Timeout: 5 * time.Second}
	// The public host is the dashboard's actual origin, not the relay's host.
	origin := a.dashboard.Origin()
	request := func(method, token string, want int) {
		t.Helper()
		req, err := http.NewRequest(method, origin+"/auth/token", strings.NewReader(token))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Origin", origin)
		res, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		io.Copy(io.Discard, res.Body)
		res.Body.Close()
		if res.StatusCode != want {
			t.Fatalf("%s auth status %d, want %d", method, res.StatusCode, want)
		}
	}
	request(http.MethodGet, "", http.StatusUnauthorized)
	request(http.MethodPost, cfg.Dashboard.AccessToken, http.StatusNoContent)
	request(http.MethodGet, "", http.StatusNoContent)
	existing := a.pn.RelayForTest()
	registered := existing.State().Since
	rly.Drop()
	waitFor(t, "relay control reconnect", func() bool { st := existing.State(); return st.Connected && st.Since.After(registered) })
	request(http.MethodGet, "", http.StatusNoContent)
	carried := rly.Opens()
	off := a.configSnapshot()
	off.Dashboard.RequireToken = false
	off.Dashboard.AccessToken = ""
	if _, err := saveConfig(&off); err != nil {
		t.Fatal(err)
	}
	a.reloadConfig()
	if !a.dashboard.AccessTokenRequired() || a.dashboardToken() != initialToken {
		t.Fatal("file reload removed the working relay credential")
	}
	running := a.configSnapshot().Dashboard
	if !running.RequireToken || running.AccessToken != initialToken {
		t.Fatal("refused values replaced running configuration")
	}
	same := a.pn.RelayForTest() == existing
	if !same {
		t.Fatal("refused reload replaced the working relay")
	}
	request(http.MethodGet, "", http.StatusNoContent)
	if rly.Opens() != carried {
		t.Fatal("refused downgrade broke the existing carried HTTP connection")
	}
	if !a.configState().Dashboard.RequireToken {
		t.Fatal("configuration readback concealed the still-required token")
	}
	// The operator's file is not secretly rewritten. A refusal is live policy,
	// not a rollback of an external edit or a promise that edit is safe to reboot.
	loaded, err := LoadConfig(cfg.SourcePath)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Dashboard.RequireToken || loaded.Dashboard.AccessToken != "" {
		t.Fatal("reload silently rewrote the operator's file")
	}
	rotate := a.configSnapshot()
	rotate.Dashboard.RequireToken = true
	rotate.Dashboard.AccessToken = "fixture-rotated-token"
	if _, err := saveConfig(&rotate); err != nil {
		t.Fatal(err)
	}
	a.reloadConfig()
	request(http.MethodGet, "", http.StatusUnauthorized)
	request(http.MethodPost, rotate.Dashboard.AccessToken, http.StatusNoContent)
	request(http.MethodGet, "", http.StatusNoContent)
	retry := a.configSnapshot()
	retry.Dashboard.RequireToken = false
	retry.Dashboard.AccessToken = ""
	if _, err := saveConfig(&retry); err != nil {
		t.Fatal(err)
	}
	a.reloadConfig()
	if !a.dashboard.AccessTokenRequired() {
		t.Fatal("second unsafe edit removed authentication")
	}
	if done := a.pn.StopRelayForTest(); done != nil {
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("relay did not finish after cancellation")
		}
	}
	retired := a.pn.RelayForTest() == nil
	if !retired {
		t.Fatal("completed relay did not retire its exposure")
	}
	a.reloadConfig() // reapply the SAME file once its requirement is satisfied
	if a.dashboard.AccessTokenRequired() {
		t.Fatal("retry of the same file after relay completion was treated as already applied")
	}
	if _, err := saveConfig(&rotate); err != nil {
		t.Fatal(err)
	}
	a.reloadConfig()
	// A normal restart from the corrected durable configuration keeps the
	// same credential, never a newly minted token or alternate credential store.
	stopApp(t, a)
	restored, err := LoadConfig(cfg.SourcePath)
	if err != nil {
		t.Fatal(err)
	}
	b := New(restored)
	d, err := b.newDashboard(nil)
	if err != nil {
		t.Fatal(err)
	}
	if !d.AccessTokenRequired() || b.dashboardToken() != rotate.Dashboard.AccessToken {
		t.Fatal("restart lost the durable rotated credential")
	}
}

func relayAuthApp() *App {
	cfg := defaultConfig()
	cfg.Dashboard.Host = "127.0.0.1"
	cfg.Dashboard.RequireToken = true
	cfg.Dashboard.AccessToken = "fixture-running-token"
	a := New(cfg)
	a.dashboard = dashboard.New("127.0.0.1", 0, &dashboard.WSHandler{})
	a.dashboard.SetAccessToken(true, cfg.Dashboard.AccessToken)
	a.setDashboardToken(cfg.Dashboard.AccessToken)
	return a
}

func TestRelayTokenDowngradeKeepsRunningPolicyAndConnection(t *testing.T) {
	a := relayAuthApp()
	cancelled := false
	client := a.pn.CarryForTest("fixture.example.test", func() { cancelled = true }, nil)
	off := a.cfg.Dashboard
	off.RequireToken = false
	off.AccessToken = "replacement-must-not-land"
	var refusal *dashboardTokenRefusal
	if err := a.rearmDashboardToken(off); !errors.As(err, &refusal) {
		t.Fatalf("want typed refusal, got %v", err)
	}
	if cancelled || a.pn.RelayForTest() != client || !a.dashboard.AccessTokenRequired() || a.dashboardToken() != a.cfg.Dashboard.AccessToken {
		t.Fatal("refused downgrade changed the relay or running token")
	}
	rotate := off
	rotate.RequireToken = true
	if err := a.rearmDashboardToken(rotate); err != nil {
		t.Fatal(err)
	}
	if a.pn.RelayForTest() != client || cancelled || a.dashboardToken() != rotate.AccessToken || !a.dashboard.AccessTokenRequired() {
		t.Fatal("valid rotation lost authentication or relay")
	}
	rotate.AccessToken = ""
	if err := a.rearmDashboardToken(rotate); !errors.As(err, &refusal) {
		t.Fatalf("empty required token not refused: %v", err)
	}
	if a.dashboardToken() != off.AccessToken {
		t.Fatal("empty token erased running token")
	}
}

func TestRelayExposureEndsAfterCompletionNotCancellation(t *testing.T) {
	a := relayAuthApp()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	a.pn.CarryForTest("fixture.example.test", cancel, done)
	if got := a.pn.StopRelayForTest(); got != done {
		t.Fatal("stop lost worker completion")
	}
	if ctx.Err() == nil || a.pn.RelayForTest() == nil {
		t.Fatal("exposure retired before relay completion")
	}
	off := a.cfg.Dashboard
	off.RequireToken = false
	if err := a.rearmDashboardToken(off); err == nil {
		t.Fatal("downgrade admitted during relay drain")
	}
	// Real worker completion/unpublication is covered by the relay journey;
	// this synthetic owner proves that a stop request alone cannot retire it.
}
