package app

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/tools"
)

// .
// .
// .
// .
// .
// .
func TestTheStartupCeilingIsValidatedAndReadBack(t *testing.T) {
	for _, tc := range []struct {
		value   string
		want    time.Duration
		invalid bool
	}{
		{"", 300 * time.Second, false}, {`60000`, 60 * time.Second, false},
		{`0`, 0, true}, {`-1`, 0, true}, {`9223372036854775807`, 0, true}, {`9223372036855`, 0, true}, {`1.5`, 0, true},
	} {
		t.Run("value="+tc.value, func(t *testing.T) {
			runtime := "{}"
			if tc.value != "" {
				runtime = `{"max_startup_ms":` + tc.value + `}`
			}
			path := filepath.Join(t.TempDir(), "config.json")
			if err := os.WriteFile(path, []byte(`{"plugins":{"runtime":`+runtime+`}}`), 0600); err != nil {
				t.Fatal(err)
			}
			cfg, err := LoadConfig(path)
			if tc.invalid {
				if err == nil {
					t.Fatalf("an invalid ceiling was accepted: %s", tc.value)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			cfg.Identity.LedgerPath = filepath.Join(t.TempDir(), "identity.ledger")
			a := &App{cfg: cfg}
			opts, err := a.buildPluginOptions(newAdmissionStore(t, t.TempDir()), tools.NewRegistry(t.TempDir(), nil, tools.Timeouts{}), nil)
			if err != nil {
				t.Fatal(err)
			}
			if opts.StartupCeiling != tc.want {
				t.Fatalf("the ceiling reaching activation is %v, want %v", opts.StartupCeiling, tc.want)
			}
			// .
			// .
			if got := a.pluginsState(cfg).Runtime.MaxStartupMS; got != int64(tc.want/time.Millisecond) {
				t.Fatalf("the readback says %d ms, want %d", got, int64(tc.want/time.Millisecond))
			}
		})
	}
}
