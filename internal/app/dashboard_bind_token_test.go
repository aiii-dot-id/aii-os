package app

import (
	"path/filepath"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/dashboard"
)

func TestOnlyALoopbackBindMayServeWithoutAToken(t *testing.T) {
	for host, loopback := range map[string]bool{
		"127.0.0.1":    true,
		"127.0.0.53":   true,
		"127.0.0.2":    true,
		" 127.0.0.1 ":  true,
		"::1":          true,
		"[::1]":        true,
		"localhost":    true,
		"LOCALHOST":    true,
		"192.0.2.6":    false, // the test identities
		"192.0.2.2":    false,
		"0.0.0.0":      false,
		"::":           false,
		"":             false, // every interface is the least loopback thing there is
		" ":            false,
		"dash.example": false, // a name this host cannot read is not one it may trust
		"192.168.1.10": false,
		"2001:db8::1":  false,
	} {
		if got := dashboard.IsLoopback(host); got != loopback {
			t.Errorf("IsLoopback(%q) = %v, want %v", host, got, loopback)
		}
	}
	if dashboardBindName("") != "every interface" {
		t.Error("an empty bind is named for what it is")
	}
	if dashboardBindName("192.0.2.6") != "192.0.2.6" {
		t.Error("a real bind is named as itself")
	}
}

// The escalation is real: a non-loopback bind comes back requiring a
// token in config.json, and a loopback bind is left exactly as
// the operator wrote it.
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

		a.cfg = &Config{SourcePath: filepath.Join(t.TempDir(), "config.json"),
			Dashboard: DashboardConfig{Host: tc.host, Port: 8181}}
		if err := a.ensureDashboardToken(); err != nil {
			t.Fatal(err)
		}
		got := a.configSnapshot().Dashboard
		if got.RequireToken != tc.wantToken {
			t.Fatalf("bind %q: require_token = %v, want %v", tc.host, got.RequireToken, tc.wantToken)
		}
		if tc.wantToken {
			if got.AccessToken == "" {
				t.Fatalf("bind %q: a required token must exist, not merely be required", tc.host)
			}
		} else if got.AccessToken != "" {
			t.Fatalf("bind %q: a loopback bind is left exactly as the operator wrote it", tc.host)
		}
	}
}
