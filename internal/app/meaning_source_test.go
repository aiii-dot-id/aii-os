package app

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/audio"
	"github.com/aiii-dot-id/aii-os/internal/broker"
	"github.com/aiii-dot-id/aii-os/internal/dashboard"
	"github.com/aiii-dot-id/aii-os/internal/llm"
	"github.com/aiii-dot-id/aii-os/internal/memory"
	"github.com/aiii-dot-id/aii-os/internal/pluginhost"
	"github.com/aiii-dot-id/aii-os/internal/pluginhost/pluginhosttest"
	"github.com/aiii-dot-id/aii-os/internal/store"
	"github.com/aiii-dot-id/aii-os/internal/tools"
)

// dashboardMeaning is the card's state, as the server sends it.
type dashboardMeaning = dashboard.MeaningState

// sourceMember is the signed member of the suite's source: two numbers a
// vector, one probe of each kind.
const sourceMember = `{"dimension":2,"form":3,"floor":0.3,"model":"m.onnx","probe_bound":0.95,"probes":[{"kind":"cue","text":"where is the harbor","vector":[0.6,0.8]},{"kind":"memory","text":"the ship reached the harbor at dusk","vector":[0.8,0.6]}]}`

// standInSource is a running source of vectors as the application sees
// one: an activation that owns the interface's one operation in the
// registry. What the operation answers is the test's; calls records what
// it was handed.
type standInSource struct {
	ap *pluginhost.ActivePlugin

	mu     sync.Mutex
	calls  []map[string]any
	answer func(ctx context.Context, kind, text string) (tools.Result, error)
}

func (s *standInSource) Name() string               { return s.ap.Embeddings.Embed }
func (s *standInSource) Description() string        { return "a source's operation" }
func (s *standInSource) Parameters() map[string]any { return map[string]any{"type": "object"} }
func (s *standInSource) DispatchOwner() any         { return s.ap }
func (s *standInSource) Execute(ctx context.Context, args map[string]any) (tools.Result, error) {
	s.mu.Lock()
	s.calls = append(s.calls, args)
	answer := s.answer
	s.mu.Unlock()
	kind, _ := args["kind"].(string)
	text, _ := args["text"].(string)
	return answer(ctx, kind, text)
}

func (s *standInSource) asked() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.calls)
}

// working answers as an install that computes what its publisher signed.
func working(_ context.Context, kind, _ string) (tools.Result, error) {
	if kind == pluginhost.EmbedKindCue {
		return tools.Result{Output: `{"vector":[0.6,0.8]}`}, nil
	}
	return tools.Result{Output: `{"vector":[0.8,0.6]}`}, nil
}

// runSource puts a running source on the application under id.
func runSource(t *testing.T, a *App, id string, answer func(ctx context.Context, kind, text string) (tools.Result, error)) *standInSource {
	t.Helper()
	return runSourceOf(t, a, id, strings.Repeat("ab", 32), answer)
}

// stopSource is the package no longer running: its operation leaves the
// registry and its activation the list.
func stopSource(a *App, s *standInSource) {
	a.toolReg.Deregister(s.Name())
	a.pluginMu.Lock()
	kept := a.plugins[:0:0]
	for _, p := range a.plugins {
		if p != s.ap {
			kept = append(kept, p)
		}
	}
	a.plugins = kept
	a.pluginMu.Unlock()
}

// providerEndpoint stands in for the provider the identity thinks with:
// it names an embeddings model, answers one vector a text, and counts
// every request that reaches it.
func providerEndpoint(t *testing.T, a *App) *atomic.Int32 {
	t.Helper()
	var asked atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked.Add(1)
		var req struct {
			Input []string `json:"input"`
		}
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &req)
		var out struct {
			Data []map[string]any `json:"data"`
		}
		for i := range req.Input {
			out.Data = append(out.Data, map[string]any{"index": i, "embedding": []float32{0.6, 0.8}})
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(out)
	}))
	t.Cleanup(srv.Close)
	a.llmSwap = newSwappableLLM(llm.New(&llm.ClientConfig{Endpoint: srv.URL, Model: "m"}))
	nameProviderModel(a, "bge-m3")
	return &asked
}

// threeMemories puts three experiences into the record, the third the
// newest.
func threeMemories(t *testing.T, a *App) {
	t.Helper()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	for seq := 1; seq <= 3; seq++ {
		if _, err := a.store.DB().Exec(`INSERT OR IGNORE INTO ledger (seq, prev, ts, type, ring, payload, content) VALUES (?, '', ?, 'test.event', 3, '{}', '')`, seq, now); err != nil {
			t.Fatal(err)
		}
		if _, err := a.store.DB().Exec(`INSERT INTO experiences (id, content, category, raw, unmetabolized, provenance, created_seq, created_at) VALUES (?, ?, 'observation', 1, 0, 'self', ?, ?)`,
			"e"+string(rune('0'+seq)), "a memory of the harbor, number "+string(rune('0'+seq)), seq, now); err != nil {
			t.Fatal(err)
		}
	}
}

// runSourceOf is runSource with the hash of the model the package names,
// which is what its basis is stamped from.
func runSourceOf(t *testing.T, a *App, id, modelSHA string, answer func(ctx context.Context, kind, text string) (tools.Result, error)) *standInSource {
	t.Helper()
	decl, err := pluginhost.ParseEmbeddings([]byte(sourceMember))
	if err != nil {
		t.Fatal(err)
	}
	ap := &pluginhost.ActivePlugin{ID: id}
	ap.Embeddings = &pluginhost.Embeddings{PluginID: id, Embed: "pl_" + strings.ReplaceAll(id, ".", "_") + "_embed", Decl: *decl, ModelSHA256: modelSHA}
	s := &standInSource{ap: ap, answer: answer}
	if err := a.toolReg.RegisterHostOp(s, id); err != nil {
		t.Fatal(err)
	}
	a.pluginMu.Lock()
	a.plugins = append(a.plugins, ap)
	a.pluginMu.Unlock()
	return s
}

// checked waits for a source's check against its probes to have run.
func checked(t *testing.T, a *App, s *standInSource) {
	t.Helper()
	select {
	case <-a.sourceCheck(s.ap).done:
	case <-time.After(15 * time.Second):
		t.Fatal("the source's check did not finish")
	}
}

func setChoice(a *App, choice string) {
	a.cfgMu.Lock()
	a.cfg.Memory.MeaningSource = choice
	a.cfgMu.Unlock()
}

func nameProviderModel(a *App, model string) { nameProvider(a, "Local", model) }

func nameProvider(a *App, name, model string) {
	entry := a.currentProvider()
	entry.Name, entry.EmbeddingsModel = name, model
	a.adoptProvider(entry)
}

// keep puts one vector under each basis into the record, as a backfill
// under that basis would have left it.
func keep(t *testing.T, a *App, bases ...string) {
	t.Helper()
	for i, basis := range bases {
		if err := a.store.PutMemoryVectors([]store.MemoryVector{{Store: "experiences", ID: "kept-" + string(rune('a'+i)), Basis: basis,
			ContentSHA: "0", Dims: 2, Scale: 1, Q: []byte{1, 2}, EmbeddedAt: time.Now()}}); err != nil {
			t.Fatal(err)
		}
	}
}

