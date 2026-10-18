package install

import (
	"encoding/json"
	"os"
	"testing"

	configdir "github.com/aiii-dot-id/aii-os/config"
)

func TestANewSlotAsksForTheAccessTokenAndTheTemplateStaysSilent(t *testing.T) {
	dir, err := Create(t.TempDir(), 0)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(ConfigPathIn(dir))
	if err != nil {
		t.Fatal(err)
	}
	var cfg struct {
		Dashboard map[string]any `json:"dashboard"`
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatal(err)
	}
	if v, ok := cfg.Dashboard["require_token"]; !ok || v != true {
		t.Fatalf("a new slot's config must say require_token: true; dashboard = %v", cfg.Dashboard)
	}

	var template struct {
		Dashboard map[string]any `json:"dashboard"`
	}
	if err := json.Unmarshal(configdir.Config, &template); err != nil {
		t.Fatal(err)
	}
	if _, named := template.Dashboard["require_token"]; named {
		t.Fatal("the template names require_token: an existing config.json, decoded over it, would change what it asks for on an upgrade")
	}
}
