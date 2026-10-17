package app

import (
	"bytes"
	"errors"
	"net"
	"net/http"
	"net/http/cookiejar"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/broker"
	"github.com/aiii-dot-id/aii-os/internal/dashboard"
	"github.com/aiii-dot-id/aii-os/internal/install"
	"github.com/aiii-dot-id/aii-os/internal/pluginhost"
	"github.com/aiii-dot-id/aii-os/internal/publicname"
	"github.com/aiii-dot-id/aii-os/internal/publicname/publicnametest"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

// rotationApp is a live-shaped app on a real loopback dashboard with a token
// required and minted, its config on disk, the live handler's rotation hook
// wired as app.go wires it.
func rotationApp(t *testing.T) (*App, string) {
	t.Helper()
	cfg := defaultConfig()
	cfg.SourcePath = filepath.Join(t.TempDir(), "config.json")
	cfg.Identity.KeyPath = filepath.Join(filepath.Dir(cfg.SourcePath), "identity.sec")
	cfg.Dashboard = DashboardConfig{Host: "127.0.0.1", Port: 0, RequireToken: true}
	a := New(cfg)
	d, err := a.newDashboard(&dashboard.WSHandler{DashboardToken: a.dashboardToken, RotateDashboardToken: a.rotateDashboardAccess})
	if err != nil {
		t.Fatal(err)
	}
	a.dashboard = d
	if _, err := d.Start(""); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Shutdown(t.Context()) })
	return a, a.DashboardURL()
}

func signedIn(t *testing.T, base, token string) *http.Client {
	t.Helper()
	jar, _ := cookiejar.New(nil)
	c := &http.Client{Jar: jar, Timeout: 5 * time.Second, Transport: &http.Transport{DisableKeepAlives: true}}
	r, err := c.Post(base+"auth/token", "text/plain", strings.NewReader(token))
	if err != nil {
		t.Fatal(err)
	}
	r.Body.Close()
	if r.StatusCode != http.StatusNoContent {
		t.Fatalf("sign-in with the token in force: %d", r.StatusCode)
	}
	return c
}

func statusOf(t *testing.T, c *http.Client, url string) int {
	t.Helper()
	r, err := c.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	r.Body.Close()
	return r.StatusCode
}

// SETTINGS ROTATES THE TOKEN THROUGH THE CONFIG WRITER, AND THE RUNNING
// SERVER TAKES IT. The new token is minted, not chosen; it is config.json's
// and the server's at once; the browser that asked stays signed in on the
// cookie the answer carried; every other browser's cookie is refused from
// then on; the requirement itself is exactly what it was.
func TestSettingsRotatesTheTokenThroughTheConfigWriter(t *testing.T) {
	a, base := rotationApp(t)
	old := a.dashboardToken()
	if old == "" || !a.dashboard.AccessTokenRequired() {
		t.Fatal("the fixture has no token in force")
	}
	before, err := LoadConfig(a.configSnapshot().SourcePath)
	if err != nil || before.Dashboard.AccessToken != old {
		t.Fatalf("the token in force is not config.json's: %v", err)
	}
	rotator, other := signedIn(t, base, old), signedIn(t, base, old)

	req, _ := http.NewRequest(http.MethodPost, base+"auth/rotate", nil)
	req.Header.Set("Origin", strings.TrimSuffix(base, "/"))
	r, err := rotator.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	r.Body.Close()
	if r.StatusCode != http.StatusNoContent {
		t.Fatalf("rotation from Settings over this machine's own connection: %d", r.StatusCode)
	}

	after, err := LoadConfig(a.configSnapshot().SourcePath)
	if err != nil {
		t.Fatal(err)
	}
	rotated := after.Dashboard.AccessToken
	if rotated == old || !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(rotated) {
		t.Fatalf("config.json does not hold a freshly minted token: %q", rotated)
	}
	if a.dashboardToken() != rotated || a.configSnapshot().Dashboard.AccessToken != rotated {
		t.Fatal("the running server, the live configuration and config.json disagree about the token")
	}
	if after.Dashboard.RequireToken != before.Dashboard.RequireToken {
		t.Fatal("a rotation changed whether a token is required")
	}
	if got := statusOf(t, rotator, base+"auth/token"); got != http.StatusNoContent {
		t.Fatalf("the rotating browser was signed out: %d", got)
	}
	if got := statusOf(t, other, base+"auth/token"); got != http.StatusUnauthorized {
		t.Fatalf("another browser's old cookie still signs in: %d", got)
	}
	if signedIn(t, base, rotated) == nil {
		t.Fatal("the new token does not sign a browser in")
	}
}

// AN OPEN DASHBOARD HAS NO TOKEN TO ROTATE: the refusal is typed, and
// config.json is untouched.
func TestAnOpenDashboardHasNoTokenToRotate(t *testing.T) {
	cfg := defaultConfig()
	cfg.SourcePath = filepath.Join(t.TempDir(), "config.json")
	cfg.Dashboard = DashboardConfig{Host: "127.0.0.1", Port: 0}
	if _, err := saveConfig(cfg); err != nil {
		t.Fatal(err)
	}
	a := New(cfg)
	a.dashboard = dashboard.New("127.0.0.1", 0, &dashboard.WSHandler{})
	raw, _ := os.ReadFile(cfg.SourcePath)
	if err := a.rotateDashboardAccess(); !errors.Is(err, errNoTokenToRotate) {
		t.Fatalf("want the typed refusal, got %v", err)
	}
	if now, _ := os.ReadFile(cfg.SourcePath); !bytes.Equal(raw, now) {
		t.Fatal("a refused rotation wrote config.json")
	}
}

// serviceAnswer is what the certificate service last told routeApp's
// runtime: nothing yet, record sets with relays, or record sets only.
type serviceAnswer int

const (
	serviceUnanswered serviceAnswer = iota
	serviceOffersRelays
	serviceOffersNoRelays
)

// routeApp is an app with a claimed name, reached on 443, whose certificate
// service last advertised two relays: one on the name's port, one not.
func routeApp(t *testing.T, tokenRequired bool, answer serviceAnswer) *App {
	t.Helper()
	cfg := defaultConfig()
	cfg.SourcePath = filepath.Join(t.TempDir(), "config.json")
	if _, err := saveConfig(cfg); err != nil {
		t.Fatal(err)
	}
	a := New(cfg)
	a.dashboard = dashboard.New("127.0.0.1", 0, &dashboard.WSHandler{})
	a.dashboard.SetAccessToken(tokenRequired, "fixture-running-token")
	name := store.PublicName{Name: "ui.abcdefghijklmnopqrstuvwxyz.id.example.test", Zone: "id.example.test"}
	origin := "https://ui.abcdefghijklmnopqrstuvwxyz.id.example.test"
	relays := []string{"relay.example.test:443", "far.example.test:9443"}
	switch answer {
	case serviceOffersRelays:
		a.pn.ServeForTest(name, origin, publicnametest.Service("id.example.test", relays...))
	case serviceOffersNoRelays:
		a.pn.ServeForTest(name, origin, publicnametest.ServiceWithoutRelays("id.example.test", relays...))
	default:
		a.pn.ServeForTest(name, origin, nil)
	}
	return a
}