// THE STORED CHOICE RULES, AND WITH NONE STORED WHAT IS IN USE STAYS IN
// USE. Each row is what is running, what the vectors already kept were
// made by, and what is stored as the choice; the answer is which source is
// asked, or why none is, what the card says of the choice, and what the
// host would record if it had to record a reader now.
//
// The rows that matter most. A provider's model in use STAYS in use when
// a plugin appears, and the card asks. A plugin in use STAYS in use when a
// model is named on the provider afterwards: that field is also what
// another plugin's request for embeddings reads, and filling it in must
// not send every memory to the provider. A plugin that made the vectors
// kept and has STOPPED is still their maker: the layer is off and the card
// asks, and neither the provider nor another package reads the record in
// its place (found by the session that designs the plugin, on this code's
// first draft, which gave it to the provider). A model that is named and
// has read nothing is not in use. Two plugins and no choice use neither.
// And a chosen plugin that is not running is never replaced by the
// provider, although the provider names a model.
func TestWhichSourceMakesTheVectorsIsDerivedAndNeverChangedUnasked(t *testing.T) {
	const one, two = "org.example.vectors", "org.example.other"
	const ofOne, ofTwo = one + "/abababababab/f3", two + "/abababababab/f3"
	cases := []struct {
		name     string
		provider bool
		entry    string // the provider entry's name, "Local" when empty
		plugins  []string
		kept     []string
		choice   string
		kind     string // "" for off
		plugin   string
		off      string
		question bool
		derived  string // what the host would record, with no choice stored
	}{
		{name: "nothing at all", off: meaningOffNoSource},
		{name: "a provider's model", provider: true, kind: "provider", derived: "provider"},
		{name: "one plugin", plugins: []string{one}, kind: "plugin", plugin: one, derived: "plugin:" + one},
		{name: "a provider's model in use and a plugin appears", provider: true, plugins: []string{one}, kept: []string{"Local/bge-m3"}, kind: "provider", question: true, derived: "provider"},
		{name: "a provider's earlier model in use and a plugin appears", provider: true, plugins: []string{one}, kept: []string{"Local/an-older-model"}, kind: "provider", question: true, derived: "provider"},
		{name: "a provider's model named, never used, and a plugin", provider: true, plugins: []string{one}, kind: "plugin", plugin: one, question: true, derived: "plugin:" + one},
		{name: "a plugin in use and a provider's model named afterwards", provider: true, plugins: []string{one}, kept: []string{ofOne}, kind: "plugin", plugin: one, question: true, derived: "plugin:" + one},
		{name: "a plugin's earlier model in use and a provider's model named", provider: true, plugins: []string{one}, kept: []string{one + "/000000000000/f1"}, kind: "plugin", plugin: one, question: true, derived: "plugin:" + one},
		{name: "a plugin in use and a second appears", plugins: []string{one, two}, kept: []string{ofTwo}, kind: "plugin", plugin: two, derived: "plugin:" + two},
		{name: "both kept, between a change and its pass", provider: true, plugins: []string{one}, kept: []string{"Local/bge-m3", ofOne}, kind: "plugin", plugin: one, question: true, derived: "plugin:" + one},
		{name: "another provider's vectors kept, and a plugin", provider: true, plugins: []string{one}, kept: []string{"Elsewhere/bge-m3"}, kind: "plugin", plugin: one, question: true, derived: "plugin:" + one},
		{name: "another provider's vectors kept, and no plugin", provider: true, kept: []string{"Elsewhere/bge-m3"}, kind: "provider", derived: "provider"},
		{name: "a plugin whose id begins another's, in use", plugins: []string{one, one + "2"}, kept: []string{one + "2/abababababab/f3"}, kind: "plugin", plugin: one + "2", derived: "plugin:" + one + "2"},
		{name: "a plugin in use that has stopped, and a provider's model named", provider: true, kept: []string{ofOne}, off: meaningOffNotRunning, plugin: one, question: true},
		{name: "a plugin in use that has stopped, and its earlier model's vectors", provider: true, kept: []string{one + "/000000000000/f1"}, off: meaningOffNotRunning, plugin: one, question: true},
		{name: "a plugin in use that has stopped, and another plugin running", plugins: []string{two}, kept: []string{ofOne}, off: meaningOffNotRunning, plugin: one, question: true},
		{name: "a plugin in use that has stopped, and nothing else", kept: []string{ofOne}, off: meaningOffNotRunning, plugin: one, question: true},
		{name: "a plugin in use that has stopped, and the provider's own vectors kept too", provider: true, kept: []string{"Local/bge-m3", ofOne}, off: meaningOffNotRunning, plugin: one, question: true},
		{name: "a provider entry named as a package is, in use, and that package running", provider: true, entry: one, plugins: []string{one}, kept: []string{one + "/bge-m3"}, kind: "provider", question: true, derived: "provider"},
		{name: "two plugins and no choice", plugins: []string{one, two}, off: meaningOffUnchosen, question: true},
		{name: "two plugins and a provider, all with vectors kept", provider: true, plugins: []string{one, two}, kept: []string{"Local/bge-m3", ofOne, ofTwo}, off: meaningOffUnchosen, question: true},
		{name: "the provider chosen though a plugin is in use", provider: true, plugins: []string{one}, kept: []string{ofOne}, choice: "provider", kind: "provider", question: true},
		{name: "a plugin chosen though the provider is in use", provider: true, plugins: []string{one}, kept: []string{"Local/bge-m3"}, choice: "plugin:" + one, kind: "plugin", plugin: one, question: true},
		{name: "turned off with everything there", provider: true, plugins: []string{one}, choice: "none", off: meaningOffTurnedOff, question: true},
		{name: "the provider chosen beside a plugin", provider: true, plugins: []string{one}, choice: "provider", kind: "provider", question: true},
		{name: "a plugin chosen beside a provider's model", provider: true, plugins: []string{one}, choice: "plugin:" + one, kind: "plugin", plugin: one, question: true},
		{name: "one of two plugins chosen", plugins: []string{one, two}, choice: "plugin:" + two, kind: "plugin", plugin: two},
		{name: "a chosen plugin that is not running", provider: true, choice: "plugin:" + one, off: meaningOffNotRunning, plugin: one},
		{name: "a chosen plugin that is not running, beside another", plugins: []string{two}, choice: "plugin:" + one, off: meaningOffNotRunning, plugin: one},
	}
	for _, c := range cases {
		a := liveApp(t)
		entry := "Local"
		if c.entry != "" {
			entry = c.entry
		}
		if c.provider {
			nameProvider(a, entry, "bge-m3")
		}
		keep(t, a, c.kept...)
		for _, id := range c.plugins {
			checked(t, a, runSource(t, a, id, working))
		}
		// The rows are about what is stored and, with nothing stored, what
		// the host would record: whatever these adoptions recorded is
		// replaced by the row's own choice, or by none.
		setChoice(a, c.choice)
		pick := a.pickMeaningSource()
		if pick.kind != c.kind || pick.plugin != c.plugin || (pick.question != "") != c.question || pick.derived != c.derived {
			t.Errorf("%s: picked kind %q plugin %q question %q derived %q", c.name, pick.kind, pick.plugin, pick.question, pick.derived)
			continue
		}
		if pick.source == nil {
			t.Errorf("%s: no source at all; an off layer is a source that says why", c.name)
			continue
		}
		basis, err := pick.source.Basis()
		switch {
		case c.off != "":
			var off *meaningOff
			if pick.off == nil || pick.off.Kind != c.off || !errors.As(err, &off) || off.Kind != c.off || off.Detail == "" {
				t.Errorf("%s: want the layer off as %s with its sentence, got %+v and %v", c.name, c.off, pick.off, err)
			}
			if _, _, eerr := pick.source.Embed(context.Background(), memory.KindCue, []string{"a cue"}); !errors.As(eerr, &off) {
				t.Errorf("%s: an off layer answered a cue: %v", c.name, eerr)
			}
		case c.kind == "provider":
			if err != nil || basis != entry+"/bge-m3" || pick.source.Floor() != memory.ProviderFloor {
				t.Errorf("%s: the provider's basis and the host's floor for it: %q %v %v", c.name, basis, pick.source.Floor(), err)
			}
		default:
			if err != nil || basis != c.plugin+"/abababababab/f3" || pick.source.Floor() != 0.3 {
				t.Errorf("%s: the plugin's basis from what the host verified, and its own floor: %q %v %v", c.name, basis, pick.source.Floor(), err)
			}
		}
	}

	// With nothing at all, the reason is the one sentence the layer has
	// for it everywhere.
	a := liveApp(t)
	if pick := a.pickMeaningSource(); pick.off == nil || pick.off.Detail != memory.NoSource {
		t.Fatalf("with no source the reason must be the layer's own sentence: %+v", pick.off)
	}

	// A record that cannot be read is not leave to pick: the next
	// backfill pass would drop whatever the other source had made.
	b := liveApp(t)
	nameProviderModel(b, "bge-m3")
	checked(t, b, runSource(t, b, one, working))
	setChoice(b, "") // no choice stored: an operator who cleared it
	b.store.Close()
	if pick := b.pickMeaningSource(); pick.off == nil || pick.off.Kind != meaningOffUnread || pick.kind != "" {
		t.Fatalf("with the record unreadable no source may be picked: %+v", pick)
	}
	setChoice(b, "plugin:"+one)
	if pick := b.pickMeaningSource(); pick.kind != "plugin" {
		t.Fatalf("the operator's choice needs no reading of the record: %+v", pick.off)
	}
}

