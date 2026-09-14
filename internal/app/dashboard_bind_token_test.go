package app

import (
	"path/filepath"
	"testing"
)

// .
// .
// .
// .
// .
// .
// .
// .
func TestOnlyALoopbackBindMayServeWithoutAToken(t *testing.T) {
	for host, loopback := range map[string]bool{
		"127.0.0.1":    true,
		"127.0.0.53":   true,
		"::1":          true,
		"[::1]":        true,
		"localhost":    true,
		"LOCALHOST":    true,
		"192.0.2.6":    false,
		"192.0.2.2":    false,
		"0.0.0.0":      false,
		"::":           false,
		"":             false,
		"dash.example": false,
		"192.168.1.10": false,
		"2001:db8::1":  false,
	} {
		if got := loopbackBind(host); got != loopback {
			t.Errorf("loopbackBind(%q) = %v, want %v", host, got, loopback)
		}
	}
	if dashboardBindName("") != "every interface" {
		t.Error("an empty bind is named for what it is")
	}
	if dashboardBindName("192.0.2.6") != "192.0.2.6" {
		t.Error("a real bind is named as itself")
	}
}

// .
// .
// .
func TestANonLoopbackBindEscalatesToRequiringAToken(t *testing.T) {
	for _, tc := range []struct {
		host      string
		wantToken bool
	}{
		{"127.0.0.1", false},
		{"192.0.2.6", true},
		{"", true},
	} {
		a := &App{}
		// .
		// .
		// .
		// .
		// .
		a.cfg = &Config{SourcePath: filepath.Join(t.TempDir(), "config.json"),
			Dashboard: DashboardConfig{Host: tc.host, Port: 8181}}
		a.ensureDashboardToken()
		got := a.configSnapshot().Dashboard
		if got.RequireToken != tc.wantToken {
			t.Fatalf("bind %q: require_token = %v, want %v", tc.host, got.RequireToken, tc.wantToken)
		}
		if tc.wantToken {
			if got.AuthTokenSHA256 == "" {
				t.Fatalf("bind %q: a required token must exist, not merely be required", tc.host)
			}
			if raw := a.DashboardMintedToken(); raw == "" {
				t.Fatalf("bind %q: the raw token is handed over once so the operator can have it", tc.host)
			}
			if raw := a.DashboardMintedToken(); raw != "" {
				t.Fatalf("bind %q: and only once", tc.host)
			}
		} else if got.AuthTokenSHA256 != "" {
			t.Fatalf("bind %q: a loopback bind is left exactly as the operator wrote it", tc.host)
		}
	}
}
