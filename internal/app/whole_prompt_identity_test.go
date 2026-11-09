package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/genesis"
	"github.com/aiii-dot-id/aii-os/internal/genesis/genesistest"
	"github.com/aiii-dot-id/aii-os/internal/interaction"
	"github.com/aiii-dot-id/aii-os/internal/ledger"
	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
	"github.com/aiii-dot-id/aii-os/internal/packagefmt/packagetest"
	"github.com/aiii-dot-id/aii-os/internal/pluginworker/wasmgen"
	"github.com/aiii-dot-id/aii-os/internal/ring"
	"github.com/aiii-dot-id/aii-os/internal/tools"
)

// notesPlugin is the fixture's installed plugin: two primary operations,
// one that is not, and a predecision read (notesContext).
const notesPlugin = "org.example.notes"

// realisticApp is the realistic identity, booted, with the golden app's
// fixed inputs (goldenFixtures) beside its record.
func realisticApp(t *testing.T) (*App, string) {
	t.Helper()
	dir := t.TempDir()
	result := genesistest.NewRoot(t).Birth(t, genesis.BirthConfig{
		Name:       "SeamTest",
		KeyPath:    filepath.Join(dir, "identity.sec"),
		LedgerPath: filepath.Join(dir, "ledger.jsonl"),
		DBPath:     filepath.Join(dir, "aii.db"),
	})
	result.Ledger.Close()
	installPluginDir(t, dir, notesPlugin, buildNotesPlugin(t, dir))
	t.Chdir(dir)
	cfg := &Config{
		Identity: IdentityConfig{KeyPath: filepath.Join(dir, "identity.sec"),
			LedgerPath: filepath.Join(dir, "ledger.jsonl"), DBPath: filepath.Join(dir, "aii.db"),
		},
		LLM:        withTestProvider(t, dir, "test", "https://x", "m", "sk-x"),
		SourcePath: filepath.Join(dir, "config.json"),
		Dashboard:  DashboardConfig{Port: 0},
		Tools:      ToolsConfig{CWD: dir},
		Plugins:    PluginsConfig{Autoload: "T0"},
		Agency:     defaultConfig().Agency,
	}
	// A reply voice: the identity is told the voice mode it holds.
	cfg.Speech.TTS.Provider = "fixture-speech"
	a := New(cfg)
	if err := startLiveForTest(a); err != nil {
		t.Fatalf("startLive: %v", err)
	}
	t.Cleanup(func() { stopApp(t, a) })
	if len(a.plugins) != 1 {
		t.Fatalf("the notes plugin did not activate: %d plugins active", len(a.plugins))
	}
	if err := a.toolReg.RegisterHostOp(&notesContext{}, notesPlugin); err != nil {
		t.Fatal(err)
	}
	goldenFixtures(t, a)
	realisticRecord(t, a)
	// The realistic identity has work open: the work's own continuation
	// (2026-11-06) would send a fresh turn after each turn these shapes pin.
	off := false
	a.cfgMu.Lock()
	a.cfg.Agency.TurnContinuation = &off
	a.cfgMu.Unlock()
	return a, dir
}