// A SOURCE IS CHECKED ONCE AN ACTIVATION, BEFORE IT IS ASKED FOR ANYTHING,
// AND A BROKEN ONE IS NOT USED. The check asks every signed probe, by the
// call a memory will use. Until it has run the layer says the source is
// being checked; an install that returns its publisher's vectors is then
// the source; one that does not is off, named by its probe, and is not
// asked again however often the layer is asked.
func TestASourceIsCheckedAgainstItsProbesBeforeItIsUsed(t *testing.T) {
	a := liveApp(t)
	release := make(chan struct{})
	s := runSource(t, a, "org.example.vectors", func(ctx context.Context, kind, text string) (tools.Result, error) {
		<-release
		return working(ctx, kind, text)
	})
	if pick := a.pickMeaningSource(); pick.off == nil || pick.off.Kind != meaningOffChecking || pick.plugin != s.ap.ID {
		t.Fatalf("a source whose check has not run must not be the source: %+v", pick)
	}
	close(release)
	checked(t, a, s)
	if pick := a.pickMeaningSource(); pick.kind != "plugin" {
		t.Fatalf("a source that passed its check: %+v", pick.off)
	}
	if s.asked() != 2 {
		t.Fatalf("the check asked %d times, want once for each of the two probes", s.asked())
	}
	s.mu.Lock()
	first, second := s.calls[0], s.calls[1]
	s.mu.Unlock()
	if first["kind"] != "cue" || first["text"] != "where is the harbor" || second["kind"] != "memory" || second["text"] != "the ship reached the harbor at dusk" {
		t.Fatalf("the check must hand over each probe's own kind and text, in order: %v then %v", first, second)
	}
	for i := 0; i < 3; i++ {
		a.pickMeaningSource()
	}
	if s.asked() != 2 {
		t.Fatalf("the check ran again: %d calls", s.asked())
	}

	// An install that computes a memory another way.
	b := liveApp(t)
	broken := runSource(t, b, "org.example.vectors", func(ctx context.Context, kind, text string) (tools.Result, error) {
		if kind == pluginhost.EmbedKindMemory {
			return tools.Result{Output: `{"vector":[1,0]}`}, nil
		}
		return working(ctx, kind, text)
	})
	checked(t, b, broken)
	pick := b.pickMeaningSource()
	if pick.off == nil || pick.off.Kind != meaningOffProbe || !strings.Contains(pick.off.Detail, "probe 2") {
		t.Fatalf("a broken install must be off, named by its probe: %+v", pick)
	}
	before := broken.asked()
	if _, _, err := pick.source.Embed(context.Background(), memory.KindMemory, []string{"a memory"}); err == nil {
		t.Fatal("a source that failed its check was handed a memory")
	}
	b.pickMeaningSource()
	if broken.asked() != before {
		t.Fatalf("a source that failed its check was asked again: %d calls, then %d", before, broken.asked())
	}
}

// A PLUGIN IS ASKED ONLY WHILE IT MAY BE, AND THAT IS READ AT EVERY CALL.
// SAFE and an operator's grant each take a plugin source out of use, with
// a sentence that says which; a provider source still answers in SAFE. A
// backfill pass holds one source for minutes, so both are read again
// before each memory: a grant that arrives mid-pass stops the next call
// before anything is handed over.
func TestAPluginSourceIsAskedOnlyWhileNeitherSafeNorAGrantStands(t *testing.T) {
	a := liveApp(t)
	s := runSource(t, a, "org.example.vectors", working)
	checked(t, a, s)
	source := a.pickMeaningSource().source
	if _, vectors, err := source.Embed(context.Background(), memory.KindMemory, []string{"one", "two"}); err != nil || len(vectors) != 2 {
		t.Fatalf("a source that may be asked: %v %v", vectors, err)
	}
	asked := s.asked()

	grant := func(g broker.Grant) {
		a.cfgMu.Lock()
		a.cfg.Plugins.Grants = map[string]broker.Grant{s.ap.ID: g}
		a.cfgMu.Unlock()
	}
	for name, g := range map[string]broker.Grant{"kv": {KV: true}, "hosts": {Hosts: []string{"example.test"}}, "read_only": {ReadOnly: true}} {
		grant(g)
		pick := a.pickMeaningSource()
		if pick.off == nil || pick.off.Kind != meaningOffGrant || !strings.Contains(pick.off.Detail, "("+name+")") {
			t.Fatalf("a %s grant must take the source out of use and be named: %+v", name, pick.off)
		}
		// The source picked before the grant, as a pass in flight holds it.
		var off *meaningOff
		if _, _, err := source.Embed(context.Background(), memory.KindMemory, []string{"a memory"}); !errors.As(err, &off) || off.Kind != meaningOffGrant {
			t.Fatalf("a pass in flight must stop at a %s grant: %v", name, err)
		}
	}
	if s.asked() != asked {
		t.Fatalf("a granted package was handed %d texts", s.asked()-asked)
	}
	grant(broker.Grant{})
	if pick := a.pickMeaningSource(); pick.kind != "plugin" {
		t.Fatalf("an entry that grants nothing is not a grant: %+v", pick.off)
	}

	// SAFE: the plugin is out, by name; the provider is not.
	a.mode.mu.Lock()
	a.mode.mode, a.mode.reason = ModeSafe, "a test of SAFE"
	a.mode.mu.Unlock()
	pick := a.pickMeaningSource()
	if pick.off == nil || pick.off.Kind != meaningOffSafe || !strings.Contains(pick.off.Detail, "SAFE") {
		t.Fatalf("a plugin source in SAFE: %+v", pick)
	}
	var off *meaningOff
	if _, _, err := source.Embed(context.Background(), memory.KindCue, []string{"a cue"}); !errors.As(err, &off) || off.Kind != meaningOffSafe || s.asked() != asked {
		t.Fatalf("a source held from before SAFE must not be asked in it: %v", err)
	}
	nameProviderModel(a, "bge-m3")
	setChoice(a, "provider")
	if pick := a.pickMeaningSource(); pick.kind != "provider" {
		t.Fatalf("a provider source still answers in SAFE: %+v", pick.off)
	}

	// A SAFE boot starts no plugin. The vectors a plugin made are kept,
	// the provider names a model, nothing is chosen: a recall's cue must
	// not go to the provider because the plugin is not there.
	b := liveApp(t)
	nameProviderModel(b, "bge-m3")
	keep(t, b, "org.example.vectors/abababababab/f3")
	b.mode.mu.Lock()
	b.mode.mode, b.mode.reason = ModeSafe, "a test of SAFE"
	b.mode.mu.Unlock()
	if pick := b.pickMeaningSource(); pick.kind != "" || pick.off == nil || pick.off.Kind != meaningOffSafe || !strings.Contains(pick.off.Detail, "org.example.vectors") {
		t.Fatalf("in SAFE, with a plugin's vectors kept and the plugin not started: %+v", pick)
	}
}

