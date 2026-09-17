package app

import (
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/dashboard"
)

// .
// .
// .
// .
// .
// .
func TestAReloadCannotDisarmTheTokenOnAListenerStillServingTheNetwork(t *testing.T) {
	cfg := defaultConfig()
	cfg.Dashboard.Host, cfg.Dashboard.RequireToken, cfg.Dashboard.AccessToken = "0.0.0.0", true, "old-token"
	a := New(cfg)
	a.dashboard = dashboard.New("0.0.0.0", 0, &dashboard.WSHandler{})
	a.dashboard.SetAccessToken(true, "old-token")
	a.setDashboardToken("old-token")

	future := cfg.Dashboard
	future.Host, future.RequireToken = "127.0.0.1", false
	a.rearmDashboardToken(future)
	if !a.dashboard.AccessTokenRequired() {
		t.Fatal("the token was disarmed while the listener still serves every interface")
	}
	if a.dashboardToken() != "old-token" {
		t.Fatalf("the token in force changed: %q", a.dashboardToken())
	}

	// .
	// .
	future.AccessToken = "new-token"
	a.rearmDashboardToken(future)
	if !a.dashboard.AccessTokenRequired() || a.dashboardToken() != "new-token" {
		t.Fatalf("the rotation did not land on the public listener: required=%v token=%q", a.dashboard.AccessTokenRequired(), a.dashboardToken())
	}

	// .
	// .
	// .
	local := New(cfg)
	local.dashboard = dashboard.New("127.0.0.1", 0, &dashboard.WSHandler{})
	local.dashboard.SetAccessToken(true, "old-token")
	local.setDashboardToken("old-token")
	off := cfg.Dashboard
	off.Host, off.RequireToken, off.AccessToken = "0.0.0.0", false, "old-token"
	local.rearmDashboardToken(off)
	if local.dashboard.AccessTokenRequired() || local.dashboardToken() != "" {
		t.Fatal("the operator's word to turn the token off on loopback was not honoured")
	}
}
