package identity

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"
)

const (
	ordinaryToolsDescription = "Discovery: your organs and sandbox tools; and the plugin operations installed beside you, the primary ones in your tool list and any other run with action=call."
	ordinaryToolsDepth       = "1 = names, 2 = with descriptions, 3 = full detail"
	ordinaryToolsAction      = "organs (default: organs, sandbox tools and the plugin brief), brief (plugin operations by family), search (query: by need), show (name: one operation's arguments, receipt rule and release), call (name, args)"
	ordinaryToolsQuery       = "(search) what you need, in your own words"
	ordinaryToolsName        = "(show, call) the operation id as your prompt or the brief names it, or its tool name"
	ordinaryToolsArgs        = "(call) the operation's arguments, as tools action=show gives them"
	ordinaryBriefNext        = "\nEach family names its primary operations first; they are in your tool list. Next: tools action=search query=<what you need> · action=show name=<operation> · action=call name=<operation> args={…} for one that is not. A plugin's success is what the host's receipt says, never the plugin's own text."
)

func toolsOrgan(t *testing.T, verbs []Verb) Verb {
	t.Helper()
	for _, v := range verbs {
		if v.Name == "tools" {
			return v
		}
	}
	t.Fatal("no tools organ")
	return Verb{}
}

// OUT OF SAFE THE TOOLS ORGAN IS WHAT IT WAS, TO THE BYTE: what the model
// is given of it (description and arguments) and what its listing ends
// with. And out of SAFE the offered organs are the six, each as declared.
func TestOutOfSafeTheToolsOrganIsUnchangedToTheByte(t *testing.T) {
	engine, _, _, _, _ := setupEngine(t)
	engine.toolDisc = &fakePlane{}
	offered := OfferedVerbs(false)
	declared := Verbs()
	if len(offered) != 6 || len(declared) != 6 {
		t.Fatalf("out of SAFE %d organs are offered of %d, want six of six", len(offered), len(declared))
	}
	for i := range declared {
		got, _ := json.Marshal(offered[i].Params)
		want, _ := json.Marshal(declared[i].Params)
		if offered[i].Name != declared[i].Name || offered[i].Description != declared[i].Description || string(got) != string(want) {
			t.Errorf("out of SAFE %s is not offered as it is declared", declared[i].Name)
		}
	}
	v := toolsOrgan(t, offered)
	if v.Description != ordinaryToolsDescription {
		t.Errorf("the tools organ's description moved:\n got %q\nwant %q", v.Description, ordinaryToolsDescription)
	}
	props, _ := v.Params["properties"].(map[string]any)
	for arg, want := range map[string]string{"depth": ordinaryToolsDepth, "action": ordinaryToolsAction, "query": ordinaryToolsQuery, "name": ordinaryToolsName, "args": ordinaryToolsArgs} {
		p, _ := props[arg].(map[string]any)
		if got, _ := p["description"].(string); got != want {
			t.Errorf("the tools organ's %s argument moved:\n got %q\nwant %q", arg, got, want)
		}
	}
	if len(props) != 5 {
		t.Errorf("the tools organ takes %d arguments, want five: %v", len(props), props)
	}
	res, err := engine.ExecuteAction(context.Background(), "verb", "tools", map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res, ordinaryBriefNext+"\n\nSeeded docs at your root") {
		t.Errorf("out of SAFE the listing's brief does not end with its next step as it did:\n%s", res)
	}
}

