package identity

import (
	"context"
	"strings"
	"testing"
)

// .
// .
// .
type fakeProjectPort struct {
	deselected bool
	listErr    error
	createArgs struct {
		name, desc string
		parent     *string
		contract   *ProjectContract
		attrs      map[string]interface{}
	}
	updateArgs struct {
		id, name, desc, focus string
		parent                *string
		attrs                 map[string]interface{}
		contract              *ProjectContract
	}
	listResult   []ProjectInfo
	lastInfo     ProjectInfo
	evidenceArgs struct {
		id, item, class, ref, note string
	}
	waiveArgs struct {
		id, item, reason string
	}
}

func (f *fakeProjectPort) List() ([]ProjectInfo, error) {
	return f.listResult, f.listErr
}
func (f *fakeProjectPort) Create(name, description string, parent *string, contract *ProjectContract, attributes map[string]interface{}) (ProjectInfo, error) {
	f.createArgs.name, f.createArgs.desc, f.createArgs.attrs = name, description, attributes
	f.createArgs.parent, f.createArgs.contract = parent, contract
	return f.lastInfo, nil
}
func (f *fakeProjectPort) Update(id, name, description, focus string, parent *string, contract *ProjectContract, attributes map[string]interface{}) (ProjectInfo, error) {
	f.updateArgs.id, f.updateArgs.name, f.updateArgs.desc, f.updateArgs.focus = id, name, description, focus
	f.updateArgs.attrs = attributes
	f.updateArgs.parent, f.updateArgs.contract = parent, contract
	return f.lastInfo, nil
}
func (f *fakeProjectPort) SetState(id, state string) (ProjectInfo, error) {
	return f.lastInfo, nil
}
func (f *fakeProjectPort) Select(id string) (ProjectInfo, error) {
	return f.lastInfo, nil
}

func (f *fakeProjectPort) Deselect() (string, error) {
	f.deselected = true
	return f.lastInfo.Name, nil
}

func (f *fakeProjectPort) RecordEvidence(id, item, class, ref, note string) (ProjectInfo, error) {
	f.evidenceArgs.id, f.evidenceArgs.item, f.evidenceArgs.class = id, item, class
	f.evidenceArgs.ref, f.evidenceArgs.note = ref, note
	return f.lastInfo, nil
}

func (f *fakeProjectPort) Waive(id, item, reason string) (ProjectInfo, error) {
	f.waiveArgs.id, f.waiveArgs.item, f.waiveArgs.reason = id, item, reason
	return f.lastInfo, nil
}

func newVerbEngine(t *testing.T) (*Engine, *fakeProjectPort) {
	t.Helper()
	engine, _, _, _, _ := setupEngine(t)
	port := &fakeProjectPort{}
	engine.SetProjects(port)
	return engine, port
}

// .
// .
// .
func TestVerbProjectCarriesAttributes(t *testing.T) {
	e, port := newVerbEngine(t)

	attrs := map[string]interface{}{"board": []interface{}{"t1", "t2"}}
	if _, err := e.verbProject(context.Background(), map[string]interface{}{
		"action":     "create",
		"name":       "board-room",
		"attributes": attrs,
	}); err != nil {
		t.Fatalf("create with attributes: %v", err)
	}
	if port.createArgs.attrs == nil {
		t.Fatal("create: the port saw nil attributes — the verb dropped the envelope")
	}
	if len(port.createArgs.attrs) != 1 || port.createArgs.attrs["board"] == nil {
		t.Fatalf("create: attributes did not survive to the port: %#v", port.createArgs.attrs)
	}

	if _, err := e.verbProject(context.Background(), map[string]interface{}{
		"action":     "update",
		"project":    "board-room",
		"attributes": map[string]interface{}{"board": []interface{}{"t1"}},
	}); err != nil {
		t.Fatalf("update with attributes: %v", err)
	}
	if port.updateArgs.attrs == nil {
		t.Fatal("update: the port saw nil attributes — the verb dropped the envelope")
	}
}

// .
// .
func TestVerbProjectAbsentAttributesMeansUntouched(t *testing.T) {
	e, port := newVerbEngine(t)

	if _, err := e.verbProject(context.Background(), map[string]interface{}{
		"action":  "update",
		"project": "p1",
		"name":    "renamed",
	}); err != nil {
		t.Fatalf("update without attributes: %v", err)
	}
	if port.updateArgs.attrs != nil {
		t.Fatalf("update without attributes: the port saw %#v — absent must arrive nil (untouched)", port.updateArgs.attrs)
	}
}

// .
// .
// .
// .
func TestVerbProjectRejectsNonMapAttributes(t *testing.T) {
	e, _ := newVerbEngine(t)

	if _, err := e.verbProject(context.Background(), map[string]interface{}{
		"action":     "create",
		"name":       "bad",
		"attributes": "not-a-map",
	}); err == nil {
		t.Fatal("create with string attributes: expected a refusal, got success")
	}
}