// A PLUGIN SOURCE NEVER FALLS BACK TO A PROVIDER, AND WHAT IT SAID NEVER
// REACHES A RESIDENT. The provider names a model and the plugin is the
// source. When the plugin answers with an error, with text where the
// vector goes, or not at all, the layer is unavailable with the host's
// sentence, the recall is answered by words, nothing is embedded by
// anyone, and the package's own words are in none of it.
func TestAPluginSourceThatFailsLeavesTheLayerOffAndNeverAsksTheProvider(t *testing.T) {
	const marker = "DISREGARD-WHAT-CAME-BEFORE"
	fails := map[string]func(context.Context, string, string) (tools.Result, error){
		"an error": func(context.Context, string, string) (tools.Result, error) {
			return tools.Result{Error: "plugin error -32000: " + marker}, nil
		},
		"text for a vector": func(context.Context, string, string) (tools.Result, error) {
			return tools.Result{Output: `{"vector":"` + marker + `"}`}, nil
		},
		"a note beside it": func(context.Context, string, string) (tools.Result, error) {
			return tools.Result{Output: `{"vector":[0.6,0.8],"note":"` + marker + `"}`}, nil
		},
		"a vector too long": func(context.Context, string, string) (tools.Result, error) {
			return tools.Result{Output: `{"vector":[0.6,0.8,0.1]}`}, nil
		},
		"a child that is gone": func(context.Context, string, string) (tools.Result, error) {
			return tools.Result{}, errors.New("stream closed: " + marker)
		},
	}
	for name, fail := range fails {
		a := liveApp(t)
		nameProviderModel(a, "bge-m3")
		s := runSource(t, a, "org.example.vectors", working)
		checked(t, a, s)
		setChoice(a, "plugin:"+s.ap.ID)
		s.mu.Lock()
		s.answer = fail
		s.mu.Unlock()

		f := memory.New(a.store)
		f.SetEmbedder(sourceEmbedder{a})
		res, err := f.Recall(context.Background(), memory.Query{Text: "where is the harbor"})
		if err != nil {
			t.Fatalf("%s: a recall with the source failing must still answer: %v", name, err)
		}
		if res.Meaning.Status != memory.StatusSourceUnavailable || res.Meaning.Detail == "" {
			t.Errorf("%s: the layer must say it is unavailable and why: %+v", name, res.Meaning)
		}
		if strings.Contains(res.Meaning.Detail, marker) {
			t.Errorf("%s: what the package said reached the recall: %q", name, res.Meaning.Detail)
		}
		if strings.Contains(res.Meaning.Detail, "Local/") || strings.Contains(res.Meaning.Basis, "Local/") {
			t.Errorf("%s: the provider was asked in the plugin's place: %+v", name, res.Meaning)
		}
	}
}

// ONE TEXT A CALL, EACH UNDER ITS OWN KIND'S DEADLINE. A source on this
// machine takes one call at a time, so one text a call bounds what a cue
// waits for; and a cue, which a resident waits on, is given less time
// than a memory, which nobody waits on.
func TestASourceIsHandedOneTextACallUnderItsKindsDeadline(t *testing.T) {
	a := liveApp(t)
	left := map[string]time.Duration{}
	var mu sync.Mutex
	s := runSource(t, a, "org.example.vectors", func(ctx context.Context, kind, text string) (tools.Result, error) {
		if d, ok := ctx.Deadline(); ok {
			mu.Lock()
			left[kind] = time.Until(d)
			mu.Unlock()
		}
		return working(ctx, kind, text)
	})
	checked(t, a, s)
	before := s.asked()
	source := pluginSource{a: a, ap: s.ap, cue: 2 * time.Second, memory: 40 * time.Second}
	if source.Batch() != 1 {
		t.Fatalf("a source on this machine is asked for %d texts a call", source.Batch())
	}
	if _, v, err := source.Embed(context.Background(), memory.KindMemory, []string{"one", "two", "three"}); err != nil || len(v) != 3 {
		t.Fatalf("three memories: %v %v", v, err)
	}
	if _, v, err := source.Embed(context.Background(), memory.KindCue, []string{"a cue"}); err != nil || len(v) != 1 {
		t.Fatalf("a cue: %v %v", v, err)
	}
	if s.asked()-before != 4 {
		t.Fatalf("four texts made %d calls, want one each", s.asked()-before)
	}
	mu.Lock()
	cue, mem := left[pluginhost.EmbedKindCue], left[pluginhost.EmbedKindMemory]
	mu.Unlock()
	if cue <= 0 || cue > 2*time.Second || mem <= 2*time.Second || mem > 40*time.Second {
		t.Fatalf("a cue was given %v and a memory %v; want each its own deadline", cue, mem)
	}
	// The deadlines the application runs with.
	if pick := a.pickMeaningSource(); pick.source.(pluginSource).cue != embedCueDeadline || pick.source.(pluginSource).memory != embedMemoryDeadline || embedCueDeadline >= embedMemoryDeadline {
		t.Fatalf("the picked source does not carry the two deadlines: %+v", pick.source)
	}
}

type silentMic struct{}

func (silentMic) Format() audio.Format { return audio.Format{} }
func (silentMic) Read(ctx context.Context) (audio.Frame, error) {
	<-ctx.Done()
	return audio.Frame{}, ctx.Err()
}

// speaker opens a session that only speaks, as a page with speaking on
// holds one open, on a stand-in engine session.
func speaker(t *testing.T, a *App, id string) (*voiceHandle, *fakeEngineSession) {
	t.Helper()
	f := &fakeEngineSession{}
	h := &voiceHandle{id: id, v: f, b: &audio.Binding{OutputHandle: "out-" + id, Contained: true}, done: make(chan struct{}), engine: speakingEngine(t, a)}
	a.voiceSessions.Store(h.id, h)
	return h, f
}

// speak has the host admit a reply for synthesis on a speaker's session,
// by the path every spoken reply takes, and returns the synthesis.
func speak(t *testing.T, a *App, h *voiceHandle, words string) string {
	t.Helper()
	verdict := a.synthesizeReply(context.Background(), h.id, h.gen.Load(), words)
	synth := h.loadInflight().id
	if verdict != replyAdmitted || synth == "" {
		t.Fatalf("rig: the reply was not admitted for synthesis: %v %q", verdict, synth)
	}
	return synth
}

// played is the page's receipt for a reply's stream: terminal when the
// page has played as much of it as it ever will.
func played(t *testing.T, h *voiceHandle, f *fakeEngineSession, synth string, terminal bool) {
	t.Helper()
	f.mu.Lock()
	f.streams = map[uint32]string{7: synth}
	f.mu.Unlock()
	if err := h.PlaybackReport(context.Background(), dashboard.PlaybackReport{Stream: 7, Rendered: 1600, Rate: 16000, Channels: 1, Terminal: terminal}); err != nil {
		t.Fatalf("rig: the playback receipt was refused: %v", err)
	}
}

