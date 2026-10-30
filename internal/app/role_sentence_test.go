package app

import (
	"encoding/json"
	"maps"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/dashboard"
	"github.com/aiii-dot-id/aii-os/internal/llm"
	"github.com/aiii-dot-id/aii-os/internal/tools"
)

func TestTheRoleSentenceNamesTheOperatorsRoles(t *testing.T) {
	const unrouted = "(spawn) named inference route in agency.roles; it grants no authority, and an unrouted name falls back to your active model"
	const twoRoles = `(spawn, optional) named inference route in agency.roles. Your operator has configured: "critic", Reads a draft and lists its faults. No rewriting; "judge", Compares two answers and picks one, with the reason. An unrouted name falls back to your active model.`

	a := &App{toolReg: tools.NewRegistry(t.TempDir(), nil, tools.Timeouts{})}
	none := a.buildToolDefinitions()
	if got := roleSentence(t, none); got != unrouted {
		t.Fatalf("with no role configured the role sentence changed:\n got %s\nwant %s", got, unrouted)
	}

	// Written in config.json as an operator writes it: "judge" first, and
	// a description over two lines.
	var cfg Config
	if err := json.Unmarshal([]byte(`{"agency": {"roles": {
		"judge":  {"provider": "local", "model": "m2", "description": "Compares two answers and picks one, with the reason."},
		"critic": {"provider": "cloud", "model": "m1", "description": "Reads a draft and lists its faults.\nNo rewriting."}
	}}}`), &cfg); err != nil {
		t.Fatal(err)
	}
	a.cfg = &cfg
	two := a.buildToolDefinitions()
	if got := roleSentence(t, two); got != twoRoles {
		t.Fatalf("with two roles the role sentence is not the golden:\n got %s\nwant %s", got, twoRoles)
	}

	// Only that sentence differs: every other tool, and every other
	// parameter of work, is the list a turn with no role is sent.
	if len(two) != len(none) {
		t.Fatalf("two roles changed the tool count: %d, %d", len(two), len(none))
	}
	for i := range none {
		if !reflect.DeepEqual(withoutRole(none[i]), withoutRole(two[i])) {
			t.Errorf("the roles changed %s beyond its role sentence", none[i].Function.Name)
		}
	}
	// Same roles, same bytes, whatever order the map gives them in.
	for range 5 {
		if got := roleSentence(t, a.buildToolDefinitions()); got != twoRoles {
			t.Fatalf("the same roles wrote another sentence: %s", got)
		}
	}
}

// roleSentence is the work tool's role= description among defs.
func roleSentence(t *testing.T, defs []llm.ToolDefinition) string {
	t.Helper()
	for _, d := range defs {
		if d.Function.Name == "work" {
			return d.Function.Parameters["properties"].(map[string]any)["role"].(map[string]any)["description"].(string)
		}
	}
	t.Fatal("no work tool")
	return ""
}

// withoutRole is a tool definition with the work tool's role parameter
// taken out.
func withoutRole(d llm.ToolDefinition) llm.ToolDefinition {
	if d.Function.Name != "work" {
		return d
	}
	params := maps.Clone(d.Function.Parameters)
	props := maps.Clone(params["properties"].(map[string]any))
	delete(props, "role")
	params["properties"] = props
	d.Function.Parameters = params
	return d
}

// THE AGENCY CARD READS THE ROLES AS THE IDENTITY IS TOLD THEM:
// the configuration's readback carries each role, sorted by name as the
// work tool names them, with its provider entry, model and description.
func TestTheConfigReadbackListsTheRolesSorted(t *testing.T) {
	a := newBareApp(t, &Config{SourcePath: filepath.Join(t.TempDir(), "config.json"), Agency: AgencyConfig{Roles: map[string]RoleRoute{
		"judge":  {Provider: "local", Model: "m2"},
		"critic": {Provider: "cloud", Model: "m1", Description: "reads drafts"},
	}}})
	got := a.configState().Agency.Roles
	want := []dashboard.RoleState{{Name: "critic", Provider: "cloud", Model: "m1", Description: "reads drafts"}, {Name: "judge", Provider: "local", Model: "m2"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("the readback's roles: %+v, want %+v", got, want)
	}
	a = newBareApp(t, &Config{SourcePath: filepath.Join(t.TempDir(), "config.json")})
	if roles := a.configState().Agency.Roles; roles != nil {
		t.Fatalf("no role, and the readback lists %+v", roles)
	}
}