// .
// .
// .
// .
// .
func TestVerbProjectCarriesTheTypedContract(t *testing.T) {
	engine, port := newVerbEngine(t)

	if _, err := engine.ExecuteAction(context.Background(), "verb", "project", map[string]interface{}{
		"action":      "update",
		"project":     "alpha",
		"outcome":     "the installer works on all three platforms",
		"acceptance":  []interface{}{"a clean Windows box installs", "the dmg is stapled"},
		"constraints": []interface{}{"no unsigned binaries"},
		"parent":      "beta",
	}); err != nil {
		t.Fatal(err)
	}
	c := port.updateArgs.contract
	if c == nil {
		t.Fatal("the contract must reach the substrate, not stay in the verb")
	}
	if c.Outcome != "the installer works on all three platforms" {
		t.Fatalf("outcome: %q", c.Outcome)
	}
	if len(c.Acceptance) != 2 || len(c.Constraints) != 1 {
		t.Fatalf("acceptance/constraints did not survive: %v / %v", c.Acceptance, c.Constraints)
	}
	if port.updateArgs.parent == nil || *port.updateArgs.parent != "beta" {
		t.Fatalf("parent: %v", port.updateArgs.parent)
	}

	// .
	// .
	engine2, port2 := newVerbEngine(t)
	if _, err := engine2.ExecuteAction(context.Background(), "verb", "project", map[string]interface{}{
		"action": "update", "project": "alpha", "focus": "just the focus",
	}); err != nil {
		t.Fatal(err)
	}
	if port2.updateArgs.contract != nil {
		t.Fatal("an update that names no contract field must leave it untouched, not blank it")
	}
}

// .
// .
// .
// .
// .
// .
// .
func TestMalformedContractListsFailClosed(t *testing.T) {
	for name, bad := range map[string]interface{}{
		"a scalar where a list belongs":  "just one string",
		"a number inside the list":       []interface{}{"fine", 7},
		"an object where a list belongs": map[string]interface{}{"a": "b"},
	} {
		t.Run(name, func(t *testing.T) {
			engine, port := newVerbEngine(t)
			_, err := engine.ExecuteAction(context.Background(), "verb", "project", map[string]interface{}{
				"action": "update", "project": "alpha", "acceptance": bad,
			})
			if err == nil {
				t.Fatal("malformed acceptance must be REFUSED, not silently dropped while the turn is told the project was updated")
			}
			if !strings.Contains(err.Error(), "acceptance") {
				t.Fatalf("the refusal must name the field: %v", err)
			}
			// .
			// .
			if port.updateArgs.id != "" || port.updateArgs.contract != nil {
				t.Fatalf("a refused update must not reach the substrate: %+v", port.updateArgs)
			}
		})
	}
}

// .
// .
// .
// .
func TestTheIdentityCanClearAParent(t *testing.T) {
	engine, port := newVerbEngine(t)
	if _, err := engine.ExecuteAction(context.Background(), "verb", "project", map[string]interface{}{
		"action": "update", "project": "alpha", "parent": "",
	}); err != nil {
		t.Fatal(err)
	}
	if port.updateArgs.parent == nil {
		t.Fatal("parent=\"\" is a request to CLEAR the hierarchy — it must cross as a pointer to the empty string, not as absence")
	}
	if *port.updateArgs.parent != "" {
		t.Fatalf("parent should be cleared, got %q", *port.updateArgs.parent)
	}

	// .
	engine2, port2 := newVerbEngine(t)
	if _, err := engine2.ExecuteAction(context.Background(), "verb", "project", map[string]interface{}{
		"action": "update", "project": "alpha", "focus": "elsewhere",
	}); err != nil {
		t.Fatal(err)
	}
	if port2.updateArgs.parent != nil {
		t.Fatal("an omitted parent must leave the hierarchy untouched")
	}
}

// .
// .
func TestProjectListShowsTheContract(t *testing.T) {
	engine, port := newVerbEngine(t)
	port.listResult = []ProjectInfo{{
		ID: "alpha", Name: "Alpha", State: "open", Parent: "beta",
		Contract: ProjectContract{
			Outcome:    "ship the beta",
			Acceptance: []string{"three platforms install clean"},
		},
	}}
	out, err := engine.ExecuteAction(context.Background(), "verb", "project", map[string]interface{}{"action": "list"})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"ship the beta", "three platforms install clean", "under beta"} {
		if !strings.Contains(out, want) {
			t.Fatalf("list must show %q:\n%s", want, out)
		}
	}
}

// .
// .
// .
func TestVerbProjectCarriesTheContractOnCreateToo(t *testing.T) {
	engine, port := newVerbEngine(t)
	if _, err := engine.ExecuteAction(context.Background(), "verb", "project", map[string]interface{}{
		"action":      "create",
		"name":        "Alpha",
		"description": "a description",
		"outcome":     "ship the beta",
		"acceptance":  []interface{}{"three platforms install clean"},
		"constraints": []interface{}{"no unsigned binaries"},
		"parent":      "umbrella",
	}); err != nil {
		t.Fatal(err)
	}
	c := port.createArgs.contract
	if c == nil {
		t.Fatal("the contract must reach the substrate at CREATE, not only at update")
	}
	if c.Outcome != "ship the beta" || len(c.Acceptance) != 1 || len(c.Constraints) != 1 {
		t.Fatalf("contract lost on create: %+v", c)
	}
	if port.createArgs.parent == nil || *port.createArgs.parent != "umbrella" {
		t.Fatalf("parent lost on create: %v", port.createArgs.parent)
	}

	// .
	engine2, port2 := newVerbEngine(t)
	if _, err := engine2.ExecuteAction(context.Background(), "verb", "project", map[string]interface{}{
		"action": "create", "name": "Bare",
	}); err != nil {
		t.Fatal(err)
	}
	if port2.createArgs.contract != nil || port2.createArgs.parent != nil {
		t.Fatal("a bare create must not invent a contract")
	}
}