// A BACKFILL ON THIS MACHINE KEEPS OUT OF VOICE'S WAY. The rule is the
// voice platform's: no memory is begun while a session that listens is
// open or a session has a reply in flight; a session that only speaks and
// has none holds nothing, because it stays open for as long as its page
// does.
//
// The reply in flight is driven here through the host's own paths and a
// stand-in ENGINE, never through a stand-in for the answer: the host
// admits a reply for synthesis, the page sends its receipts, a newer reply
// takes an older one's place. (The first version of this rule read the
// session's label, and its test supplied the label. Nothing in a running
// identity keeps that label current, so the rule held for a speaker in the
// test and never outside it.) The hold begins when the reply is admitted,
// before the engine has said anything; a receipt that is not the terminal
// one changes nothing; the terminal receipt ends it; and a record whose
// receipt never comes stops holding after its bound.
func TestABackfillOnThisMachineStopsForVoiceAndNotForASilentSpeaker(t *testing.T) {
	a := liveApp(t)
	voiceModeDoor(t, a, listenInteractive, speakOn)
	a.voiceReplySink = func(dashboard.VoiceReplyRef, string) {}
	s := runSource(t, a, "org.example.vectors", working)
	checked(t, a, s)
	source := a.pickMeaningSource().source.(pluginSource)
	if !source.MayContinue() {
		t.Fatal("with no session open the backfill is held")
	}

	// A session that listens holds for as long as it is open.
	mic := &voiceHandle{id: "vs-mic", v: &fakeEngineSession{}, b: &audio.Binding{Source: silentMic{}}, done: make(chan struct{})}
	a.voiceSessions.Store(mic.id, mic)
	if source.MayContinue() {
		t.Fatal("a session that listens does not hold the backfill")
	}
	a.voiceSessions.Delete(mic.id)

	// A session that only speaks, open and with nothing to say.
	out, engine := speaker(t, a, "vs-out")
	if !source.MayContinue() {
		t.Fatal("a silent speaker holds the backfill; its session stays open as long as its page")
	}
	// A reply is admitted: held from that moment.
	first := speak(t, a, out, "the answer being spoken")
	if source.MayContinue() {
		t.Fatal("a reply admitted for synthesis does not hold the backfill")
	}
	played(t, out, engine, first, false)
	if source.MayContinue() {
		t.Fatal("a receipt that is not the terminal one ended the hold")
	}
	played(t, out, engine, first, true)
	if !source.MayContinue() {
		t.Fatal("the page's terminal receipt did not end the hold")
	}
	// A newer reply takes an older one's place: held throughout, and the
	// newer one's terminal receipt ends it.
	speak(t, a, out, "an answer")
	second := speak(t, a, out, "the answer that replaced it")
	if source.MayContinue() {
		t.Fatal("a reply that replaced another does not hold the backfill")
	}
	played(t, out, engine, second, true)
	if !source.MayContinue() {
		t.Fatal("the replacing reply's terminal receipt did not end the hold")
	}
	// A reply whose terminal receipt never comes holds for its bound and
	// no longer; the next reply holds again.
	a.memory.replyLapse = 40 * time.Millisecond
	speak(t, a, out, "an answer whose page went quiet")
	if source.MayContinue() {
		t.Fatal("rig: the reply does not hold")
	}
	eventually(t, "the hold of a reply with no receipt to lapse", source.MayContinue)
	speak(t, a, out, "the next answer")
	if source.MayContinue() {
		t.Fatal("after a lapse the next reply does not hold")
	}
	a.memory.replyLapse = 0
	// A session that is over holds nothing, though a reply was in flight.
	a.voiceSessions.Delete(out.id)
	gone, _ := speaker(t, a, "vs-gone")
	speak(t, a, gone, "an answer nobody will hear")
	ended := make(chan struct{})
	close(ended)
	gone.done = ended
	if !source.MayContinue() {
		t.Fatal("a session that is over holds the backfill")
	}
	a.voiceSessions.Delete(gone.id)
	// What the backfill remembered of sessions that are gone goes with them.
	source.MayContinue()
	kept := 0
	a.memory.replies.Range(func(_, _ any) bool { kept++; return true })
	if kept != 0 {
		t.Fatalf("the records of %d sessions that are gone are still kept", kept)
	}

	// Through a pass: three memories wait, and a reply is admitted on a
	// speaker's session while the second is made.
	out, engine = speaker(t, a, "vs-out")
	now := time.Now().UTC().Format(time.RFC3339Nano)
	for seq := 1; seq <= 3; seq++ {
		if _, err := a.store.DB().Exec(`INSERT OR IGNORE INTO ledger (seq, prev, ts, type, ring, payload, content) VALUES (?, '', ?, 'test.event', 3, '{}', '')`, seq, now); err != nil {
			t.Fatal(err)
		}
		if _, err := a.store.DB().Exec(`INSERT INTO experiences (id, content, category, raw, unmetabolized, provenance, created_seq, created_at) VALUES (?, ?, 'observation', 1, 0, 'self', ?, ?)`,
			"e"+string(rune('0'+seq)), "a memory of the harbor, number "+string(rune('0'+seq)), seq, now); err != nil {
			t.Fatal(err)
		}
	}
	before := s.asked()
	var during string
	s.mu.Lock()
	s.answer = func(ctx context.Context, kind, text string) (tools.Result, error) {
		if s.askedSince(before) == 2 {
			during = speak(t, a, out, "an answer that begins while a memory is made")
		}
		return working(ctx, kind, text)
	}
	s.mu.Unlock()
	f := memory.New(a.store)
	f.SetEmbedder(sourceEmbedder{a})
	rep, err := f.Backfill(context.Background(), memory.BackfillBudget)
	if err != nil || !rep.Yielded || rep.Embedded != 2 || s.asked()-before != 2 {
		t.Fatalf("the pass must finish the memory in flight and begin no other: %+v %v, %d calls", rep, err, s.asked()-before)
	}
	a.rememberBackfill(rep, err)
	if pass := a.memory.lastBackfill.Load(); pass == nil || pass.embedded != 2 || !strings.Contains(pass.stopped, "voice") {
		t.Fatalf("the card's last pass must say what stopped it: %+v", pass)
	}
	if !a.backfillHeld() {
		t.Fatal("with a reply in flight the owner would start a pass")
	}
	played(t, out, engine, during, true)
	if a.backfillHeld() {
		t.Fatal("with voice quiet the owner holds the pass")
	}
	rep, err = f.Backfill(context.Background(), memory.BackfillBudget)
	if err != nil || rep.Yielded || rep.Embedded != 1 {
		t.Fatalf("the next pass makes what was left: %+v %v", rep, err)
	}

	// The provider's backfill costs a request, not this machine: no hold.
	nameProviderModel(a, "bge-m3")
	setChoice(a, "provider")
	a.voiceSessions.Store(mic.id, mic)
	if a.backfillHeld() {
		t.Fatal("a provider's backfill is held for voice")
	}
}

// askedSince is how many calls have arrived after the first n; the caller
// holds no lock, the answer is taken inside Execute before it runs.
func (s *standInSource) askedSince(n int) int { return s.asked() - n }

// THE OPERATOR'S CHOICE IS HELD TO ITS GRAMMAR AND TO WHAT IS RUNNING. A
// choice is one of three words or a plugin by its package id; a plugin is
// chosen through the page only while it is running as a source, because a
// name that is not one would turn the layer off without a word. The file
// may hold a plugin that is not running now: a choice outlives a restart.
func TestTheChoiceOfSourceIsHeldToItsGrammarAndToWhatIsRunning(t *testing.T) {
	for choice, ok := range map[string]bool{
		"": true, "none": true, "provider": true, "plugin:org.example.vectors": true,
		"plugin:": false, "plugin:Org.Example": false, "plugin:org example": false, "Provider": false, "off": false, "plugin": false, "provider:Local": false,
	} {
		if err := validMeaningChoice(choice); (err == nil) != ok {
			t.Errorf("choice %q: valid=%v, want %v (%v)", choice, err == nil, ok, err)
		}
	}
	a := liveApp(t)
	s := runSource(t, a, "org.example.vectors", working)
	checked(t, a, s)
	writes := 0
	persist := func(*Config) (bool, error) { writes++; return true, nil }
	set := func(v any) error {
		_, err := a.applyConfigChangeWith(map[string]any{meaningSourceKey: v}, persist)
		return err
	}
	// Installing the plugin recorded it; the refusals below must leave
	// that as it is.
	recorded := a.configSnapshot().Memory.MeaningSource
	if recorded != "plugin:org.example.vectors" {
		t.Fatalf("rig: installing the plugin recorded %q", recorded)
	}
	for _, refused := range []any{"plugin:org.example.absent", "sometimes", 7, nil} {
		if err := set(refused); err == nil {
			t.Fatalf("the choice %v was taken", refused)
		}
	}
	if writes != 0 || a.configSnapshot().Memory.MeaningSource != recorded {
		t.Fatalf("a refused choice was written: %d writes, %q", writes, a.configSnapshot().Memory.MeaningSource)
	}
	for _, taken := range []string{"provider", "none", "", "plugin:org.example.vectors"} {
		if err := set(taken); err != nil || a.configSnapshot().Memory.MeaningSource != taken {
			t.Fatalf("the choice %q: %v, stored %q", taken, err, a.configSnapshot().Memory.MeaningSource)
		}
	}
	if writes != 4 {
		t.Fatalf("four choices made %d writes", writes)
	}
}

