package app

import (
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/identity"
	"github.com/aiii-dot-id/aii-os/internal/tools"
)

// .
// .
// .
// .
// .
func TestVerbSchemasMatchAdvertisedCapabilities(t *testing.T) {
	var work map[string]interface{}
	for _, v := range identity.Verbs() {
		if v.Name == "work" {
			work = v.Params
		}
	}
	props := work["properties"].(map[string]interface{})
	action, ok := props["action"].(map[string]interface{})
	if !ok {
		t.Fatal("work schema must carry action")
	}
	enum := action["enum"].([]string)
	found := false
	for _, a := range enum {
		if a == "spawn" {
			found = true
		}
	}
	if !found {
		t.Fatal("work action enum must advertise spawn")
	}
	if _, ok := props["goal"]; !ok {
		t.Fatal("work schema must carry goal for spawn")
	}

	for _, v := range identity.Verbs() {
		params := v.Params
		if p, _ := params["properties"].(map[string]interface{}); len(p) == 0 {
			t.Errorf("function-calling verb %q has an EMPTY schema — the model cannot use what it cannot see", v.Name)
		}
		if v.Name == "work" && !strings.Contains(v.Description, "spawn") {
			t.Error("work registry description must advertise spawn")
		}
		if v.Name == "work" &&
			(!strings.Contains(v.Description, "independent bounded work") ||
				!strings.Contains(v.Description, "tightly coupled loop")) {
			t.Error("work description must state the spawn decision boundary")
		}
	}
}

func TestRecallProviderContractStatesLiteralQuery(t *testing.T) {
	a := &App{toolReg: tools.NewRegistry(t.TempDir(), nil, tools.Timeouts{})}
	for _, def := range a.buildToolDefinitions() {
		if def.Function.Name != "recall" {
			continue
		}
		description := def.Function.Description
		if !strings.Contains(description, "every word must appear") ||
			!strings.Contains(description, "fuzzy") ||
			!strings.Contains(description, "exact=true forces the verbatim phrase") ||
			!strings.Contains(description, "separate recall calls") {
			t.Fatalf("recall provider contract does not explain how queries match: %q", description)
		}
		query := def.Function.Parameters["properties"].(map[string]interface{})["query"].(map[string]interface{})["description"].(string)
		if !strings.Contains(query, "Every word must appear") || !strings.Contains(query, "exact=true") {
			t.Fatalf("recall query parameter does not explain how it matches: %q", query)
		}
		return
	}
	t.Fatal("recall missing from provider tool definitions")
}