func TestARelayThatMovesTheAddressIsChosenByNamingItsPort(t *testing.T) {
	a := routeApp(t, true, serviceOffersRelays)
	relay := func(endpoint string) map[string]any {
		return map[string]any{"certificate.route_mode": "relay", "certificate.relay_endpoint": endpoint}
	}
	_, err := a.applyConfigChange(relay("far.example.test"))
	var unnamed *publicname.RelayPortUnnamed
	if !errors.As(err, &unnamed) {
		t.Fatalf("a relay on another port, its port not named: want the typed refusal, got %v", err)
	}
	if unnamed.Port != 9443 || !strings.HasSuffix(unnamed.To, ":9443") || !strings.Contains(err.Error(), "far.example.test:9443") {
		t.Fatalf("the refusal does not say where the address would move and how to accept it: %+v", unnamed)
	}
	st, err := a.applyConfigChange(relay("far.example.test:9443"))
	if err != nil {
		t.Fatalf("the move, its port named: %v", err)
	}
	if want := "https://ui.abcdefghijklmnopqrstuvwxyz.id.example.test:9443"; st.PublicName.Origin != want || a.dashboard.AdvertisedOrigin() != want {
		t.Fatalf("the name's address did not follow the route: the card reads %q and the dashboard advertises %q, want %q", st.PublicName.Origin, a.dashboard.AdvertisedOrigin(), want)
	}
	if !a.dashboard.HostAllowedForTest("ui.abcdefghijklmnopqrstuvwxyz.id.example.test:9443") {
		t.Fatal("the dashboard refuses the Host a browser sends at the name's new address")
	}
	// The second move: back to the dashboard's own port, through another relay.
	if _, err := a.applyConfigChange(relay("relay.example.test")); !errors.As(err, &unnamed) {
		t.Fatalf("from the relay on 9443 to the relay on 443 with nothing said: %v", err)
	}
	if st, err = a.applyConfigChange(relay("relay.example.test:443")); err != nil {
		t.Fatalf("the second move, its port named: %v", err)
	}
	if want := "https://ui.abcdefghijklmnopqrstuvwxyz.id.example.test"; st.PublicName.Origin != want {
		t.Fatalf("the name's address is %q after the second move, want %q", st.PublicName.Origin, want)
	}
	// The way back to direct stays open, and the address returns with it.
	a.pn.CarryForTest("ui.abcdefghijklmnopqrstuvwxyz.id.example.test", func() {}, nil)
	if _, err := a.applyConfigChange(relay("far.example.test:9443")); err != nil {
		t.Fatal(err)
	}
	if st, err = a.applyConfigChange(map[string]any{"certificate.route_mode": "direct"}); err != nil {
		t.Fatalf("the way back to direct: %v", err)
	}
	if want := "https://ui.abcdefghijklmnopqrstuvwxyz.id.example.test"; st.PublicName.Origin != want {
		t.Fatalf("back to direct the name's address is %q, want %q", st.PublicName.Origin, want)
	}
}

// THE CARD IS TOLD WHERE EACH CHOICE LEADS AND WHAT MOVES WITH THE ADDRESS,
// by name, computed where the route owner computes it: a sign-in profile
// whose registered redirect is at the name's URL, and the plugins receiving
// webhooks there. With dashboard.origin configured the webhook addresses are
// the operator's own front, and a route change does not move them.
func TestTheCardNamesWhereEachRouteLeadsAndWhatMovesWithIt(t *testing.T) {
	const name = "https://ui.abcdefghijklmnopqrstuvwxyz.id.example.test"
	a := routeApp(t, true, serviceOffersRelays)
	a.cfgMu.Lock()
	a.cfg.Plugins.AuthProfiles = map[string]broker.AuthProfile{
		"mail":  {RedirectURI: name + "/oauth/callback"},
		"other": {RedirectURI: "https://elsewhere.example.test/oauth/callback"},
	}
	a.cfgMu.Unlock()
	a.pluginMu.Lock()
	a.plugins = append(a.plugins, &pluginhost.ActivePlugin{ID: "plug.example", Webhooks: []pluginhost.WebhookDecl{{Path: "in"}}}, &pluginhost.ActivePlugin{ID: "quiet.example"})
	a.pluginMu.Unlock()

	st := a.configState().PublicName
	if st.DirectOrigin != name || st.Relays == nil || len(*st.Relays) != 2 || (*st.Relays)[0].Origin != name || (*st.Relays)[1].Origin != name+":9443" {
		t.Fatalf("the card is not told the address each route gives: direct %q, relays %+v", st.DirectOrigin, st.Relays)
	}
	if want := []string{"sign-in profile mail", "webhooks of plug.example"}; !reflect.DeepEqual(st.MovesWith, want) {
		t.Fatalf("what moves with the address: %v, want %v", st.MovesWith, want)
	}
	a.cfgMu.Lock()
	a.cfg.Dashboard.Origin = "https://front.example.test"
	a.cfgMu.Unlock()
	if got, want := a.configState().PublicName.MovesWith, []string{"sign-in profile mail"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("with dashboard.origin configured the webhook addresses do not move, and the card listed %v", got)
	}
}

func TestARelayIsNotChosenBehindAShortToken(t *testing.T) {
	choose := map[string]any{"certificate.route_mode": "relay", "certificate.relay_endpoint": "relay.example.test"}
	a := routeApp(t, true, serviceOffersRelays)
	a.dashboard.SetAccessToken(true, "fifteen-chars-x")
	path := a.configSnapshot().SourcePath
	before, _ := os.ReadFile(path)
	_, err := a.applyConfigChange(choose)
	var short *RelayTokenShort
	if !errors.As(err, &short) {
		t.Fatalf("a relay behind a fifteen-character token: want the typed refusal, got %v", err)
	}
	if short.Length != 15 || short.Line != dashboard.ShortAccessToken || !strings.Contains(err.Error(), "Rotate access token") || strings.Contains(err.Error(), "generated") {
		t.Fatalf("the refusal does not say the length, the line and the way out, or claims to know how the token was made: %v", err)
	}
	if after, _ := os.ReadFile(path); !bytes.Equal(before, after) {
		t.Fatal("the refused choice changed config.json")
	}
	if st := a.configState(); !st.Dashboard.TokenShort {
		t.Fatal("the card is not told the token in force is short")
	}
	// The way back to direct needs no token of any length.
	if _, err := a.applyConfigChange(map[string]any{"certificate.route_mode": "direct"}); err != nil {
		t.Fatalf("direct behind a short token: %v", err)
	}
	// A token at the line: the relay is chosen.
	a.dashboard.SetAccessToken(true, "sixteen-chars-xy")
	if _, err := a.applyConfigChange(choose); err != nil {
		t.Fatalf("a relay behind a sixteen-character token: %v", err)
	}
	if st := a.configState(); st.Dashboard.TokenShort {
		t.Fatal("the card is told a sixteen-character token is short")
	}
}

