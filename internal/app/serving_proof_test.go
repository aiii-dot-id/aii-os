package app

import (
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/dashboard"
	"github.com/aiii-dot-id/aii-os/internal/install"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
)

func TestTheDashboardIsBuiltWithAKeyInTheIdentitysHomeAndProvesItselfWithIt(t *testing.T) {
	dir := t.TempDir()
	cfg := defaultConfig()
	cfg.Dashboard.Port = 0
	cfg.SourcePath = filepath.Join(dir, "config", "config.json")
	a := New(cfg)
	d, err := a.newDashboard(&dashboard.WSHandler{})
	if err != nil {
		t.Fatal(err)
	}
	bound, err := d.Start("")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Shutdown(t.Context()) })
	_, portText, _ := net.SplitHostPort(bound)
	port, _ := strconv.Atoi(portText)
	if err := install.WaitServing(port, dir, 2*time.Second); err != nil {
		t.Fatalf("the identity's dashboard does not prove itself to its home's key: %v", err)
	}
	if _, err := a.newDashboard(&dashboard.WSHandler{}); err != nil {
		t.Fatal(err)
	}
	if err := install.WaitServing(port, dir, 300*time.Millisecond); err == nil {
		t.Fatal("a dashboard built before the home's key was written again still proved itself")
	}
}

func TestTheStartWaitAndThePortSearchSpanAreSetAtTheDoor(t *testing.T) {
	a := routeApp(t, true, serviceOffersRelays)
	home := identityHomeFromConfig(a.configSnapshot().SourcePath)
	st, err := a.applyConfigChange(map[string]any{"dashboard.start_wait_seconds": float64(120), "dashboard.port_search_span": float64(20)})
	if err != nil {
		t.Fatal(err)
	}
	if len(st.RestartRequired) != 0 {
		t.Fatalf("the keys are read at each start and were held for a restart: %v", st.RestartRequired)
	}
	if got, span := install.ConfiguredStartWait(home), install.ConfiguredPortSearchSpan(home); got != 2*time.Minute || span != 20 {
		t.Fatalf("the commands that start the identity read a wait of %s and a span of %d", got, span)
	}
	read := map[string]string{}
	for _, r := range a.configState().Dashboard.Settings {
		read[r.Key] = r.Value
	}
	if read["dashboard.start_wait_seconds"] != "120 seconds" || read["dashboard.port_search_span"] != "20" {
		t.Fatalf("the card reads %q and %q", read["dashboard.start_wait_seconds"], read["dashboard.port_search_span"])
	}
	for key, bad := range map[string][]any{
		"dashboard.start_wait_seconds": {float64(-1), float64(install.MinStartWaitSeconds - 1), float64(install.MaxStartWaitSeconds + 1), "soon"},
		"dashboard.port_search_span":   {float64(-1), float64(install.MaxPortSearchSpan + 1), 2.5},
	} {
		for _, v := range bad {
			if _, err := a.applyConfigChange(map[string]any{key: v}); err == nil || !strings.Contains(err.Error(), key) {
				t.Fatalf("%s took %v, or refused it without naming the key: %v", key, v, err)
			}
		}
	}
	if _, err := a.applyConfigChange(map[string]any{"dashboard.start_wait_seconds": float64(0), "dashboard.port_search_span": float64(0)}); err != nil {
		t.Fatalf("zero takes the default: %v", err)
	}
	if got, span := install.ConfiguredStartWait(home), install.ConfiguredPortSearchSpan(home); got != time.Minute || span != install.DefaultPortSearchSpan {
		t.Fatalf("the defaults: a wait of %s and a span of %d", got, span)
	}
}

func TestEverySafePageComesUpWhenItsProofCannotBeWritten(t *testing.T) {
	for _, tc := range []struct {
		name    string
		mode    string // the strip's mode
		prepare func(t *testing.T, cfg *Config, dir string) (start func(*App) error)
	}{
		{"a record that does not pass its checks", "safe", func(t *testing.T, cfg *Config, _ string) func(*App) error {
			tamperChain(t, cfg.Identity.KeyPath, cfg.Identity.LedgerPath)
			return startLiveForTest
		}},
		{"a start that waits for Ring 5", "degraded_ring5", func(t *testing.T, cfg *Config, _ string) func(*App) error {
			holdRing5AsksOff(t)
			gate, _, _ := ring5Gate(t)
			cfg.Genesis.FirewallURL, cfg.Genesis.FetchAttempts = gate, 1
			return func(a *App) error { return a.startLive() }
		}},
		{"the page that asks which files are the identity's", "safe", func(t *testing.T, cfg *Config, dir string) func(*App) error {
			found := cfg.Identity.LedgerPath
			cfg.Identity.LedgerPath = filepath.Join(dir, "gone", "ledger.jsonl")
			choice := IdentityChoice{Home: dir, Why: "ledger_path names no record; one is at " + found, Files: []IdentityFiles{
				{Key: "ledger_path", Was: cfg.Identity.LedgerPath, Found: []string{found}},
				{Key: "db_path", Was: cfg.Identity.DBPath},
				{Key: "key_path", Was: cfg.Identity.KeyPath},
			}}
			return func(a *App) error { return a.startAtChoice(choice) }
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			logs := logsink.CaptureForTest(t)
			dir := t.TempDir()
			keyPath, ledgerPath, dbPath := birthFixture(t, dir, "Unkept")
			buildPriorProjection(t, ledgerPath, dbPath)
			cfg := safebootConfig(t, dir, "Unkept", keyPath, ledgerPath, dbPath)
			start := tc.prepare(t, cfg, dir)
			// The home the proof is written in takes no write; the record's
			// directory is left as it was.
			home := t.TempDir()
			cfg.SourcePath = filepath.Join(home, "config.json")
			if err := os.Chmod(home, 0o555); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = os.Chmod(home, 0o755) })
			if f, err := os.CreateTemp(home, "probe"); err == nil {
				f.Close()
				t.Skip("this host writes in a directory whose mode forbids it (an administrator's account, or Windows)")
			}
			a := New(cfg)
			t.Cleanup(func() { stopApp(t, a) })
			if err := start(a); err != nil {
				t.Fatalf("THE PAGE DID NOT COME UP: the start ended because its proof could not be written: %v", err)
			}
			if a.dashboard == nil || a.DashboardURL() == "" {
				t.Fatal("no page is served")
			}
			c, err := a.continuityState()
			if err != nil || c.Mode != tc.mode {
				t.Fatalf("the strip: mode=%q err=%v", c.Mode, err)
			}
			if !strings.Contains(c.ServingUnkept, "was not kept") || !strings.Contains(c.ServingUnkept, "another program holds") {
				t.Fatalf("the page does not say its proof was not kept: %q", c.ServingUnkept)
			}
			if !logs.Contains("was not kept") {
				t.Fatalf("the log does not say the proof was not kept:\n%s", logs.String())
			}
			if fileExists(install.ServingKeyPath(home)) {
				t.Fatal("a key was written in a home that takes no write")
			}
		})
	}
	home := t.TempDir()
	cfg := defaultConfig()
	cfg.Dashboard.Port = 0
	cfg.SourcePath = filepath.Join(home, "config.json")
	if err := os.Chmod(home, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(home, 0o755) })
	if f, err := os.CreateTemp(home, "probe"); err == nil {
		f.Close()
		return
	}
	if _, err := New(cfg).newDashboard(&dashboard.WSHandler{}); err == nil || !strings.Contains(err.Error(), "serving key") {
		t.Fatalf("a live dashboard was built without its proof written: %v", err)
	}
}
