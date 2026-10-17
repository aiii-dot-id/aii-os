package app

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/dashboard"
	"github.com/aiii-dot-id/aii-os/internal/publicname"
)

// jsonKeys is every key a configuration block can state, by its own fields.
func jsonKeys(prefix string, block any) []string {
	var keys []string
	t := reflect.TypeOf(block)
	for i := 0; i < t.NumField(); i++ {
		name, _, _ := strings.Cut(t.Field(i).Tag.Get("json"), ",")
		if name != "" && name != "-" {
			keys = append(keys, prefix+"."+name)
		}
	}
	return keys
}

func TestEveryDashboardAndCertificateSettingIsReadOnTheCard(t *testing.T) {
	read := func(cfg *Config) map[string]dashboard.SettingRead {
		t.Helper()
		// The view reads the providers' registry beside the configuration
		// and writes a first one where there is none: beside this test's
		// own file, not in the package's directory.
		cfg.SourcePath = filepath.Join(t.TempDir(), "config.json")
		rows := New(cfg).configState().Dashboard.Settings
		by := map[string]dashboard.SettingRead{}
		for _, r := range rows {
			if _, twice := by[r.Key]; twice {
				t.Fatalf("%s is read twice", r.Key)
			}
			by[r.Key] = r
		}
		return by
	}
	rows := read(defaultConfig())
	keys := append(jsonKeys("dashboard", DashboardConfig{}), jsonKeys("certificate", publicname.CertificateConfig{})...)
	if len(keys) < 40 {
		t.Fatalf("only %d keys were found in the two blocks: the walk is not reading them", len(keys))
	}
	for _, key := range keys {
		if _, ok := rows[key]; !ok {
			t.Errorf("%s can be set in config.json and is read on no card", key)
		}
	}
	if len(rows) != len(keys) {
		t.Errorf("%d rows for %d keys", len(rows), len(keys))
	}

	// What is in force with nothing stated: each default, by name.
	for key, want := range map[string]string{
		"dashboard.access_code_seconds":          "120 seconds",
		"dashboard.refused_pauses":               "32",
		"dashboard.unsigned_requests_per_second": "20",
		"dashboard.unsigned_request_burst":       "100",
		"dashboard.carried_idle_seconds":         "15 seconds",
		"dashboard.channels":                     "on",
		"dashboard.direct_wait_ms":               "3000 ms",
		"dashboard.direct_ready_allowance_ms":    "1500 ms",
		"dashboard.channel_sessions":             "16",
		"dashboard.channel_queue_bytes":          "4194304 bytes",
		"dashboard.channel_stall_ms":             "10000 ms",
		"dashboard.ice_servers":                  "what the relay names",
		"dashboard.record":                       "page_rows 50, first_view_bytes 4096",
		"dashboard.sent_requests":                "kept 20, max_bytes 33554432",
		"certificate.relay_ping_seconds":         "30 seconds, or what the relay states",
		"certificate.relay_backoff_base_ms":      "1000 ms",
		"certificate.relay_backoff_max_ms":       "60000 ms",
		"certificate.relay_answer_seconds":       "15 seconds",
		"certificate.relay_carried_max":          "64",
		"certificate.relay_attaches_per_second":  "5",
		"certificate.relay_attach_burst":         "30",
		"certificate.prepare_certificate":        "off",
		"certificate.route_mode":                 "direct",
	} {
		if got := rows[key]; got.Value != want || got.Set {
			t.Errorf("%s with nothing stated is read as %q (stated: %v), want the default %q", key, got.Value, got.Set, want)
		}
	}
	if got := rows["certificate.server_url"]; !strings.HasPrefix(got.Value, "https://") || got.Set {
		t.Errorf("the certificate service in force by default is read as %+v", got)
	}

	// What config.json states is read as stated, a value outside its
	// bounds as what is used, and the two secrets never.
	const token, credential = "fixture-secret-token-0123456789", "fixture-turn-credential"
	off := false
	cfg := defaultConfig()
	cfg.Dashboard.AccessToken = token
	cfg.Dashboard.Channels = &off
	cfg.Dashboard.ChannelSessions = 3
	cfg.Dashboard.RefusedPauses = 1 << 20
	cfg.Dashboard.ICEServers = &[]dashboard.ICEServer{{URLs: []string{"turn:turn.example.test:3478"}, Username: "u", Credential: credential}}
	cfg.Certificate.RelayCarriedMax = 7
	cfg.Certificate.RelayPingSeconds = 45
	cfg.Certificate.Prepare = &off
	cfg.Certificate.ServerURL = "none"
	rows = read(cfg)
	for key, want := range map[string]string{
		"dashboard.channels":             "off",
		"dashboard.channel_sessions":     "3",
		"dashboard.refused_pauses":       "1024",
		"certificate.relay_carried_max":  "7",
		"certificate.relay_ping_seconds": "45 seconds",
		"certificate.prepare":            "off",
		"certificate.server_url":         "none: the certificate service is declined",
	} {
		if got := rows[key]; got.Value != want || !got.Set {
			t.Errorf("%s as stated is read as %q (stated: %v), want %q", key, got.Value, got.Set, want)
		}
	}
	if got := rows["dashboard.ice_servers"]; !got.Set || !strings.Contains(got.Value, "turn:turn.example.test:3478") {
		t.Errorf("the operator's session servers are read as %+v, want the address named", got)
	}
	if got := rows["dashboard.access_token"]; !got.Set || !strings.HasPrefix(got.Value, "set") {
		t.Errorf("the access token is read as %+v, want that it is set", got)
	}
	for _, r := range rows {
		if strings.Contains(r.Value, token) || strings.Contains(r.Value, credential) {
			t.Errorf("%s carries a secret to every signed-in page: %q", r.Key, r.Value)
		}
	}
}