func TestTheDirectAddressesAreSetThroughTheConfigDoor(t *testing.T) {
	restore := publicname.InterfaceAddrsForTest(func() ([]net.Addr, error) {
		var out []net.Addr
		for _, c := range []string{"10.8.0.2/24", "192.168.1.5/24"} {
			ip, n, err := net.ParseCIDR(c)
			if err != nil {
				t.Fatal(err)
			}
			n.IP = ip
			out = append(out, n)
		}
		return out, nil
	})
	t.Cleanup(restore)
	a := routeApp(t, true, serviceOffersRelays)
	a.cfgMu.Lock()
	a.cfg.Dashboard.Host = "0.0.0.0" // every interface: automatic cannot choose between the two
	a.cfgMu.Unlock()
	held := func() *[]string {
		t.Helper()
		onDisk, err := LoadConfig(a.configSnapshot().SourcePath)
		if err != nil {
			t.Fatal(err)
		}
		live := a.configSnapshot().Certificate.DirectAddresses
		if !reflect.DeepEqual(onDisk.Certificate.DirectAddresses, live) {
			t.Fatalf("config.json holds %v and the running configuration %v", onDisk.Certificate.DirectAddresses, live)
		}
		return live
	}
	set := func(v any) map[string]any { return map[string]any{"certificate.direct_addresses": v} }

	// A list: published as the whole route, and the route owner looks again.
	asked := a.pn.RouteRevisionForTest()
	st, err := a.applyConfigChange(set([]any{"10.8.0.2"}))
	if err != nil {
		t.Fatalf("one address, named: %v", err)
	}
	if got := held(); got == nil || !reflect.DeepEqual(*got, []string{"10.8.0.2"}) {
		t.Fatalf("the list was not stored: %v", got)
	}
	if a.pn.RouteRevisionForTest() == asked {
		t.Fatal("an address-only change did not wake the route owner: it would wait for its idle pass")
	}
	if st.PublicName.DirectAddresses == nil || !reflect.DeepEqual(*st.PublicName.DirectAddresses, []string{"10.8.0.2"}) || len(st.PublicName.MachineAddresses) != 2 {
		t.Fatalf("the card's readback: %+v, machine %v", st.PublicName.DirectAddresses, st.PublicName.MachineAddresses)
	}
	// Omitted: another key's change leaves the override as it is.
	if _, err := a.applyConfigChange(map[string]any{"certificate.route_mode": "direct"}); err != nil {
		t.Fatal(err)
	}
	if got := held(); got == nil || len(*got) != 1 {
		t.Fatalf("a change that did not name the addresses changed them: %v", got)
	}
	unchanged := func(change map[string]any, why string, wants ...string) {
		t.Helper()
		before, _ := os.ReadFile(a.configSnapshot().SourcePath)
		_, err := a.applyConfigChange(change)
		if err == nil {
			t.Fatalf("%s was accepted", why)
		}
		for _, want := range wants {
			if !strings.Contains(err.Error(), want) {
				t.Fatalf("%s: the refusal does not say %q: %v", why, want, err)
			}
		}
		if after, _ := os.ReadFile(a.configSnapshot().SourcePath); !bytes.Equal(before, after) {
			t.Fatalf("%s changed config.json", why)
		}
	}
	unchanged(set([]any{"vpn.example.test"}), "a name where an address belongs", "not an IP address")
	unchanged(set("10.8.0.2"), "a string where a list belongs", "certificate.direct_addresses")
	unchanged(set([]any{7}), "a number in the list", "certificate.direct_addresses")
	// null on this machine, the route direct: automatic cannot choose, and
	// the working route is not thrown away.
	unchanged(set(nil), "removing the override where automatic cannot choose", "10.8.0.2", "192.168.1.5")
	// An empty list keeps its own meaning and is taken as given.
	if _, err := a.applyConfigChange(set([]any{})); err != nil {
		t.Fatalf("an intentionally empty list: %v", err)
	}
	if got := held(); got == nil || len(*got) != 0 {
		t.Fatalf("an empty list was stored as no override: %v", got)
	}

	// WHILE A RELAY CARRIES THE NAME nothing is published from the
	// addresses: a bad one is still refused for what it is, and removing
	// the override is taken.
	if _, err := a.applyConfigChange(map[string]any{"certificate.route_mode": "relay", "certificate.relay_endpoint": "relay.example.test"}); err != nil {
		t.Fatal(err)
	}
	unchanged(set([]any{"10.8.0.2", "not-an-address"}), "a bad address while a relay carries the name", "not an IP address")
	if _, err := a.applyConfigChange(set(nil)); err != nil {
		t.Fatalf("removing the override while a relay carries the name: %v", err)
	}
	if got := held(); got != nil {
		t.Fatalf("null did not remove the override: %v", *got)
	}
}

func TestTheAccessCodesLifeIsSetAtTheDoor(t *testing.T) {
	a := routeApp(t, true, serviceOffersRelays)
	if got := a.configState().Dashboard.AccessCodeSeconds; got != dashboard.DefaultAccessCodeSeconds {
		t.Fatalf("unset, a code lives %d seconds, want the default", got)
	}
	st, err := a.applyConfigChange(map[string]any{"dashboard.access_code_seconds": float64(300)})
	if err != nil {
		t.Fatal(err)
	}
	if st.Dashboard.AccessCodeSeconds != 300 || len(st.RestartRequired) != 0 {
		t.Fatalf("five minutes: the card reads %d, restart-required %v", st.Dashboard.AccessCodeSeconds, st.RestartRequired)
	}
	if onDisk, err := LoadConfig(a.configSnapshot().SourcePath); err != nil || onDisk.Dashboard.AccessCodeSeconds != 300 {
		t.Fatalf("config.json does not hold it: %v", err)
	}
	for _, bad := range []float64{10, 9999, -1} {
		if _, err := a.applyConfigChange(map[string]any{"dashboard.access_code_seconds": bad}); err == nil || !strings.Contains(err.Error(), "dashboard.access_code_seconds") {
			t.Fatalf("a life of %v seconds was taken: %v", bad, err)
		}
	}
	if st, err = a.applyConfigChange(map[string]any{"dashboard.access_code_seconds": float64(0)}); err != nil || st.Dashboard.AccessCodeSeconds != dashboard.DefaultAccessCodeSeconds {
		t.Fatalf("zero takes the default: %v, %d", err, st.Dashboard.AccessCodeSeconds)
	}
}

