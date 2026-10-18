package app

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/genesis"
	"github.com/aiii-dot-id/aii-os/internal/genesis/genesistest"
	"github.com/aiii-dot-id/aii-os/internal/ledger"
)

func TestTheMarkIsShownOnTheNoteWhereverTheOperatorReadsIt(t *testing.T) {
	dir := t.TempDir()
	result := genesistest.NewRoot(t).Birth(t, genesis.BirthConfig{
		Name:       "MarkShown",
		KeyPath:    filepath.Join(dir, "identity.sec"),
		LedgerPath: filepath.Join(dir, "ledger.jsonl"),
		DBPath:     filepath.Join(dir, "aii.db"),
	})
	result.Ledger.Close()
	cfg := &Config{
		Identity: IdentityConfig{KeyPath: filepath.Join(dir, "identity.sec"),
			LedgerPath: filepath.Join(dir, "ledger.jsonl"), DBPath: filepath.Join(dir, "aii.db"),
		},
		LLM:        withTestProvider(t, dir, "test", "https://x", "m", "sk-x"),
		SourcePath: filepath.Join(dir, "config.json"),
		Dashboard:  DashboardConfig{Port: 0},
		Tools:      ToolsConfig{CWD: dir},
		Agency:     defaultConfig().Agency,
	}
	app := New(cfg)
	if err := startLiveForTest(app); err != nil {
		t.Fatalf("startLive: %v", err)
	}
	defer stopApp(t, app)
	h := app.buildLiveHandler()

	const mark = "kept out of DREAM and CONSOLIDATE"
	const hard = "THISTLEDOWN the storm took the east pier and I do not want it turned over again"
	const plain = "MARLINSPIKE the west pier was repainted"
	if _, err := app.engine.ExecuteAction(t.Context(), "verb", "note", map[string]any{"content": hard, "unmetabolized": true}); err != nil {
		t.Fatalf("note unmetabolized=true: %v", err)
	}
	if _, err := app.engine.ExecuteAction(t.Context(), "verb", "note", map[string]any{"content": plain}); err != nil {
		t.Fatal(err)
	}

	// THE IDENTITY PAGE'S STATE.
	st, err := h.GetIdentity()
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]string{}
	for _, e := range st.Experiences {
		switch e.Content {
		case hard:
			ids[hard] = e.ID
			if !e.Unmetabolized {
				t.Errorf("THE IDENTITY PAGE IS GIVEN THE MARKED NOTE WITHOUT ITS MARK: %+v", e)
			}
		case plain:
			ids[plain] = e.ID
			if e.Unmetabolized {
				t.Errorf("the identity page is given an ordinary note with the mark: %+v", e)
			}
		}
	}
	if len(ids) != 2 {
		t.Fatalf("the identity page is given %d of the two notes: %+v", len(ids), st.Experiences)
	}

	// THE MEMORY PAGE'S SEARCH.
	line := func(out, words string) string {
		for _, l := range strings.Split(out, "\n") {
			if strings.Contains(l, words) {
				return l
			}
		}
		return ""
	}
	out, err := h.Recall("THISTLEDOWN")
	if err != nil {
		t.Fatal(err)
	}
	if l := line(out, hard); !strings.Contains(l, mark) {
		t.Errorf("THE SEARCH'S HIT DOES NOT CARRY THE MARK: %q in\n%s", l, out)
	}
	out, err = h.Recall("MARLINSPIKE")
	if err != nil {
		t.Fatal(err)
	}
	if l := line(out, plain); l == "" || strings.Contains(l, mark) {
		t.Errorf("an ordinary note's hit is missing or marked: %q in\n%s", l, out)
	}

	mint := func(et ledger.EventType, payload map[string]any) {
		t.Helper()
		if _, err := app.door.Append(et, 3, payload, ""); err != nil {
			t.Fatalf("mint %s: %v", et, err)
		}
	}
	mint(ledger.EventBeliefUpsert, map[string]any{"id": "b_piers", "statement": "Both piers are sound", "ring": 3, "confidence": 0.5,
		"origin_refs": []map[string]string{{"kind": "experience", "id": ids[plain]}}})
	mint(ledger.EventEdgeCreate, map[string]any{"id": "edge_hard", "from_id": ids[hard], "to_id": "b_piers", "edge_type": "CONTRADICTS"})
	review := app.cognition.reviewFacility.Load()
	if review == nil {
		t.Fatal("the live identity has no review facility")
	}
	if err := review.Execute(context.Background()); err != nil {
		t.Fatalf("the review: %v", err)
	}
	cont, err := h.GetContinuity()
	if err != nil {
		t.Fatal(err)
	}
	finding := line(strings.Join(cont.ReviewLines, "\n"), hard[:40])
	if finding == "" || !strings.Contains(finding, mark) {
		t.Errorf("THE REVIEW'S FINDING DOES NOT QUOTE THE NOTE WITH ITS MARK: %q in %q", finding, cont.ReviewLines)
	}
}