func TestInSafeTheToolsOrganNamesNothingThatOnlyRefuses(t *testing.T) {
	engine, _, _, _, _ := setupEngine(t)
	engine.toolDisc = &fakePlane{}
	listing := func() string {
		t.Helper()
		res, err := engine.ExecuteAction(context.Background(), "verb", "tools", map[string]any{})
		if err != nil {
			t.Fatal(err)
		}
		return res
	}
	before := listing()

	engine.SetSafeMode("the record did not verify")
	var names []string
	for _, v := range OfferedVerbs(true) {
		names = append(names, v.Name)
	}
	if strings.Join(names, " ") != "recall work tools" {
		t.Fatalf("in SAFE the offered organs are %v", names)
	}
	v := toolsOrgan(t, OfferedVerbs(true))
	def, err := json.Marshal(map[string]any{"description": v.Description, "parameters": v.Params})
	if err != nil {
		t.Fatal(err)
	}
	props, _ := v.Params["properties"].(map[string]any)
	depth, _ := props["depth"].(map[string]any)
	if v.Description != toolsInSafe || len(props) != 1 || depth["description"] != ordinaryToolsDepth {
		t.Errorf("in SAFE the tools organ is not its listing by depth and nothing else: %s", def)
	}
	if !strings.HasPrefix(ordinaryToolsDescription, strings.TrimSuffix(toolsInSafe, ".")) {
		t.Errorf("SAFE's description is not the ordinary one up to the plugin operations: %q", toolsInSafe)
	}
	if strings.Contains(string(def), "call") || strings.Contains(string(def), "search") {
		t.Errorf("in SAFE the tools organ is described with a call or a search: %s", def)
	}

	in := listing()
	for _, never := range []string{"your edits win", "annotate", "action=call", "call one", "Each family", "  note —", "  commit —", "  send —", "Next:"} {
		if strings.Contains(in, never) {
			t.Errorf("in SAFE the listing says %q:\n%s", never, in)
		}
	}
	if !strings.Contains(in, "(1 unavailable).") || !strings.Contains(in, "  weather — 1: weather.current\n\nSeeded docs at your root (yours to read):") {
		t.Errorf("in SAFE the listing's brief does not end with its counts and families:\n%s", in)
	}
	for _, organ := range []string{"  recall —", "  work —", "  tools — " + toolsInSafe + "\n"} {
		if !strings.Contains(in, organ) {
			t.Errorf("in SAFE the listing lacks %q:\n%s", organ, in)
		}
	}
	if brief, err := engine.ExecuteAction(context.Background(), "verb", "tools", map[string]any{"action": "brief"}); err != nil || strings.Contains(brief, "action=call") || strings.Contains(brief, "Next:") {
		t.Errorf("in SAFE the brief still points at a call: %q %v", brief, err)
	}

	engine.SetSafeMode("")
	if after := listing(); after != before {
		t.Errorf("when SAFE ends the listing is not what it was:\n got %s\nwant %s", after, before)
	}
}

func TestInSafeTheWorkOrganNamesOnlyWhatAnswers(t *testing.T) {
	var ordinary, safe Verb
	for _, v := range Verbs() {
		if v.Name == "work" {
			ordinary = v
		}
	}
	for _, v := range OfferedVerbs(true) {
		if v.Name == "work" {
			safe = v
		}
	}
	if safe.Name == "" {
		t.Fatal("in SAFE work is not offered")
	}
	def, err := json.Marshal(map[string]any{"description": safe.Description, "parameters": safe.Params})
	if err != nil {
		t.Fatal(err)
	}
	props, _ := safe.Params["properties"].(map[string]any)
	action, _ := props["action"].(map[string]any)
	if got, _ := action["enum"].([]string); safe.Description != workInSafe || strings.Join(got, " ") != "status yield measure" {
		t.Errorf("in SAFE work is not its three actions that answer: %s", def)
	}
	ordinaryProps, _ := ordinary.Params["properties"].(map[string]any)
	var args []string
	for arg := range props {
		args = append(args, arg)
		if _, ok := ordinaryProps[arg]; !ok {
			t.Errorf("in SAFE work takes %s, which it does not take outside SAFE", arg)
		}
	}
	if slices.Sort(args); strings.Join(args, " ") != "action answer hours id" {
		t.Errorf("in SAFE work takes %v, want the arguments status, measure and yield read: action, answer, hours, id", args)
	}
	for _, never := range []string{"spawn", "alarm", "project", "backup", "voice", "curiosity", "a delivery wakes you"} {
		if strings.Contains(string(def), never) {
			t.Errorf("in SAFE work is described with %q: %s", never, def)
		}
	}
}
