package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/tools"
)

func TestPluginStartupDeadlineLoadsPersistsAndReachesActivation(t *testing.T) {
	for _, tc := range []struct {
		value   string
		want    time.Duration
		invalid bool
	}{
		{"", 0, false}, {`null`, 0, false}, {`180000`, 180 * time.Second, false},
		{`0`, 0, true}, {`-1`, 0, true}, {`9223372036854775807`, 0, true}, {`1.5`, 0, true},
	} {
		t.Run("value="+tc.value, func(t *testing.T) {
			field := ""
			if tc.value != "" {
				field = `"startup_timeout_ms":` + tc.value
			}
			path := filepath.Join(t.TempDir(), "config.json")
			if err := os.WriteFile(path, []byte(`{"plugins":{"resources":{"id.aiii.voice":{`+field+`}}}}`), 0600); err != nil {
				t.Fatal(err)
			}
			cfg, err := LoadConfig(path)
			if tc.invalid {
				if err == nil {
					t.Fatal("invalid deadline accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			got, err := cfg.Plugins.Resources["id.aiii.voice"].startupTimeout()
			if err != nil || got != tc.want {
				t.Fatalf("deadline %v: %v", got, err)
			}
			cfg.Identity.LedgerPath = filepath.Join(t.TempDir(), "identity.ledger")
			a := &App{cfg: cfg}
			opts, err := a.buildPluginOptions(newAdmissionStore(t, t.TempDir()), tools.NewRegistry(t.TempDir(), nil, tools.Timeouts{}), nil)
			if err != nil {
				t.Fatal(err)
			}
			if opts.ReadyTimeout["id.aiii.voice"] != tc.want {
				t.Fatal("deadline lost before activation")
			}
			if _, err := saveConfig(cfg); err != nil {
				t.Fatal(err)
			}
			reloaded, err := LoadConfig(path)
			if err != nil {
				t.Fatal(err)
			}
			before, _ := json.Marshal(cfg.Plugins.Resources)
			after, _ := json.Marshal(reloaded.Plugins.Resources)
			if string(before) != string(after) {
				t.Fatal("deadline changed on restart")
			}
		})
	}
}