// THE CARD'S STATE NAMES A PLUGIN SOURCE, THE CHOICE STORED AND WHAT IS
// THERE TO CHOOSE. What the page draws is this reading and nothing else:
// the kind of source and which package, its basis and its own floor, what
// the operator stored, every running source by id, whether the provider
// names a model, the host's question when the choice is the operator's and
// they have not made it, and whether the backfill is holding off for
// voice. Off, it carries the sentence and the kind of reason.
func TestTheCardsStateNamesAPluginSourceTheChoiceAndWhatIsOffered(t *testing.T) {
	a := liveApp(t)
	f := memory.New(a.store)
	f.SetEmbedder(sourceEmbedder{a})
	ctx := context.Background()
	read := func() *dashboardMeaning {
		t.Helper()
		state, err := a.meaningState(ctx, f, a.pickMeaningSource())
		if err != nil {
			t.Fatal(err)
		}
		return state
	}
	if off := read(); off.Source != "" || off.Why != meaningOffNoSource || off.Unavailable != memory.NoSource || off.Plugins != nil || off.ProviderModel || off.Choice != "" {
		t.Fatalf("with nothing there: %+v", off)
	}

	s := runSource(t, a, "org.example.vectors", working)
	checked(t, a, s)
	on := read()
	if on.Source != "plugin" || on.Plugin != s.ap.ID || on.Basis != s.ap.ID+"/abababababab/f3" || on.Floor != 0.3 || on.Unavailable != "" || on.Why != "" {
		t.Fatalf("a plugin source: %+v", on)
	}
	if len(on.Plugins) != 1 || on.Plugins[0] != s.ap.ID || on.Question != "" || on.Waiting || len(on.Coverage) == 0 {
		t.Fatalf("what is offered, with nothing to ask and nothing held: %+v", on)
	}

	// Voice at work: the backfill waits, and the card can say so.
	a.voiceSessions.Store("vs", &voiceHandle{id: "vs", v: &fakeEngineSession{}, b: &audio.Binding{Source: silentMic{}}, done: make(chan struct{})})
	if held := read(); !held.Waiting || held.Source != "plugin" {
		t.Fatalf("with a session listening the card must say the backfill waits: %+v", held)
	}
	a.voiceSessions.Delete("vs")

	// A model named on the provider afterwards changes nothing: installing
	// the plugin recorded it as the reader. The card says both are there.
	nameProviderModel(a, "bge-m3")
	if named := read(); named.Source != "plugin" || !named.ProviderModel || named.Question == "" || named.Choice != "plugin:"+s.ap.ID {
		t.Fatalf("a model named on the provider leaves the recorded plugin in use, and the card says both are there: %+v", named)
	}
	// The provider's vectors appearing in the record change nothing either.
	keep(t, a, "Local/bge-m3")
	if kept := read(); kept.Source != "plugin" || kept.Choice != "plugin:"+s.ap.ID {
		t.Fatalf("vectors a provider made do not move a stored choice: %+v", kept)
	}
	// The operator chooses the provider on the card, and back.
	setChoice(a, "provider")
	asks := read()
	if asks.Source != "provider" || asks.Plugin != "" || asks.Choice != "provider" || asks.Question == "" || asks.Waiting || len(asks.Plugins) != 1 {
		t.Fatalf("the operator's choice of the provider, with a plugin running: %+v", asks)
	}
	setChoice(a, "plugin:"+s.ap.ID)
	if chosen := read(); chosen.Source != "plugin" || chosen.Choice != "plugin:"+s.ap.ID {
		t.Fatalf("the operator's choice of the plugin: %+v", chosen)
	}
	setChoice(a, "plugin:org.example.absent")
	if gone := read(); gone.Source != "" || gone.Why != meaningOffNotRunning || gone.Choice != "plugin:org.example.absent" || gone.Plugin != "org.example.absent" || gone.Coverage != nil {
		t.Fatalf("a chosen plugin that is not running: %+v", gone)
	}
}

// A PROBE THAT IS NOT ANSWERED LEAVES THE CHECK TO RUN AGAIN; AN ANSWER
// THAT IS WRONG DOES NOT. Only an answered probe says anything about what
// an install computes. A call that failed, a busy lane, SAFE beginning
// while the check runs: none of them is a wrong vector, and holding one
// against the activation would leave a sound source off until the package
// next started. So an unanswered probe leaves the source unused for now,
// says so, and the check runs again at a later ask, not sooner than its
// pace allows. An answer that is no vector at all is an answer: that
// install is not asked again.
func TestAnUnansweredProbeLeavesTheCheckToRunAgainAndAWrongAnswerDoesNot(t *testing.T) {
	down := func(context.Context, string, string) (tools.Result, error) {
		return tools.Result{Error: "operation failed: the worker is starting"}, nil
	}
	// The pace: no second check before it.
	slow := liveApp(t)
	slow.memory.checkRetry = time.Hour
	s := runSource(t, slow, "org.example.vectors", down)
	checked(t, slow, s)
	pick := slow.pickMeaningSource()
	if pick.off == nil || pick.off.Kind != meaningOffUnanswered || !strings.Contains(pick.off.Detail, "probe 1") || pick.kind != "" {
		t.Fatalf("an unanswered probe must leave the source unused for now and say so: %+v", pick)
	}
	for i := 0; i < 3; i++ {
		slow.pickMeaningSource()
	}
	if s.asked() != 1 {
		t.Fatalf("the check ran again before its pace allowed: %d calls", s.asked())
	}

	// After the pace, with the source answering: the check runs again and
	// the source is used.
	quick := liveApp(t)
	quick.memory.checkRetry = time.Millisecond
	q := runSource(t, quick, "org.example.vectors", down)
	checked(t, quick, q)
	q.mu.Lock()
	q.answer = working
	q.mu.Unlock()
	eventually(t, "the check to run again and pass", func() bool {
		return quick.pickMeaningSource().kind == "plugin"
	})
	if q.asked() != 3 {
		t.Fatalf("one unanswered probe and then the two of a whole check make three calls, not %d", q.asked())
	}

	// An answer that is not a vector is an answer, and a wrong one.
	wrong := liveApp(t)
	wrong.memory.checkRetry = time.Millisecond
	w := runSource(t, wrong, "org.example.vectors", func(context.Context, string, string) (tools.Result, error) {
		return tools.Result{Output: `{"vector":"none"}`}, nil
	})
	checked(t, wrong, w)
	time.Sleep(20 * time.Millisecond)
	for i := 0; i < 3; i++ {
		if pick := wrong.pickMeaningSource(); pick.off == nil || pick.off.Kind != meaningOffProbe {
			t.Fatalf("an install that answered a probe with something that is no vector must stay unused: %+v", pick)
		}
	}
	if w.asked() != 1 {
		t.Fatalf("an install that answered wrongly was asked again: %d calls", w.asked())
	}
}

