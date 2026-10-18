package app

import (
	"context"
	"encoding/json"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
	"github.com/aiii-dot-id/aii-os/internal/tools"
)

func TestAReloadOnItsWayCannotPutBackAGrantTheDoorTookAway(t *testing.T) {
	const plugin = "org.example.kept"
	cfg := defaultConfig()
	cfg.SourcePath = filepath.Join(t.TempDir(), "config.json")
	cfg.Identity.LedgerPath = filepath.Join(t.TempDir(), "identity.ledger")
	if _, err := saveConfig(cfg); err != nil {
		t.Fatal(err)
	}
	a := New(cfg)
	st := newAdmissionStore(t, t.TempDir())
	opts, err := a.buildPluginOptions(st, tools.NewRegistry(t.TempDir(), nil, tools.Timeouts{}), nil)
	if err != nil {
		t.Fatal(err)
	}
	a.pluginOpts = opts
	a.live.Store(true)
	binding := opts.Broker.Bind(plugin, packagefmt.TierT1, []string{"ring4.kv"})
	defer binding.Close()
	put := func() string {
		t.Helper()
		params, _ := json.Marshal(map[string]any{"operation": "kv.put", "target": map[string]any{"key": "k"}, "arguments": map[string]any{"value": "v"}})
		reply, err := binding.Dispatch(context.Background(), "invoke-call", params)
		if err != nil {
			t.Fatal(err)
		}
		// A call that ran answers its status; one the policy refused
		// answers the refusal's reason code.
		var result struct {
			Status string `json:"status"`
			Data   struct {
				ReasonCode string `json:"reasonCode"`
			} `json:"data"`
		}
		if err := json.Unmarshal(reply, &result); err != nil || result.Status+result.Data.ReasonCode == "" {
			t.Fatalf("the broker's answer names neither a status nor a reason: %s (%v)", reply, err)
		}
		return result.Status + result.Data.ReasonCode
	}
	grant := func(on bool) {
		t.Helper()
		if _, err := a.applyConfigChange(map[string]any{"plugins.grants." + plugin + ".kv": on}); err != nil {
			t.Fatal(err)
		}
	}

	grant(true)
	if got := put(); got != "succeeded" {
		t.Fatalf("with the grant the plugin's write is %s", got)
	}

	// The reload of that save: let it publish the configuration, and hold
	// it before it hands the broker its policy.
	held, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	prev := reloadStep
	t.Cleanup(func() { reloadStep = prev })
	reloadStep = func(step string) {
		if step == "published" {
			close(held)
			<-release
		}
	}
	go func() { defer close(done); a.reloadConfig() }()
	// However this test ends, the reload is let go and joined before the
	// seam is given back.
	var once sync.Once
	letGo := func() { once.Do(func() { close(release) }); <-done }
	t.Cleanup(letGo)
	select {
	case <-held:
	case <-done:
		t.Fatal("the reload ended without publishing: nothing was held")
	case <-time.After(30 * time.Second):
		t.Fatal("the reload never reached its publication")
	}

	grant(false) // the operator takes it away; the door publishes
	if got := put(); got != "POLICY_DENY" {
		t.Fatalf("after the door took the grant away the plugin's write is %s", got)
	}
	letGo() // the older reload arrives last
	if got := put(); got != "POLICY_DENY" {
		t.Fatalf("a reload that was on its way put the grant back: the plugin's write is %s", got)
	}
	if g, ok := a.configSnapshot().Plugins.Grants[plugin]; ok {
		t.Fatalf("the configuration still grants: %+v", g)
	}
}
