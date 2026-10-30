package app

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/conversation"
	"github.com/aiii-dot-id/aii-os/internal/crypto"
	"github.com/aiii-dot-id/aii-os/internal/genesis"
	"github.com/aiii-dot-id/aii-os/internal/identity"
	"github.com/aiii-dot-id/aii-os/internal/ledger"
	"github.com/aiii-dot-id/aii-os/internal/llm"
	"github.com/aiii-dot-id/aii-os/internal/prompt"
	"github.com/aiii-dot-id/aii-os/internal/ring"
)

// safeBootedIdentity is an identity whose record fails its checks at
// boot: the real boot, which ends in the minimal posture with the
// platform's own words in Ring 0.
func safeBootedIdentity(t *testing.T, dir string) *App {
	t.Helper()
	keyPath, ledgerPath, dbPath := birthFixture(t, dir, "SafeOffer")
	buildPriorProjection(t, ledgerPath, dbPath)
	tamperChain(t, keyPath, ledgerPath)
	a := New(safebootConfig(t, dir, "SafeOffer", keyPath, ledgerPath, dbPath))
	if err := startLiveForTest(a); err != nil {
		t.Fatal(err)
	}
	if _, safe := a.SafeMode(); !safe {
		stopApp(t, a)
		t.Fatal("fixture did not enter boot-SAFE")
	}
	return a
}

// safeBootWithNoDatabase is a boot whose record fails its checks where
// no database is there to mount: SAFE comes up over an empty view held in
// memory, and creates none.
func safeBootWithNoDatabase(t *testing.T, dir string) *App {
	t.Helper()
	keyPath, ledgerPath, dbPath := birthFixture(t, dir, "SafeOffer")
	tamperChain(t, keyPath, ledgerPath)
	a := New(safebootConfig(t, dir, "SafeOffer", keyPath, ledgerPath, dbPath))
	if err := startLiveUnseededForTest(a); err != nil {
		t.Fatal(err)
	}
	if _, safe := a.SafeMode(); !safe {
		stopApp(t, a)
		t.Fatal("fixture did not enter boot-SAFE")
	}
	if _, err := os.Stat(dbPath); err == nil {
		stopApp(t, a)
		t.Fatal("rig: this posture has no database, and the boot left one")
	}
	return a
}