func TestPreparationIsSetAtTheDoor(t *testing.T) {
	// A relay is the route, and the running dashboard asks for no token: any
	// change held to the route's rules is refused here (errRelayNeedsToken).
	a := routeApp(t, false, serviceOffersRelays)
	a.cfgMu.Lock()
	a.cfg.Certificate.RouteMode, a.cfg.Certificate.RelayEndpoint = publicname.RouteRelay, "relay.example.test"
	a.cfgMu.Unlock()
	if _, err := a.applyConfigChange(map[string]any{"certificate.relay_endpoint": "far.example.test:9443"}); err == nil {
		t.Fatal("the rig does not refuse a route choice: this test would prove nothing")
	}
	asked := a.pn.RouteRevisionForTest()
	st, err := a.applyConfigChange(map[string]any{"certificate.prepare": false, "certificate.prepare_certificate": true})
	if err != nil {
		t.Fatalf("the two preparation keys were held to a route's rules, or refused: %v", err)
	}
	if len(st.RestartRequired) != 0 {
		t.Fatalf("read where they are used, and held for a restart: %v", st.RestartRequired)
	}
	onDisk, err := LoadConfig(a.configSnapshot().SourcePath)
	if err != nil {
		t.Fatal(err)
	}
	if c := onDisk.Certificate; c.Prepare == nil || *c.Prepare || !c.PrepareCertificate {
		t.Fatalf("config.json does not hold them: %+v", c)
	}
	if a.pn.RouteRevisionForTest() == asked {
		t.Fatal("the route owner was not asked to look: a certificate asked for ahead of time would wait for its idle pass")
	}
	for _, key := range []string{"certificate.prepare", "certificate.prepare_certificate"} {
		if _, err := a.applyConfigChange(map[string]any{key: "yes"}); err == nil || !strings.Contains(err.Error(), key) {
			t.Fatalf("%s took a value that is not a boolean: %v", key, err)
		}
	}
}

// HOW LONG AN IDLE CARRIED CONNECTION IS KEPT IS SET AT THE DOOR, FOR THE
// NEXT START. The server that serves a relay's sessions reads its idle
// limit unlocked, so the key is held for a restart and says so; it is
// config.json's, and held to its bounds with the key named.
func TestTheCarriedIdleLimitIsSetAtTheDoorForTheNextStart(t *testing.T) {
	a := routeApp(t, true, serviceOffersRelays)
	st, err := a.applyConfigChange(map[string]any{"dashboard.carried_idle_seconds": float64(60)})
	if err != nil {
		t.Fatal(err)
	}
	held := false
	for _, key := range st.RestartRequired {
		held = held || key == "dashboard.carried_idle_seconds"
	}
	if !held {
		t.Fatalf("the limit is read when the dashboard starts and was not held for a restart: %v", st.RestartRequired)
	}
	if onDisk, err := LoadConfig(a.configSnapshot().SourcePath); err != nil || onDisk.Dashboard.CarriedIdleSeconds != 60 {
		t.Fatalf("config.json does not hold it: %v", err)
	}
	for _, bad := range []float64{1, 4, 601, -1} {
		if _, err := a.applyConfigChange(map[string]any{"dashboard.carried_idle_seconds": bad}); err == nil || !strings.Contains(err.Error(), "dashboard.carried_idle_seconds") {
			t.Fatalf("an idle limit of %v seconds was taken: %v", bad, err)
		}
	}
	if _, err := a.applyConfigChange(map[string]any{"dashboard.carried_idle_seconds": float64(0)}); err != nil {
		t.Fatalf("zero takes the default: %v", err)
	}
}