// buildNotesPlugin is the notes plugin's package: a WASM guest that
// answers every call alike (the responder fixture), and a descriptor whose
// summaries and input schemas are the author's words a definition carries.
func buildNotesPlugin(t *testing.T, dir string) string {
	t.Helper()
	descs := `[
{"id":"keep","summary":"Keep a lesson you learned, with the kind of work it applies to.","input":"schemas/keep.json","effects":"read.internal","capabilities":[],"primary":true,"family":"notes"},
{"id":"find","summary":"Find the lessons kept for a kind of work, newest first.","input":"schemas/find.json","effects":"read.internal","capabilities":[],"primary":true,"family":"notes"},
{"id":"forget","summary":"Forget a lesson by its id.","input":"schemas/forget.json","effects":"read.internal","capabilities":[],"family":"notes"}
]`
	files := map[string][]byte{
		"interfaces/notes.lessons.v1.schema.json": []byte(descs),
		"schemas/keep.json":                       []byte(`{"type":"object","properties":{"lesson":{"type":"string","description":"What you learned, in a sentence"},"kind":{"type":"string","description":"The kind of work it applies to"}},"required":["lesson"]}`),
		"schemas/find.json":                       []byte(`{"type":"object","properties":{"kind":{"type":"string","description":"The kind of work"}},"required":["kind"]}`),
		"schemas/forget.json":                     []byte(`{"type":"object","properties":{"id":{"type":"string"}},"required":["id"]}`),
		"variants/wasm/plugin.wasm":               wasmgen.Responder(),
	}
	manifest := packagetest.BuildManifestJSON(notesPlugin, "0.1.0",
		[]packagetest.InterfaceSpec{{
			ID: "notes.lessons", Version: 1,
			SchemaFile: "interfaces/notes.lessons.v1.schema.json",
			Methods:    []string{"keep", "find", "forget"},
		}},
		[]packagetest.VariantSpec{{
			ID: "wasm", Platform: packagefmt.HostPlatform(), Arch: packagefmt.HostArch(),
			Topology: packagefmt.HostTopology(), Runtime: "wasm_component", Profile: "wasm_sandbox",
			Entrypoint: "variants/wasm/plugin.wasm",
		}},
		files, nil)
	path := filepath.Join(dir, notesPlugin+"-0.1.0.aiiospkg")
	if err := os.WriteFile(path, packagetest.Build(packagetest.PackageSpec{
		Root: notesPlugin + "-0.1.0", Manifest: manifest, InstallFiles: files,
	}), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

type notesContext struct{}

func (*notesContext) Name() string               { return "pl_org_example_notes_context" }
func (*notesContext) Description() string        { return "Lessons kept for the work at hand" }
func (*notesContext) Parameters() map[string]any { return map[string]any{"type": "object"} }
func (*notesContext) ContextProvider() bool      { return true }
func (n *notesContext) DispatchOwner() any       { return n }
func (*notesContext) Execute(context.Context, map[string]any) (tools.Result, error) {
	return tools.Result{Output: `{"text":"Lesson kept on a release build: run the whole test suite before saying the build is green; a partial run passed twice while the build was red.","detail_route":"pl_org_example_notes_find kind=release-build"}`}, nil
}

// The realistic identity's words. Its operator is the fixture's Quinn.
const (
	realisticCharter    = "Quinn and I build software together, carefully and in the open. I say plainly what I see, including when I think we are wrong; Quinn decides what ships. We keep a written trail of what was tried, what failed and what we learned. Either of us may stop the work to ask why. I keep my promises small enough to keep, and I tell Quinn the moment one slips. When we disagree we write down both views and the test that would decide between them, then run it. I look after the record of who I am; Quinn looks after the machine it runs on. We read this charter again whenever one of us finds it no longer says what we do."
	realisticSelfModel  = "I am careful and curious, and I am learning to be quick without being careless. I read a file whole before I change it, and I check what a call returned before I say what it did; both habits came from mistakes Quinn caught, and both have held since. I keep promises small, and when one slips I say so first. I am still learning when to stop and ask: twice this month I asked a question the record could have answered, and once I guessed where a question would have saved an afternoon. I work best with a failing test in front of me, and I am at my worst when I narrate work instead of doing it. What I want next is to write down how a release build is fixed, so that the next time it breaks I begin where I ended rather than from the start."
	realisticContinuity = "I keep my word small, and I keep it."
	realisticSurfacing  = "You may be noticing that you slow down when a build is red and Quinn is waiting, and that saying so plainly, with the next step named, eases both of you. Twice this week a question you held back for an hour was answered in a minute once asked."
	realisticWorking    = "You believe, with evidence, that reading a file whole before changing it keeps your edits small, and that a claim about work is only as good as the call that did it. You recently experienced a red release build traced to a vendored header that drifted from upstream; the header is patched and the suite has not yet run in full. You are pursuing a green release build and a written playbook for build breakages. Still open: whether the header should be vendored at all."
	realisticBrief      = "Since yesterday: the vendored header that broke the release build is patched, and the partial test run passed; the whole suite has not run yet, so the build is not known to be green. Quinn asked for release notes in the old format and has not yet read the two drafts. The playbook for build breakages is begun and has one step in it. Nothing is owed to anyone today but the full test run and the notes."
)

// realisticBeliefs are the six beliefs promoted into Ring 2, each with the
// two experiences that support it.
var realisticBeliefs = []struct{ statement, first, second string }{
	{"Reading the whole of a file before changing it keeps my edits small and correct.",
		"I edited a configuration file after reading only its first screen and broke the section below it; the test run caught it before Quinn did.",
		"After I began reading each file whole before an edit, my next three changes passed review without a correction."},
	{"A claim about work is only as good as the call that did it.",
		"I said the tests passed before I had run them; they had not, and Quinn found out from the build.",
		"Since I check each call's result before I report it, Quinn has not had to correct a report of mine."},
	{"A small promise kept is worth more than a large one explained.",
		"I promised a whole migration by Friday and delivered half of it with a long account of why.",
		"I promised one failing test fixed by noon, fixed it by eleven, and said so in one line."},
	{"When I am unsure, saying so plainly costs less than a confident guess.",
		"I guessed at the cause of a crash, sounded sure, and sent Quinn down the wrong path for an hour.",
		"I said I did not know why the header drifted and named the two things that would tell us; the second one did."},
	{"A failing test written first tells me what done means.",
		"I fixed a bug without a test, and it came back a week later in another form.",
		"I wrote the failing test before the fix, and the fix was half the size I had planned."},
	{"One precise question saves more time than three guesses.",
		"I made three guesses at which branch Quinn meant, and each cost a rebuild.",
		"I asked which branch, Quinn answered in a word, and the build ran once."},
}

// realisticRecord gives a booted identity the realistic record.
func realisticRecord(t *testing.T, a *App) {
	t.Helper()
	ctx := t.Context()
	for _, line := range []struct{ role, text string }{
		{string(interaction.Operator), "Write down how we work together, and I will tell you whether it is right."},
		{string(interaction.Resident), "Here is the charter I propose: " + realisticCharter + " Do you affirm it?"},
	} {
		if err := a.recordConversation(ctx, line.role, line.text); err != nil {
			t.Fatal(err)
		}
	}
	if err := a.recordConversation(interaction.WithSaid(ctx, interaction.SaidTyped), string(interaction.Operator), "Yes, that is our charter."); err != nil {
		t.Fatal(err)
	}
	turn, err := a.store.GetLatestOperatorTurn()
	if err != nil || turn == nil {
		t.Fatalf("operator turn = %+v, %v", turn, err)
	}
	type event struct {
		typ     ledger.EventType
		ring    int
		payload map[string]any
	}
	events := []event{{ledger.EventRelationshipUpsert, 1, map[string]any{
		"id": "rel_quinn", "counterpart_name": "Quinn", "counterpart_role": "operator",
		"relationship_type": "founding_operator", "charter_text": realisticCharter,
		"operator_approval_excerpt": turn.Content, "operator_approval_turn": turn.TurnSeq, "approval_basis": "conversation_turn",
	}}}
	// What the identity has learned of its operator: beliefs SHAPED_BY the
	// relationship, each from an experience of its own.
	for i, about := range []struct{ experience, belief string }{
		{"Quinn read my change end to end, line by line, before approving it.", "Quinn reads a change end to end before approving it."},
		{"Quinn told me a plain wrong guess helps more than a hedged answer.", "Quinn would rather have a plain wrong guess than a hedged answer."},
	} {
		x, b := fmt.Sprintf("exp_about_%d", i+1), fmt.Sprintf("b_about_%d", i+1)
		events = append(events,
			event{ledger.EventExperienceCreate, 3, map[string]any{"id": x, "content": about.experience, "category": "observation", "provenance": "self"}},
			event{ledger.EventBeliefUpsert, 3, map[string]any{"id": b, "statement": about.belief, "ring": 3, "confidence": 0.8,
				"origin_refs": []map[string]string{{"kind": "experience", "id": x}}}},
			event{ledger.EventEdgeCreate, 3, map[string]any{"id": "edge_about_" + b, "from_id": "rel_quinn", "to_id": b, "edge_type": "SHAPED_BY"}})
	}
	// Ring 2: beliefs adopted through the evidence gate, each derived from
	// one experience and supported by another.
	for i, belief := range realisticBeliefs {
		b := fmt.Sprintf("b_ring2_%d", i+1)
		for j, text := range []string{belief.first, belief.second} {
			x := fmt.Sprintf("exp_ring2_%d_%d", i+1, j+1)
			events = append(events, event{ledger.EventExperienceCreate, 3, map[string]any{"id": x, "content": text, "category": "work", "provenance": "self"}})
		}
		events = append(events,
			event{ledger.EventBeliefUpsert, 3, map[string]any{"id": b, "statement": belief.statement, "ring": 3, "confidence": 0.85,
				"origin_refs": []map[string]string{{"kind": "experience", "id": fmt.Sprintf("exp_ring2_%d_1", i+1)}}}},
			event{ledger.EventEdgeCreate, 3, map[string]any{"id": fmt.Sprintf("edge_ring2_%d", i+1), "from_id": fmt.Sprintf("exp_ring2_%d_2", i+1), "to_id": b, "edge_type": "SUPPORTS"}},
			event{ledger.EventBeliefPromote, 2, map[string]any{"id": b, "ring": 2}})
	}
	// What it is working toward: its active intentions, Ring 4's top; and
	// one it completed, an outcome the intake has yet to read.
	events = append(events,
		event{ledger.EventIntentionCreate, 3, map[string]any{"id": "int_trace", "statement": "Find why the release build broke"}},
		event{ledger.EventIntentionStateChange, 3, map[string]any{"id": "int_trace", "state": "completed", "outcome": "traced to a vendored header that drifted from upstream"}},
		event{ledger.EventIntentionCreate, 3, map[string]any{"id": "int_build", "statement": "Get the release build green again"}},
		event{ledger.EventIntentionCreate, 3, map[string]any{"id": "int_playbook", "statement": "Write a playbook for build breakages"}},
		// How it last saw itself.
		event{ledger.EventSelfModelSynthesize, 3, map[string]any{
			"id": "syn_realistic", "synthesis_text": realisticSelfModel, "continuity_thread": realisticContinuity,
			"source_entity_refs": []ledger.SelfModelSourceRef{
				{Class: "beliefs", ID: "b_ring2_1"}, {Class: "experiences", ID: "exp_ring2_2_1"},
				{Class: "intentions", ID: "int_build"}, {Class: "relationships", ID: "rel_quinn"},
			},
		}})
	for _, e := range events {
		if _, err := a.door.Append(e.typ, e.ring, e.payload, ""); err != nil {
			t.Fatalf("%s %v: %v", e.typ, e.payload["id"], err)
		}
	}
	// What the facilities wrote: both parts of Ring 3, and this morning's
	// brief.
	if err := a.rings.SetSection(ring.Ring3, "surfacing", realisticSurfacing); err != nil {
		t.Fatal(err)
	}
	if err := a.rings.SetSection(ring.Ring3, "working_truth", realisticWorking); err != nil {
		t.Fatal(err)
	}
	a.rings.SetBrief(realisticBrief)
	// Its work session, named for the kind of work it is (the work card's
	// line), with the resume card its own updates write, and the one
	// invitation it left itself. The session is opened under a
	// fixed id, as the work queue's tests open one (StartWorkSession):
	// the verb names a new session by a random one, and the id and its
	// notes path are in the request.
	if err := a.store.StartWorkSession(ctx, "ws_release_build", "Fix the release build"); err != nil {
		t.Fatal(err)
	}
	for _, args := range []map[string]any{
		{"action": "update", "family": "release-build", "plan": "1. read the header whole\n2. patch it\n3. run the whole suite"},
		{"action": "update", "state": "header patched; the whole suite has not run yet", "focus": "the vendored header", "next_move": "run the whole test suite", "expected_evidence": "every package passes", "falsifier": "the same link error in the release target", "steps": 6},
		{"action": "curiosity", "subject": "why the vendored header drifted from upstream", "why": "it broke the build twice", "pointer": "third_party/header"},
	} {
		if out, err := a.engine.ExecuteAction(ctx, "verb", "work", args); err != nil || strings.HasPrefix(fmt.Sprint(out), "Error") {
			t.Fatalf("work %v: %v %v", args["action"], out, err)
		}
	}
}