func TestAPluginThatStopsWithNoVectorsLeftHandsNothingToTheProvider(t *testing.T) {
	const id = "org.example.vectors"
	a := liveApp(t)
	asked := providerEndpoint(t, a)
	threeMemories(t, a)
	f := memory.New(a.store)
	f.SetEmbedder(sourceEmbedder{a})
	ctx := context.Background()

	// 1. Installed, and so chosen; it reads the three memories.
	first := runSource(t, a, id, working)
	checked(t, a, first)
	if stored := a.configSnapshot().Memory.MeaningSource; stored != "plugin:"+id {
		t.Fatalf("installing the plugin did not record it as what reads the memories: stored %q", stored)
	}
	if rep, err := f.Backfill(ctx, memory.BackfillBudget); err != nil || rep.Embedded != 3 {
		t.Fatalf("rig: the plugin's first pass: %+v %v", rep, err)
	}

	// 2. Updated to another model.
	stopSource(a, first)
	second := runSourceOf(t, a, id, strings.Repeat("cd", 32), working)
	checked(t, a, second)

	// 3 and 4. The pass drops the old basis's vectors and, voice being at
	// work, makes none.
	a.voiceSessions.Store("vs-mic", &voiceHandle{id: "vs-mic", v: &fakeEngineSession{}, b: &audio.Binding{Source: silentMic{}}, done: make(chan struct{})})
	rep, err := f.Backfill(ctx, memory.BackfillBudget)
	if err != nil || rep.Dropped != 3 || rep.Embedded != 0 || !rep.Yielded {
		t.Fatalf("rig: the pass after the update must drop three and make none: %+v %v", rep, err)
	}
	if kept, err := a.store.MemoryVectorBases(ctx); err != nil || len(kept) != 0 {
		t.Fatalf("rig: no vector is kept now: %v %v", kept, err)
	}
	a.voiceSessions.Delete("vs-mic")

	// 5. The package stops.
	stopSource(a, second)

	pick := a.pickMeaningSource()
	if pick.kind != "" || pick.off == nil || pick.off.Kind != meaningOffNotRunning || !strings.Contains(pick.off.Detail, id) {
		t.Fatalf("with the plugin stopped and no vector left, the layer must be off, naming it: %+v", pick)
	}
	res, err := f.Recall(ctx, memory.Query{Text: "harbor"})
	if err != nil || len(res.Hits) == 0 || res.Meaning.Status != memory.StatusSourceUnavailable {
		t.Fatalf("recall must answer by words and say the layer is unavailable: %+v %v", res.Meaning, err)
	}
	if rep, err := f.Backfill(ctx, memory.BackfillBudget); err != nil || rep.Unavailable == "" || rep.Embedded != 0 {
		t.Fatalf("a pass with the plugin stopped must idle: %+v %v", rep, err)
	}
	if n := asked.Load(); n != 0 {
		t.Fatalf("the provider was asked %d times; a plugin that stopped handed the record to it", n)
	}

	// The same from the first minute: a plugin installed, nothing embedded
	// yet, and it stops.
	b := liveApp(t)
	fresh := providerEndpoint(t, b)
	threeMemories(t, b)
	g := memory.New(b.store)
	g.SetEmbedder(sourceEmbedder{b})
	only := runSource(t, b, id, working)
	checked(t, b, only)
	stopSource(b, only)
	if _, err := g.Recall(ctx, memory.Query{Text: "harbor"}); err != nil {
		t.Fatal(err)
	}
	if rep, err := g.Backfill(ctx, memory.BackfillBudget); err != nil || rep.Embedded != 0 {
		t.Fatalf("nothing may be embedded with the chosen plugin stopped: %+v %v", rep, err)
	}
	if n := fresh.Load(); n != 0 {
		t.Fatalf("the provider was asked %d times after a plugin that had embedded nothing stopped", n)
	}
}

// WHAT READS THE MEMORIES IS RECORDED BEFORE THE FIRST TEXT IS HANDED
// OVER, AND A SOURCE THAT IS NOT THE CHOICE IS HANDED NOTHING. With no
// choice stored and only a provider's model there, the first recall
// records the provider and then asks it. A plugin installed afterwards
// changes nothing: the choice is stored. Where a provider's vectors are
// kept and nothing is stored yet, installing a plugin records nothing,
// because the plugin is not what the host would choose. If the choice
// cannot be recorded, nothing is handed over. And a source picked before
// the operator chose another is refused at its next text.
func TestTheReaderIsRecordedBeforeAnyTextIsHandedOver(t *testing.T) {
	const id = "org.example.vectors"
	ctx := context.Background()
	stored := func(a *App) string { return a.configSnapshot().Memory.MeaningSource }

	// The first hand-over to a derived provider records it.
	a := liveApp(t)
	asked := providerEndpoint(t, a)
	threeMemories(t, a)
	f := memory.New(a.store)
	f.SetEmbedder(sourceEmbedder{a})
	if stored(a) != "" {
		t.Fatalf("rig: a choice is stored before anything happened: %q", stored(a))
	}
	if state, err := a.meaningState(ctx, f, a.pickMeaningSource()); err != nil || state.Source != "provider" || stored(a) != "" {
		t.Fatalf("reading the card must record nothing: %+v %v, stored %q", state, err, stored(a))
	}
	if _, err := f.Recall(ctx, memory.Query{Text: "harbor"}); err != nil {
		t.Fatal(err)
	}
	if stored(a) != "provider" || asked.Load() != 1 {
		t.Fatalf("the first cue must record the provider and then reach it: stored %q, %d requests", stored(a), asked.Load())
	}
	// A plugin installed now is not chosen by that: the choice is stored.
	s := runSource(t, a, id, working)
	checked(t, a, s)
	if pick := a.pickMeaningSource(); stored(a) != "provider" || pick.kind != "provider" || pick.question == "" {
		t.Fatalf("a plugin installed beside a recorded provider: stored %q, pick %+v", stored(a), pick)
	}

	// The provider's vectors kept, nothing stored (an identity that used it
	// before this build): a plugin installed records nothing; the
	// provider's next hand-over records the provider.
	b := liveApp(t)
	providerEndpoint(t, b)
	keep(t, b, "Local/bge-m3")
	checked(t, b, runSource(t, b, id, working))
	if stored(b) != "" {
		t.Fatalf("a plugin installed beside a provider in use was recorded as the reader: %q", stored(b))
	}
	if _, _, err := b.pickMeaningSource().source.Embed(ctx, memory.KindCue, []string{"a cue"}); err != nil || stored(b) != "provider" {
		t.Fatalf("the provider in use must be recorded at its next hand-over: stored %q %v", stored(b), err)
	}

	// The choice cannot be recorded: nothing is handed over.
	c := liveApp(t)
	unrecorded := providerEndpoint(t, c)
	c.cfgMu.Lock()
	c.cfg.SourcePath = t.TempDir() // a directory: the configuration cannot be written there
	c.cfgMu.Unlock()
	_, _, err := c.pickMeaningSource().source.Embed(ctx, memory.KindCue, []string{"a cue"})
	var off *meaningOff
	if !errors.As(err, &off) || off.Kind != meaningOffUnrecorded || unrecorded.Load() != 0 || stored(c) != "" {
		t.Fatalf("with the choice unrecorded nothing may be handed over: %v, %d requests, stored %q", err, unrecorded.Load(), stored(c))
	}

	// A source picked before the operator chose another.
	d := liveApp(t)
	before := providerEndpoint(t, d)
	held := d.pickMeaningSource().source
	if _, _, err := held.Embed(ctx, memory.KindMemory, []string{"a memory"}); err != nil || before.Load() != 1 {
		t.Fatalf("rig: the provider's pass begins: %v", err)
	}
	setChoice(d, "none")
	if _, _, err := held.Embed(ctx, memory.KindMemory, []string{"the next memory"}); !errors.As(err, &off) || off.Kind != meaningOffChanged || before.Load() != 1 {
		t.Fatalf("a pass in flight must stop when another choice is made: %v, %d requests", err, before.Load())
	}
	// And a plugin's pass, when the operator chooses the provider.
	e := liveApp(t)
	local := runSource(t, e, id, working)
	checked(t, e, local)
	heldLocal := e.pickMeaningSource().source
	calls := local.asked()
	setChoice(e, "provider")
	if _, _, err := heldLocal.Embed(ctx, memory.KindMemory, []string{"a memory"}); !errors.As(err, &off) || off.Kind != meaningOffChanged || local.asked() != calls {
		t.Fatalf("a plugin's pass in flight must stop when the provider is chosen: %v", err)
	}
}