func TestChannelSessionsAreSetAtTheDoorAndApplyToTheNextSession(t *testing.T) {
	a := routeApp(t, true, serviceOffersRelays)
	asked := func() dashboard.ChannelSettings { return a.configSnapshot().Dashboard.channelSettings() }
	if set := asked(); set.Off || set.ICEServersNamed {
		t.Fatalf("with nothing set, channel sessions are off or their servers are the operator's: %+v", set)
	}
	st, err := a.applyConfigChange(map[string]any{
		"dashboard.direct_wait_ms": float64(5000), "dashboard.direct_ready_allowance_ms": float64(2000),
		"dashboard.channel_sessions": float64(4), "dashboard.channel_queue_bytes": float64(1 << 20), "dashboard.channel_stall_ms": float64(20000),
		"dashboard.ice_servers": []any{map[string]any{"urls": []any{"turn:turn.example.test:3478"}, "username": "u", "credential": "c"}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(st.RestartRequired) != 0 {
		t.Fatalf("the keys are read when a session asks for a channel and were held for a restart: %v", st.RestartRequired)
	}
	onDisk, err := LoadConfig(a.configSnapshot().SourcePath)
	if err != nil {
		t.Fatal(err)
	}
	for name, set := range map[string]dashboard.ChannelSettings{"config.json": onDisk.Dashboard.channelSettings(), "the next session": asked()} {
		if set.Off || set.WaitMS != 5000 || set.ReadyAllowanceMS != 2000 || set.Sessions != 4 || set.QueueBytes != 1<<20 || set.StallMS != 20000 ||
			!set.ICEServersNamed || len(set.ICEServers) != 1 || set.ICEServers[0].URLs[0] != "turn:turn.example.test:3478" || set.ICEServers[0].Username != "u" || set.ICEServers[0].Credential != "c" {
			t.Fatalf("%s does not hold what was set: %+v", name, set)
		}
	}
	for key, bad := range map[string]float64{"dashboard.direct_wait_ms": 100, "dashboard.direct_ready_allowance_ms": 20000,
		"dashboard.channel_sessions": 1000, "dashboard.channel_queue_bytes": 1024, "dashboard.channel_stall_ms": 500} {
		if _, err := a.applyConfigChange(map[string]any{key: bad}); err == nil || !strings.Contains(err.Error(), key) {
			t.Fatalf("%s = %v was taken, or refused without naming it: %v", key, bad, err)
		}
	}
	for _, bad := range []any{
		"turn:turn.example.test:3478",
		[]any{map[string]any{"urls": []any{}}},
		[]any{map[string]any{"urls": []any{"https://turn.example.test"}}},
		[]any{map[string]any{"urls": []any{"turn:u:p@turn.example.test:3478"}}},
	} {
		if _, err := a.applyConfigChange(map[string]any{"dashboard.ice_servers": bad}); err == nil || !strings.Contains(err.Error(), "dashboard.ice_servers") {
			t.Fatalf("servers given as %v were taken, or refused without naming the key: %v", bad, err)
		}
	}
	if _, err := a.applyConfigChange(map[string]any{"dashboard.channels": "no"}); err == nil || !strings.Contains(err.Error(), "dashboard.channels") {
		t.Fatalf("dashboard.channels took a value that is not a boolean: %v", err)
	}
	if set := asked(); set.Off || set.WaitMS != 5000 || len(set.ICEServers) != 1 {
		t.Fatalf("a refused change reached the next session: %+v", set)
	}
	// The empty list is none, and is the operator's answer; null hands the
	// choice back to the relay.
	if _, err := a.applyConfigChange(map[string]any{"dashboard.ice_servers": []any{}}); err != nil {
		t.Fatal(err)
	}
	if set := asked(); !set.ICEServersNamed || len(set.ICEServers) != 0 {
		t.Fatalf("an empty list of servers is not the operator's answer of none: %+v", set)
	}
	if _, err := a.applyConfigChange(map[string]any{"dashboard.ice_servers": nil}); err != nil {
		t.Fatal(err)
	}
	if set := asked(); set.ICEServersNamed {
		t.Fatalf("null did not hand the servers back to the relay: %+v", set)
	}
	if _, err := a.applyConfigChange(map[string]any{"dashboard.channels": false, "dashboard.direct_wait_ms": float64(0)}); err != nil {
		t.Fatal(err)
	}
	if set := asked(); !set.Off || set.WaitMS != 0 {
		t.Fatalf("channel sessions were turned off, and the wait given back to its default, and the next session is told %+v", set)
	}
}

func TestTheBoundOnSlowedRefusalsIsSetAtTheDoor(t *testing.T) {
	a := routeApp(t, true, serviceOffersRelays)
	st, err := a.applyConfigChange(map[string]any{"dashboard.refused_pauses": float64(8)})
	if err != nil {
		t.Fatal(err)
	}
	if len(st.RestartRequired) != 0 {
		t.Fatalf("the key is read at each refusal and was held for a restart: %v", st.RestartRequired)
	}
	onDisk, err := LoadConfig(a.configSnapshot().SourcePath)
	if err != nil {
		t.Fatal(err)
	}
	if onDisk.Dashboard.RefusedPauses != 8 || a.configSnapshot().Dashboard.RefusedPauses != 8 {
		t.Fatalf("set to 8: config.json holds %d and the dashboard would be told %d", onDisk.Dashboard.RefusedPauses, a.configSnapshot().Dashboard.RefusedPauses)
	}
	for _, bad := range []any{float64(-1), float64(dashboard.MaxRefusedPauses + 1), "many", 2.5} {
		if _, err := a.applyConfigChange(map[string]any{"dashboard.refused_pauses": bad}); err == nil || !strings.Contains(err.Error(), "dashboard.refused_pauses") {
			t.Fatalf("%v was taken, or refused without naming the key: %v", bad, err)
		}
	}
	if got := a.configSnapshot().Dashboard.RefusedPauses; got != 8 {
		t.Fatalf("a refused change reached the dashboard: %d", got)
	}
	if _, err := a.applyConfigChange(map[string]any{"dashboard.refused_pauses": float64(0)}); err != nil {
		t.Fatalf("zero takes the default: %v", err)
	}
}

func TestTheUnsignedRateIsSetAtTheDoor(t *testing.T) {
	a := routeApp(t, true, serviceOffersRelays)
	told := func() (int, int) {
		d := a.configSnapshot().Dashboard
		return d.UnsignedRequestsPerSecond, d.UnsignedRequestBurst
	}
	st, err := a.applyConfigChange(map[string]any{"dashboard.unsigned_requests_per_second": float64(5), "dashboard.unsigned_request_burst": float64(40)})
	if err != nil {
		t.Fatal(err)
	}
	if len(st.RestartRequired) != 0 {
		t.Fatalf("the keys are read at each request and were held for a restart: %v", st.RestartRequired)
	}
	onDisk, err := LoadConfig(a.configSnapshot().SourcePath)
	if err != nil {
		t.Fatal(err)
	}
	if rate, burst := told(); rate != 5 || burst != 40 || onDisk.Dashboard.UnsignedRequestsPerSecond != 5 || onDisk.Dashboard.UnsignedRequestBurst != 40 {
		t.Fatalf("set to 5 a second in bursts of 40: the dashboard would be told %d and %d, and config.json holds %d and %d", rate, burst, onDisk.Dashboard.UnsignedRequestsPerSecond, onDisk.Dashboard.UnsignedRequestBurst)
	}
	for key, bad := range map[string]any{"dashboard.unsigned_requests_per_second": float64(dashboard.MaxUnsignedRequestsPerSecond + 1), "dashboard.unsigned_request_burst": float64(-3)} {
		if _, err := a.applyConfigChange(map[string]any{key: bad}); err == nil || !strings.Contains(err.Error(), key) {
			t.Fatalf("%s = %v was taken, or refused without naming the key: %v", key, bad, err)
		}
	}
	if _, err := a.applyConfigChange(map[string]any{"dashboard.unsigned_request_burst": "many"}); err == nil {
		t.Fatal("a burst that is not a number was taken")
	}
	if rate, burst := told(); rate != 5 || burst != 40 {
		t.Fatalf("a refused change reached the dashboard: %d a second in bursts of %d", rate, burst)
	}
	if _, err := a.applyConfigChange(map[string]any{"dashboard.unsigned_requests_per_second": float64(0), "dashboard.unsigned_request_burst": float64(0)}); err != nil {
		t.Fatalf("zero takes the default: %v", err)
	}
}

func TestTheCarryBoundsAreSetAtTheDoor(t *testing.T) {
	a := routeApp(t, true, serviceOffersRelays)
	st, err := a.applyConfigChange(map[string]any{
		"certificate.relay_carried_max": float64(16), "certificate.relay_attaches_per_second": float64(2), "certificate.relay_attach_burst": float64(8)})
	if err != nil {
		t.Fatal(err)
	}
	if len(st.RestartRequired) != 0 {
		t.Fatalf("the keys are read at each request and were held for a restart: %v", st.RestartRequired)
	}
	onDisk, err := LoadConfig(a.configSnapshot().SourcePath)
	if err != nil {
		t.Fatal(err)
	}
	if c := onDisk.Certificate; c.RelayCarriedMax != 16 || c.RelayAttachesPerSecond != 2 || c.RelayAttachBurst != 8 {
		t.Fatalf("config.json does not hold them: %+v", c)
	}
	if tune, held := a.configSnapshot().Certificate.RelayTuning(); tune.Carried != 16 || tune.AttachesPerSecond != 2 || tune.AttachBurst != 8 || held != nil {
		t.Fatalf("the relay client would be told %+v (held %v)", tune, held)
	}
	for key, bad := range map[string]float64{"certificate.relay_carried_max": 257, "certificate.relay_attaches_per_second": 101, "certificate.relay_attach_burst": -1} {
		var out *publicname.RelayTuningOutOfBounds
		if _, err := a.applyConfigChange(map[string]any{key: bad}); !errors.As(err, &out) || out.Key != key {
			t.Fatalf("%s = %v was taken, or refused without naming it: %v", key, bad, err)
		}
	}
	if tune, _ := a.configSnapshot().Certificate.RelayTuning(); tune.Carried != 16 || tune.AttachesPerSecond != 2 || tune.AttachBurst != 8 {
		t.Fatalf("a refused change reached the relay client: %+v", tune)
	}
	if _, err := a.applyConfigChange(map[string]any{"certificate.relay_carried_max": float64(0)}); err != nil {
		t.Fatalf("zero takes the default: %v", err)
	}
}

// THE RELAY CONNECTION'S FIGURES ARE SET AT THE DOOR AND APPLY AT ONCE. Its
// ping interval, its two reconnect waits and its wait for an answer were
// constants. Each is a key now: config.json's, held to its bounds with the
// key named, never held for a restart, and the two waits held to each other
// whichever of them a change sets.
func TestTheRelayConnectionsFiguresAreSetAtTheDoor(t *testing.T) {
	a := routeApp(t, true, serviceOffersRelays)
	st, err := a.applyConfigChange(map[string]any{
		"certificate.relay_ping_seconds": float64(45), "certificate.relay_backoff_base_ms": float64(500),
		"certificate.relay_backoff_max_ms": float64(120000), "certificate.relay_answer_seconds": float64(30)})
	if err != nil {
		t.Fatal(err)
	}
	if len(st.RestartRequired) != 0 {
		t.Fatalf("the figures are read at each wait and were held for a restart: %v", st.RestartRequired)
	}
	onDisk, err := LoadConfig(a.configSnapshot().SourcePath)
	if err != nil {
		t.Fatal(err)
	}
	if c := onDisk.Certificate; c.RelayPingSeconds != 45 || c.RelayBackoffBaseMS != 500 || c.RelayBackoffMaxMS != 120000 || c.RelayAnswerSeconds != 30 {
		t.Fatalf("config.json does not hold them: %+v", c)
	}
	if tune, held := a.configSnapshot().Certificate.RelayTuning(); tune.PingSeconds != 45 || tune.BackoffBase != 500*time.Millisecond || tune.BackoffMax != 2*time.Minute || tune.Answer != 30*time.Second || held != nil {
		t.Fatalf("the relay client would be told %+v (held %v)", tune, held)
	}
	for key, bad := range map[string]float64{"certificate.relay_ping_seconds": 5, "certificate.relay_backoff_base_ms": 50, "certificate.relay_backoff_max_ms": 4e6, "certificate.relay_answer_seconds": 1} {
		var out *publicname.RelayTuningOutOfBounds
		if _, err := a.applyConfigChange(map[string]any{key: bad}); !errors.As(err, &out) || out.Key != key {
			t.Fatalf("%s = %v was taken, or refused without naming it: %v", key, bad, err)
		}
	}
	if _, err := a.applyConfigChange(map[string]any{"certificate.relay_ping_seconds": 1.5}); err == nil {
		t.Fatal("a ping interval of a second and a half was taken")
	}
	// The first wait is never longer than the longest, whichever is set:
	// against the other as it stands, and against the other's default.
	var out *publicname.RelayTuningOutOfBounds
	if _, err := a.applyConfigChange(map[string]any{"certificate.relay_backoff_max_ms": float64(400)}); !errors.As(err, &out) || !out.BaseOverMax {
		t.Fatalf("a longest wait under the first wait was taken: %v", err)
	}
	if _, err := a.applyConfigChange(map[string]any{"certificate.relay_backoff_base_ms": float64(0), "certificate.relay_backoff_max_ms": float64(400)}); !errors.As(err, &out) || !out.BaseOverMax {
		t.Fatalf("a longest wait under the default first wait was taken: %v", err)
	}
	if onDisk, err = LoadConfig(a.configSnapshot().SourcePath); err != nil || onDisk.Certificate.RelayBackoffMaxMS != 120000 || onDisk.Certificate.RelayBackoffBaseMS != 500 {
		t.Fatalf("a refused change reached config.json: %+v, %v", onDisk.Certificate, err)
	}
	if _, err := a.applyConfigChange(map[string]any{"certificate.relay_ping_seconds": float64(0), "certificate.relay_backoff_base_ms": float64(0), "certificate.relay_backoff_max_ms": float64(0), "certificate.relay_answer_seconds": float64(0)}); err != nil {
		t.Fatalf("zero takes each default: %v", err)
	}
	if tune, _ := a.configSnapshot().Certificate.RelayTuning(); tune.PingSeconds != 0 || tune.BackoffBase != 0 || tune.BackoffMax != 0 || tune.Answer != 0 {
		t.Fatalf("back at the defaults, the client is still told %+v", tune)
	}
}

// THE RELAY ROUTE IS CHOSEN THROUGH THE CONFIG DOOR, from what discovery
// advertises, and only while the running dashboard asks for its token. The
// accepted choice is config.json's and the readback's; every refusal names
// its requirement and leaves config.json as it was; the way back to direct
// is always open.
func TestTheRelayRouteIsChosenThroughTheConfigDoor(t *testing.T) {
	choose := map[string]any{"certificate.route_mode": "relay", "certificate.relay_endpoint": "relay.example.test"}

	a := routeApp(t, true, serviceOffersRelays)
	revision := a.pn.RouteRevisionForTest
	asked := revision()
	st, err := a.applyConfigChange(choose)
	if err != nil {
		t.Fatalf("a relay the service advertises, on the name's port, with the token required: %v", err)
	}
	if revision() == asked {
		t.Fatal("the route owner was not asked to look again: the choice would wait for its idle pass")
	}
	onDisk, err := LoadConfig(a.configSnapshot().SourcePath)
	if err != nil {
		t.Fatal(err)
	}
	if onDisk.Certificate.RouteMode != publicname.RouteRelay || onDisk.Certificate.RelayEndpoint != "relay.example.test" {
		t.Fatalf("config.json does not hold the route chosen: %+v", onDisk.Certificate)
	}
	pn := st.PublicName
	if pn.RouteMode != publicname.RouteRelay || pn.RelayEndpoint != "relay.example.test" || pn.Relays == nil || len(*pn.Relays) != 2 {
		t.Fatalf("the card's readback does not carry the route and the relays offered: %+v", pn)
	}
	if len(st.RestartRequired) != 0 {
		t.Fatalf("the route applies at once, not at restart: %v", st.RestartRequired)
	}
	// Leaving the relay stops the tunnel on the change itself, before any
	// pass reaches the network.
	stopped := false
	a.pn.CarryForTest("ui.abcdefghijklmnopqrstuvwxyz.id.example.test", func() { stopped = true }, nil)
	if _, err := a.applyConfigChange(map[string]any{"certificate.route_mode": "direct"}); err != nil {
		t.Fatalf("the way back to direct: %v", err)
	}
	if !stopped {
		t.Fatal("the relay kept carrying the name after the person chose direct")
	}

	unchanged := func(a *App, change map[string]any, why string, want error) {
		t.Helper()
		path := a.configSnapshot().SourcePath
		raw, _ := os.ReadFile(path)
		live := a.configSnapshot().Certificate
		_, err := a.applyConfigChange(change)
		if err == nil {
			t.Fatalf("%s was accepted", why)
		}
		if want != nil && !errors.Is(err, want) {
			t.Fatalf("%s: want %v, got %v", why, want, err)
		}
		if now, _ := os.ReadFile(path); !bytes.Equal(raw, now) || a.configSnapshot().Certificate != live {
			t.Fatalf("%s changed the configuration", why)
		}
	}

	open := routeApp(t, false, serviceOffersRelays)
	unchanged(open, choose, "a relay while the running dashboard asks for no token", errRelayNeedsToken)
	// The refusal points at the page's own way to the token, not a terminal.
	if _, err := open.applyConfigChange(choose); err == nil || !strings.Contains(err.Error(), "Settings → Dashboard → Ask for an access token") || strings.Contains(err.Error(), "aii dashboard-token") || strings.Contains(err.Error(), "config.json") {
		t.Fatalf("the relay refusal does not send the person to Ask for an access token: %v", err)
	}
	if _, err := open.applyConfigChange(map[string]any{"certificate.route_mode": "direct"}); err != nil {
		t.Fatalf("the way back to direct is open without a token: %v", err)
	}
	unchanged(a, map[string]any{"certificate.route_mode": "relay", "certificate.relay_endpoint": "stranger.example.test"}, "a relay the service does not advertise", nil)
	unchanged(a, map[string]any{"certificate.route_mode": "relay", "certificate.relay_endpoint": "far.example.test"}, "a relay on another port than the name's", nil)
	unchanged(a, map[string]any{"certificate.route_mode": "relay", "certificate.relay_endpoint": ""}, "a relay mode naming no relay", nil)
	unchanged(a, map[string]any{"certificate.route_mode": "sideways"}, "a mode the route owner does not know", nil)
	unchanged(routeApp(t, true, serviceUnanswered), choose, "a relay before the service has answered", nil)
	unchanged(routeApp(t, true, serviceOffersNoRelays), choose, "a relay from a service that offers none", nil)
	if st := routeApp(t, true, serviceOffersNoRelays).pn.State(); st.Relays == nil || len(*st.Relays) != 0 {
		t.Fatalf("a service that offers no relay must read as none, not as unknown: %+v", st.Relays)
	}
	if st := routeApp(t, true, serviceUnanswered).pn.State(); st.Relays != nil {
		t.Fatalf("a service that has not answered must read as unknown, not as none: %+v", *st.Relays)
	}
}

// THE PORT THE LAUNCHER OFFERS IS WRITTEN BY THE ONE CONFIG WRITER. A slot
// as `aii init` makes it takes the new port and nothing else changes that
// the loader would read differently: the identity paths stay the runtime's
// defaults, so an unborn slot stays birthable. A port out of range is
// refused and the file is untouched.
func TestSetDashboardPortWritesThroughTheOneConfigWriter(t *testing.T) {
	root := t.TempDir()
	dir, err := install.Create(root, 0)
	if err != nil {
		t.Fatal(err)
	}
	path := install.ConfigPathIn(dir)
	before, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := SetDashboardPort(path, 8199); err != nil {
		t.Fatal(err)
	}
	after, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if after.Dashboard.Port != 8199 || install.ConfiguredPort(dir, 0) != 8199 {
		t.Fatalf("the port was not written: %d", after.Dashboard.Port)
	}
	after.Dashboard.Port = before.Dashboard.Port
	if after.Identity != before.Identity || !reflect.DeepEqual(after.Dashboard, before.Dashboard) {
		t.Fatalf("writing the port changed what the loader reads besides it: %+v vs %+v", after.Identity, before.Identity)
	}
	raw, _ := os.ReadFile(path)
	for _, bad := range []int{0, -1, 65536} {
		if err := SetDashboardPort(path, bad); err == nil {
			t.Fatalf("port %d was accepted", bad)
		}
	}
	if now, _ := os.ReadFile(path); !bytes.Equal(raw, now) {
		t.Fatal("a refused port changed config.json")
	}
}

// openApp is a live-shaped app on a real loopback dashboard that asks for no
// token — carrying a dormant one in its file, as an earlier requirement
// leaves behind — with the live handler's token hooks wired as app.go wires
// them.
func openApp(t *testing.T) (*App, string) {
	t.Helper()
	cfg := defaultConfig()
	cfg.SourcePath = filepath.Join(t.TempDir(), "config.json")
	cfg.Identity.KeyPath = filepath.Join(filepath.Dir(cfg.SourcePath), "identity.sec")
	cfg.Dashboard = DashboardConfig{Host: "127.0.0.1", Port: 0, AccessToken: "dormant-token-from-before"}
	if _, err := saveConfig(cfg); err != nil {
		t.Fatal(err)
	}
	a := New(cfg)
	d, err := a.newDashboard(&dashboard.WSHandler{DashboardToken: a.dashboardToken, RotateDashboardToken: a.rotateDashboardAccess, RequireDashboardToken: a.requireDashboardAccess})
	if err != nil {
		t.Fatal(err)
	}
	a.dashboard = d
	if _, err := d.Start(""); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Shutdown(t.Context()) })
	if d.AccessTokenRequired() {
		t.Fatal("the fixture already asks for a token")
	}
	return a, a.DashboardURL()
}

func TestAskingForATokenIsOneWriteCarryingBothKeys(t *testing.T) {
	a, _ := openApp(t)
	path := a.configSnapshot().SourcePath
	var writes []DashboardConfig
	persist := func(c *Config) (bool, error) {
		writes = append(writes, c.Dashboard)
		return saveConfig(c)
	}
	if err := a.newDashboardToken(true, persist); err != nil {
		t.Fatal(err)
	}
	if len(writes) != 1 {
		t.Fatalf("asking for a token took %d config writes, not one", len(writes))
	}
	w := writes[0]
	if !w.RequireToken || !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(w.AccessToken) || w.AccessToken == "dormant-token-from-before" {
		t.Fatalf("the one write does not carry the requirement with a freshly minted token: require_token=%v token=%q", w.RequireToken, w.AccessToken)
	}
	onDisk, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if !onDisk.Dashboard.RequireToken || onDisk.Dashboard.AccessToken != w.AccessToken {
		t.Fatalf("config.json does not hold what was written: %+v", onDisk.Dashboard)
	}
	live := a.configSnapshot().Dashboard
	if !a.dashboard.AccessTokenRequired() || a.dashboardToken() != w.AccessToken || !live.RequireToken || live.AccessToken != w.AccessToken {
		t.Fatal("the running server, the live configuration and config.json disagree")
	}

	raw, _ := os.ReadFile(path)
	if err := a.newDashboardToken(true, persist); !errors.Is(err, errTokenAlreadyRequired) {
		t.Fatalf("asked again: want the typed refusal, got %v", err)
	}
	if now, _ := os.ReadFile(path); len(writes) != 1 || !bytes.Equal(raw, now) {
		t.Fatal("asking again wrote the configuration")
	}
}

// ASKING FOR A TOKEN FROM SETTINGS KEEPS THE BROWSER THAT ASKED. Through the
// real route and the live hook: the asking browser is answered with its
// cookie and stays signed in; another browser, admitted a moment earlier with
// nothing asked, is asked for the token from then on.
func TestAskingForATokenFromSettingsKeepsTheBrowserThatAsked(t *testing.T) {
	a, base := openApp(t)
	jar, _ := cookiejar.New(nil)
	asker := &http.Client{Jar: jar, Timeout: 5 * time.Second, Transport: &http.Transport{DisableKeepAlives: true}}
	other := &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{DisableKeepAlives: true}}
	if got := statusOf(t, other, base+"auth/token"); got != http.StatusNoContent {
		t.Fatalf("the open dashboard asked for a token already: %d", got)
	}
	req, _ := http.NewRequest(http.MethodPost, base+"auth/require", nil)
	req.Header.Set("Origin", strings.TrimSuffix(base, "/"))
	r, err := asker.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	r.Body.Close()
	if r.StatusCode != http.StatusNoContent {
		t.Fatalf("asking for a token over this machine's own connection: %d", r.StatusCode)
	}
	if got := statusOf(t, asker, base+"auth/token"); got != http.StatusNoContent {
		t.Fatalf("the browser that asked was signed out: %d", got)
	}
	if got := statusOf(t, other, base+"auth/token"); got != http.StatusUnauthorized {
		t.Fatalf("another browser is not asked for the token: %d", got)
	}
	onDisk, err := LoadConfig(a.configSnapshot().SourcePath)
	if err != nil {
		t.Fatal(err)
	}
	if !onDisk.Dashboard.RequireToken || onDisk.Dashboard.AccessToken != a.dashboardToken() || signedIn(t, base, onDisk.Dashboard.AccessToken) == nil {
		t.Fatal("config.json and the running server do not agree on the token asked for")
	}
}