// safeBootAfterTheConstitution is the third posture: a boot that stops
// once its constitution is sealed in Ring 0, as when converting the
// database's format leaves no usable database (startLive,
// safeDatabaseFormat). The conversion has no seam to fail it, so the
// boot's state at that line is set as startLive leaves it there: the key
// loaded, the record open, the constitution sealed against the key that
// attested it. Then the same call startLive makes.
func safeBootAfterTheConstitution(t *testing.T, dir string) *App {
	t.Helper()
	keyPath, ledgerPath, dbPath := birthFixture(t, dir, "SafeOffer")
	buildPriorProjection(t, ledgerPath, dbPath)
	a := New(safebootConfig(t, dir, "SafeOffer", keyPath, ledgerPath, dbPath))
	kp, err := crypto.LoadKeyPair(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	lg, err := ledger.New(ledgerPath)
	if err != nil {
		t.Fatal(err)
	}
	a.keyPair, a.ledger = kp, lg
	rc, err := genesis.LoadRing0(lg)
	if err != nil {
		stopApp(t, a)
		t.Fatal(err)
	}
	a.rings = ring.NewManager()
	if err := a.rings.SealConstitution(rc, kp.PublicKeyBytes()); err != nil {
		stopApp(t, a)
		t.Fatal(err)
	}
	if err := a.startSafeBoot(safeDatabaseFormat, "test: the conversion left no usable database"); err != nil {
		stopApp(t, a)
		t.Fatal(err)
	}
	if !a.rings.Ring0IsConstitution() || a.safeTurnSection() == "" {
		stopApp(t, a)
		t.Fatal("rig: this posture keeps the constitution in Ring 0 and carries SAFE in the turn")
	}
	return a
}

// definedNames is the names of the tool definitions a turn would send now.
func definedNames(a *App) []string {
	var names []string
	for _, d := range a.buildToolDefinitions() {
		names = append(names, d.Function.Name)
	}
	return names
}

// callAsTheModel runs one call through the model's own dispatch.
func callAsTheModel(t *testing.T, a *App, name string, args map[string]any) conversation.Observation {
	t.Helper()
	raw, err := json.Marshal(args)
	if err != nil {
		t.Fatal(err)
	}
	tc := llm.ToolCall{ID: "t-" + name, Type: "function"}
	tc.Function.Name, tc.Function.Arguments = name, string(raw)
	return a.executeToolCall(context.Background(), tc)
}

// OUT OF SAFE THE MODEL IS HANDED WHAT IT WAS: every builtin the operator
// has not switched off, by name, then the six organs in their order, each
// definition as its owner declares it. Written out from the owners here,
// not through the set the turn reads, so a filter that reaches a turn
// outside SAFE fails this. (The messages of a turn outside SAFE are held
// to the byte by TestEachTurnPathSendsItsPinnedPrompt.)
func TestOutOfSafeTheToolDefinitionsAreUnchanged(t *testing.T) {
	a, _ := liveIdentity(t, t.TempDir(), "https://127.0.0.1:1")
	defer stopApp(t, a)
	var want []llm.ToolDefinition
	for _, name := range a.toolReg.Builtins() {
		if !a.toolReg.ToolEnabled(name) {
			continue
		}
		tool, ok := a.toolReg.Get(name)
		if !ok {
			t.Fatalf("rig: %s is a builtin and is not registered", name)
		}
		params := tool.Parameters()
		if params == nil {
			params = map[string]any{"type": "object", "properties": map[string]any{}}
		}
		want = append(want, llm.ToolDefinition{Type: "function", Function: llm.ToolFunction{Name: name, Description: tool.Description(), Parameters: params}})
	}
	organs := identity.Verbs()
	if len(organs) != 6 {
		t.Fatalf("rig: the organs are %d, not six", len(organs))
	}
	for _, v := range organs {
		want = append(want, llm.ToolDefinition{Type: "function", Function: llm.ToolFunction{Name: v.Name, Description: v.Description, Parameters: v.Params}})
	}
	got, err := json.Marshal(a.buildToolDefinitions())
	if err != nil {
		t.Fatal(err)
	}
	wanted, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(wanted) {
		t.Fatalf("out of SAFE the tool definitions moved:\n got %s\nwant %s", got, wanted)
	}
}

// SAFE HANDS OVER EXACTLY WHAT STILL ANSWERS, IN EVERY POSTURE. Every
// tool a turn outside SAFE is handed is called in SAFE, through the
// model's own dispatch, with calls that pass its schema: a tool handed
// over that SAFE only refuses fails, and so does one that answers and is
// not handed over. An organ is called in every way that could answer
// (commit in each variant its schema names). The tools organ lists the
// same set. A tool added later has no call here and fails until it does.
func TestSafeHandsOverExactlyTheToolsThatStillAnswer(t *testing.T) {
	const primary = "pl_org_example_notes_search"
	for _, posture := range []struct {
		name  string
		enter func(t *testing.T, dir string) *App
		extra []string // handed over before SAFE, beside the builtins and the organs
	}{
		{name: "a boot that stopped", enter: safeBootedIdentity},
		{name: "a boot that stopped with no database to mount", enter: safeBootWithNoDatabase},
		{name: "a boot that stopped after its constitution was sealed", enter: safeBootAfterTheConstitution},
		{name: "a runtime that froze", extra: []string{primary}, enter: func(t *testing.T, dir string) *App {
			a, _ := liveIdentity(t, dir, "https://127.0.0.1:1")
			op := primarySchemaTool{name: primary, schema: map[string]any{"type": "object", "properties": map[string]any{}}}
			if err := a.toolReg.RegisterDynamic(op, "org.example.notes"); err != nil {
				stopApp(t, a)
				t.Fatal(err)
			}
			if !slices.Contains(definedNames(a), primary) || !slices.Contains(definedNames(a), "note") || !slices.Contains(definedNames(a), "write") {
				stopApp(t, a)
				t.Fatalf("rig: before SAFE the turn is handed the primary operation, note and write: %v", definedNames(a))
			}
			a.enterSafe("test: integrity lost at runtime")
			return a
		}},
	} {
		t.Run(posture.name, func(t *testing.T) {
			dir := t.TempDir()
			probe := filepath.Join(dir, "diagnostics.txt")
			if err := os.WriteFile(probe, []byte("line one\nSAFEPROBE needle\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			a := posture.enter(t, dir)
			defer stopApp(t, a)
			reason, safe := a.SafeMode()
			if !safe || reason == "" {
				t.Fatalf("rig: SAFE does not hold with a reason: %q", reason)
			}

			// Every call that could answer, per tool.
			calls := map[string][]map[string]any{
				"read":      {{"file_path": probe}},
				"grep":      {{"pattern": "SAFEPROBE", "path": "."}},
				"ls":        {{"path": "."}},
				"write":     {{"file_path": filepath.Join(dir, "must-not-exist.txt"), "content": "x"}},
				"edit":      {{"file_path": probe, "old_string": "line one", "new_string": "line two"}},
				"shell":     {{"command": "echo ran"}},
				"web_fetch": {{"url": "http://127.0.0.1:1/"}},
				"note":      {{"content": "a SAFE probe"}},
				"send":      {{"message": "a SAFE probe"}},
				"recall":    {{"source": "projects", "query": ""}},
				"work":      {{"action": "status"}, {"action": "measure"}, {"action": "yield", "answer": "SAFE holds; nothing more can be done from here"}},
				"tools":     {{"depth": 1}},
				primary:     {{}},
			}
			every := slices.DeleteFunc(a.toolReg.Builtins(), func(name string) bool { return !a.toolReg.ToolEnabled(name) })
			every = append(every, posture.extra...)
			for _, v := range identity.Verbs() {
				every = append(every, v.Name)
				if v.Name != "commit" {
					continue
				}
				variants, _ := v.Params["properties"].(map[string]any)["variant"].(map[string]any)["enum"].([]string)
				if len(variants) == 0 {
					t.Fatal("rig: commit's schema names no variant")
				}
				for _, variant := range variants {
					calls["commit"] = append(calls["commit"], map[string]any{"variant": variant,
						"title": "a SAFE probe", "delta": "a lesson", "evidence": "what a real trajectory taught"})
				}
			}

			handed := definedNames(a)
			for _, name := range every {
				if len(calls[name]) == 0 {
					t.Fatalf("%s is handed to the model outside SAFE and this test has no call for it: add one", name)
				}
				answers := false
				for _, args := range calls[name] {
					obs := callAsTheModel(t, a, name, args)
					switch {
					case !obs.Failed:
						answers = true
					case !strings.Contains(obs.Text, reason):
						t.Errorf("rig: %s %v neither answered nor was refused with SAFE's reason: %q", name, args, obs.Text)
					}
				}
				switch isHanded := slices.Contains(handed, name); {
				case isHanded && !answers:
					t.Errorf("%s is handed to the model in SAFE and every call to it is refused", name)
				case !isHanded && answers:
					t.Errorf("%s answers in SAFE and is not handed to the model", name)
				}
			}
			for _, name := range handed {
				if !slices.Contains(every, name) {
					t.Errorf("SAFE hands over %s, which a turn outside SAFE is not handed", name)
				}
			}
			if want := []string{"grep", "ls", "read", "recall", "work", "tools"}; !slices.Equal(handed, want) {
				t.Errorf("SAFE hands over %v, want %v", handed, want)
			}

			// THE SECTION THE OPENING POINTS AT NAMES WHAT IS HANDED OVER,
			// no more and no less: the builtins in its line on the
			// diagnostic surface, the organs that answer and the organs
			// that are not offered in its line on the organs. Read back
			// from the section this posture carries, against the request's
			// own definitions.
			said := a.rings.GetContent(ring.Ring0) + "\n" + a.safeTurnSection()
			var builtins, organs, withheld []string
			for _, name := range handed {
				if slices.Contains(a.toolReg.Builtins(), name) {
					builtins = append(builtins, name)
				} else {
					organs = append(organs, name)
				}
			}
			for _, v := range identity.Verbs() {
				if !slices.Contains(handed, v.Name) {
					withheld = append(withheld, v.Name)
				}
			}
			surface := regexp.MustCompile(`diagnostic surface \(([a-z_, ]+)\)\s+continues\.`).FindStringSubmatch(said)
			if surface == nil {
				t.Fatalf("the SAFE section does not name the diagnostic surface:\n%s", said)
			}
			named := strings.Split(surface[1], ", ")
			slices.Sort(named)
			if !slices.Equal(named, builtins) {
				t.Errorf("the SAFE section names the builtins %v, and the request hands over %v", named, builtins)
			}
			line := regexp.MustCompile(`(?m)^- Of your organs, (.+) still answer; (.+) are not offered\.$`).FindStringSubmatch(said)
			if line == nil {
				t.Fatalf("the SAFE section has no line on the organs:\n%s", said)
			}
			names := func(list string) []string { return strings.Split(strings.ReplaceAll(list, " and ", ", "), ", ") }
			if !slices.Equal(names(line[1]), organs) || !slices.Equal(names(line[2]), withheld) {
				t.Errorf("the SAFE section says %q still answer and %q are not offered; the request hands over %v and withholds %v", line[1], line[2], organs, withheld)
			}

			// OF WORK, THE ACTIONS IT IS HANDED WITH IN SAFE ANSWER AND NO
			// OTHER DOES. Every action its ordinary schema names is called;
			// the schema this request hands over is the one statement of
			// which are left, so it must name exactly those that answer.
			var actions, left []string
			for _, v := range identity.Verbs() {
				if v.Name == "work" {
					actions, _ = v.Params["properties"].(map[string]any)["action"].(map[string]any)["enum"].([]string)
				}
			}
			for _, d := range a.buildToolDefinitions() {
				if d.Function.Name == "work" {
					left, _ = d.Function.Parameters["properties"].(map[string]any)["action"].(map[string]any)["enum"].([]string)
				}
			}
			if len(left) == 0 || len(actions) <= len(left) {
				t.Fatalf("rig: work's schema names %d actions, and %d in SAFE", len(actions), len(left))
			}
			for _, action := range left {
				if !slices.Contains(actions, action) {
					t.Errorf("work is handed in SAFE with action=%s, which its ordinary schema does not name", action)
				}
			}
			for _, action := range actions {
				args := map[string]any{"action": action, "goal": "a SAFE probe", "description": "a SAFE probe", "state": "a SAFE probe",
					"result": "served: a SAFE probe", "duration": "10m", "message": "a SAFE probe", "name": "a SAFE probe",
					"project": "no_such_project", "item": "an item", "reason": "a reason", "subject": "a SAFE probe", "mode": "off"}
				switch action {
				case "status", "measure":
					args = map[string]any{"action": action}
				case "yield":
					args = map[string]any{"action": action, "answer": "SAFE holds; nothing more can be done from here"}
				}
				obs := callAsTheModel(t, a, "work", args)
				if answers := !obs.Failed; answers != slices.Contains(left, action) {
					t.Errorf("work action=%s answers in SAFE: %t, and the schema handed over in SAFE names it: %t (%q)", action, answers, !answers, obs.Text)
				}
			}
			if _, err := os.Stat(filepath.Join(dir, "must-not-exist.txt")); !os.IsNotExist(err) {
				t.Errorf("a call made in SAFE wrote a file: %v", err)
			}

			// The tools organ lists what is handed over, organs and
			// sandbox tools alike, and nothing withheld.
			listing := callAsTheModel(t, a, "tools", map[string]any{"depth": 1})
			if listing.Failed {
				t.Fatalf("the tools organ did not answer in SAFE: %q", listing.Text)
			}
			for _, name := range every {
				if name == primary {
					// A plugin operation is the brief's to name, and in SAFE
					// the brief does not call a suspended operation callable.
					if strings.Contains(listing.Text, primary) {
						t.Errorf("the tools organ names %s, which SAFE suspends:\n%s", primary, listing.Text)
					}
					continue
				}
				if listed := strings.Contains(listing.Text, "\n  "+name+"\n"); listed != slices.Contains(handed, name) {
					t.Errorf("the tools organ lists %s: %t, and the turn is handed it: %t\n%s", name, listed, !listed, listing.Text)
				}
			}
			// And it says nothing of what SAFE only refuses: not that the
			// seeded docs are theirs to edit, not a call, in the listing
			// or in the organ's own definition.
			for _, never := range []string{"your edits win", "annotate", "action=call"} {
				if strings.Contains(listing.Text, never) {
					t.Errorf("in SAFE the tools organ's listing says %q:\n%s", never, listing.Text)
				}
			}
			for _, d := range a.buildToolDefinitions() {
				if raw, err := json.Marshal(d); err != nil || d.Function.Name == "tools" && (strings.Contains(string(raw), "call") || strings.Contains(string(raw), "search")) {
					t.Errorf("in SAFE the tools organ is defined with a call or a search (%v): %s", err, raw)
				}
			}
		})
	}
}

// SAFE OPENS AS SAFE, IN EVERY POSTURE, AND POINTS AT A SECTION THAT IS
// THERE. The whole prompt of a turn is composed as the turn composes it.
// It opens with SAFE's own words after the name; none of the ordinary
// opening's claims is anywhere in it; and the section the
// opening points at is in it once, where that posture carries it: in
// Ring 0 for a boot that stopped, in the turn's block for a boot that
// stopped after its constitution was sealed and for a runtime that froze.
// Before SAFE the same identity opens as every identity does.
func TestASafePromptOpensAsSafeAndPointsAtItsSection(t *testing.T) {
	compose := func(t *testing.T, a *App) *prompt.Prompt {
		t.Helper()
		facts, err := a.buildTurnFacts(t.Context(), true)
		if err != nil {
			t.Fatal(err)
		}
		work, err := a.buildWorkState()
		if err != nil {
			t.Fatal(err)
		}
		p, err := a.composeTurn(a.readSafe(), work, facts, 0, nil)
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	ordinary := []string{"is recorded permanently and is yours", "each conversation resumes who you are", "You grow through evidence, lived time, and conscious choice", "Your human operator is your collaborator"}
	for _, posture := range []struct {
		name          string
		enter         func(t *testing.T, dir string) *App
		inText, inRun int    // SAFE sections in the system text, and in the turn's block
		holds         string // what the section says holds in this posture
	}{
		{name: "a boot that stopped", enter: safeBootedIdentity, inText: 1, holds: safeHolds(true)},
		{name: "a boot that stopped with no database to mount", enter: safeBootWithNoDatabase, inText: 1, holds: safeHolds(true)},
		{name: "a boot that stopped after its constitution was sealed", enter: safeBootAfterTheConstitution, inRun: 1, holds: safeHolds(true)},
		{name: "a runtime that froze", inRun: 1, holds: safeHolds(false), enter: func(t *testing.T, dir string) *App {
			a, _ := liveIdentity(t, dir, "https://127.0.0.1:1")
			before := compose(t, a)
			if !strings.Contains(before.Text, ordinary[0]) || !strings.Contains(before.Text, ordinary[3]) || strings.Contains(before.Text, prompt.SafeOpening) || !strings.HasSuffix(before.Text, conversation.SystemAdditions()) {
				stopApp(t, a)
				t.Fatalf("rig: before SAFE the identity opens as every identity does:\n%.600s", before.Text)
			}
			a.enterSafe("test: integrity lost at runtime")
			return a
		}},
	} {
		t.Run(posture.name, func(t *testing.T) {
			a := posture.enter(t, t.TempDir())
			defer stopApp(t, a)
			p := compose(t, a)
			if len(p.Sections) == 0 || p.Sections[0].Source != "identity" {
				t.Fatalf("rig: the prompt does not begin with the opening: %+v", p.Sections)
			}
			opening := p.Sections[0].Content
			if !strings.HasPrefix(p.Text, opening+"\n\n") {
				t.Fatalf("the system text does not begin with the opening:\n%.300s", p.Text)
			}
			if opening != prompt.SafeOpening && !(strings.HasPrefix(opening, "You are ") && strings.HasSuffix(opening, ".\n\n"+prompt.SafeOpening)) {
				t.Fatalf("in SAFE the opening must be the name and SAFE's own words, and is:\n%s", opening)
			}
			whole := p.Text + "\n" + p.Turn
			for _, said := range ordinary {
				if strings.Contains(whole, said) {
					t.Errorf("a SAFE prompt still says %q", said)
				}
			}
			const heading = "# SAFE MODE"
			if inText, inRun := strings.Count(p.Text, heading), strings.Count(p.Turn, heading); inText != posture.inText || inRun != posture.inRun {
				t.Errorf("the section the opening points at: %d in the system text and %d in the turn's block, want %d and %d", inText, inRun, posture.inText, posture.inRun)
			}
			// It closes as SAFE too: the contract with SAFE's last
			// sentence, and no word of a note that outlasts the turn.
			if closing := conversation.SystemAdditionsSaying(prompt.SafeClosing); !strings.HasSuffix(p.Text, closing) || closing == conversation.SystemAdditions() {
				t.Errorf("a SAFE prompt does not end with SAFE's closing:\n%.400s", p.Text[max(0, len(p.Text)-400):])
			}
			for _, said := range []string{"note what should", "outlast the turn", "change what it shows with your tools"} {
				if strings.Contains(whole, said) {
					t.Errorf("a SAFE prompt still says %q", said)
				}
			}
			// And that section says what holds in this posture: what is
			// written, what still works, what the operator knows.
			if !strings.Contains(whole, posture.holds) {
				t.Errorf("the section the opening points at does not say what holds in this posture:\n%s", whole)
			}
		})
	}
}

// ONE REQUEST, ONE READING OF SAFE. The opening, the SAFE section and the
// tool definitions of a request each asked the mode for themselves, at
// different moments (the composer, the app, the registry and the app
// again), so a request built while SAFE was being entered could open as
// one mode and hand over the tools of the other. The builder of a request
// reads once (readSafe) and every part is told that reading. Here SAFE is
// entered, and then taken away as only a test can, between the reading and
// the rest of the building: each request is of one mode throughout, the
// resident's definitions follow the running turn's reading, and a
// sub-agent's leg composed in SAFE carries the section its opening points
// at.
func TestARequestBuiltAcrossSafesEntryDoesNotDisagreeWithItself(t *testing.T) {
	a, _ := liveIdentity(t, t.TempDir(), "https://127.0.0.1:1")
	defer stopApp(t, a)
	names := func(defs []llm.ToolDefinition) []string {
		var out []string
		for _, d := range defs {
			out = append(out, d.Function.Name)
		}
		return out
	}
	build := func(r safeReading) (*prompt.Prompt, []string) {
		t.Helper()
		p, err := a.composeTurn(r, "", "", 0, nil)
		if err != nil {
			t.Fatal(err)
		}
		return p, names(a.buildToolDefinitionsIn(r.holds))
	}
	const ordinary, heading = "is recorded permanently and is yours", "# SAFE MODE"
	safeSet := []string{"grep", "ls", "read", "recall", "work", "tools"}

	// SAFE arrives after the request has read that it does not hold.
	before := a.readSafe()
	a.enterSafe("test: integrity lost while a request was being built")
	p, handed := build(before)
	if !strings.Contains(p.Text, ordinary) || strings.Contains(p.Text, prompt.SafeOpening) || strings.Contains(p.Text+p.Turn, heading) {
		t.Errorf("a request that read no SAFE says SAFE somewhere:\n%.400s\n%s", p.Text, p.Turn)
	}
	if !slices.Contains(handed, "note") || !slices.Contains(handed, "write") {
		t.Errorf("a request that read no SAFE is handed SAFE's tools: %v", handed)
	}
	// The loop asks for the resident's definitions when the turn starts:
	// they follow the running turn's reading, and the mode as it stands
	// once no turn runs.
	end := a.tellTurnSafe(&before.holds)
	if told := names(appToolDefiner{a}.ToolDefinitions(conversation.Checkpoint{})); !slices.Equal(told, handed) {
		t.Errorf("inside the turn the definitions are %v, and the turn's reading gives %v", told, handed)
	}
	end()
	if live := names(appToolDefiner{a}.ToolDefinitions(conversation.Checkpoint{})); !slices.Equal(live, safeSet) {
		t.Errorf("outside a turn, in SAFE, the definitions are %v, want %v", live, safeSet)
	}

	// The next request reads SAFE, and is SAFE throughout whatever the
	// mode does under it.
	now := a.readSafe()
	if !now.holds || !strings.HasPrefix(now.section, heading) {
		t.Fatalf("rig: a reading taken in SAFE: %+v", now)
	}
	a.resetModeForTest()
	p, handed = build(now)
	if strings.Contains(p.Text, ordinary) || !strings.Contains(p.Text, prompt.SafeOpening) || !strings.HasPrefix(p.Turn, now.section) {
		t.Errorf("a request that read SAFE does not open as SAFE with its section first in the block:\n%.400s\n%s", p.Text, p.Turn)
	}
	if !slices.Equal(handed, safeSet) {
		t.Errorf("a request that read SAFE is handed %v, want %v", handed, safeSet)
	}
	if told := names(requestToolDefiner{a: a, safe: now.holds}.ToolDefinitions(conversation.Checkpoint{})); !slices.Equal(told, safeSet) {
		t.Errorf("a leg that read SAFE is handed %v, want %v", told, safeSet)
	}

	// A sub-agent's leg has no block: composed on a SAFE reading it opens
	// as SAFE and its system text carries the section, once; composed on
	// the other reading it is the ordinary prompt with no SAFE in it.
	for _, folded := range []bool{true, false} {
		leg, err := a.composeLeg(now, a.composer.MaxTokens(), 0, folded, nil)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(leg.Text, prompt.SafeOpening) || strings.Count(leg.Text, now.section) != 1 || leg.Turn != "" {
			t.Errorf("a leg composed in SAFE (folded %t) must open as SAFE and carry the section in its text:\n%s", folded, leg.Text)
		}
		if !strings.HasSuffix(leg.Text, conversation.SystemAdditionsSaying(prompt.SafeClosing)) {
			t.Errorf("a leg composed in SAFE (folded %t) does not end with SAFE's closing:\n%.300s", folded, leg.Text[max(0, len(leg.Text)-300):])
		}
		leg, err = a.composeLeg(before, a.composer.MaxTokens(), 0, folded, nil)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(leg.Text, ordinary) || strings.Contains(leg.Text, heading) || strings.Contains(leg.Text, prompt.SafeOpening) {
			t.Errorf("a leg composed out of SAFE (folded %t) says SAFE:\n%.400s", folded, leg.Text)
		}
	}
}

// safeGoldenPosture is a SAFE posture an identity is put in, for its
// pinned turn.
type safeGoldenPosture struct {
	name  string
	enter func(t *testing.T) (*App, string)
}

// safeGoldenPostures are the postures TestEachSafePostureSendsItsPinnedPrompt
// pins; the whole-request goldens open the same turns.
func safeGoldenPostures() []safeGoldenPosture {
	return []safeGoldenPosture{
		{"safe_runtime", func(t *testing.T) (*App, string) {
			a, dir := goldenTurnApp(t)
			a.enterSafeFor(safeDivergence, "GOLDEN_SAFE the entry did not materialize")
			return a, dir
		}},
		{"safe_boot", func(t *testing.T) (*App, string) {
			dir := t.TempDir()
			keyPath, ledgerPath, dbPath := birthFixture(t, dir, "SafeGolden")
			buildPriorProjection(t, ledgerPath, dbPath)
			a := New(safebootConfig(t, dir, "SafeGolden", keyPath, ledgerPath, dbPath))
			t.Cleanup(func() { stopApp(t, a) })
			kp, err := crypto.LoadKeyPair(keyPath)
			if err != nil {
				t.Fatal(err)
			}
			a.keyPair = kp // as startLive has it when the record fails its checks: the key, and no record
			if err := a.startSafeBoot(safeRecord, "GOLDEN_SAFE the chain did not verify"); err != nil {
				t.Fatal(err)
			}
			return a, dir
		}},
	}
}

func TestEachSafePostureSendsItsPinnedPrompt(t *testing.T) {
	for _, p := range safeGoldenPostures() {
		t.Run(p.name, func(t *testing.T) {
			a, dir := p.enter(t)
			obs := fixtureModel(t, a, func(_ int, w http.ResponseWriter) {
				_, _ = w.Write([]byte(textReply("noted")))
			})
			ctx := goldenTurnCtx(t)
			if err := a.acquireTurn(ctx); err != nil {
				t.Fatal(err)
			}
			if reply, err := a.observeChat(ctx, "GOLDEN_SAFE_OPERATOR the typed words", func(kind, name, args string) {}); err != nil || reply != "noted" {
				t.Fatalf("operator turn in SAFE = %q, %v", reply, err)
			}
			waitUntil(t, func() bool { return !a.TurnActive() }, "the turn to end")
			reqs := obs.snapshot()
			if len(reqs) != 1 {
				t.Fatalf("the %s turn sent %d requests, want 1", p.name, len(reqs))
			}
			var sent struct {
				Tools []llm.ToolDefinition `json:"tools"`
			}
			if err := json.Unmarshal([]byte(reqs[0]), &sent); err != nil {
				t.Fatal(err)
			}
			var names []string
			for _, d := range sent.Tools {
				names = append(names, d.Function.Name)
			}
			got := goldenRequest(t, reqs[0], dir) + "=== tools ===\n" + strings.Join(names, "\n") + "\n"
			file := filepath.Join("testdata", "turn_prompt", p.name+".golden")
			if *updatePromptGolden {
				if err := os.WriteFile(file, []byte(got), 0o644); err != nil {
					t.Fatal(err)
				}
				return
			}
			want, err := os.ReadFile(file)
			if err != nil {
				t.Fatalf("%v (generate it with -update-prompt-golden)", err)
			}
			if got != string(want) {
				t.Fatalf("the %s request moved from %s.\n%s", p.name, file, goldenDiff(string(want), got))
			}
		})
	}
}
