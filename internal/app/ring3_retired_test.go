package app

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/cognitive"
	"github.com/aiii-dot-id/aii-os/internal/genesis"
	"github.com/aiii-dot-id/aii-os/internal/genesis/genesistest"
	"github.com/aiii-dot-id/aii-os/internal/llm"
)

const (
	ferryDawn = "The ferry to the island leaves the harbor at dawn"
	ferryNoon = "The ferry to the island leaves the harbor at noon since the timetable changed"
)

// ferryIdentity births an identity, boots it, and has one pass write a Ring 3
// that states the dawn claim. boot brings the same identity up again.
type ferryIdentity struct {
	t      *testing.T
	boot   func() (*App, *contScript)
	app    *App
	obs    *contScript
	dawnID string
	origin string
}

func newFerryIdentity(t *testing.T) *ferryIdentity {
	t.Helper()
	dir := t.TempDir()
	genesistest.NewRoot(t).Birth(t, genesis.BirthConfig{
		Name: "RingLag", KeyPath: filepath.Join(dir, "identity.sec"),
		LedgerPath: filepath.Join(dir, "ledger.jsonl"), DBPath: filepath.Join(dir, "aii.db"),
	}).Ledger.Close()
	f := &ferryIdentity{t: t}
	f.boot = func() (*App, *contScript) {
		app := newBareApp(t, &Config{
			Identity: IdentityConfig{KeyPath: filepath.Join(dir, "identity.sec"),
				LedgerPath: filepath.Join(dir, "ledger.jsonl"), DBPath: filepath.Join(dir, "aii.db")},
			LLM:        withTestProvider(t, dir, "test", "https://x", "m", "sk-x"),
			SourcePath: filepath.Join(dir, "config.json"),
			Dashboard:  DashboardConfig{Port: 0},
			Tools:      ToolsConfig{CWD: dir},
			Agency:     defaultConfig().Agency,
		})
		if err := startLiveForTest(app); err != nil {
			t.Fatal(err)
		}
		obs := &contScript{}
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			body, _ := io.ReadAll(r.Body)
			obs.add(string(body))
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, textReply("noted"))
		}))
		t.Cleanup(srv.Close)
		app.llmSwap.Swap(llm.New(&llm.ClientConfig{Endpoint: srv.URL, Model: "fake", MaxOutputTokens: 4096, Retries: -1}))
		return app, obs
	}
	f.app, f.obs = f.boot()
	noteID := func(args map[string]any) string {
		f.verb("note", args)
		exps, err := f.app.store.ListExperiences(1)
		if err != nil || len(exps) != 1 {
			t.Fatal(exps, err)
		}
		return exps[0].ID
	}
	f.origin = noteID(map[string]any{"content": "a prior observed fact", "unmetabolized": true})
	var said []string
	for _, c := range []string{"the ferry left the harbor at dawn again", "caught the dawn ferry from the harbor", "harbor at dawn: the ferry was on time"} {
		said = append(said, noteID(map[string]any{"content": c}))
	}
	env := `{"operations":[{"op":"upsert","statement":"` + ferryDawn + `","confidence":0.8,"evidence":["` + strings.Join(said, `","`) +
		`"]}],"ring3_view":"You believe the ferry to the island leaves the harbor at dawn."}`
	var shown string
	pass := cognitive.NewConsolidate(f.app.store, capturingLLM{out: env, user: &shown}, f.app.door,
		cognitive.NewPersistingRingWriter(f.app.rings, f.app.store), cognitive.ConsolidateConfig{Threshold: 3})
	if err := pass.Execute(context.Background()); err != nil {
		t.Fatal(err)
	}
	held, _ := f.app.store.ListBeliefs()
	for _, b := range held {
		if b.Statement == ferryDawn {
			f.dawnID = b.ID
		}
	}
	if f.dawnID == "" {
		t.Fatalf("the pass minted no dawn belief: %v", held)
	}
	if !strings.Contains(f.app.rings.Section(3, "working_truth"), "at dawn") {
		t.Fatalf("the fixture's Ring 3 does not state the claim: %q", f.app.rings.Section(3, "working_truth"))
	}
	return f
}

func (f *ferryIdentity) verb(name string, args map[string]any) {
	f.t.Helper()
	if _, err := f.app.engine.ExecuteAction(context.Background(), "verb", name, args); err != nil {
		f.t.Fatalf("%s %v: %v", name, args, err)
	}
}

// replaceWithNoon is the identity's own act: a belief that says noon, and the
// dawn belief superseded by it.
func (f *ferryIdentity) replaceWithNoon() {
	f.verb("commit", map[string]any{"variant": "belief.upsert", "id": "b_ferry_noon", "statement": ferryNoon, "evidence_refs": []any{f.origin}})
	f.verb("commit", map[string]any{"variant": "belief.supersede", "old_id": f.dawnID, "new_id": "b_ferry_noon", "reason": "the timetable changed"})
}

// askTheFerry runs one turn and returns the system message the model was sent.
func askTheFerry(t *testing.T, app *App, obs *contScript) string {
	t.Helper()
	operatorTurn(t, app, "what time does the ferry leave?")
	reqs := obs.snapshot()
	return wireMessages(t, reqs[len(reqs)-1])[0].Content
}

func TestRing3DoesNotStateAClaimItsBeliefWasSupersededFrom(t *testing.T) {
	f := newFerryIdentity(t)
	f.replaceWithNoon()

	system := askTheFerry(t, f.app, f.obs)
	if strings.Contains(system, "leaves the harbor at dawn") {
		t.Errorf("the next turn's system message still states the claim, under Ring 3: %q", f.app.rings.Section(3, "working_truth"))
	}
	if !strings.Contains(system, "at noon") {
		t.Errorf("the next turn's system message does not state what replaced it: %q", f.app.rings.Section(3, "working_truth"))
	}
	stopApp(t, f.app)
	app2, obs2 := f.boot()
	if system := askTheFerry(t, app2, obs2); strings.Contains(system, "leaves the harbor at dawn") {
		t.Errorf("after a clean restart the system message states the claim again: %q", app2.rings.Section(3, "working_truth"))
	}
}

func TestAnArchivedBeliefLeavesRing3AtOnce(t *testing.T) {
	f := newFerryIdentity(t)
	f.verb("commit", map[string]any{"variant": "belief.archive", "id": f.dawnID})

	if system := askTheFerry(t, f.app, f.obs); strings.Contains(system, "leaves the harbor at dawn") {
		t.Errorf("the next turn's system message states an archived belief under Ring 3: %q", f.app.rings.Section(3, "working_truth"))
	}
}

// The process can end between the retiring event and the rewrite it causes.
// The restart finds a Ring 3 written before a retirement the record holds.
func TestARestartDoesNotRestoreARing3OlderThanARetirement(t *testing.T) {
	f := newFerryIdentity(t)
	f.app.cognition.consolidateFacility.Store(nil) // the hook is not there: the rewrite never happens
	f.replaceWithNoon()
	if !strings.Contains(f.app.rings.Section(3, "working_truth"), "at dawn") {
		t.Fatalf("the fixture did not leave Ring 3 stale: %q", f.app.rings.Section(3, "working_truth"))
	}
	stopApp(t, f.app)

	app2, obs2 := f.boot()
	if system := askTheFerry(t, app2, obs2); strings.Contains(system, "leaves the harbor at dawn") {
		t.Errorf("a restart restored a Ring 3 that states what the record retired: %q", app2.rings.Section(3, "working_truth"))
	}
}