// THE CONFIG DOOR STILL REFUSES THE REQUIREMENT, IN BOTH DIRECTIONS, AND THE
// TOKEN. Settings turns the requirement on only through its own act, which
// mints the token in the same write; nothing on the page turns it off.
func TestTheConfigDoorStillRefusesTheRequirement(t *testing.T) {
	for _, required := range []bool{false, true} {
		a := routeApp(t, required, serviceOffersRelays)
		path := a.configSnapshot().SourcePath
		raw, _ := os.ReadFile(path)
		live := a.configSnapshot().Dashboard
		for _, change := range []map[string]any{
			{"dashboard.require_token": true},
			{"dashboard.require_token": false},
			{"dashboard.access_token": "chosen-by-a-page"},
		} {
			_, err := a.applyConfigChange(change)
			if err == nil || !strings.Contains(err.Error(), "not an operator-settable field") {
				t.Fatalf("the config door took %v: %v", change, err)
			}
		}
		if now, _ := os.ReadFile(path); !bytes.Equal(raw, now) || !reflect.DeepEqual(a.configSnapshot().Dashboard, live) {
			t.Fatal("a refused change to the requirement changed the configuration")
		}
	}
}

// SESSION SERVERS WRITTEN BY HAND ARE HELD WHEN THE FILE IS READ, by the
// check the config door applies. The list goes to the library and to the
// page as written, and the door is not the only way into the file: so a
// list that is not STUN or TURN addresses, that names no address, or that
// carries a credential inside an address is refused by its key at a start
// and at a reload, and a good list, the empty list and no list are read.
func TestSessionServersWrittenByHandAreHeldWhenTheFileIsRead(t *testing.T) {
	read := func(servers string) (*Config, error) {
		t.Helper()
		path := filepath.Join(t.TempDir(), "config.json")
		body := `{"dashboard":{` + servers + `}}`
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return ReadConfig(path)
	}
	for _, bad := range []string{
		`"ice_servers":[{"urls":["https://turn.example.test"]}]`,
		`"ice_servers":[{"urls":[]}]`,
		`"ice_servers":[{"urls":["turn:u:p@turn.example.test:3478"]}]`,
		`"ice_servers":[{"urls":["stun:stun.example.test:3478"]},{"urls":["turn.example.test:3478"]}]`,
	} {
		if cfg, err := read(bad); err == nil || cfg != nil || !strings.HasPrefix(err.Error(), "dashboard.ice_servers:") {
			t.Fatalf("a file with %s was read, or refused without naming the key first: %v", bad, err)
		}
	}
	good, err := read(`"ice_servers":[{"urls":["stun:stun.example.test:3478","turns:turn.example.test:5349"],"username":"u","credential":"c"}]`)
	if err != nil {
		t.Fatalf("a list of STUN and TURN addresses was refused: %v", err)
	}
	if set := good.Dashboard.channelSettings(); !set.ICEServersNamed || len(set.ICEServers) != 1 || len(set.ICEServers[0].URLs) != 2 {
		t.Fatalf("the list read is not the list written: %+v", set)
	}
	if none, err := read(`"ice_servers":[]`); err != nil || !none.Dashboard.channelSettings().ICEServersNamed {
		t.Fatalf("the empty list, the operator's answer of none, was not read as theirs: %v", err)
	}
	if unset, err := read(`"tls":false`); err != nil || unset.Dashboard.channelSettings().ICEServersNamed {
		t.Fatalf("a file that names no servers was refused, or read as naming them: %v", err)
	}
}
