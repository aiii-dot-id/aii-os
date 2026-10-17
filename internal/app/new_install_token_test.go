package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	configdir "github.com/aiii-dot-id/aii-os/config"
	"github.com/aiii-dot-id/aii-os/internal/dashboard"
	"github.com/aiii-dot-id/aii-os/internal/install"
)

func TestANewInstallRequiresTheTokenAndAnUpgradeKeepsItsSetting(t *testing.T) {
	start := func(t *testing.T, path string) (*Config, *dashboard.Server) {
		t.Helper()
		cfg, err := ReadConfig(path)
		if err != nil {
			t.Fatal(err)
		}
		cfg.Identity.KeyPath = filepath.Join(filepath.Dir(path), "identity.key")
		a := New(cfg)
		d, err := a.newDashboard(&dashboard.WSHandler{})
		if err != nil {
			t.Fatal(err)
		}
		return cfg, d
	}

	t.Run("a new install", func(t *testing.T) {
		slot, err := install.Create(t.TempDir(), 0)
		if err != nil {
			t.Fatal(err)
		}
		cfg, d := start(t, install.ConfigPathIn(slot))
		if !cfg.Dashboard.RequireToken || !d.AccessTokenRequired() {
			t.Fatalf("a new install's dashboard does not ask for its access token: require_token=%v required=%v", cfg.Dashboard.RequireToken, d.AccessTokenRequired())
		}
		if len(cfg.Dashboard.AccessToken) != 64 {
			t.Fatalf("no token was minted at the new install's first start: %q", cfg.Dashboard.AccessToken)
		}
		if again, err := ReadConfig(install.ConfigPathIn(slot)); err != nil || again.Dashboard.AccessToken != cfg.Dashboard.AccessToken {
			t.Fatalf("the minted token is not kept in config.json: %v", err)
		}
	})

	// The shape an earlier version's aii init wrote: the template as it
	// stands, with the slot's port, and no require_token.
	earlier := func(t *testing.T, set func(dash map[string]any)) string {
		t.Helper()
		var m map[string]any
		if err := json.Unmarshal(configdir.Config, &m); err != nil {
			t.Fatal(err)
		}
		delete(m, "identity")
		dash := m["dashboard"].(map[string]any)
		dash["port"] = 8180
		set(dash)
		raw, err := json.MarshalIndent(m, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(t.TempDir(), "config", "config.json")
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, raw, 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	for _, tc := range []struct {
		name string
		set  func(dash map[string]any)
		want bool
	}{
		{"upgraded, the key never written", func(map[string]any) {}, false},
		{"upgraded, false in so many words", func(d map[string]any) { d["require_token"] = false }, false},
		{"upgraded, turned on in Settings", func(d map[string]any) { d["require_token"] = true }, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := earlier(t, tc.set)
			cfg, d := start(t, path)
			if cfg.Dashboard.RequireToken != tc.want || d.AccessTokenRequired() != tc.want {
				t.Fatalf("an upgrade changed what the dashboard asks for: require_token=%v required=%v, want %v", cfg.Dashboard.RequireToken, d.AccessTokenRequired(), tc.want)
			}
			if !tc.want && cfg.Dashboard.AccessToken != "" {
				t.Fatal("an upgrade minted a token for a dashboard that asks for none")
			}
			if _, err := saveConfig(cfg); err != nil {
				t.Fatal(err)
			}
			if saved, err := ReadConfig(path); err != nil || saved.Dashboard.RequireToken != tc.want {
				t.Fatalf("a save wrote the setting back changed: %v %v", saved.Dashboard.RequireToken, err)
			}
		})
	}
}