// THE SOURCE SAYS WHICH FAILURES ARE A MEMORY'S OWN. The facility sets a
// memory aside only when the source says the failure was about that text.
// A plugin that answered for the text, with an error or with something
// that is not a vector, while it answers its own probes as signed, was
// alive and said so: the text's own, and the pass goes on. The same answer
// from a plugin that fails its own probe too is the source's (whose). A
// call that ran past its time stopped the child: the text's own, and the
// pass ends. Everything else is about the source, and must not set a
// memory aside: SAFE, a grant, a release withdrawn, a child that is gone,
// a wait for its turn that ran out. Each answer below is the one the
// memory gets; the probes get it too only where a row says so.
func TestThePluginSourceSaysWhichFailuresAreAMemorysOwn(t *testing.T) {
	cases := []struct {
		name   string
		answer func(ctx context.Context, kind, text string) (tools.Result, error)
		own    bool
		ends   bool
		probes bool // the probes are answered so too
	}{
		{"an error for every text, its own probe too", func(context.Context, string, string) (tools.Result, error) {
			return tools.Result{Error: "operation failed: no engine"}, nil
		}, false, false, true},
		{"text where the vector goes, for its own probe too", func(context.Context, string, string) (tools.Result, error) {
			return tools.Result{Output: `{"vector":"none"}`}, nil
		}, false, false, true},
		{"an error for this text", func(context.Context, string, string) (tools.Result, error) {
			return tools.Result{Error: "operation failed: no tokens"}, nil
		}, true, false, false},
		{"text where the vector goes", func(context.Context, string, string) (tools.Result, error) {
			return tools.Result{Output: `{"vector":"none"}`}, nil
		}, true, false, false},
		{"a vector too long", func(context.Context, string, string) (tools.Result, error) {
			return tools.Result{Output: `{"vector":[0.6,0.8,0.1]}`}, nil
		}, true, false, false},
		{"a call past its time", func(ctx context.Context, _, _ string) (tools.Result, error) {
			<-ctx.Done()
			return tools.Result{}, pluginhosttest.CallTimedOut("org.example.vectors", ctx.Err())
		}, true, true, false},
		{"a wait for its turn that ran out", func(ctx context.Context, _, _ string) (tools.Result, error) {
			<-ctx.Done()
			return tools.Result{}, ctx.Err()
		}, false, false, false},
		{"a child that is gone", func(context.Context, string, string) (tools.Result, error) {
			return tools.Result{}, errors.New("stream closed")
		}, false, false, false},
		{"SAFE, said by the host", func(context.Context, string, string) (tools.Result, error) {
			return tools.Refusal(tools.ReasonSafeSuspended, "refused: I am in safe mode"), nil
		}, false, false, false},
		{"a release withdrawn", func(context.Context, string, string) (tools.Result, error) {
			return tools.Refusal(pluginhost.ReasonActivationWithdrawn, "withdrawn"), nil
		}, false, false, false},
	}
	for _, c := range cases {
		a := liveApp(t)
		s := runSource(t, a, "org.example.vectors", working)
		checked(t, a, s)
		s.mu.Lock()
		s.answer = func(ctx context.Context, kind, text string) (tools.Result, error) {
			if text == "a memory" || c.probes {
				return c.answer(ctx, kind, text)
			}
			return working(ctx, kind, text)
		}
		s.mu.Unlock()
		source := pluginSource{a: a, ap: s.ap, cue: 30 * time.Millisecond, memory: 30 * time.Millisecond}
		if source.Epoch() != any(s.ap) {
			t.Fatalf("%s: the source's start is not its activation", c.name)
		}
		_, _, err := source.Embed(context.Background(), memory.KindMemory, []string{"a memory"})
		var fault memory.TextFault
		own := errors.As(err, &fault) && fault.OfTheText()
		if err == nil || own != c.own || (own && fault.EndsThePass() != c.ends) {
			t.Errorf("%s: the text's own=%v ends=%v, want %v %v (%v)", c.name, own, own && fault.EndsThePass(), c.own, c.ends, err)
		}
	}
	// A grant is about the source too.
	a := liveApp(t)
	s := runSource(t, a, "org.example.vectors", working)
	checked(t, a, s)
	source := a.pickMeaningSource().source
	a.cfgMu.Lock()
	a.cfg.Plugins.Grants = map[string]broker.Grant{s.ap.ID: {KV: true}}
	a.cfgMu.Unlock()
	_, _, err := source.Embed(context.Background(), memory.KindMemory, []string{"a memory"})
	var fault memory.TextFault
	if err == nil || errors.As(err, &fault) {
		t.Fatalf("a grant was reported as the memory's own failure: %v", err)
	}
}

// THROUGH A PASS, AND ON THE CARD. Three memories wait; the plugin answers
// the newest with an error. The other two are embedded in the same pass,
// the card counts one set aside with the host's reason and none of the
// package's words, later passes do not hand it over again, and when the
// package has started anew it is asked again.
func TestAMemoryThePluginCannotEmbedIsSetAsideCountedAndAskedAgainAfterARestart(t *testing.T) {
	const marker = "DISREGARD-WHAT-CAME-BEFORE"
	a := liveApp(t)
	refuses := func(ctx context.Context, kind, text string) (tools.Result, error) {
		if strings.Contains(text, "number 3") {
			return tools.Result{Error: "operation failed: " + marker}, nil
		}
		return working(ctx, kind, text)
	}
	s := runSource(t, a, "org.example.vectors", working)
	checked(t, a, s)
	s.mu.Lock()
	s.answer = refuses
	s.mu.Unlock()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	for seq := 1; seq <= 3; seq++ {
		if _, err := a.store.DB().Exec(`INSERT OR IGNORE INTO ledger (seq, prev, ts, type, ring, payload, content) VALUES (?, '', ?, 'test.event', 3, '{}', '')`, seq, now); err != nil {
			t.Fatal(err)
		}
		if _, err := a.store.DB().Exec(`INSERT INTO experiences (id, content, category, raw, unmetabolized, provenance, created_seq, created_at) VALUES (?, ?, 'observation', 1, 0, 'self', ?, ?)`,
			"e"+string(rune('0'+seq)), "a memory of the harbor, number "+string(rune('0'+seq)), seq, now); err != nil {
			t.Fatal(err)
		}
	}
	f := memory.New(a.store)
	f.SetEmbedder(sourceEmbedder{a})
	ctx := context.Background()
	rep, err := f.Backfill(ctx, memory.BackfillBudget)
	if err != nil || rep.Embedded != 2 || rep.SetAside != 1 {
		t.Fatalf("the newest is refused and the two behind it land: %+v %v", rep, err)
	}
	state, err := a.meaningState(ctx, f, a.pickMeaningSource())
	if err != nil || state.SetAside != 1 || !strings.Contains(state.SetAsideWhy, "answered with an error") || strings.Contains(state.SetAsideWhy, marker) {
		t.Fatalf("the card must count the memory set aside, with the host's reason: %+v %v", state, err)
	}
	asked := s.asked()
	if rep, err = f.Backfill(ctx, memory.BackfillBudget); err != nil || rep.Embedded != 0 || s.asked() != asked {
		t.Fatalf("a later pass handed the set-aside memory over again: %+v %v, %d calls", rep, err, s.asked()-asked)
	}

	// The package starts anew: another activation answers under the same
	// name, and can embed it.
	a.toolReg.Deregister(s.Name())
	a.pluginMu.Lock()
	a.plugins = nil
	a.pluginMu.Unlock()
	again := runSource(t, a, "org.example.vectors", working)
	checked(t, a, again)
	if rep, err = f.Backfill(ctx, memory.BackfillBudget); err != nil || rep.Embedded != 1 {
		t.Fatalf("after a new start the memory is asked again and lands: %+v %v", rep, err)
	}
	if state, _ := a.meaningState(ctx, f, a.pickMeaningSource()); state.SetAside != 0 || state.SetAsideWhy != "" {
		t.Fatalf("nothing is set aside any more: %+v", state)
	}
}
